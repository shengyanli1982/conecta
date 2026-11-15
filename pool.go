package conecta

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shengyanli1982/conecta/internal/pool"
)

// 连接池包的哨兵错误定义；Go 惯用哨兵错误使用 Err 前缀（stdlib 惯例）
// Sentinel error definitions for the pool package; idiomatic Go sentinel errors use the Err prefix (stdlib convention)
var (
	// ErrQueueClosed 表示底层队列已关闭，池停止后的所有操作返回该错误
	// ErrQueueClosed indicates the underlying queue is closed; all pool operations return it after Stop
	ErrQueueClosed = errors.New("queue is closed")

	// ErrQueueIsNil 表示创建池时传入的队列接口为 nil
	// ErrQueueIsNil indicates the queue interface passed to New is nil
	ErrQueueIsNil = errors.New("queue interface is nil")

	// ErrPoolEmpty 表示池中没有可借用的元素
	// ErrPoolEmpty indicates the pool has no element available to borrow
	ErrPoolEmpty = errors.New("pool is empty")

	// ErrPoolFull 表示池已达到最大容量，无法再接收新元素
	// ErrPoolFull indicates the pool has reached its maximum capacity and cannot accept new elements
	ErrPoolFull = errors.New("pool is full")

	// ErrValueIsNil 表示要放入池中的元素为 nil
	// ErrValueIsNil indicates the element to be put into the pool is nil
	ErrValueIsNil = errors.New("value is nil")
)

// errNoCapacity 是内部哨兵：容量已满（在途总数 inFlight 达到 maxSize），有界创建无法预留在途槽位。
// 仅用于 GetWithContext 内部控制流（转入 waiter 注册），永远不会返回给调用方
// errNoCapacity is an internal sentinel: the pool is at capacity (the in-flight total inFlight reached
// maxSize) so a bounded creation cannot reserve an in-flight slot. It only drives control flow inside
// GetWithContext (switching to waiter registration) and is never returned to callers
var errNoCapacity = errors.New("pool is at capacity")

// errBorrowCheckFailed 是内部哨兵：TestOnBorrow 预设校验失败（pingFunc 判定借出对象不健康）。
// 仅作为预设 BeforeGet 钩子的错误输入（随销毁传递给 OnClose 回调供归因），永远不会直接返回给
// 调用方——调用方看到的是 ErrPoolEmpty（销毁预算耗尽）或下一个合格对象。与 errNoCapacity 同族
// 的内部哨兵，不扩展公共错误契约
// errBorrowCheckFailed is an internal sentinel: the TestOnBorrow preset check failed (pingFunc
// reported the borrowed object unhealthy). It only feeds the preset BeforeGet hook's error (it
// travels to the OnClose callback with the destruction for attribution) and is never returned to
// callers directly — callers see ErrPoolEmpty (the destruction budget is exhausted) or the next
// qualified object. An internal sentinel of the same family as errNoCapacity, never extending the
// public error contract
var errBorrowCheckFailed = errors.New("borrow check failed")

// callHook 执行一个用户决策钩子（BeforeGet/AfterPut 都是 func(element any) error 别名，单点
// 复用），并把钩子 panic 转为 error：defer recover 捕获 panic 值并包装进错误文本（真实上下文
// 增量，不吞 panic 值），随后走调用方对应的销毁路径——钩子自身的 bug 不拖垮整个池。
// 错误文本格式 "<name> hook panicked: <panic 值>"（哨兵直返纪律：与现有哨兵一致，无包名前缀）。
// 调用点必须以 hook != nil 门禁保护：未设钩子时热路径只多一次 nil 检查，零分配零调用；
// defer 为编译器开放编码（open-coded），无堆分配；fmt.Errorf 仅在 panic 慢路径执行。
// 注意：调用 callHook 时必须不持有任何池内锁（stopGate/queue.lock/element.mu/waitersMu）——
// 钩子是用户代码，持锁调用会把钩子的阻塞与重入风险放大为全池死锁面
// callHook runs one user decision hook (BeforeGet/AfterPut are both aliases of func(element any)
// error, so a single helper serves both) and converts a hook panic into an error: a deferred
// recover captures the panic value and wraps it into the error text (a real context increment — the
// panic value is never swallowed), after which the caller proceeds down its destruction path — a
// bug in the hook itself never takes the whole pool down. The error text reads "<name> hook
// panicked: <panic value>" (the sentinel-direct-return discipline: like the existing sentinels, no
// package-name prefix). Call sites must guard with hook != nil: with no hook configured the hot
// path pays exactly one extra nil check — no allocation, no call; the defer is open-coded by the
// compiler (no heap allocation) and fmt.Errorf only runs on the panic slow path. Note: callHook
// must be invoked holding no pool lock (stopGate/queue.lock/element.mu/waitersMu) — the hook is
// user code, and calling it under a lock would amplify its blocking and re-entry risks into a
// pool-wide deadlock surface
func callHook(name string, hook func(element any) error, element any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s hook panicked: %v", name, r)
		}
	}()
	return hook(element)
}

// poolStats 集中管理池的累计计数：全部字段为 atomic.Int64，热路径只做无锁自增，
// 不加锁也不参与 stopGate→queue.lock→element.mu 锁序。对外快照经 Stats()（stats.go）导出。
// poolStats centralizes the pool's cumulative counters: every field is an atomic.Int64, so hot paths
// only perform lock-free increments, taking no lock and staying outside the stopGate→queue.lock→element.mu
// lock order. The public snapshot is exported through Stats() (stats.go).
type poolStats struct {
	// totalCreated 累计成功创建的元素数（initialize 预建、GetOrCreate 按需创建、
	// GetWithContext 有界创建与 MinIdle 补齐四类来源；创建失败与 born-nil 拒绝不计数）
	// totalCreated is the cumulative number of successfully created elements (from the four sources
	// of initialize pre-creation, GetOrCreate on-demand creation, GetWithContext bounded creation
	// and MinIdle refill; creation failures and born-nil rejections are not counted)
	totalCreated atomic.Int64

	// totalDestroyed 累计销毁的元素数，恒等于下面五个 destroyedBy* 分类之和
	//（ping + lifetime + idle + borrow + stop）
	// totalDestroyed is the cumulative number of destroyed elements, always equal to the sum of the
	// five destroyedBy* categories below (ping + lifetime + idle + borrow + stop)
	totalDestroyed atomic.Int64

	// destroyedByPing 后台扫描中 ping 连续失败达到上限而销毁的元素数
	// destroyedByPing is the number of elements destroyed by the background scan after ping failures reached the retry limit
	destroyedByPing atomic.Int64

	// destroyedByLifetime 对象年龄达到 maxLifetime 而销毁的元素数（后台扫描与借出判定两条路径）
	// destroyedByLifetime is the number of elements destroyed because their age reached maxLifetime (both the background scan and the borrow check)
	destroyedByLifetime atomic.Int64

	// destroyedByIdle 对象持续空闲达到 maxIdleTime 而被后台扫描回收的元素数（P1-3；
	// 回收尊重 minIdle 下限，仅淘汰超出水位的部分）
	// destroyedByIdle is the number of elements reclaimed by the background scan after idling
	// continuously for maxIdleTime (P1-3; the reclaim respects the minIdle floor and only evicts
	// the surplus above the water level)
	destroyedByIdle atomic.Int64

	// destroyedByBorrow 借出/归还校验销毁的元素数：BeforeGet / AfterPut 钩子返回 error
	//（或 panic 被转为 error）与 TestOnBorrow 预设校验失败统一计入该分类
	// destroyedByBorrow is the number of elements destroyed by the borrow/return checks: BeforeGet /
	// AfterPut hooks returning an error (or a panic converted into one) and TestOnBorrow preset
	// failures all count into this category
	destroyedByBorrow atomic.Int64

	// destroyedByStop 四类来源销毁的元素数（G2 完整枚举，与 stats.go 的 DestroyedByStop 文档
	// 同口径）：① Stop/Cleanup（closeAllIdle 认领与 Phase2 drain 回收）；② 初始化失败清理
	//（initialize 的入队失败分支与 drainAndClose 排空已入队元素）；③ MinIdle 补齐入队失败
	//（refillOne，Stop 竞态/队列已关闭）；④ tombstone GC 的 live 重入队失败（gcTombstones，
	// 仅 BYO 队列被并发关闭时可达）
	// destroyedByStop is the number of elements destroyed by the four sources (the complete G2
	// enumeration, in lockstep with the DestroyedByStop documentation in stats.go): (1) Stop/Cleanup
	// (closeAllIdle claims and the Phase2 drain reclaim), (2) failed-initialization cleanup
	// (initialize's enqueue-failure branch and drainAndClose draining already-enqueued elements),
	// (3) MinIdle refill enqueue failures (refillOne, Stop races / a closed queue), and (4) the
	// tombstone GC's live re-enqueue failure (gcTombstones, reachable only when a BYO queue is
	// closed concurrently)
	destroyedByStop atomic.Int64

	// totalBorrowed 累计移交调用方的对象次数：队列借出（Get/GetOrCreate/GetWithContext 返回
	// 元素）与创建直交（GetOrCreate/GetWithContext 的按需创建路径把新建对象直接交给调用方，
	// 同样负有归还义务）。该账本与 totalReturned 配对派生 Stats.ActiveCount（B3b）：漏记创建
	// 直交会让“创建→归还”循环的派生值系统性负漂
	// totalBorrowed is the cumulative number of objects handed over to callers: queue borrows
	// (Get/GetOrCreate/GetWithContext returning an element) and creation hand-overs (the on-demand
	// creation paths of GetOrCreate/GetWithContext hand a freshly created object straight to the
	// caller, equally owing a return). Paired with totalReturned it derives Stats.ActiveCount
	// (B3b): missing the creation hand-overs would drift the derived value systematically negative
	// across create→return cycles
	totalBorrowed atomic.Int64

	// totalReturned 累计归还处理成功次数：Put 入队成功，或 AfterPut 钩子拒绝后由池销毁
	//（N2——所有权已移交回池即算归还，否则 ActiveCount 每次钩子拒绝永久 +1 漂移）；
	// ErrPoolFull/ErrValueIsNil/ErrQueueClosed 拒绝不入账（所有权未转移）
	// totalReturned is the cumulative number of successfully processed returns: a Put enqueued
	// successfully, or a Put rejected by the AfterPut hook and destroyed by the pool (N2 — the
	// ownership has already transferred back to the pool, so it counts as a return; otherwise
	// ActiveCount would drift +1 permanently per hook rejection); ErrPoolFull/ErrValueIsNil/
	// ErrQueueClosed rejections are not booked (ownership never transfers)
	totalReturned atomic.Int64

	// emptyCount 空池事件计数（与 stats.go 的 EmptyCount 文档同口径）：Get 返回 ErrPoolEmpty
	//（队列暂空/到期销毁达上限/借出校验拒绝达上限）与 GetOrCreate 的空池探测计入
	//（countEmpty=true，探测后转创建、不返回该哨兵）；GetWithContext 的内部重试不计数
	//（tryAcquire(false)）
	// emptyCount is the empty-pool event count (in lockstep with the EmptyCount documentation in
	// stats.go): incremented by Get returning ErrPoolEmpty (queue momentarily empty / expiry
	// destructions at the cap / borrow-check rejections at the cap) and by GetOrCreate's
	// empty-pool probe (countEmpty=true — it then creates and never returns the sentinel);
	// GetWithContext's internal retries do not count (tryAcquire(false))
	emptyCount atomic.Int64

	// fullPutCount Put 返回 ErrPoolFull 的次数
	// fullPutCount is the number of times Put returned ErrPoolFull
	fullPutCount atomic.Int64

	// waitCount GetWithContext 注册 waiter 并实际进入挂起的累计次数（spurious wake 后重新挂起会再次计数）
	// waitCount is the cumulative number of times GetWithContext registered a waiter and actually suspended
	// (re-suspending after a spurious wake counts again)
	waitCount atomic.Int64

	// waitTimeoutCount GetWithContext 挂起等待后（select 命中 ctx.Done 且 D9 复查仍无所得）因超时返回的次数；
	// 取消不计入，入口 fast-fail 与主循环轮次头的 ctx 检查（P1-A）返回的 DeadlineExceeded 也不计入
	// waitTimeoutCount is the number of times GetWithContext timed out after having suspended (the select
	// picked ctx.Done and the D9 recheck still gained nothing); cancellations do not count, and neither do
	// DeadlineExceeded returns from the entry fast-fail or the main loop's per-round ctx check (P1-A)
	waitTimeoutCount atomic.Int64

	// totalWaitDurationNs 注册到醒来（含被唤醒、超时、取消）的累计等待时长（纳秒）；
	// 时钟读取只发生在注册/唤醒慢路径（F8）
	// totalWaitDurationNs is the cumulative wait duration in nanoseconds from registration to wake-up
	// (including being woken, timeout and cancellation); clock reads only happen on the registration/wake
	// slow path (F8)
	totalWaitDurationNs atomic.Int64
}

// recordDestroyed 记录一次销毁：分类计数与总计各恰好自增一次。
// 所有销毁分支必须经由本方法计数（传入对应的分类字段），从而在结构上保证五分类恒等式
// totalDestroyed == destroyedByPing + destroyedByLifetime + destroyedByIdle + destroyedByBorrow +
// destroyedByStop 始终成立（Stats() 快照在池静止时精确满足该恒等式；并发销毁期间各 atomic
// 分别读取，快照可能出现瞬时偏差，见 Stats 文档）。
// recordDestroyed records one destruction: the category counter and the total are each incremented exactly once.
// Every destroy branch must count through this method (passing its own category field), which structurally
// guarantees the five-category identity totalDestroyed == destroyedByPing + destroyedByLifetime +
// destroyedByIdle + destroyedByBorrow + destroyedByStop always holds (a Stats() snapshot satisfies the
// identity exactly while the pool is quiescent; during concurrent destructions the atomics are read
// separately, so a snapshot may be transiently off — see the Stats documentation).
func (s *poolStats) recordDestroyed(category *atomic.Int64) {
	category.Add(1)
	s.totalDestroyed.Add(1)
}

// Pool represents a connection pool that manages a collection of elements
// Pool 表示一个管理元素集合的连接池
type Pool struct {
	queue       Queue              // The underlying queue for storing elements / 用于存储元素的底层队列
	config      *Config            // Configuration for the pool / 连接池的配置
	wg          sync.WaitGroup     // WaitGroup for goroutine synchronization / 用于goroutine同步的等待组
	once        sync.Once          // Ensures Stop() is called only once / 确保Stop()只被调用一次
	ctx         context.Context    // Context for cancellation / 用于取消操作的上下文
	cancel      context.CancelFunc // Context cancellation function / 上下文取消函数
	elementPool *pool.ElementPool  // Pool for element objects / 元素对象池
	stopGate    sync.RWMutex       // Stop 门闩，防止 Stop 窗口期并发 Put 泄漏 / stop gate preventing concurrent Put leaks during Stop window
	stopped     atomic.Bool        // 停止标记位，热路径免锁快速判定 / lock-free stop flag for hot paths
	size        atomic.Int64       // 当前池内元素计数，镜像 queue.Len() 语义（含 tombstone；有界创建预留已随 P0-B 移至 inFlight，size 仅在 Put 入队窗口内短暂 +1）。A3：只在离队点单点递减；tombstone 回收 = Get ∪ supervise 末尾 GC（N1），每轮扫描把 size 收敛回真实活对象数（上界新纪律点）；无锁原子量，不参与 stopGate→queue.lock→element.mu 锁序 / current element count in the pool, mirroring queue.Len() semantics (including tombstones; the bounded-creation reservation moved to inFlight with P0-B, so size is only transiently +1 during a Put enqueue window). A3: decremented at the single dequeue point only; tombstone reclamation = Get ∪ the GC at the end of supervise (N1), converging size back to the true live-object count every pass (the new upper-bound discipline point); lock-free atomic outside the stopGate→queue.lock→element.mu lock order

	// inFlight 是在途对象总数：池已创建且尚未销毁的对象数（已借出 + 池内空闲 + 在途创建预留）。
	// maxSize 封顶该计数（P0-B，对齐 pgxpool.MaxConns / database/sql.MaxOpenConns）：reserveSlot 以
	// inFlight<maxSize 的 CAS 门禁有界创建，在途对象全部借出后新的 GetWithContext 挂起等待归还，
	// 绝不突破创建第 maxSize+1 个对象。记账生命周期：任何 newFunc 成功 +1（initialize /
	// boundedCreate 的 reserveSlot 预留 / GetOrCreate）；任何已计数对象的销毁 -1（各 closeFunc 调用点
	// 与 boundedCreate 失败/panic 的 releaseSlot）；Get 借出、Put 归还、wakeWaiter 均不改变
	// （对象只在“借出”与“池内空闲”间移动，始终在途）。全部递减经 releaseSlot 单点收口并以 0 为下限：
	// 外部经 Put 收编的对象从未经 newFunc +1，其销毁不会把计数压成负数。
	// 与 size 解耦：size 继续镜像队列占用（含 tombstone）并门禁 Put 归还，A3 单点递减不变式不动。
	// inFlight is the total in-flight object count: objects the pool has created and not yet destroyed
	// (borrowed + idle in the pool + in-flight creation reservations). maxSize caps this counter (P0-B,
	// aligning with pgxpool.MaxConns / database/sql.MaxOpenConns): reserveSlot gates bounded creation with
	// an inFlight<maxSize CAS, so once every in-flight object is borrowed a new GetWithContext suspends and
	// waits for a return instead of ever creating the maxSize+1-th object. Accounting lifecycle: +1 on any
	// successful newFunc (initialize / boundedCreate's reserveSlot reservation / GetOrCreate); -1 on any
	// destruction of a counted object (each closeFunc call site and releaseSlot on boundedCreate
	// failure/panic); Get lend-out, Put return and wakeWaiter never change it (an object only moves between
	// "borrowed" and "idle in the pool", staying in flight throughout). Every decrement funnels through the
	// single releaseSlot entry point and floors at zero: a foreign object adopted via Put never went through
	// a newFunc +1, so destroying it can never push the counter negative.
	// Decoupled from size: size keeps mirroring the queue occupancy (tombstones included) and gating Put
	// returns, leaving the A3 single-decrement invariant untouched.
	inFlight atomic.Int64

	stats poolStats // 累计计数（全 atomic，热路径无锁自增），不参与锁序 / cumulative counters (all atomic, lock-free increments on hot paths), outside the lock order

	// refilling 是 MinIdle 补齐的节流标志（P1-4）：supervise 末尾以 CAS(false→true) 竞争启动
	// 补齐 goroutine，goroutine 结束时置回 false——保证同一时刻至多一个补齐 goroutine 在途，
	// 防止 scanInterval 短 + newFunc 慢时补齐 goroutine 叠加造成创建风暴。atomic 布尔量，
	// 不参与锁序
	// refilling is the throttle flag of the MinIdle refill (P1-4): the end of supervise races to
	// launch the refill goroutine with a CAS(false→true), and the goroutine resets it to false on
	// exit — at most one refill goroutine is in flight at any moment, preventing stacked refills
	// (a creation storm) when the scanInterval is short and newFunc is slow. An atomic bool
	// outside the lock order
	refilling atomic.Bool

	// waitersMu 是 waiter 列表的独立叶子锁：持有它时禁止再获取 stopGate/queue.lock/element.mu（D10）
	// waitersMu is the dedicated leaf lock guarding the waiter list: while holding it, acquiring
	// stopGate/queue.lock/element.mu is forbidden (D10)
	waitersMu sync.Mutex

	// waiters 是 FIFO 等待队列，元素为 close 语义的信号通道（唤醒不携带元素——D7 信号量语义，
	// 被唤醒者重跑完整获取路径；close 先于 select 发生时信号依然可见，电平持续不丢失）
	// waiters is the FIFO waiting list whose entries are close-semantics signal channels (a wake carries no
	// element — D7 semaphore semantics: the woken caller re-runs the full acquire path; a close that happens
	// before the select is still observed, so the signal is level-persistent and never lost)
	waiters []chan struct{}

	// waiterCount 是 len(waiters) 的原子镜像（两者在 waitersMu 内同步变更）：
	// 无 waiter 时 Put/Get 快速路径的唤醒探测只花一次 atomic.Load，零锁零分配（D10）
	// waiterCount is the atomic mirror of len(waiters) (both are updated together under waitersMu):
	// with no waiters, the wake probe on the Put/Get fast paths costs a single atomic.Load,
	// taking no lock and allocating nothing (D10)
	waiterCount atomic.Int64

	// ageEvictionOn 在 New 时预计算的 stampEnqueue 门禁（年龄/空闲淘汰是否启用）：
	// maxLifetime>0 || maxIdleTime>0，与原 stampEnqueue 函数内门禁（maxLifetime<=0 &&
	// maxIdleTime<=0 即不打戳）De Morgan 逐字等价。config 是池持有的私有只读副本
	//（P1-1 浅拷贝隔离），门禁结果在池生命周期内不变，故可从被调方每次读取两个 config
	// 字段提升为调用方一次 bool 读取。GO-1：门禁检查提升至全部 3 个调用点
	//（initialize/Put/refillOne）；未启用时热路径省掉一次不可内联调用与两次指针追逐
	// config 字段读取，启用时打戳行为逐字不变
	// ageEvictionOn is the stampEnqueue gate precomputed at New time (whether age/idle eviction
	// is enabled): maxLifetime>0 || maxIdleTime>0, De Morgan-verbatim equivalent to the original
	// in-stampEnqueue gate (no stamping when maxLifetime<=0 && maxIdleTime<=0). config is the
	// pool's private read-only copy (P1-1 shallow-copy isolation), so the gate result never changes
	// across the pool's lifetime and can be lifted from "the callee reads two config fields every
	// time" to "one bool read at the caller". GO-1: the gate check is hoisted to all 3 call sites
	// (initialize/Put/refillOne); when disabled the hot path saves one non-inlinable call and two
	// pointer-chasing config field reads, and when enabled the stamping behaves verbatim as before
	ageEvictionOn bool
}

// New creates a new Pool instance with the given queue and configuration
// 使用给定的队列和配置创建一个新的Pool实例
func New(queue Queue, conf *Config) (*Pool, error) {
	// Check if queue interface is valid
	// 检查队列接口是否有效
	if queue == nil {
		return nil, ErrQueueIsNil
	}
	// Validate and normalize configuration
	// 验证并规范化配置
	conf = normalizeConfig(conf)
	ctx, cancel := context.WithCancel(context.Background())

	// Create new pool instance with initialized fields
	// 创建新的连接池实例并初始化字段
	p := &Pool{
		queue:       queue,
		config:      conf,
		elementPool: pool.NewElementPool(),
		ctx:         ctx,
		cancel:      cancel,
		// GO-1：stampEnqueue 门禁预计算（见 ageEvictionOn 字段注释）——conf 已归一化
		//（负值钳 0），且必须先于 initialize 赋值（initialize 的入队路径经由该门禁）
		// GO-1: precomputed stampEnqueue gate (see the ageEvictionOn field comment) — conf is
		// already normalized (negatives clamped to 0) and this must be set before initialize
		// (initialize's enqueue path goes through the gate)
		ageEvictionOn: conf.maxLifetime > 0 || conf.maxIdleTime > 0,
	}

	// Initialize pool with initial elements
	// 初始化连接池的初始元素
	if err := p.initialize(); err != nil {
		cancel()
		return nil, err
	}

	// Start the executor goroutine
	// 启动执行器goroutine
	p.wg.Add(1)
	go p.monitor()
	return p, nil
}

// initialize creates and adds initial elements to the pool
// 初始化连接池，创建并添加初始元素
func (p *Pool) initialize() error {
	// Create and enqueue initial elements one by one
	// 逐个创建并入队初始元素
	for range p.config.initialize {
		value, err := p.config.newFunc()
		if err != nil {
			// newFunc 失败：关闭本次创建的 value（从未入队），并清理已入队的元素
			// newFunc failed: close this value (never enqueued) and drain already enqueued elements
			if value != nil {
				_ = p.config.closeFunc(value)
			}
			p.drainAndClose()
			return err
		}
		// D3：newFunc 成功却返回 nil（born-nil）——born-nil 元素会被后续一切销毁路径的
		// “nil 即已释放”豁免跳过，其 +1 永不配对，在途配额静默永久泄漏。按项目“显式误用
		// 返回明确错误”哲学视同创建失败：清理已入队元素并返回 ErrValueIsNil（与 Put 拒绝
		// nil 的契约对称），绝不静默计数入队
		// D3: newFunc succeeded yet returned nil (born-nil) — a born-nil element is skipped by the
		// "nil means already released" exemption of every later destruction path, so its +1 never
		// pairs and its in-flight slot leaks silently and permanently. Following the project's
		// "explicit misuse returns an explicit error" philosophy this counts as a creation failure:
		// drain the enqueued elements and return ErrValueIsNil (symmetric with Put's nil-rejection
		// contract), never counting or enqueuing it silently
		if value == nil {
			p.drainAndClose()
			return ErrValueIsNil
		}
		// 创建成功即计数（含 initialize 预建与 GetOrCreate 按需创建两类来源）；
		// 在途计数 +1（inFlight 生命周期：任何 newFunc 成功即计入在途）
		// Count on successful creation (covering both initialize pre-creation and GetOrCreate on-demand
		// creation); in-flight count +1 (inFlight lifecycle: any successful newFunc enters the in-flight set)
		p.stats.totalCreated.Add(1)
		p.inFlight.Add(1)

		// Wrap value in element and enqueue
		// 将 value 包装到 element 并入队
		element := p.elementPool.Get()
		// E1 纪律一致性：与 Put 同序——先打时间戳再写 data（initialize 运行于 New 返回前、
		// monitor 启动前，无并发观察者，此处重排纯属结构纪律：让“data 已写但未打戳”的中间态
		// 在全代码库中不存在）
		// E1 discipline consistency: the same order as Put — stamp the timestamps before writing
		// data (initialize runs before New returns and before the monitor starts, with no concurrent
		// observer; this reorder is purely structural discipline, making the intermediate state
		// "data written but unstamped" nonexistent across the whole codebase)
		// GO-1：门禁提升至调用点——与原 stampEnqueue 函数内门禁逐字等价（见 ageEvictionOn 字段注释）
		// GO-1: the gate is hoisted to the call site — verbatim equivalent to the original
		// in-stampEnqueue gate (see the ageEvictionOn field comment)
		if p.ageEvictionOn {
			p.stampEnqueue(element)
		}
		element.SetData(value)
		if err := p.queue.Put(element); err != nil {
			// queue.Put 失败：归还 wrapper 并关闭本次创建的 value，并清理已入队的元素。
			// 注意上面 newFunc 失败路径的 closeFunc 不在此列——value 从未 +1（newFunc 出错不计入在途），
			// 而本分支的 value 已计数，销毁必须配对 -1
			// queue.Put failed: return wrapper, close this value (never enqueued), and drain already enqueued
			// elements. Note the closeFunc on the newFunc-failure path above is deliberately excluded — that
			// value was never +1'd (a failed newFunc does not enter the in-flight set), while this branch's
			// value was counted and its destruction must pair with a -1
			p.elementPool.Put(element)
			if value != nil {
				// P2-2 销毁观察者对称：镜像 refillOne 入队失败分支的既有配对——closeFunc 错误
				// 不再吞没，随 value 交给 OnClose（账本已计入 DestroyedByStop，观察者事件不得
				// 缺席——interface.go 的 OnClose 契约“在销毁对象时调用”）。initialize 运行于
				// New 返回前：无并发、不持任何池内锁，回调安全
				// P2-2 destruction-observer symmetry: mirroring refillOne's existing pairing on the
				// enqueue-failure branch — the closeFunc error is no longer swallowed and travels
				// with the value into OnClose (the ledger already books DestroyedByStop, so the
				// observer event must not be absent — interface.go's OnClose contract "called when
				// an object is destroyed"). initialize runs before New returns: no concurrency and
				// no pool lock held, so the callback is safe
				closeErr := p.config.closeFunc(value)
				p.config.callback.OnClose(value, closeErr)
				p.stats.recordDestroyed(&p.stats.destroyedByStop)
				// 销毁已计数对象：在途计数 -1（初始化期间无并发 waiter，唤醒为空转）
				// Destroying a counted object: in-flight count -1 (no concurrent waiters during
				// initialization, so the wake is a no-op)
				p.releaseSlot()
			}
			p.drainAndClose()
			return err
		}
		// 入队成功：池内计数加一。免容量检查——normalizeConfig 已保证 initialize ≤ maxSize，且发生在 New 返回前无并发
		// Enqueue succeeded: increment the pool count. Capacity check is exempt — normalizeConfig already guarantees initialize ≤ maxSize, and this runs before New returns with no concurrency
		p.size.Add(1)
	}

	// All elements successfully enqueued; caller (Stop/Cleanup) is responsible for cleanup
	// 所有元素已成功入队；调用方（Stop/Cleanup）负责后续清理
	return nil
}

// drainAndClose drains all queued elements, closes their values, and returns wrappers to the element pool.
// 排空队列中所有已入队的元素，关闭它们的 value，并将 wrapper 归还给 elementPool。
func (p *Pool) drainAndClose() {
	for {
		elem, err := p.queue.Get()
		if err != nil {
			break
		}
		p.queue.Done(elem)
		// 元素离队：池内计数减一
		// The element left the queue: decrement the pool count
		p.size.Add(-1)
		wrapper := elem.(*pool.Element)
		if v := wrapper.GetData(); v != nil {
			// P2-2 销毁观察者对称：与 initialize 入队失败分支同理——closeFunc 错误随 value
			// 交给 OnClose，账本与观察者事件同席（本路径仅在 New 失败清理中运行：无并发、
			// 不持任何池内锁，回调安全）
			// P2-2 destruction-observer symmetry: same rationale as initialize's enqueue-failure
			// branch — the closeFunc error travels with the value into OnClose so the ledger and
			// the observer event stay in step (this path only runs in the failed-New cleanup: no
			// concurrency and no pool lock held, so the callback is safe)
			closeErr := p.config.closeFunc(v)
			p.config.callback.OnClose(v, closeErr)
			p.stats.recordDestroyed(&p.stats.destroyedByStop)
			// 销毁已计数对象：在途计数 -1（releaseSlot 以 0 为下限并唤醒；本路径仅在初始化失败清理中
			// 运行，无并发 waiter，唤醒为空转）
			// Destroying a counted object: in-flight count -1 (releaseSlot floors at zero and wakes; this
			// path only runs in the failed-initialization cleanup, where no concurrent waiter exists and
			// the wake is a no-op)
			p.releaseSlot()
		}
		p.elementPool.Put(wrapper)
	}
}

// stampEnqueue 为即将入队的 wrapper 打时间戳：createdAt/idleSince 置为当前时刻，
// 并预计算 expireAt = now + maxLifetime + jitter（每个元素一次性算好，判定侧只需一次比较，O(1)）。
// 门禁（F4，B1 A2 传承义务的落地；GO-1 提升至调用点）：本函数仅在 ageEvictionOn（New 时预计算
// maxLifetime>0 || maxIdleTime>0）为真时被调用——全部 3 个调用点（initialize/Put/refillOne）以
// `if p.ageEvictionOn` 守卫，与原函数内门禁「maxLifetime<=0 && maxIdleTime<=0 即不打戳立即返回」
// De Morgan 逐字等价；未启用时热路径不读时钟、不写时间戳，与引入年龄/空闲淘汰之前完全一致，
// 且省掉一次不可内联调用与两次指针追逐 config 字段读取。任一启用即打全套时间戳（仅启用
// maxIdleTime 时 effectiveLifetime()=0，expireAt=now 属陈旧但无害：lifetime 判定侧被
// lifetimeOn=false 短路永不读取，wrapper 复用时经 Reset/PutRaw 统一清零）。
// 打戳必须先于 data 写入（E1 纪律：消灭“data 已写但未打戳”的中间态，fail-closed 判定
// 依赖 data!=nil ⟹ 时间戳已打）。
// stampEnqueue timestamps a wrapper that is about to be enqueued: createdAt/idleSince are set to now and
// expireAt = now + maxLifetime + jitter is precomputed (once per element, so the check is a single comparison, O(1)).
// Gate (F4, fulfilling the obligation inherited from B1's A2 ruling; hoisted to the call sites by GO-1):
// this function is only called when ageEvictionOn (maxLifetime>0 || maxIdleTime>0, precomputed at New)
// holds — all 3 call sites (initialize/Put/refillOne) guard it with `if p.ageEvictionOn`, De Morgan-verbatim
// equivalent to the original in-function gate "return unstamped when maxLifetime<=0 && maxIdleTime<=0";
// when disabled the hot path reads no clock and writes no timestamp, behaving exactly as before age/idle
// eviction existed, and additionally saves one non-inlinable call and two pointer-chasing config field
// reads. With either enabled the full timestamp set is stamped (when only maxIdleTime is enabled
// effectiveLifetime()=0, so expireAt=now is stale but harmless: the lifetime check short-circuits on
// lifetimeOn=false and never reads it, and wrapper reuse clears all timestamps through Reset/PutRaw).
// Stamping must precede the data write (the E1 discipline: the intermediate state "data written but
// unstamped" must not exist; the fail-closed checks rely on data!=nil ⟹ timestamps stamped).
func (p *Pool) stampEnqueue(element *pool.Element) {
	now := time.Now().UnixNano()
	element.SetCreatedAt(now)
	element.SetIdleSince(now)
	// P2-1 溢出卫：病态超大 maxLifetime（≳235 年，含 time.Duration(math.MaxInt64) 的“永久”
	// 惯用写法）使加法在 int64 纳秒域回绕为负，fail-closed 判定（now>=expireAt）会把新生对象
	// 误判为已到期——全池退化为“创建即销毁”。回绕（exp<now，单调时钟下合法寿命恒非负）即钳到
	// MaxInt64，等价“永不过期”的用户意图；门禁与 fail-closed 语义不变，非溢出路径逐字同前
	// P2-1 overflow guard: a pathologically huge maxLifetime (≳235 years, including the idiomatic
	// time.Duration(math.MaxInt64) "forever") wraps the addition negative in the int64 nanosecond
	// domain, and the fail-closed check (now>=expireAt) would judge every newborn object already
	// expired — the whole pool degenerates into create-then-destroy. A wraparound (exp<now; a legal
	// lifetime is never negative under the monotonic wall clock) clamps to MaxInt64, matching the
	// user's "never expires" intent; the stamp gate and the fail-closed semantics stay untouched
	// and non-overflow paths behave verbatim as before
	exp := now + p.effectiveLifetime()
	if exp < now {
		exp = math.MaxInt64
	}
	element.SetExpireAt(exp)
}

// effectiveLifetime 返回本次入池对象的实际存活时长（纳秒）：maxLifetime 加上 [0, jitter] 内的一次随机取值，
// 用于打散同批对象的到期时刻，避免集体过期引发创建风暴；jitter 已由 normalizeConfig 钳制到 maxLifetime/2。
// 抖动使用 math/rand/v2 的全局源（并发安全、无需 seed），保持主模块零依赖。
// effectiveLifetime returns the effective lifetime (in nanoseconds) of an object entering the pool now:
// maxLifetime plus one random draw from [0, jitter], spreading the expiry times of a cohort so they do not
// all expire at once and trigger a creation storm; normalizeConfig already clamps jitter to maxLifetime/2.
// The jitter uses the global source of math/rand/v2 (concurrency-safe, no seeding needed), keeping the main module dependency-free.
func (p *Pool) effectiveLifetime() int64 {
	lifetime := int64(p.config.maxLifetime)
	if jitter := int64(p.config.maxLifetimeJitter); jitter > 0 {
		lifetime += rand.Int64N(jitter + 1)
	}
	return lifetime
}

// Stop gracefully shuts down the pool.
// 注意（P2-f）：Stop 持有 stopGate 写锁期间经 Cleanup 执行用户 closeFunc/OnClose——在这些回调内
// 重入池的任何 API（Get/Put/GetWithContext/Stop 等，均需获取 stopGate）将自死锁；supervise 路径的
// 销毁回调虽不持写锁，同样禁止重入（回调应快速返回，重入语义未定义）
// Stop gracefully shuts down the pool.
// NOTE (P2-f): Stop holds the stopGate write lock while running the user's closeFunc/OnClose through
// Cleanup — calling back into any pool API from these callbacks (Get/Put/GetWithContext/Stop etc., all of
// which acquire stopGate) self-deadlocks; destruction callbacks on the supervise path do not hold the
// write lock but re-entry is equally forbidden (callbacks must return quickly; re-entrant semantics are
// undefined)
func (p *Pool) Stop() {
	p.once.Do(func() {
		// 先置位停止标记，使热路径（Get/Put）的免锁判定立即生效，再取消上下文
		// Set the stop flag first so the lock-free hot-path checks (Get/Put) take effect immediately, then cancel the context
		p.stopped.Store(true)
		p.cancel()
		p.wg.Wait()
		// 持有 Stop 门闩写锁，排除并发 Put 后再执行清理与关闭队列
		// Hold the stop gate write lock to exclude concurrent Put before cleanup and queue shutdown
		p.stopGate.Lock()
		defer p.stopGate.Unlock()
		// 广播唤醒全部挂起 waiter：它们重跑获取路径时看到停止标记，统一返回 ErrQueueClosed。
		// waiter 注册在 stopGate.RLock 之内完成，写锁在此天然等待在途注册结束，不存在丢失 waiter
		// 的竞态窗口（D10）
		// Broadcast-wake every suspended waiter: re-running the acquire path they observe the stop flag
		// and uniformly return ErrQueueClosed. Waiter registration completes inside stopGate.RLock, so the
		// write lock held here naturally waits for in-flight registrations — there is no race window that
		// could lose a waiter (D10)
		p.broadcastWaiters()
		p.Cleanup()
		p.queue.Shutdown()
	})
}

// Cleanup cleans up all elements in the pool
// 清理连接池中的所有元素。注意：standalone Cleanup（非 Stop 触发）后池仍存活——启用 MinIdle 时
// 下一轮后台扫描会把空闲水位自动补齐回 MinIdle（语义合理但可能出乎意料；Stop 才是终止态）
// Cleanup cleans up all elements in the pool. Note: after a standalone Cleanup (not via Stop) the
// pool stays alive — with MinIdle enabled the next background pass automatically refills the idle
// water level back to MinIdle (semantically sound but possibly surprising; Stop is the terminal state)
func (p *Pool) Cleanup() {
	// Phase 1: Close all values but do NOT return wrappers to elementPool
	// 第一阶段：关闭所有 value，但不归还 wrapper 到 elementPool
	p.closeAllIdle()
	// Phase 2: Drain queue and return all wrappers to elementPool
	// 第二阶段：排空队列，回收所有 wrapper 到 elementPool
	for {
		element, err := p.queue.Get()
		if err != nil {
			break
		}
		p.queue.Done(element)
		// 元素离队：池内计数减一
		// The element left the queue: decrement the pool count
		p.size.Add(-1)
		wrapper := element.(*pool.Element)
		// 关闭守卫：standalone Cleanup 期间并发 Put 进入的元素（阶段1 Range 之后入队）仍持有 data，
		// 按认领模式关闭，避免被静默丢失关闭
		// Close guard: elements enqueued by concurrent Put during standalone Cleanup
		// (after the phase-1 Range) still hold data; claim and close them so they
		// are not silently lost without closing
		wrapper.Lock()
		value := wrapper.GetDataNoLock()
		if value == nil {
			wrapper.Unlock()
			p.elementPool.Put(wrapper)
			continue
		}
		wrapper.SetDataNoLock(nil)
		wrapper.Unlock()
		// 在元素锁外执行关闭与回调
		// Execute close and callback outside the element lock
		closeErr := p.config.closeFunc(value)
		p.config.callback.OnClose(value, closeErr)
		p.stats.recordDestroyed(&p.stats.destroyedByStop)
		// 销毁已计数对象：在途计数 -1 + 定向唤醒（standalone Cleanup 期间可能存在挂起 waiter：
		// 销毁释放在途配额，等价于容量释放，按 [状态→信号] 顺序必须唤醒——双序协议纪律）
		// Destroying a counted object: in-flight count -1 + a targeted wake (a standalone Cleanup may run
		// while waiters are suspended: destruction releases in-flight quota, which is a capacity release and
		// must wake a waiter in [state → signal] order — the dual-order protocol discipline)
		p.releaseSlot()
		p.elementPool.Put(wrapper)
	}
}

// closeAllIdle 关闭队列中所有空闲元素的 value（Cleanup 第一阶段）：先在元素锁内认领 value，
// 再于锁外执行 closeFunc 与 OnClose，与 supervise 的销毁路径互斥，杜绝双重关闭。
// 元素本身留在队列中成为 tombstone（data 已置 nil），wrapper 由后续 Get 或 Cleanup 第二阶段回收。
// closeAllIdle closes the values of all idle elements in the queue (phase 1 of Cleanup): each value is
// claimed under the element lock first, then closeFunc and OnClose run outside that lock, which is exclusive
// with the supervise destroy path and prevents double close. Elements themselves stay in the queue as
// tombstones (data already set to nil); their wrappers are reclaimed by a later Get or by phase 2 of Cleanup.
func (p *Pool) closeAllIdle() {
	p.queue.Range(func(data any) bool {
		element := data.(*pool.Element)
		// 在元素锁内原子认领 value，避免与 supervise 双重关闭
		// Atomically claim the value under the element lock to avoid double close with supervise
		element.Lock()
		value := element.GetDataNoLock()
		if value == nil {
			element.Unlock()
			return true
		}
		element.SetDataNoLock(nil)
		element.Unlock()
		// 在元素锁外执行关闭与回调
		// Execute close and callback outside the element lock
		err := p.config.closeFunc(value)
		p.config.callback.OnClose(value, err)
		p.stats.recordDestroyed(&p.stats.destroyedByStop)
		// 销毁已计数对象：在途计数 -1 + 定向唤醒（销毁释放在途配额，等价于容量释放）。
		// 锁序核验：本调用位于 queue.Range 回调内（持队列锁），waitersMu 是叶子锁，新增边
		// queue.lock→waitersMu 与既有 stopGate→waitersMu 同向、waitersMu 内零外部锁获取，
		// 锁序图仍无环；close(w) 永不阻塞
		// Destroying a counted object: in-flight count -1 + a targeted wake (destruction releases in-flight
		// quota — a capacity release). Lock-order check: this call runs inside the queue.Range callback
		// (holding the queue lock); waitersMu is a leaf lock, so the new queue.lock→waitersMu edge points the
		// same way as the existing stopGate→waitersMu edge and waitersMu never acquires another lock — the
		// lock graph stays acyclic, and close(w) never blocks
		p.releaseSlot()
		return true
	})
}

// Get retrieves an element from the pool
// 从连接池中获取一个元素
func (p *Pool) Get() (any, error) {
	return p.tryAcquire(true)
}

// tryAcquire 是 Get/GetOrCreate 与 GetWithContext 共享的单轮获取逻辑：非阻塞地从队列取出一个
// 可借出的元素。返回 ErrQueueClosed（队列关闭或池停止）、ErrPoolEmpty（队列暂空，或到期销毁/
// 借出校验拒绝各自达到 maxRetries 上限）或 (value, nil)；返回的哨兵恒为裸值（不包装），调用方
// 可直接用 == 判定。借出成功路径在返回前执行 BeforeGet 钩子（含 TestOnBorrow 预设；未设时仅
// 一次 nil 检查）：钩子拒绝的对象被销毁（destroyedByBorrow 分类 + releaseSlot）并继续尝试
// 队列下一个。countEmpty 决定 ErrPoolEmpty 是否计入 emptyCount：面向调用方的 Get/GetOrCreate
// 传 true（B1 语义逐一保持），GetWithContext 的内部重试传 false，避免等待循环污染该计数。
// tombstone 回收使 size 递减、释放容量，是 waiter 的唤醒点之一（D8），唤醒探测在无 waiter 时
// 只花一次 atomic.Load
// tryAcquire is the single-round acquisition logic shared by Get/GetOrCreate and GetWithContext: it
// takes one lendable element out of the queue without blocking. It returns ErrQueueClosed (queue closed
// or pool stopped), ErrPoolEmpty (queue momentarily empty, or expiry destructions / borrow-check
// rejections each reached their maxRetries cap) or (value, nil); returned sentinels are always bare
// (never wrapped), so callers may compare with ==. The successful-borrow path runs the BeforeGet
// hook (the TestOnBorrow preset included; with none configured it costs a single nil check) before
// returning: an object rejected by the hook is destroyed (the destroyedByBorrow category +
// releaseSlot) and the loop continues with the next queued element. countEmpty decides whether
// ErrPoolEmpty feeds emptyCount: the caller-facing Get/GetOrCreate pass true (preserving B1 semantics
// one by one), while GetWithContext's internal retries pass false so the wait loop never pollutes the
// count. A tombstone reclaim decrements size and releases capacity, which is one of the D8 wake
// points — the wake probe costs a single atomic.Load when there are no waiters
func (p *Pool) tryAcquire(countEmpty bool) (any, error) {
	// Check if queue is closed
	// 检查队列是否已关闭
	if p.queue.IsClosed() {
		return nil, ErrQueueClosed
	}

	// 年龄判定门禁：maxLifetime=0（默认）时整段判定短路，热路径不读时钟，仅一次配置读取加分支；
	// 启用时每次调用只取一次 now（与 supervise 同策略），重试循环内不重复读时钟
	// Age-check gate: with maxLifetime=0 (the default) the whole check short-circuits, so the hot path
	// reads no clock and costs only one config load plus a branch; when enabled, now is read once per call
	// (the same strategy as supervise) instead of once per loop iteration
	lifetimeOn := p.config.maxLifetime > 0
	var now int64
	if lifetimeOn {
		now = time.Now().UnixNano()
	}
	// 到期销毁的次数上限沿用 maxRetries：避免一池同时到期时单次 Get 放大成大量 closeFunc 调用
	// The cap on expiry destructions reuses maxRetries: it prevents a single Get from amplifying into many
	// closeFunc calls when a whole cohort expires at once
	expiredDestroyed := 0
	// 借出校验（BeforeGet/TestOnBorrow 预设）拒绝的次数上限同样沿用 maxRetries，与到期销毁
	// 独立计数（同一模式）：避免一池坏对象时单次借出放大成大量 closeFunc 调用
	// The cap on borrow-check rejections (the BeforeGet/TestOnBorrow preset) equally reuses
	// maxRetries, counted independently from expiry destructions (the same pattern): it prevents a
	// single borrow from amplifying into many closeFunc calls when a whole cohort is unhealthy
	borrowRejected := 0

	for {
		// Check if the pool is stopped (lock-free fast path, avoids cancelCtx mutex)
		// 检查连接池是否已停止（免锁快速路径，避免 cancelCtx 互斥锁开销）
		if p.stopped.Load() {
			return nil, ErrQueueClosed
		}

		// Try to get element from queue
		// 尝试从队列中获取元素
		element, err := p.queue.Get()
		if err != nil {
			// 队列可能已被并发关闭：优先返回关闭错误，否则返回预分配的空池哨兵错误（零分配）
			// The queue may be closed concurrently: prefer the closed error, otherwise return the pre-allocated empty-pool sentinel (zero allocation)
			if p.queue.IsClosed() {
				return nil, ErrQueueClosed
			}
			if countEmpty {
				p.stats.emptyCount.Add(1)
			}
			return nil, ErrPoolEmpty
		}
		p.queue.Done(element)

		// 元素离队即减容量：单点递减同时覆盖 tombstone 回收与借出两个分支
		// Decrement on dequeue: this single point covers both the tombstone reclaim and the lend-out branches
		p.size.Add(-1)

		wrapper := element.(*pool.Element)
		wrapper.Lock()
		value := wrapper.GetDataNoLock()
		if value == nil {
			wrapper.SetRetriesNoLock(0)
			wrapper.Unlock()
			p.elementPool.PutRaw(wrapper)
			// tombstone 回收使 size 递减、释放队列占用。P0-B 后创建门禁在 inFlight（tombstone 的在途
			// 配额已在销毁点释放并唤醒过），此处唤醒不再释放创建容量：按“每个容量状态变更配对一次信号”
			// 的协议统一性保留（D7 spurious wake 良性——挂起 waiter 的复查条件含 queue.Len==0，
			// 该唤醒点实际打不到挂起中的 waiter，属防御性保留）
			// A tombstone reclaim decremented size, releasing queue occupancy. After P0-B the creation gate
			// lives on inFlight (the tombstone's in-flight quota was already released and woken at the
			// destruction site), so this wake no longer releases creation capacity: it is kept for protocol
			// uniformity ("every capacity state change pairs with one signal") — a D7 spurious wake is
			// benign, and since a suspended waiter's recheck requires queue.Len==0 this wake point cannot
			// actually hit a suspended waiter (defensive retention)
			p.wakeWaiter()
			continue
		}
		// 借出前年龄判定：到期元素就地销毁（锁内认领 + 锁外 closeFunc/OnClose），再继续尝试队列下一个。
		// 元素已离队，故无需留 tombstone，wrapper 清零后直接归还 elementPool（与上面的 tombstone 回收分支同语义）
		// Age check before lending: an expired element is destroyed in place (claimed under the lock, then
		// closeFunc/OnClose outside it) and the loop tries the next queued element. The element has already
		// left the queue, so no tombstone is needed and the wrapper is returned to elementPool after clearing
		// (same semantics as the tombstone reclaim branch above)
		if lifetimeOn && now >= wrapper.GetExpireAt() {
			wrapper.SetDataNoLock(nil)
			wrapper.SetRetriesNoLock(0)
			wrapper.Unlock()
			p.elementPool.PutRaw(wrapper)
			closeErr := p.config.closeFunc(value)
			p.config.callback.OnClose(value, closeErr)
			p.stats.recordDestroyed(&p.stats.destroyedByLifetime)
			// 销毁已计数对象：在途计数 -1 + 定向唤醒（销毁释放在途配额）。本路径的行为者会继续
			// 自身循环（自补偿），唤醒面向其他可能挂起的 waiter——每次递减与信号一一配对，
			// 不做逐点可达性豁免（双序协议的机械纪律，防演化脆弱）
			// Destroying a counted object: in-flight count -1 + a targeted wake (destruction releases
			// in-flight quota). The actor on this path continues its own loop (self-compensating); the wake
			// serves any other suspended waiters — every decrement pairs one-to-one with a signal, with no
			// per-site reachability exemptions (the mechanical discipline of the dual-order protocol,
			// guarding against evolutionary fragility)
			p.releaseSlot()
			expiredDestroyed++
			if expiredDestroyed >= p.config.maxRetries {
				if countEmpty {
					p.stats.emptyCount.Add(1)
				}
				return nil, ErrPoolEmpty
			}
			continue
		}
		wrapper.SetRetriesNoLock(0)
		// Clear the reference so the wrapper returned to sync.Pool does not retain the user value
		// 清除引用，防止归还 sync.Pool 的 wrapper 滞留用户 value
		wrapper.SetDataNoLock(nil)
		wrapper.Unlock()

		p.elementPool.PutRaw(wrapper)

		// BeforeGet 钩子（P1-7）：在池外调用——此刻 wrapper 已在锁内认领清零并归还 elementPool，
		// 队列锁已随 Done 释放、stopGate 未持有，满足“钩子调用不持任何池内锁”（用户钩子内调用
		// Len 等只读 API 不会死锁；写操作重入仍被文档禁止）。返回 error 或 panic（callHook 转为
		// 携带 panic 值的 error）→ 销毁三件套：认领已在借出时完成（对象已离队，size 已在单点
		// 递减处 -1），此处补 closeFunc + OnClose(value, hookErr) + recordDestroyed(borrow) +
		// releaseSlot（inFlight-1 + 唤醒，销毁释放在途配额）；随后沿用到期销毁的计数模式，
		// 达 maxRetries 上限返回 ErrPoolEmpty，否则 continue 尝试队列下一个。
		// 未设钩子（默认）时本段仅一次 nil 检查，热路径零开销
		// BeforeGet hook (P1-7): invoked outside the pool — at this point the wrapper has been
		// claimed and cleared under its lock and returned to elementPool, the queue lock was
		// released with Done, and stopGate is not held, satisfying "hooks run with no pool lock
		// held" (a user hook calling read-only APIs like Len cannot deadlock; re-entering mutating
		// APIs remains forbidden by the docs). An error return or a panic (callHook converts it
		// into an error carrying the panic value) → the destruction triple: the claim already
		// happened at borrow time (the object left the queue and size was decremented at the
		// single dequeue point), so here closeFunc + OnClose(value, hookErr) +
		// recordDestroyed(borrow) + releaseSlot (inFlight-1 + wake — destruction releases in-flight
		// quota) are added; the expiry-destruction counting pattern is then reused: ErrPoolEmpty at
		// the maxRetries cap, otherwise continue with the next queued element. With no hook
		// configured (the default) this block costs exactly one nil check — zero cost on the hot path
		if p.config.beforeGet != nil {
			if hookErr := callHook("beforeGet", p.config.beforeGet, value); hookErr != nil {
				_ = p.config.closeFunc(value)
				p.config.callback.OnClose(value, hookErr)
				p.stats.recordDestroyed(&p.stats.destroyedByBorrow)
				// 销毁已计数对象：在途计数 -1 + 定向唤醒（releaseSlot 单点收口，双序协议自动合规）。
				// 本路径的行为者继续自身循环（自补偿），唤醒面向其他可能挂起的 waiter
				// Destroying a counted object: in-flight count -1 + a targeted wake (funneling through
				// the single releaseSlot entry point keeps the dual-order protocol automatically
				// satisfied). The actor on this path continues its own loop (self-compensating); the
				// wake serves any other suspended waiters
				p.releaseSlot()
				borrowRejected++
				if borrowRejected >= p.config.maxRetries {
					if countEmpty {
						p.stats.emptyCount.Add(1)
					}
					return nil, ErrPoolEmpty
				}
				continue
			}
		}

		p.stats.totalBorrowed.Add(1)
		return value, nil
	}
}

// GetOrCreate gets an element from the pool or creates a new one if none is available.
// 注意：按需创建不受 maxSize 约束（A1 向后兼容语义）——池空时该路径直接调用 newFunc，不经过在途
// CAS，也不挂起等待，因此在途对象总数可能瞬时超过容量上限；但创建出的对象同样计入在途总数
// inFlight——突破期间 GetWithContext 会挂起等待（在途配额已耗尽），直至对象归还后被销毁或池内
// 腾出配额。创建出的裸值不经 Element 包装直接返回，调用方通过 Put 归还时才被包装并打时间戳。
// 需要有界的阻塞等待语义（在途满时挂起、传播 ctx 超时/取消）请使用 GetWithContext
// GetOrCreate gets an element from the pool or creates a new one if none is available.
// NOTE: on-demand creation is NOT constrained by maxSize (the A1 backward-compatible semantics) — on an
// empty pool this path calls newFunc directly, with no in-flight CAS and no suspension, so the total
// number of live objects may momentarily exceed the capacity cap; a created object still counts toward
// the in-flight total inFlight, however — while the break-through persists GetWithContext suspends (the
// in-flight quota is exhausted) until objects are returned and destroyed or quota frees up. The created
// raw value is handed straight to the caller without an Element wrapper, and is only wrapped and
// timestamped when the caller returns it via Put. For bounded blocking acquisition (suspending when the
// in-flight quota is full, propagating ctx timeout/cancellation) use GetWithContext
func (p *Pool) GetOrCreate() (any, error) {
	// 直接复用 tryAcquire（而非经由 Get 包装器）：调用层数与抽取前完全一致，热路径不引入额外
	// 函数帧；countEmpty=true 使空池计数语义与经由 Get 时逐一相同。tryAcquire 的哨兵恒为裸值，
	// 直接 == 判定即可（哨兵直返纪律）
	// Reuse tryAcquire directly (instead of going through the Get wrapper): the call depth is exactly
	// what it was before the extraction, so the hot path gains no extra frame; countEmpty=true keeps the
	// empty-pool counting semantics identical to going through Get. tryAcquire's sentinels are always
	// bare, so a direct == comparison suffices (the sentinel-direct-return discipline)
	value, err := p.tryAcquire(true)
	if err == nil {
		return value, nil
	}

	// 池已关闭：不创建新元素
	// Pool is closed: do not create new elements
	if err == ErrQueueClosed {
		return nil, err
	}

	value, err = p.config.newFunc()
	if err == nil {
		// D3：newFunc 成功却返回 nil（born-nil）——显式拒绝并返回 ErrValueIsNil（与 Put 拒绝
		// nil 的契约对称），不计数：born-nil 对象若被 +1，其销毁豁免（nil ⟹ 已释放）会令
		// 该在途配额静默永久泄漏
		// D3: newFunc succeeded yet returned nil (born-nil) — reject it explicitly with
		// ErrValueIsNil (symmetric with Put's nil-rejection contract) without counting: had a
		// born-nil object been +1'd, its destruction exemption (nil ⟹ already released) would
		// leak that in-flight slot silently and permanently
		if value == nil {
			return nil, ErrValueIsNil
		}
		// 创建成功即计数；该 value 直接交给调用方，其时间戳在调用方通过 Put 归还入池时打上。
		// 在途计数 +1：A1 突破语义保留（此处不做 inFlight<maxSize 检查），但突破对象同样计入在途——
		// 突破期间 inFlight 可 > maxSize，此时 GetWithContext 挂起等待归还或销毁（正确语义）。
		// totalBorrowed +1（Stats 账本，B3b）：创建出的对象直接移交调用方持有 = 一次借出（调用方
		// 负有归还义务）——ActiveCount = totalBorrowed - totalReturned 的派生要求每个移交调用方的
		// 对象都入账，否则“创建→归还”循环会让派生值系统性负漂
		// Count on successful creation; this value is handed straight to the caller and its timestamps are
		// stamped when the caller returns it to the pool via Put.
		// In-flight count +1: the A1 break-through semantics are preserved (no inFlight<maxSize check here),
		// but a break-through object still counts as in-flight — while the break-through persists inFlight
		// may exceed maxSize, during which GetWithContext suspends until a return or destruction (the
		// correct semantics).
		// totalBorrowed +1 (the Stats ledger, B3b): a created object handed straight to the caller is a
		// borrow (the caller owes its return) — the ActiveCount = totalBorrowed - totalReturned derivation
		// requires every hand-over to be booked, otherwise create→return cycles would drift the derived
		// value systematically negative
		p.stats.totalCreated.Add(1)
		p.stats.totalBorrowed.Add(1)
		p.inFlight.Add(1)
	}
	return value, err
}

// GetWithContext 阻塞式获取一个池内元素，传播 context 的超时与取消。
// 容量语义（P0-B，用户裁决选项 A）：maxSize 封顶在途对象总数 inFlight（已借出 + 池内空闲 + 在途
// 创建预留），对齐 pgxpool.MaxConns / database/sql.MaxOpenConns——在途对象全部借出后，新调用挂起
// 等待归还，绝不突破创建第 maxSize+1 个对象。
// 获取序（D8）：① 尝试从队列借出（与 Get 共享同一轮 tryAcquire 逻辑，含年龄判定与 maxRetries 上限）；
// ② 队列空则通过 inFlight CAS 做有界创建——预留成功即在途 +1，创建成功后预留转为正式在途（不释放），
// 创建出的裸值不入队、直接返回调用方（F6），调用方通过 Put 归还时才被包装并打时间戳；创建失败/panic
// 释放预留并唤醒一个 waiter；③ 在途满（inFlight≥maxSize 且队列空）则注册 waiter 挂起，被 Put 归还、
// 在途配额释放（销毁/创建失败）或 Stop 广播唤醒后回到 ① 重跑完整获取路径（D7 信号量语义，唤醒不
// 携带元素）。每轮循环头检查 ctx，已 done 则快速失败（对照 database/sql 的循环头惯例，P1-A）。
// 归还义务（在途封顶的固有契约，同 pgxpool 的 Release 义务）：取得的对象（无论借出还是新建）必须由
// 调用方 Put 归还（或经池的销毁路径销毁）；不归还即永久泄漏该在途配额，后续 GetWithContext 将无限期
// 挂起。D9 复查在 ctx 已 done 时交还 (value, nil) 也一样——拿到 value 即负有归还义务，不因 ctx 已
// done 而免除。Put 被拒（ErrPoolFull/ErrQueueClosed）时所有权未转移，对象与其在途配额仍由调用方处置。
// 错误契约（D1/D9）：入口 ctx 已 done 时立即返回 ctx.Err()；等待超时返回 context.DeadlineExceeded、
// 取消返回 context.Canceled（不引入新哨兵错误）；池停止后返回 ErrQueueClosed；newFunc 错误原样透传
// 不包装。acquireTimeout>0 且 ctx 无 deadline 时，内部按该配置派生超时上下文；ctx 自带 deadline 时
// 以 ctx 为准，不派生。等待按 FIFO 公平唤醒。
// GetWithContext blocks until an element is available, propagating the context's timeout and cancellation.
// Capacity semantics (P0-B, user-ruled option A): maxSize caps the total in-flight object count inFlight
// (borrowed + idle in the pool + in-flight creation reservations), aligning with pgxpool.MaxConns /
// database/sql.MaxOpenConns — once every in-flight object is borrowed, a new caller suspends and waits
// for a return instead of ever creating the maxSize+1-th object.
// Acquisition order (D8): ① try to borrow from the queue (sharing the same single-round tryAcquire logic
// with Get, including the age check and the maxRetries cap); ② on an empty queue, perform a bounded
// creation through an inFlight CAS — a successful reservation is an in-flight +1 that converts into a
// formal in-flight slot on success (never released), and the created raw value is handed straight to the
// caller without being enqueued (F6), getting wrapped and timestamped only when returned via Put; a
// failed/panicked creation releases the reservation and wakes one waiter; ③ when the in-flight quota is
// full (inFlight≥maxSize with an empty queue), register as a waiter and suspend; once woken by a Put
// return, an in-flight quota release (destruction / failed creation) or the Stop broadcast, re-run the
// full acquire path from ① (D7 semaphore semantics: the wake carries no element). Each loop round checks
// the ctx at its head and fails fast once done (matching database/sql's loop-head convention, P1-A).
// Return obligation (inherent to the in-flight cap, the same contract as pgxpool's Release): an object
// obtained here (borrowed or freshly created) must be returned by the caller via Put (or destroyed through
// a pool path); never returning it leaks that in-flight slot permanently and later GetWithContext calls
// will suspend indefinitely. The same holds when the D9 recheck hands back (value, nil) with the ctx
// already done — holding the value means owning its return, not excused by the done ctx. A rejected Put
// (ErrPoolFull/ErrQueueClosed) transfers no ownership: the value and its in-flight slot stay the caller's
// to dispose of.
// Error contract (D1/D9): an already-done ctx at entry returns ctx.Err() immediately; a wait timeout
// returns context.DeadlineExceeded and a cancellation returns context.Canceled (no new sentinel error is
// introduced); after the pool stops, ErrQueueClosed is returned; a newFunc error is passed through
// unwrapped. When acquireTimeout>0 and the ctx carries no deadline, a timeout context is derived
// internally from the config; a ctx that already carries a deadline takes precedence and no context is
// derived. Waiting waiters are woken in FIFO order.
func (p *Pool) GetWithContext(ctx context.Context) (any, error) {
	// D9：入口 ctx 已 done → 立即快速失败，不进入获取路径
	// D9: an already-done ctx at entry fails fast immediately, never entering the acquire path
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// D9：acquireTimeout>0 且 ctx 无 deadline → 派生超时上下文（派生分配仅在该配置下发生一次）
	// D9: acquireTimeout>0 with a deadline-free ctx derives a timeout context (the derivation allocates
	// once, and only under this configuration)
	if timeout := p.config.acquireTimeout; timeout > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
	}

	for {
		// P1-A：循环头 ctx 检查（对照 database/sql 的 conn 循环头惯例）：轮次之间 ctx 可能已超时/取消
		//（如被唤醒后元素被并发 Get 抢走、或上一轮含任意慢的 newFunc），此处快速失败，不再跑完整一轮
		// 获取；本检查是 D9 复查的补充而非替代——select 就绪随机性仍由 waitForSlot 内部的 D9 兜底
		// P1-A: loop-head ctx check (matching database/sql's conn loop-head convention): the ctx may have
		// expired between rounds (e.g. woken but the element was stolen by a concurrent Get, or the previous
		// round ran an arbitrarily slow newFunc), so fail fast here instead of running one more full acquire
		// round; this check complements rather than replaces the D9 recheck — select readiness randomness is
		// still covered by D9 inside waitForSlot
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// ①② 单轮获取序（队列借出 → 有界创建，D8）：与 waitForSlot 的 D9 重试共用 acquireOnce
		// 单点定义（P2-b）
		// ①② One round of the acquire sequence (queue borrow → bounded creation, D8): shared with
		// waitForSlot's D9 retry through the single acquireOnce definition (P2-b)
		value, acquired, err := p.acquireOnce()
		if err != nil {
			// ErrQueueClosed 或 newFunc 错误：原样透传（value 随错误透传，与 GetOrCreate 契约一致）
			// ErrQueueClosed or a newFunc error: pass through as-is (the value travels with the error,
			// matching the GetOrCreate contract)
			return value, err
		}
		if acquired {
			return value, nil
		}

		// ③ 在途满：注册 waiter 挂起；被唤醒后回到 ① 重跑完整获取路径（D7）
		// ③ In-flight quota full: register a waiter and suspend; once woken, re-run the full acquire path
		// from ① (D7)
		value, acquired, err = p.waitForSlot(ctx)
		if err != nil {
			return value, err
		}
		if acquired {
			return value, nil
		}
	}
}

// acquireOnce 执行一轮 D8 获取序 ①②（队列借出 → 有界创建），从不挂起：GetWithContext 主循环与
// waitForSlot 的 D9 重试共用本方法，D8 获取序在此单点定义（P2-b），未来调整获取序只改这一处。
// 返回契约：(value, true, nil)=已获取（借出或有界创建成功）；(nil, false, nil)=在途满且无所得
// （调用方决定挂起或落回 ctx.Err()）；(value, false, err)=致命错误（ErrQueueClosed、newFunc 错误
// 原样透传——value 所有权随错误移交调用方，与 GetOrCreate 契约一致——或 born-nil 拒绝
// ErrValueIsNil，D3）。
// acquireOnce runs one round of the D8 acquire sequence ①② (queue borrow → bounded creation) and never
// suspends: the GetWithContext main loop and waitForSlot's D9 retry share this method, so the D8 sequence
// is defined at a single point (P2-b) and future adjustments touch only this place.
// Return contract: (value, true, nil) = acquired (borrowed or created within bounds); (nil, false, nil) =
// the in-flight quota is full with nothing gained (the caller decides whether to suspend or fall back to
// ctx.Err()); (value, false, err) = a fatal error (ErrQueueClosed, a newFunc error passed through
// unwrapped — value ownership travels with the error back to the caller, matching the GetOrCreate
// contract — or the born-nil rejection ErrValueIsNil, D3).
func (p *Pool) acquireOnce() (any, bool, error) {
	// ① 队列借出（与 Get 共享单轮获取逻辑；emptyCount 不在此路径记录）
	// ① Queue borrow (sharing the single-round logic with Get; emptyCount is not recorded on this path)
	value, err := p.tryAcquire(false)
	if err == nil {
		return value, true, nil
	}
	if err == ErrQueueClosed {
		return nil, false, err
	}
	// err == ErrPoolEmpty：落入 ② 有界创建
	// err == ErrPoolEmpty: fall through to ② bounded creation

	// ② 有界创建：在途满（errNoCapacity）则报告无所得，由调用方转入挂起
	// ② Bounded creation: when the in-flight quota is full (errNoCapacity) report no gain and let the
	// caller switch to suspension
	value, created, err := p.boundedCreate()
	if created {
		return value, true, nil
	}
	if err != errNoCapacity {
		return value, false, err
	}
	return nil, false, nil
}

// boundedCreate 执行一次有界创建（D8 ②）：通过 inFlight CAS 预留一个在途槽位（预留即在途 +1），
// 预留成功后在锁外调用 newFunc。P0-B 语义：创建成功时预留转为正式在途（inFlight 保持 +1，不释放）——
// 创建出的对象本身就占一个在途配额，直至被销毁才释放；创建出的裸值直接返回调用方、不入队（F6），
// Put 归还不改变在途计数（对象从“借出”回到“池内空闲”，始终在途）。创建失败/panic 时释放在途预留
// （inFlight-1）并定向唤醒一个 waiter。预留窗口把并发创建风暴限制在 maxSize 以内。
// boundedCreate performs one bounded creation (D8 ②): it reserves one in-flight slot via an inFlight CAS
// (the reservation is an in-flight +1), then calls newFunc outside any lock once reserved. P0-B semantics:
// on success the reservation converts into a formal in-flight slot (inFlight stays +1, never released) —
// the created object itself occupies one unit of the in-flight quota until it is destroyed; the created raw
// value goes straight to the caller without being enqueued (F6), and a Put return does not change the
// in-flight count (the object moves from "borrowed" back to "idle in the pool", staying in flight
// throughout). On failure/panic the in-flight reservation is released (inFlight-1) and one waiter is woken
// in a targeted fashion. The reservation window bounds the concurrent creation storm to maxSize.
// panic 安全（P0-A）：预留释放 defer 化——newFunc panic 时 deferred 函数在 panic 展开（unwinding）中
// 必然执行，预留恰好释放一次，随后 panic 原样传播给调用方（不吞用户 panic，无需 recover/re-panic）。
// 手动配对 reserve/release 若被 panic 绕过，在途配额将永久泄漏：累积 maxSize 次后创建、归还唤醒、
// 等待三通道全部死亡，且无任何错误信号指向根因。
// Panic safety (P0-A): the reservation release is defer'd — when newFunc panics, the deferred function
// necessarily runs during the panic unwinding, releasing the reservation exactly once, and the panic then
// propagates to the caller untouched (the user's panic is never swallowed; no recover/re-panic needed).
// Had the manual reserve/release pairing been bypassed by a panic, the in-flight quota would leak
// permanently: after maxSize accumulations the creation, return-wake and waiting channels would all die
// with no error signal pointing at the root cause.
// 返回契约：(value, true, nil)=创建成功；(nil, false, errNoCapacity)=在途满；
// (nil, false, ErrQueueClosed)=池已停止；(nil, false, ErrValueIsNil)=newFunc 返回 (nil, nil)
// （D3，预留已释放）；(value, false, err)=newFunc 错误原样透传。
// Return contract: (value, true, nil) = created; (nil, false, errNoCapacity) = the in-flight quota is
// full; (nil, false, ErrQueueClosed) = pool stopped; (nil, false, ErrValueIsNil) = newFunc returned
// (nil, nil) (D3, the reservation was released); (value, false, err) = newFunc error passed through.
func (p *Pool) boundedCreate() (any, bool, error) {
	if err := p.reserveSlot(); err != nil {
		return nil, false, err
	}
	// P0-A：defer 化配对释放——除“创建成功”外的任何路径（错误返回、panic 展开）都恰好释放一次预留。
	// releaseSlot 内部按 [状态→信号] 顺序先递减 inFlight 再唤醒（与注册侧的 [注册→复查] 共同闭合
	// 丢失唤醒窗口）
	// P0-A: the paired release is defer'd — every path other than "creation succeeded" (an error return or
	// a panic unwind) releases the reservation exactly once. releaseSlot follows the [state → signal]
	// order internally, decrementing inFlight before waking (which, together with the registration side's
	// [register → recheck] order, closes the lost-wakeup window)
	created := false
	defer func() {
		if !created {
			p.releaseSlot()
		}
	}()
	value, err := p.config.newFunc()
	if err != nil {
		// newFunc 错误直返不包装（与 GetOrCreate 同契约，value 所有权随错误透传给调用方）；
		// 在途预留由 defer 释放
		// A newFunc error is returned unwrapped (same contract as GetOrCreate; value ownership travels
		// with the error back to the caller); the in-flight reservation is released by the defer
		return value, false, err
	}
	// D3：newFunc 成功却返回 nil（born-nil）——视同创建失败：created 保持 false，defer 恰好
	// 一次释放在途预留（inFlight-1 + 唤醒，镜像 panic 路径的 P0-A 纪律），并返回明确的
	// ErrValueIsNil（与 Put 拒绝 nil 的契约对称），绝不把 born-nil 计入在途
	// D3: newFunc succeeded yet returned nil (born-nil) — treated as a creation failure: created
	// stays false, the defer releases the in-flight reservation exactly once (inFlight-1 + wake,
	// mirroring the P0-A discipline of the panic path), and an explicit ErrValueIsNil is returned
	// (symmetric with Put's nil-rejection contract); a born-nil value never enters the in-flight
	// count
	if value == nil {
		return nil, false, ErrValueIsNil
	}
	// 创建成功：预留转为正式在途（inFlight 不递减）；创建成功即计数，该 value 直接交给调用方，
	// 时间戳在调用方通过 Put 归还入池时打上（F6）。totalBorrowed +1（Stats 账本，B3b）：与
	// GetOrCreate 的创建路径同理——移交调用方持有 = 一次借出，ActiveCount 派生依赖该入账
	// Creation succeeded: the reservation converts into a formal in-flight slot (inFlight is not
	// decremented); count on successful creation — the value is handed straight to the caller and is
	// stamped when the caller returns it to the pool via Put (F6). totalBorrowed +1 (the Stats ledger,
	// B3b): same rationale as GetOrCreate's creation path — handing the value over to the caller is a
	// borrow, and the ActiveCount derivation depends on booking it
	created = true
	p.stats.totalCreated.Add(1)
	p.stats.totalBorrowed.Add(1)
	return value, true, nil
}

// reserveSlot 通过 CAS 为有界创建预留一个在途槽位（P0-B：判定基准是 inFlight<maxSize，不再是
// size<maxSize）：成功返回 nil（预留生效 = inFlight+1，即“在途创建预留”；创建成功转正式在途、
// 失败/panic 经 releaseSlot 释放）；在途满返回 errNoCapacity；池已停止或队列已关闭返回 ErrQueueClosed。
// 预留不触碰 size：size 继续镜像队列占用（A3 单点递减不变式与 Put 的容量门禁保持不变）
// reserveSlot reserves one in-flight slot for a bounded creation via CAS (P0-B: the criterion is
// inFlight<maxSize, no longer size<maxSize): nil on success (the reservation is active = inFlight+1, the
// "in-flight creation reservation"; a successful creation converts it into a formal in-flight slot while a
// failure/panic releases it through releaseSlot), errNoCapacity when the in-flight quota is full, and
// ErrQueueClosed when the pool is stopped or the queue is closed. The reservation never touches size: size
// keeps mirroring the queue occupancy (the A3 single-decrement invariant and Put's capacity gate stay
// unchanged)
func (p *Pool) reserveSlot() error {
	// 停止快速判定（免锁）：与 GetOrCreate 相同的竞态窗口语义——判定后 Stop 完成、创建仍在途
	// 属既有契约，创建结果由调用方自行处置
	// Lock-free stop fast check: the same race-window semantics as GetOrCreate — Stop completing after
	// this check while a creation is in flight is part of the existing contract, and the created value is
	// the caller's to handle
	if p.stopped.Load() || p.queue.IsClosed() {
		return ErrQueueClosed
	}
	for {
		cur := p.inFlight.Load()
		if cur >= int64(p.config.maxSize) {
			return errNoCapacity
		}
		if p.inFlight.CompareAndSwap(cur, cur+1) {
			return nil
		}
	}
}

// releaseSlot 是在途计数递减的单点入口（inFlight-1，下限 0），随后按“容量释放”唤醒点定向唤醒一个
// waiter——先改状态再发信号（与注册侧的 [注册→复查] 顺序共同闭合丢失唤醒窗口）。两类调用点：
//  1. 创建预留释放：boundedCreate 失败/panic；MinIdle 补齐（refillOne）创建失败/born-nil/panic/
//     入队失败（defer 化恰好一次，镜像 boundedCreate 的 P0-A 纪律）；
//  2. 各销毁路径（tryAcquire 到期销毁、supervise lifetime/idle/ping 销毁、closeAllIdle、Cleanup
//     第二阶段、drainAndClose、initialize 入队失败清理、tombstone GC 的 live 重入队失败——
//     队列被并发关闭时已离队元素的销毁四件套，N1）：销毁释放在途配额，等价于容量释放——
//     若不唤醒，挂起中的 waiter 将无从感知配额释放（每次递减与一次信号一一配对，不做逐点可达性豁免）。
//
// 0 下限：外部经 Put 收编的对象从未经 newFunc +1，其销毁不应把计数压成负数（负漂移会虚增可创建
// 容量、破坏在途封顶）；池自建自销的配对流程在递减点恒 ≥1，下限不会触发。
// releaseSlot is the single entry point for decrementing the in-flight count (inFlight-1, floored at
// zero), followed by a targeted wake of one waiter as the "capacity released" wake point — the state
// change happens before the signal (which, together with the registration side's [register → recheck]
// order, closes the lost-wakeup window). Two categories of call sites:
//  1. creation-reservation releases: boundedCreate on failure/panic; the MinIdle refill (refillOne) on
//     creation failure/born-nil/panic/enqueue failure (defer'd exactly once, mirroring boundedCreate's
//     P0-A discipline);
//  2. every destruction path (tryAcquire expiry destruction, supervise lifetime/idle/ping destruction,
//     closeAllIdle, Cleanup phase 2, drainAndClose, the initialize enqueue-failure cleanup, and the
//     tombstone GC's failed live re-enqueue — the destruction quadruple for an already-dequeued element
//     when the queue was closed concurrently, N1): destruction
//     releases in-flight quota — a capacity release — and without the wake a suspended waiter could never
//     learn about it (every decrement pairs one-to-one with a signal; no per-site reachability exemptions).
//
// The zero floor: a foreign object adopted via Put never went through a newFunc +1, so destroying it must
// not push the counter negative (a negative drift would virtually inflate the creatable capacity and break
// the in-flight cap); on the pool's own create/destroy pairing the counter is always ≥1 at a decrement
// point, so the floor never triggers there.
func (p *Pool) releaseSlot() {
	for {
		cur := p.inFlight.Load()
		if cur <= 0 {
			break
		}
		if p.inFlight.CompareAndSwap(cur, cur-1) {
			break
		}
	}
	p.wakeWaiter()
}

// waitForSlot 把调用方注册为 FIFO waiter 并挂起，直至以下任一情况发生：
//   - 被唤醒：返回 (nil, false, nil)，由外层循环重跑完整获取路径（D7 信号不携带元素）；
//   - ctx 超时/取消：返回 ctx.Err()；若唤醒信号与 ctx.Done 同时就绪（select 就绪随机性），
//     按 D9 非阻塞复查唤醒信号并重试一次获取，防止 spurious timeout；
//   - 池已停止：返回 ErrQueueClosed。
//
// 注册在 stopGate.RLock 之内完成（与 Stop 的写锁广播互斥，无丢失 waiter——D10）；注册后必须复查一次：
// 唤醒者按 [状态变更→发信号] 顺序、本函数按 [注册→复查] 顺序，两个顺序共同封闭“CAS 失败之后、
// 注册完成之前”容量被释放或元素入队的丢失唤醒窗口
// waitForSlot registers the caller as a FIFO waiter and suspends until one of the following happens:
//   - woken: returns (nil, false, nil) so the outer loop re-runs the full acquire path (the D7 signal
//     carries no element);
//   - ctx timeout/cancellation: returns ctx.Err(); when the wake signal and ctx.Done become ready together
//     (select readiness is random), D9's non-blocking recheck of the wake signal retries the acquisition
//     once to prevent a spurious timeout;
//   - pool stopped: returns ErrQueueClosed.
//
// Registration completes inside stopGate.RLock (mutually exclusive with Stop's write-lock broadcast, so no
// waiter is lost — D10); after registering, exactly one recheck runs: wakers follow the [state change →
// signal] order and this function the [register → recheck] order, and the two orders together close the
// lost-wakeup window in which capacity is released or an element is enqueued after the failed CAS but
// before the registration completes
func (p *Pool) waitForSlot(ctx context.Context) (any, bool, error) {
	p.stopGate.RLock()
	if p.stopped.Load() || p.queue.IsClosed() {
		p.stopGate.RUnlock()
		return nil, false, ErrQueueClosed
	}
	w := make(chan struct{})
	p.waitersMu.Lock()
	p.waiters = append(p.waiters, w)
	p.waiterCount.Add(1)
	p.waitersMu.Unlock()
	p.stopGate.RUnlock()

	// 注册后复查（必须发生在 append 之后）：唤醒信号已到达或容量/元素已可用时立即注销并返回外层重试；
	// chan 分配只发生在这条真正可能挂起的慢路径上，快速路径零分配
	// Post-registration recheck (it must run after the append): when the wake signal has already arrived or
	// capacity/an element became available, deregister immediately and let the outer loop retry; the chan
	// allocation only happens on this genuinely suspendable slow path, keeping fast paths allocation-free
	// P0-1 消费端配对义务（复查臂）：consumeWake 命中即信号已消费，而外层循环头（P1-A）可能在
	// acquireOnce 之前因 ctx done 终止本轮——若不重投递，该定向信号就此蒸发，后续存活 waiter 在
	// 对象可借时无限期挂起/伪超时。仅在“已消费 ∧ ctx 已 done”时把信号交还下一 waiter
	//（无 waiter 时成本一次 atomic.Load；spurious wake 属 D7 文档化良性）
	// P0-1 consumer-side pairing obligation (the recheck arm): a consumeWake hit means the signal is
	// consumed, yet the outer loop head (P1-A) may end this round on a done ctx before acquireOnce —
	// without redelivery the directed signal evaporates and later living waiters hang or spuriously
	// time out while an object is borrowable. Redeliver to the next waiter exactly when the signal
	// was consumed AND the ctx is already done (with no waiters the cost is one atomic.Load; a
	// spurious wake is benign per D7)
	if woken := p.consumeWake(w); woken || p.capacityAvailable() {
		p.deregisterWaiter(w)
		if woken && ctx.Err() != nil {
			p.wakeWaiter()
		}
		return nil, false, nil
	}

	p.stats.waitCount.Add(1)
	// F8：时钟读取只出现在注册/唤醒慢路径，快速路径不读时钟
	// F8: clock reads appear only on the registration/wake slow path, never on fast paths
	start := time.Now().UnixNano()
	select {
	case <-w:
		p.stats.totalWaitDurationNs.Add(time.Now().UnixNano() - start)
		// 已被唤醒：唤醒者已在 waitersMu 内将其移出列表并 close，无需注销。
		// P0-1 消费端配对义务（<-w 臂）：本分支已消费定向信号；ctx 已 done 时外层循环头（P1-A）
		// 会在 acquireOnce 之前退出，信号既无获取尝试也无重投递即蒸发（存活 waiter 被饿死，
		// 实证 500/500）——退出前把信号交还下一 waiter 完成配对；ctx 未 done 时正常返回外层
		// 重跑获取路径，信号由获取尝试承接，无需重投递
		// Woken: the waker already removed it from the list and closed it under waitersMu, so there is
		// nothing to deregister. P0-1 consumer-side pairing obligation (the <-w arm): this branch has
		// consumed the directed signal; when the ctx is already done the outer loop head (P1-A) exits
		// before acquireOnce, so the signal would evaporate with neither an acquisition attempt nor a
		// redelivery (living waiters starve — reproduced 500/500). Hand it back to the next waiter
		// before returning to complete the pairing; with a live ctx the outer loop re-runs the acquire
		// path normally and the attempt itself takes over the signal — no redelivery needed
		if ctx.Err() != nil {
			p.wakeWaiter()
		}
		return nil, false, nil
	case <-ctx.Done():
		p.stats.totalWaitDurationNs.Add(time.Now().UnixNano() - start)
		// D9：select 选中 ctx.Done 后必须非阻塞复查自己的唤醒信号（两者同时就绪时 select 随机选择，
		// 防 spurious timeout——workqueue 教训）。注销发现已不在列表 = 已被唤醒者 pop（pop 与 close 在
		// waitersMu 内原子完成，此刻信号必已到达）；注销成功时也再 drain 一次作为双保险
		// D9: after select picks ctx.Done, the waiter's own wake signal must be rechecked non-blockingly
		// (when both are ready select picks randomly — the workqueue lesson against spurious timeouts).
		// A failed deregistration means the waker already popped it (pop and close complete atomically under
		// waitersMu, so the signal has certainly arrived); on a successful deregistration the signal is
		// drained once more as a double safety
		if !p.deregisterWaiter(w) || p.consumeWake(w) {
			// 实际已被唤醒：重试一次获取（与主循环共用 acquireOnce 单点定义的 D8 获取序，P2-b），
			// 无论成败都不再挂起。注意此处不做 ctx 检查——D9 重试的目的正是 ctx.Done 与唤醒信号
			// 同时就绪时优先交付已到手的获取机会（拿到 value 即负有归还义务，不因 ctx done 免除）
			// Actually woken: retry the acquisition once (sharing the single acquireOnce definition of the
			// D8 sequence with the main loop, P2-b) and never suspend again. Note there is deliberately no
			// ctx check here — the point of the D9 retry is to hand over an acquisition opportunity that
			// already arrived when ctx.Done and the wake signal are ready together (holding the value means
			// owning its return, not excused by the done ctx)
			value, acquired, err := p.acquireOnce()
			if err != nil {
				return value, false, err
			}
			if acquired {
				return value, true, nil
			}
			// 重试仍无所得：落回 ctx.Err()
			// The retry gained nothing: fall back to ctx.Err()
		}
		if ctx.Err() == context.DeadlineExceeded {
			p.stats.waitTimeoutCount.Add(1)
		}
		return nil, false, ctx.Err()
	}
}

// consumeWake 非阻塞地检查并消费 w 的唤醒信号。close 语义的信号是电平持续的：
// 即使 close 发生在这次检查（或 select）之前，也同样能被观察到
// consumeWake checks and consumes w's wake signal without blocking. A close-semantics signal is
// level-persistent: even a close that happened before this check (or before the select) is still observed
func (p *Pool) consumeWake(w chan struct{}) bool {
	select {
	case <-w:
		return true
	default:
		return false
	}
}

// capacityAvailable 是注册后的轻量探测：存在空闲在途配额（inFlight<maxSize，可创建）或队列中已有
// 元素（可借出）时返回 true。误报只会让外层多空转一轮（良性 spurious retry）；漏报由唤醒者
// [状态变更→发信号] 与注册侧 [注册→复查] 的双序保证排除——P0-B 后 inFlight 的全部递减点
// （releaseSlot：创建预留释放与各销毁路径）都在状态变更后立即发信号，双序证明逐字迁移
// （原子操作同为 seq-cst，与审计报告风险 1 的 size 版证明同构）
// capacityAvailable is the lightweight post-registration probe: it reports true when an in-flight quota
// slot is free (inFlight<maxSize, creation possible) or the queue holds an element (borrow possible). A
// false positive only costs the outer loop one benign extra round; false negatives are ruled out by the
// dual ordering of the waker's [state change → signal] and the registration side's [register → recheck] —
// after P0-B every inFlight decrement point (releaseSlot: creation-reservation releases and all
// destruction paths) signals immediately after the state change, so the dual-order proof transfers
// verbatim (the atomics are seq-cst alike, isomorphic to the size-based proof in the audit report's risk 1)
func (p *Pool) capacityAvailable() bool {
	return p.inFlight.Load() < int64(p.config.maxSize) || p.queue.Len() > 0
}

// wakeWaiter 定向唤醒等待最久的一个 waiter（FIFO 头部，公平唤醒）。唤醒信号不携带元素（D7）：
// 被唤醒者重跑完整获取路径，元素被并发 Get 抢走属良性 spurious wake。
// pop 与 close 在 waitersMu 内原子完成，与注销侧构成互斥对偶：“注销成功=未被唤醒”、
// “发现已不在列表=必已被唤醒”。无 waiter 时快速路径成本为一次 atomic.Load，零锁零分配（D10）。
// 唤醒点（P0-B + B3b + N1）：Put 入队成功后、releaseSlot 的在途配额释放（创建预留释放与全部
// 销毁路径）、tryAcquire 的 tombstone 回收（协议统一性保留——挂起 waiter 的复查条件含
// Len==0，而彼时 Len 0→正的唯一来源是必伴唤醒的 Put，故该点打不到挂起者，良性）、
// MinIdle 补齐入队成功后（refillOne——竞态可达的承重信号，绝非防御冗余：挂起 waiter 被
// 前序 releaseSlot 唤醒后与补齐 goroutine 竞争同一配额，若补齐先赢（waiter 的有界创建得
// errNoCapacity、复查见 inFlight==maxSize 且对象尚未入队而重新挂起），补齐入队的这次唤醒
// 是 waiter 在“对象已可借出”状态下唯一可感知的信号——删除即真实丢失唤醒、无限期挂起。
// 唤醒完备性不变式要求每个 queue.Len() 0→正迁移点必伴唤醒，与 Put 同族）、
// tombstone GC（N1：墓碑回收分支为协议统一性保留，与 tryAcquire 同理；live 重入队分支是
// 第四个 Len 0→正迁移点，承重性与补齐唤醒同源——waiter 可能在 GC 的离队→重入队窗口内
// 重新挂起）、Stop 广播。
// 消费端配对义务（P0-1 教训）：每个被消费的定向信号必须配对一次获取尝试或重投递；任何在
// “已消费唤醒”与“获取尝试”之间新增的退出点（如循环头快速失败）必须处理信号交接
// wakeWaiter wakes exactly one waiter — the one waiting longest (the FIFO head, fair waking). The wake
// signal carries no element (D7): the woken caller re-runs the full acquire path, and losing the element
// to a concurrent Get is a benign spurious wake. The pop and the close complete atomically under
// waitersMu, forming a mutually exclusive duality with deregistration: "deregistered = not woken" and
// "missing from the list = certainly woken". With no waiters the fast path costs one atomic.Load, taking
// no lock and allocating nothing (D10). Wake points (P0-B + B3b + N1): after a successful Put enqueue,
// an in-flight quota release via releaseSlot (creation-reservation releases and every destruction
// path), a tombstone reclaim in tryAcquire (kept for protocol uniformity — a suspended waiter's
// recheck requires Len==0 while the only Len 0→positive source back then was the wake-paired Put,
// so this point cannot hit a suspended waiter; benign), after a successful MinIdle refill enqueue
// (refillOne — a race-reachable LOAD-BEARING signal, never defensive redundancy: a suspended
// waiter woken by the preceding releaseSlot races the refill goroutine for the same quota, and
// when the refill wins (the waiter's bounded creation gets errNoCapacity and its recheck sees
// inFlight==maxSize with the object not yet enqueued, so it re-suspends), this refill-enqueue
// wake is the only signal the waiter can ever observe while the object is borrowable — deleting
// it is a genuine lost wakeup and an indefinite suspension. The wake-completeness invariant
// requires every queue.Len() 0→positive transition point to pair with a wake, of the same family
// as Put), the tombstone GC (N1: the tombstone-reclaim branch is kept for protocol uniformity,
// same rationale as tryAcquire's; the live re-enqueue branch is the fourth Len 0→positive
// transition point, load-bearing for the same reason as the refill wake — a waiter may
// re-suspend inside the GC's dequeue→re-enqueue window), and the Stop broadcast.
// Consumer-side pairing obligation (the P0-1 lesson): every consumed directed signal must pair with
// either one acquisition attempt or one redelivery; any new exit point added between "the wake was
// consumed" and "the acquisition attempt" (such as a loop-head fast failure) must handle the signal
// handover
//
// GO-2 快慢拆分：本体只保留可内联快检——无 waiter 的常规场景（waiterCount==0）在调用方内
// 完成一次 atomic.Load 加可预测分支，不再支付不可内联函数调用；持锁 pop/close 的慢路径移入
// wakeWaiterSlow。入口名不变：全部唤醒调用点（Put/releaseSlot/tryAcquire tombstone/refillOne/
// GC live 重入队）与双序协议（先状态迁移后信号）构造性保留，快检语义与原函数首行逐字等价
// GO-2 fast/slow split: the body keeps only an inlinable fast check — in the ordinary no-waiter
// scenario (waiterCount==0) one atomic.Load plus a predictable branch now completes inside the
// caller, no longer paying a non-inlinable function call; the lock-holding pop/close slow path
// moves into wakeWaiterSlow. The entry name is unchanged: every wake call site (Put/releaseSlot/
// tryAcquire tombstone/refillOne/GC live re-enqueue) and the dual-order protocol (state transition
// before signal) are structurally preserved, and the fast check is verbatim equivalent to the
// original function's first lines
func (p *Pool) wakeWaiter() {
	if p.waiterCount.Load() != 0 {
		p.wakeWaiterSlow()
	}
}

// wakeWaiterSlow 是 wakeWaiter 的慢路径：在 waitersMu 内原子完成 FIFO 头部的 pop 与 close
// （定向唤醒），与注销侧构成互斥对偶。仅在 waiterCount!=0 时经快检进入，锁形态与拆分前
// 逐字一致（waitersMu 仍为叶子锁，锁序拓扑不变）
// wakeWaiterSlow is wakeWaiter's slow path: the FIFO-head pop and close (the directed wake)
// complete atomically under waitersMu, forming a mutually exclusive duality with deregistration.
// It is entered through the fast check only when waiterCount!=0, and the lock shape is verbatim
// identical to the pre-split form (waitersMu remains a leaf lock; the lock-order topology is
// unchanged)
func (p *Pool) wakeWaiterSlow() {
	p.waitersMu.Lock()
	if len(p.waiters) > 0 {
		w := p.waiters[0]
		// 清除底层数组中的旧通道引用，防止内存滞留
		// Clear the stale channel reference in the backing array to avoid retention
		p.waiters[0] = nil
		p.waiters = p.waiters[1:]
		p.waiterCount.Add(-1)
		close(w)
	}
	p.waitersMu.Unlock()
}

// broadcastWaiters 唤醒全部 waiter 并清空列表（Stop 在 stopGate 写锁内调用）：
// 所有挂起调用者重跑获取路径时看到停止标记，统一返回 ErrQueueClosed
// broadcastWaiters wakes every waiter and clears the list (called by Stop under the stopGate write lock):
// re-running the acquire path, all suspended callers observe the stop flag and uniformly return
// ErrQueueClosed
func (p *Pool) broadcastWaiters() {
	if p.waiterCount.Load() == 0 {
		return
	}
	p.waitersMu.Lock()
	for i, w := range p.waiters {
		p.waiters[i] = nil
		close(w)
	}
	p.waiters = nil
	p.waiterCount.Store(0)
	p.waitersMu.Unlock()
}

// deregisterWaiter 把 w 从等待列表中移除（waiter 注销无泄漏）。返回 true 表示成功移除（未被唤醒）；
// 返回 false 表示 w 已不在列表中——唤醒者的 pop 与 close 在 waitersMu 内原子完成，
// 此刻唤醒信号必已到达，调用方应按“已被唤醒”处理（D9 的注销 drain 语义）
// deregisterWaiter removes w from the waiting list (waiter deregistration never leaks). True means it was
// removed (it was not woken); false means w is no longer in the list — the waker's pop and close complete
// atomically under waitersMu, so the wake signal has certainly arrived and the caller must treat it as
// "already woken" (D9's deregistration-drain semantics)
func (p *Pool) deregisterWaiter(w chan struct{}) bool {
	p.waitersMu.Lock()
	for i, cur := range p.waiters {
		if cur == w {
			// P2-a：左移覆盖后把尾部 stale 槽位置 nil 再收缩（与 wakeWaiter 的 nil 卫生对称），
			// 防止已注销的死通道被底层数组滞留至下次 append 覆盖/扩容
			// P2-a: after the left-shift overwrite, nil out the stale tail slot before shrinking
			// (symmetric with wakeWaiter's nil hygiene), so the deregistered dead channel is not retained
			// by the backing array until the next append overwrite/reallocation
			copy(p.waiters[i:], p.waiters[i+1:])
			p.waiters[len(p.waiters)-1] = nil
			p.waiters = p.waiters[:len(p.waiters)-1]
			p.waiterCount.Add(-1)
			p.waitersMu.Unlock()
			return true
		}
	}
	p.waitersMu.Unlock()
	return false
}

// Put adds a new element to the pool
// 向连接池中添加新元素。设置 AfterPut 钩子时，入队前先在池外执行该钩子：钩子拒绝（error 或
// panic 转 error）则对象被池主动销毁（closeFunc + OnClose + destroyedByBorrow 分类 +
// releaseSlot）、不入队，且该 error 被原样透传给调用方——语义为“对象被池主动拒绝并销毁”，
// 区别于 ErrPoolFull（容量拒绝，所有权不转移）；被拒绝的对象已关闭，不得复用或再次归还。
// 账本口径（N2）：钩子拒绝的归还计入 TotalReturned——所有权已移交回池、归还义务已履行，
// ActiveCount 不漂移；ErrPoolFull 拒绝不入账（所有权未转移）
// Put adds a new element to the pool. When an AfterPut hook is configured, it runs outside the pool
// before the enqueue: a rejection by the hook (an error, or a panic converted into one) makes the
// pool actively destroy the object (closeFunc + OnClose + the destroyedByBorrow category +
// releaseSlot) without enqueuing it, and that error is passed through to the caller as-is — the
// semantics being "the object was actively rejected and destroyed by the pool", distinct from
// ErrPoolFull (a capacity rejection where ownership never transfers); a rejected object has been
// closed and must not be reused or returned again. Ledger semantics (N2): a hook-rejected return
// counts into TotalReturned — ownership has already transferred back to the pool and the return
// obligation is fulfilled, so ActiveCount never drifts; an ErrPoolFull rejection books nothing
// (ownership never transfers)
func (p *Pool) Put(data any) error {
	// 拒绝 nil 值
	// Reject nil values
	if data == nil {
		return ErrValueIsNil
	}

	// AfterPut 钩子（P1-7）：整段以 nil 检查门禁——未设钩子时热路径只多一次分支（性能护栏）。
	// 执行序：免锁停止快检（Stop 后不执行 AfterPut，直接按既有语义返回 ErrQueueClosed）→
	// 锁外调用钩子（此刻不持 stopGate/queue.lock/element.mu/waitersMu 任何池内锁，钩子内只读
	// API 调用不死锁；写操作重入被文档禁止）→ 拒绝则销毁三件套 + releaseSlot（池自建对象
	// 配对释放在途配额；外部收编对象由 releaseSlot 的 0 下限兜底）+ 透传钩子 error；
	// 通过则落入下方既有入队流程（stopGate.RLock + size CAS + 包装入队），语义逐一不变
	// AfterPut hook (P1-7): the whole block is gated by a nil check — with no hook configured the
	// hot path pays exactly one extra branch (the performance guard). Execution order: the lock-free
	// stop fast check (AfterPut never runs after Stop; ErrQueueClosed is returned per the existing
	// semantics) → the hook invocation outside every lock (no stopGate/queue.lock/element.mu/
	// waitersMu is held here, so read-only API calls inside the hook cannot deadlock; re-entering
	// mutating APIs is forbidden by the docs) → on rejection the destruction triple + releaseSlot
	// (a pool-created object releases its in-flight quota in pairs; a foreign adopted object is
	// covered by releaseSlot's zero floor) + passing the hook error through; on success control
	// falls into the existing enqueue flow below (stopGate.RLock + size CAS + wrap & enqueue),
	// preserving its semantics one by one
	if p.config.afterPut != nil {
		if p.stopped.Load() || p.queue.IsClosed() {
			return ErrQueueClosed
		}
		if hookErr := callHook("afterPut", p.config.afterPut, data); hookErr != nil {
			// N2：归还入账先于销毁四件套——钩子拒绝时调用方已履行归还义务、对象所有权已
			// 移交回池（池选择销毁而非入队），与入队成功路径的 totalReturned+1 口径对称；
			// 不补记会使 ActiveCount=TotalBorrowed-TotalReturned 每次拒绝永久 +1 漂移
			//（对照 BeforeGet 拒绝：钩子在 totalBorrowed+1 之前执行、从未入账故无漂移——
			// 曾有的不对称正是疏漏而非设计）。ErrPoolFull 拒绝仍不入账（所有权未转移，
			// 对象与配额留在调用方）——三种拒绝的账本边界由此清晰分界
			// N2: book the return before the destruction quadruple — on a hook rejection the
			// caller has already fulfilled its return obligation and ownership has transferred
			// back to the pool (the pool chooses to destroy instead of enqueue), symmetric with
			// the successful-enqueue path's totalReturned+1; omitting this booking would drift
			// ActiveCount=TotalBorrowed-TotalReturned by +1 permanently per rejection (contrast
			// the BeforeGet rejection: the hook runs before totalBorrowed+1 and never books, so
			// it cannot drift — the former asymmetry was an oversight, not a design). An
			// ErrPoolFull rejection still books nothing (ownership never transfers; the value and
			// its quota stay the caller's) — the ledger boundaries of the three rejections are
			// now cleanly separated
			p.stats.totalReturned.Add(1)
			_ = p.config.closeFunc(data)
			p.config.callback.OnClose(data, hookErr)
			p.stats.recordDestroyed(&p.stats.destroyedByBorrow)
			// 销毁已计数对象：在途计数 -1 + 定向唤醒（releaseSlot 单点收口，双序协议自动合规）
			// Destroying a counted object: in-flight count -1 + a targeted wake (funneling through
			// the single releaseSlot entry point keeps the dual-order protocol automatically
			// satisfied)
			p.releaseSlot()
			return hookErr
		}
	}

	// 持有 Stop 门闩读锁，防止 Stop 窗口期并发 Put 泄漏元素
	// Hold the stop gate read lock to prevent concurrent Put from leaking elements during the Stop window
	p.stopGate.RLock()
	defer p.stopGate.RUnlock()

	// 检查连接池是否已停止或队列是否已关闭（免锁快速路径）
	// Check if the pool is stopped or the queue is closed (lock-free fast path)
	if p.stopped.Load() || p.queue.IsClosed() {
		return ErrQueueClosed
	}

	// CAS 预留一个容量槽位；池满时返回预分配哨兵 ErrPoolFull（零分配，容量检查位于 closed 检查之后）。
	// P0-B 后 Put 的容量门禁仍基于 size（队列占用，A3 不变），与在途封顶 inFlight 解耦：归还借出对象
	// 不受 inFlight 约束（对象本就计入在途，归还只是从“借出”回到“空闲”）；被拒时所有权不转移，
	// 对象与其在途配额仍由调用方处置（inFlight 不变）
	// Reserve a capacity slot via CAS; return the pre-allocated sentinel ErrPoolFull when full (zero
	// allocation, the capacity check runs after the closed check). After P0-B, Put's capacity gate remains
	// size-based (queue occupancy, A3 unchanged), decoupled from the in-flight cap inFlight: returning a
	// borrowed object is not constrained by inFlight (the object already counts as in-flight; the return
	// only moves it from "borrowed" back to "idle"); on a rejection ownership never transfers and the value
	// with its in-flight slot stays the caller's to dispose of (inFlight unchanged)
	for {
		cur := p.size.Load()
		if cur >= int64(p.config.maxSize) {
			p.stats.fullPutCount.Add(1)
			return ErrPoolFull
		}
		if p.size.CompareAndSwap(cur, cur+1) {
			break
		}
	}

	element := p.elementPool.Get()
	// E1 修复：先打时间戳，再带锁写 data，最后经 queue.Put 发布——两步必须在发布前的同一独占
	// 窗口内完成且对 supervise 的锁内读一致。原“发布前独占、无需加锁”的假设被 supervise 的陈旧
	// Values() 快照打破：supervise 持快照遍历期间，快照中的 wrapper 可被并发 Get 离队 → PutRaw
	// 回收 → 被本 Put 复用，无锁写与 supervise 的锁内读之间没有任何 happens-before 边（内存模型
	// 意义上的数据竞争，interface 双字可撕裂）；且“data 已写、expireAt 未打”的中间态在
	// lifetimeOn 下命中 fail-closed（expireAt==0 视为到期）误判，把刚归还的健康对象误销毁。
	// 带锁写与锁内读经 element.mu 互斥（竞争消除）；先打戳保证 supervise 若见 data!=nil 必见
	// 新 expireAt（mu 的 happens-before 传递 atomic 写，误判消除）；残余仅为对尚未入队的 value
	// 提前 ping（良性）。wrapper 此刻仍为本 goroutine 独占，锁恒无争用（一次快路径 CAS 成本）。
	// 锁序核验：新增边 stopGate→element.mu——element.mu 是叶子锁（持有期间不获取任何其他锁），
	// 拓扑序 stopGate→queue.lock→{element.mu, waitersMu} 仍无环
	// E1 fix: stamp the timestamps first, then write data under the lock, and publish via queue.Put
	// last — both steps must complete inside the same exclusive pre-publication window, consistently
	// for supervise's under-lock reads. The original "exclusive before publication, no lock needed"
	// assumption was broken by supervise's stale Values() snapshot: while supervise walks a snapshot,
	// a wrapper in it can be dequeued by a concurrent Get → recycled via PutRaw → reused by this
	// Put, leaving no happens-before edge between the lock-free write and supervise's under-lock
	// read (a data race in the memory-model sense; an interface value is two words and can tear);
	// moreover the intermediate state "data written, expireAt not yet stamped" hits the fail-closed
	// misjudgment under lifetimeOn (expireAt==0 counts as expired), wrongly destroying a
	// just-returned healthy object. The locked write is mutually exclusive with the under-lock read
	// via element.mu (the race is gone); stamping first guarantees supervise sees the new expireAt
	// whenever it sees data!=nil (the mutex's happens-before carries the atomic writes, so the
	// misjudgment is gone); the only residue is a premature ping on a not-yet-enqueued value
	// (benign). The wrapper is still exclusively owned by this goroutine here, so the lock is always
	// uncontended (one fast-path CAS). Lock-order check: the new stopGate→element.mu edge — element.mu
	// is a leaf lock (no other lock is acquired while holding it), so the topology
	// stopGate→queue.lock→{element.mu, waitersMu} stays acyclic
	// GO-1：门禁提升至调用点——与原 stampEnqueue 函数内门禁逐字等价（见 ageEvictionOn 字段注释）；
	// 守卫不改变 E1 顺序纪律：启用时仍是先打戳、再带锁写 data、最后发布
	// GO-1: the gate is hoisted to the call site — verbatim equivalent to the original
	// in-stampEnqueue gate (see the ageEvictionOn field comment); the guard leaves the E1 ordering
	// discipline untouched: when enabled it is still stamp first, then the locked data write, then publish
	if p.ageEvictionOn {
		p.stampEnqueue(element)
	}
	element.SetData(data)
	if err := p.queue.Put(element); err != nil {
		// 入队失败：先释放已预留的容量槽位，再归还 wrapper。
		// P2-c 前提固化（此处不唤醒 waiter）：queue.Put 失败 ⟹ 队列已关闭 ⟹ Stop 的广播必然先行
		//（stopped.Store → broadcastWaiters → Shutdown 依序执行，且 waiter 注册完整位于 stopGate.RLock
		// 段内），不存在“挂起中的 waiter 跨越队列关闭”；本回滚是 size +1/-1 净零，未释放任何容量，
		// 无信号义务（若未来可插拔队列引入“未关闭但 Put 失败”的语义，须在此补一次 wakeWaiter）
		// Enqueue failed: release the reserved capacity slot first, then return the wrapper.
		// P2-c pinned premise (no waiter wake here): queue.Put failing ⟹ the queue is closed ⟹ Stop's
		// broadcast has necessarily already run (stopped.Store → broadcastWaiters → Shutdown in order, and
		// waiter registration completes entirely inside the stopGate.RLock section), so no suspended waiter
		// can straddle the queue closure; this rollback is a net-zero size +1/-1 that releases no capacity
		// and carries no signal obligation (should a future pluggable queue introduce "Put fails while not
		// closed", a wakeWaiter must be added here)
		p.size.Add(-1)
		p.elementPool.Put(element)
		// 队列被并发关闭时统一映射为 ErrQueueClosed；其余错误（如幂等队列的 AlreadyExist）原样透传
		// Map a concurrently closed queue to ErrQueueClosed; pass through other errors (e.g. AlreadyExist from idempotent queues) as-is
		if p.queue.IsClosed() {
			return ErrQueueClosed
		}
		return err
	}
	p.stats.totalReturned.Add(1)
	// wake-after-enqueue（D7）：入队成功后定向唤醒一个 waiter，唤醒信号不携带元素，
	// 被唤醒者重跑完整获取路径；被并发 Get 抢走属良性 spurious wake。此处已释放 queue
	// 相关临界区（D10），无 waiter 时探测成本为一次 atomic.Load
	// Wake-after-enqueue (D7): after a successful enqueue, wake one waiter. The wake signal carries no
	// element — the woken caller re-runs the full acquire path, and losing the element to a concurrent
	// Get is a benign spurious wake. The queue critical section is already released here (D10); with no
	// waiters the probe costs a single atomic.Load
	p.wakeWaiter()
	return nil
}

// Len returns the current number of elements in the pool
// 返回连接池中当前的元素数量
func (p *Pool) Len() int {
	return p.queue.Len()
}

// monitor runs the pool monitoring loop
// 运行连接池监控循环
func (p *Pool) monitor() {
	ticker := time.NewTicker(time.Millisecond * time.Duration(p.config.scanInterval))
	defer func() {
		ticker.Stop()
		p.wg.Done()
	}()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.supervise()
		}
	}
}

// supervise checks and maintains pool elements status
// 检查并维护连接池元素状态。判定序：lifetime 到期 → idle 超时（P1-3，尊重 minIdle 下限）→ ping
// 健康检查（先到期先销毁，不浪费健康检查）；遍历中顺带统计有效空闲数（data!=nil 且未销毁）与
// 既有 tombstone 数，末尾先执行 tombstone GC（N1，仅存在墓碑时）再据此触发 MinIdle 异步补齐（P1-4）
// supervise checks and maintains pool elements status. Check order: lifetime expiry → idle timeout
// (P1-3, respecting the minIdle floor) → the ping health check (an evicted object never wastes a
// health check); the pass also counts the effective idle objects (data!=nil and not destroyed) and
// the pre-existing tombstones on the side, and at the end the tombstone GC runs first (N1, only
// when tombstones exist), followed by the MinIdle async refill trigger (P1-4)
func (p *Pool) supervise() {
	// 年龄/空闲判定门禁与本轮统一时刻：maxLifetime 与 maxIdleTime 均为 0（默认）时不读时钟，
	// 判定侧零开销短路；每轮只取一次 now，避免逐元素重复读时钟（F8，与 database/sql 的
	// connectionCleaner 同策略）
	// Age/idle-check gate and a single timestamp for this pass: with both maxLifetime and
	// maxIdleTime at 0 (the default) no clock is read and the checks short-circuit at zero cost;
	// now is read once per pass instead of per element (F8, the same strategy as database/sql's
	// connectionCleaner)
	lifetimeOn := p.config.maxLifetime > 0
	idleOn := p.config.maxIdleTime > 0
	var now int64
	if lifetimeOn || idleOn {
		now = time.Now().UnixNano()
	}
	// minIdle 与本轮有效空闲数的 running count（遍历中顺带统计，避免额外 O(N) 遍历）：
	// 每个 data!=nil 且 retries>=0 的元素 +1；本轮每销毁一个（lifetime/idle/ping）-1——
	// 遍历结束时 idleCount 即“仍存活的有效空闲数”，同时服务 idle 淘汰的 minIdle 下限护栏
	// 与末尾补齐的 deficit 计算
	// minIdle and the running count of effective idle objects in this pass (counted on the side
	// during the traversal, avoiding an extra O(N) pass): +1 for every element with data!=nil and
	// retries>=0; -1 for every destruction in this pass (lifetime/idle/ping) — at the end of the
	// pass idleCount is the number of still-live effective idle objects, serving both the minIdle
	// floor guard of idle eviction and the deficit computation of the closing refill trigger
	minIdle := p.config.minIdle
	idleCount := 0
	// tombstoneCount 是本轮快照遍历中见到的既有 tombstone 数（data==nil 或 retries<0 的元素，
	// 即下面两个跳过分支；与 idleCount 同一次遍历顺带统计，零额外 O(N)）。它是末尾 tombstone
	// GC 的触发门禁（N1）：>0 才执行 GC——纯活对象池零 GC 开销。注意本轮遍历中新销毁的元素
	// 不计入：它们在快照检视时刻仍是活对象（被 idleCount 计过后才销毁），留到下一轮扫描由
	// GC 回收——这保持了“销毁后的那个扫描周期内 tombstone 仍占容量”的既有 A3 可观察语义
	// tombstoneCount is the number of pre-existing tombstones seen by this pass's snapshot
	// traversal (elements with data==nil or retries<0 — the two skip branches below; counted on
	// the side during the same traversal as idleCount, at zero extra O(N) cost). It is the
	// trigger gate of the closing tombstone GC (N1): the GC runs only when it is >0, so a pool of
	// purely live objects pays zero GC cost. Elements destroyed during this very traversal are
	// deliberately NOT counted: at snapshot-inspection time they were still live (counted into
	// idleCount before being destroyed), and they are reclaimed by the GC of the next pass — this
	// preserves the existing observable A3 semantics that a tombstone keeps occupying capacity
	// for the scan cycle in which it was created
	tombstoneCount := 0

	// 使用 Values() 获取队列短锁快照，不再持有队列锁执行用户 I/O
	// Take a short-lock snapshot of the queue via Values() instead of holding the queue lock during user I/O
	for _, item := range p.queue.Values() {
		element := item.(*pool.Element)
		element.Lock()
		value := element.GetDataNoLock()
		if value == nil {
			element.Unlock()
			tombstoneCount++
			continue
		}
		retryCount := int(element.GetRetriesNoLock())
		if retryCount < 0 {
			element.Unlock()
			tombstoneCount++
			continue
		}
		idleCount++
		// 年龄判定先于 pingFunc：到期对象不再浪费一次健康检查，直接走与 ping 失败销毁相同的
		// tombstone 认领路径（锁内置 nil + retries=-1，锁外 closeFunc/OnClose），锁序与清理语义完全一致；
		// 元素留在队列中占用容量，wrapper 由后续 Get 或下一轮扫描末尾的 tombstone GC（N1）回收，
		// 故此处不动 size（size 只在离队点单点递减）
		// The age check runs before pingFunc: an expired object skips the health check entirely and takes the
		// same tombstone claim path as a ping-failure destroy (nil + retries=-1 under the lock, closeFunc/OnClose
		// outside it), with identical lock order and cleanup semantics; the element stays in the queue occupying
		// capacity and its wrapper is reclaimed by a later Get or by the tombstone GC at the end of the next
		// pass (N1), so size is not touched here (size is decremented at the single dequeue point only)
		if lifetimeOn && now >= element.GetExpireAt() {
			element.SetDataNoLock(nil)
			element.SetRetriesNoLock(-1)
			element.Unlock()
			err := p.config.closeFunc(value)
			p.config.callback.OnClose(value, err)
			p.stats.recordDestroyed(&p.stats.destroyedByLifetime)
			// 销毁已计数对象：在途计数 -1 + 定向唤醒（销毁释放在途配额，等价于容量释放）。
			// tombstone 仍留队列占用 size 容量（A3 不变），但在途配额即刻释放——全部对象借出后
			// 空闲对象被后台销毁正是挂起 waiter 的主要配额释放来源之一
			// Destroying a counted object: in-flight count -1 + a targeted wake (destruction releases
			// in-flight quota — a capacity release). The tombstone still occupies size capacity in the
			// queue (A3 unchanged), but the in-flight quota is released immediately — with everything
			// borrowed out, background destruction of idle objects is one of the primary quota-release
			// sources for suspended waiters
			p.releaseSlot()
			// 有效空闲数 -1：本轮后续 idle 淘汰的 minIdle 下限护栏与末尾补齐的 deficit 都依赖
			// 该 running count 反映销毁后的真实水位
			// Effective idle count -1: the minIdle floor guard of the idle evictions later in this
			// pass and the deficit of the closing refill trigger both rely on this running count
			// reflecting the true post-destruction water level
			idleCount--
			continue
		}
		// idle 超时判定（P1-3）：位于 lifetime 之后、ping 之前——被回收对象不浪费一次健康检查。
		// minIdle 下限护栏：仅当本轮有效空闲数（含当前元素）高于 minIdle 时才允许淘汰，且每淘汰
		// 一个即 idleCount-1，保证同轮绝不打穿最小水位（对齐 HikariCP idleTimeout 仅在
		// idle > minimumIdle 时生效的语义）。idleSince==0 为 fail-closed（等同立即超时）——
		// F4 门禁保证启用 maxIdleTime 时一切入队对象必被打戳，该形态结构性不可达，语义与
		// expireAt==0 的 fail-closed 一致。销毁三件套与 lifetime/ping 完全同构：锁内认领
		// （data=nil + retries=-1）→ 锁外 closeFunc + OnClose → recordDestroyed(destroyedByIdle)
		// → releaseSlot（inFlight-1 + 唤醒）；tombstone 留队列占 size（A3 不变）
		// Idle-timeout check (P1-3): placed after the lifetime check and before the ping — a
		// reclaimed object never wastes a health check. minIdle floor guard: eviction is allowed
		// only while the effective idle count of this pass (current element included) exceeds
		// minIdle, and every eviction decrements idleCount, so the pass can never break through
		// the minimum water level (aligning with HikariCP's idleTimeout, which only applies when
		// idle > minimumIdle). idleSince==0 is fail-closed (counted as timed out immediately) —
		// the F4 gate guarantees every enqueued object is stamped once maxIdleTime is enabled, so
		// that shape is structurally unreachable, matching the fail-closed semantics of
		// expireAt==0. The destruction triple is fully isomorphic to lifetime/ping: claim under
		// the lock (data=nil + retries=-1) → closeFunc + OnClose outside it →
		// recordDestroyed(destroyedByIdle) → releaseSlot (inFlight-1 + wake); the tombstone stays
		// in the queue occupying size (A3 unchanged)
		if idleOn && idleCount > minIdle && now-element.GetIdleSince() > int64(p.config.maxIdleTime) {
			element.SetDataNoLock(nil)
			element.SetRetriesNoLock(-1)
			element.Unlock()
			err := p.config.closeFunc(value)
			p.config.callback.OnClose(value, err)
			p.stats.recordDestroyed(&p.stats.destroyedByIdle)
			// 销毁已计数对象：在途计数 -1 + 定向唤醒（与 lifetime 销毁同理——空闲回收释放
			// 在途配额，是挂起 waiter 的配额释放来源之一）
			// Destroying a counted object: in-flight count -1 + a targeted wake (same rationale as
			// the lifetime destruction — an idle reclaim releases in-flight quota, one of the
			// quota-release sources for suspended waiters)
			p.releaseSlot()
			idleCount--
			continue
		}
		if p.config.pingFunc(value, retryCount) {
			element.SetRetriesNoLock(0)
			element.Unlock()
			p.config.callback.OnPingSuccess(value)
			continue
		}
		retryCount++
		if retryCount >= p.config.maxRetries {
			// 先在元素锁内置 nil 认领，与 Cleanup 互斥，杜绝双重关闭
			// Claim by setting nil under the element lock first, exclusive with Cleanup to prevent double close
			element.SetDataNoLock(nil)
			element.SetRetriesNoLock(-1)
			element.Unlock()
			err := p.config.closeFunc(value)
			p.config.callback.OnClose(value, err)
			p.stats.recordDestroyed(&p.stats.destroyedByPing)
			// 销毁已计数对象：在途计数 -1 + 定向唤醒（与上面的 lifetime 销毁同理）
			// Destroying a counted object: in-flight count -1 + a targeted wake (same rationale as the
			// lifetime destruction above)
			p.releaseSlot()
			// 有效空闲数 -1（与 lifetime 销毁同理：running count 反映销毁后的真实水位）
			// Effective idle count -1 (same rationale as the lifetime destruction: the running
			// count reflects the true post-destruction water level)
			idleCount--
		} else {
			element.SetRetriesNoLock(int64(retryCount))
			element.Unlock()
			p.config.callback.OnPingFailure(value)
		}
	}

	// N1（墓碑泵根治）：tombstone GC——快照中见到既有 tombstone 才执行（纯活池零开销），
	// 且必须先于 MinIdle 补齐触发：GC 把 size 回落到真实活对象数，补齐基于回落后的准确
	// size 入队活对象。若顺序颠倒（或不 GC），“销毁留 tombstone 占 size → tombstone 压低
	// idleCount → deficit>0 触发补齐 → 补齐入队免 size 门禁 → 下轮再销毁再补齐”构成每周期
	// 净增 ~minIdle 个 tombstone 的确定性无界增长（低 Get 流量下无人回收 tombstone）
	// N1 (the tombstone-pump eradication): the tombstone GC — it runs only when the snapshot saw
	// pre-existing tombstones (zero cost on a purely live pool), and it must precede the MinIdle
	// refill trigger: the GC drops size back to the true live-object count so the refill enqueues
	// on top of an accurate size. With the order reversed (or without the GC at all), "destruction
	// leaves a tombstone occupying size → the tombstone depresses idleCount → deficit>0 fires the
	// refill → the refill enqueue bypasses the size gate → the next pass destroys and refills
	// again" forms a deterministic unbounded growth of ~minIdle tombstones per cycle (with low
	// Get traffic nobody reclaims them)
	if tombstoneCount > 0 {
		p.gcTombstones()
	}

	// MinIdle 异步补齐触发（P1-4，报告 §5.3.5：绝不在 supervise 主循环同步创建对象，避免拖慢
	// 健康扫描周期）：deficit = minIdle - 本轮结束时仍存活的有效空闲数，仅 deficit>0 时补齐。
	// refilling 节流：CAS(false→true) 成功才启动补齐 goroutine——scanInterval 短 + newFunc 慢时
	// 防止多个补齐 goroutine 叠加造成创建风暴；上一轮未完成则本轮跳过（水位由下个扫描周期
	// 重算的 deficit 收敛）。
	// goroutine 生命周期（零泄漏三要素）：① wg.Add(1) 在 monitor goroutine 内同步执行——此刻
	// monitor 自身的 +1 尚未释放（wg 计数恒 ≥1），Stop 的 wg.Wait 不可能先于本 Add 观察到
	// 计数归零（无 WaitGroup misuse）；② goroutine 经 defer wg.Done 纳入 Stop 的 wg.Wait 回收；
	// ③ 每轮创建前检查 p.ctx.Done，Stop 的 cancel 使补齐中途快速退出（勿用 drain 语义）。
	// deficit 是快照值：goroutine 实际执行时水位可能已变化（并发借出→欠补齐，下轮扫描收敛；
	// 并发归还→轻度过冲，被 reserveSlot 的在途封顶约束，良性）
	// MinIdle async refill trigger (P1-4, report §5.3.5: never create objects synchronously in the
	// supervise main loop, which would slow the health-scan cycle): deficit = minIdle minus the
	// effective idle objects still alive at the end of this pass; a refill starts only when
	// deficit>0. refilling throttle: the refill goroutine launches only on a successful
	// CAS(false→true) — with a short scanInterval and a slow newFunc this prevents stacked refill
	// goroutines (a creation storm); the pass is skipped while the previous refill is still running
	// (the water level converges via the deficit recomputed on the next scan). Goroutine lifecycle
	// (the three zero-leak elements): ① wg.Add(1) runs synchronously inside the monitor goroutine —
	// the monitor's own +1 has not been released yet (the wg counter is always ≥1 here), so Stop's
	// wg.Wait can never observe the counter reaching zero before this Add (no WaitGroup misuse);
	// ② the goroutine is tracked via defer wg.Done and reclaimed by Stop's wg.Wait; ③ every round
	// checks p.ctx.Done before creating, so Stop's cancel makes the refill exit promptly mid-flight
	// (never drain semantics). deficit is a snapshot: the water level may have shifted by the time
	// the goroutine runs (concurrent borrows → under-refill, converged on the next scan; concurrent
	// returns → a mild overshoot, bounded by reserveSlot's in-flight cap — benign)
	if minIdle > 0 {
		if deficit := minIdle - idleCount; deficit > 0 && p.refilling.CompareAndSwap(false, true) {
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				defer p.refilling.Store(false)
				p.refill(deficit)
			}()
		}
	}
}

// gcTombstones 主动回收队列中的 tombstone（N1 墓碑泵的根治机制，仅由 supervise 末尾在
// tombstoneCount>0 时调用——纯活对象池零开销）。连接池标准做法（对照 database/sql 的
// connectionCleaner 周期性清理无效连接）：A3 的“size 只在离队点单点递减”不变式原样保持，
// tombstone 的回收入口从“被动等待 Get”扩展为“Get ∪ supervise GC”两条路径。
// 机制（逐字镜像 tryAcquire 的 tombstone 回收分支 + live 原样重入队）：
//   - 有界循环：至多 bound 次（bound = GC 开始时的 queue.Len()）——防止与并发 Put/补齐的
//     入队交互时无限追逐；本轮未尽的 tombstone 由下轮扫描的 tombstoneCount 门禁兜住；
//   - 每个元素经 queue.Get 离队即 size.Add(-1)（A3 单点递减，与 tryAcquire/Cleanup 第二阶段/
//     drainAndClose 同构）；
//   - tombstone（data==nil 或 retries<0）：认领（retries 锁内清零）→ PutRaw 归还 wrapper
//     （统一清零时间戳）→ wakeWaiter（size 容量释放 = D8 唤醒点，[状态→信号] 顺序：
//     size.Add(-1) 已在先）→ size 净 -1；在途配额早在销毁点经 releaseSlot 释放，GC 绝不
//     再触碰 inFlight（双计数解耦：GC 只动 size）；
//   - live（data!=nil 且 retries>=0）：原样 queue.Put 重入队——绝不重新 stampEnqueue、绝不
//     触碰 expireAt/idleSince/createdAt（wrapper 只短暂离队又入队，对象年龄与空闲时钟不变，
//     F9 语义保持）；重入队成功 size.Add(+1) 抵消离队的 -1（净 0，对象仍在队），并配对
//     wakeWaiter——GC 重入队是 queue.Len() 的 0→正迁移点（继 Put/initialize/refillOne 之后
//     的第四个入队点），唤醒完备性不变式强制要求发信号：被唤醒的 waiter 可能在 GC 的
//     离队→重入队窗口内重挂起（复查见 Len==0），若重入队不唤醒即构成真实丢失唤醒；
//   - live 重入队失败 ⟹ 队列已被并发关闭（BYO 队列场景——池自身 Stop 的 Shutdown 恒在
//     wg.Wait 之后，而 GC 运行于 monitor goroutine 的 supervise 内，该形态结构性不可达）：
//     本元素已离队、Stop 的 Cleanup 再也见不到它，必须走销毁四件套闭环记账（与 refillOne
//     的入队失败分支同构），随后终止本轮 GC（关闭队列上的后续 Get 也必然失败）。
//
// 锁序核验：queue.Get/Done（queue.lock）→ element.mu（Lock/Unlock，叶子）→ PutRaw →
// wakeWaiter（waitersMu，叶子）——与 tryAcquire 的 tombstone 回收分支完全同构，零新边；
// live 重入队的 queue.Put 持 queue.lock、失败分支的 releaseSlot 经既有 queue.lock 后获取
// waitersMu 之外的路径（此处不在 Range 回调内，无 queue.lock→waitersMu 嵌套）——拓扑序
// stopGate → queue.lock → {element.mu, waitersMu} 保持无环。closeFunc/OnClose 均在锁外调用。
// 净效果：size -= 回收的 tombstone 数（回落到真实活对象数），根治 N1 的无界增长；GC 后
// size 的上界由“每轮扫描回收既有墓碑”保证（新纪律点，取代已死的 size≤maxSize 单门禁不变式）。
// gcTombstones actively reclaims the tombstones in the queue (the eradication mechanism of the
// N1 tombstone pump; invoked only from the end of supervise when tombstoneCount>0 — zero cost on
// a pool of purely live objects). This is the standard connection-pool practice (cf. database/sql's
// connectionCleaner periodically clearing invalid connections): the A3 invariant "size is
// decremented at the single dequeue point only" stays untouched, and the tombstone reclaim entry
// widens from "passively waiting for a Get" to "Get ∪ the supervise GC".
// Mechanics (verbatim mirror of tryAcquire's tombstone reclaim branch + an as-is live re-enqueue):
//   - a bounded loop: at most bound iterations (bound = queue.Len() at the GC's start) — never
//     chasing enqueues of concurrent Put/refill forever; tombstones this round misses are caught
//     by the next pass's tombstoneCount gate;
//   - every element dequeued via queue.Get decrements size immediately (the A3 single decrement
//     point, isomorphic to tryAcquire / Cleanup phase 2 / drainAndClose);
//   - a tombstone (data==nil or retries<0): claim (retries zeroed under the lock) → hand the
//     wrapper back via PutRaw (which clears the timestamps uniformly) → wakeWaiter (releasing
//     size capacity is a D8 wake point; the [state → signal] order holds since size.Add(-1) ran
//     first) → size nets -1; the in-flight quota was already released at the destruction site
//     via releaseSlot, so the GC never touches inFlight (the two counters stay decoupled: the GC
//     moves size only);
//   - a live element (data!=nil and retries>=0): re-enqueue as-is via queue.Put — never
//     re-stampEnqueue, never touch expireAt/idleSince/createdAt (the wrapper only left the queue
//     briefly and came back; the object's age and idle clock are unchanged, preserving the F9
//     semantics); a successful re-enqueue adds size.Add(+1) to cancel the dequeue's -1 (net 0 —
//     the object is still queued) and pairs with wakeWaiter — the GC re-enqueue is a queue.Len()
//     0→positive transition point (the fourth enqueue point after Put/initialize/refillOne), and
//     the wake-completeness invariant mandates the signal: a woken waiter may re-suspend inside
//     the GC's dequeue→re-enqueue window (its recheck sees Len==0), so re-enqueueing without a
//     wake would be a genuine lost wakeup;
//   - a failed live re-enqueue ⟹ the queue was closed concurrently (a BYO-queue scenario — the
//     pool's own Stop shuts the queue down only after wg.Wait, while the GC runs inside the
//     monitor goroutine's supervise, making that shape structurally unreachable): this element
//     has left the queue and Stop's Cleanup can never see it again, so the destruction quadruple
//     must close the ledger (isomorphic to refillOne's enqueue-failure branch), and the current
//     GC round then terminates (subsequent Gets on a closed queue necessarily fail anyway).
//
// Lock-order check: queue.Get/Done (queue.lock) → element.mu (Lock/Unlock, a leaf) → PutRaw →
// wakeWaiter (waitersMu, a leaf) — fully isomorphic to tryAcquire's tombstone reclaim branch
// with zero new edges; the live re-enqueue's queue.Put holds queue.lock, and the failure branch
// runs outside any queue callback (no queue.lock→waitersMu nesting here) — the topology
// stopGate → queue.lock → {element.mu, waitersMu} stays acyclic. closeFunc/OnClose run outside
// every lock. Net effect: size -= the number of reclaimed tombstones (dropping back to the true
// live-object count), eradicating N1's unbounded growth; after the GC the upper bound of size is
// guaranteed by "every pass reclaims the pre-existing tombstones" (the new discipline point,
// replacing the dead single-gate size≤maxSize invariant).
func (p *Pool) gcTombstones() {
	// bound 取 GC 开始时的队列长度：本轮至多处理这么多元素。并发 Put/补齐在 GC 期间入队的
	// 新元素不计入 bound——它们要么被顺带处理（重入队净 0），要么留给下轮，绝不无限循环
	// bound is the queue length at the GC's start: this round processes at most that many
	// elements. New elements enqueued by concurrent Put/refill during the GC do not extend the
	// bound — they are either handled in passing (a net-0 re-enqueue) or left to the next pass,
	// never forming an infinite chase
	bound := p.queue.Len()
	for range bound {
		element, err := p.queue.Get()
		if err != nil {
			// 队列已空或已被并发关闭：本轮 GC 到此为止
			// The queue is empty or was closed concurrently: this GC round ends here
			return
		}
		p.queue.Done(element)
		// A3：元素离队即单点递减 size（与 tryAcquire 完全一致——tombstone 回收与 live 重入队
		// 两个分支都从这次递减出发，前者净 -1，后者经重入队 +1 抵消）
		// A3: dequeue decrements size at the single point (exactly like tryAcquire — both the
		// tombstone reclaim and the live re-enqueue branches start from this decrement; the former
		// nets -1 while the latter cancels it with the re-enqueue's +1)
		p.size.Add(-1)

		wrapper := element.(*pool.Element)
		wrapper.Lock()
		value := wrapper.GetDataNoLock()
		retries := wrapper.GetRetriesNoLock()
		if value == nil || retries < 0 {
			// tombstone 分支：逐字镜像 tryAcquire 的 tombstone 回收——认领（data 清零 + retries
			// 清零）→ PutRaw（清零三个 atomic 时间戳）→ wakeWaiter。data/retries 的一致对在元素
			// 锁内读取，与一切销毁路径的锁内认领互斥，不会读到撕裂中间态。SetDataNoLock(nil) 使
			// PutRaw 的前置契约（data 与 retries 均由调用方在元素锁内清理完毕）在本分支自足
			//（G1）：value==nil 时它是幂等 no-op；若谓词经第二析取项 retries<0 命中（今日一切
			// 销毁路径先清 data，该形态结构性不可达），它保证 wrapper 绝不携带用户 value 归还
			// sync.Pool——不依赖"销毁先清 data"的远端隐式不变式
			// The tombstone branch: a verbatim mirror of tryAcquire's tombstone reclaim — claim
			// (data cleared + retries zeroed) → PutRaw (clearing the three atomic timestamps) →
			// wakeWaiter. The consistent data/retries pair is read under the element lock, mutually
			// exclusive with every destruction path's under-lock claim, so no torn intermediate
			// state is observed. SetDataNoLock(nil) makes PutRaw's precondition (data and retries
			// both cleared by the caller under the element lock) self-contained within this branch
			// (G1): an idempotent no-op when value==nil, and should the predicate ever fire via its
			// second disjunct retries<0 (structurally unreachable today — every destruction path
			// clears data first), it guarantees the wrapper never re-enters sync.Pool retaining a
			// user value, with no reliance on the remote "destruction clears data first" invariant
			wrapper.SetDataNoLock(nil)
			wrapper.SetRetriesNoLock(0)
			wrapper.Unlock()
			p.elementPool.PutRaw(wrapper)
			// [状态→信号]：size.Add(-1) 已先行；无 waiter 时本探测仅一次 atomic.Load（D10）。
			// 与 tryAcquire 的 tombstone 唤醒同属协议统一性保留（挂起 waiter 的复查条件含
			// Len==0，而 GC 能回收到 tombstone 说明彼时 Len>0，通常打不到挂起者），机械纪律
			// 逐点配对，不做可达性豁免
			// [state → signal]: size.Add(-1) already ran; with no waiters this probe costs a
			// single atomic.Load (D10). Like tryAcquire's tombstone wake this is kept for
			// protocol uniformity (a suspended waiter's recheck requires Len==0, while the GC
			// reclaiming a tombstone implies Len>0 at that moment, so it usually cannot hit a
			// suspended waiter) — the mechanical discipline pairs every site with no
			// reachability exemptions
			p.wakeWaiter()
			continue
		}
		wrapper.Unlock()
		// live 分支：原样重入队（保留时间戳——不重新 stampEnqueue、不碰 expireAt/idleSince/
		// createdAt，对象年龄不变；FIFO 位置轮转到队尾——借出顺序无公平契约，良性）
		// The live branch: re-enqueue as-is (timestamps preserved — no re-stampEnqueue, and
		// expireAt/idleSince/createdAt are untouched, so the object's age is unchanged; its FIFO
		// position rotates to the tail — there is no fairness contract on borrow order, benign)
		if err := p.queue.Put(element); err != nil {
			// 重入队失败 ⟹ 队列已被并发关闭（BYO 队列；池自身 Stop 的 Shutdown 恒在 wg.Wait
			// 之后，本代码运行于 monitor 的 supervise 内，结构性不可达）：元素已离队，Stop 的
			// Cleanup 再也见不到它——销毁四件套闭环（与 refillOne 的入队失败分支同构）：
			// wrapper 归还（Reset 清 data，不滞留用户 value）→ closeFunc → OnClose →
			// recordDestroyed(destroyedByStop) → releaseSlot（在途配额 -1 + 唤醒）。size 净 -1
			// （离队已减、不再入队）。随后终止本轮 GC（关闭队列上的后续 Get 必然失败）
			// A failed re-enqueue ⟹ the queue was closed concurrently (a BYO queue; the pool's
			// own Stop runs Shutdown only after wg.Wait while this code runs inside the monitor's
			// supervise — structurally unreachable): the element has left the queue and Stop's
			// Cleanup can never see it again — the destruction quadruple closes the ledger
			// (isomorphic to refillOne's enqueue-failure branch): hand the wrapper back (Reset
			// clears data, never retaining the user value) → closeFunc → OnClose →
			// recordDestroyed(destroyedByStop) → releaseSlot (in-flight -1 + wake). size nets -1
			// (decremented at dequeue, never re-enqueued). The GC round then terminates
			// (subsequent Gets on a closed queue necessarily fail)
			p.elementPool.Put(wrapper)
			closeErr := p.config.closeFunc(value)
			p.config.callback.OnClose(value, closeErr)
			p.stats.recordDestroyed(&p.stats.destroyedByStop)
			p.releaseSlot()
			return
		}
		// 重入队成功：size+1 抵消离队的 -1（净 0，对象仍在队）；[状态→信号] 顺序：先
		// size.Add 后 wakeWaiter。该唤醒是承重信号而非防御冗余：GC 把队列中唯一（或最后一批）
		// 活元素短暂握在手里时，一个刚被 Put/releaseSlot 唤醒的 waiter 重跑获取路径可能
		// 一无所获并重新挂起（复查见 Len==0 且 inFlight≥maxSize），重入队是它此后可感知
		// 的唯一容量事件——漏掉即丢失唤醒（唤醒完备性不变式：每个 Len 0→正迁移点必发信号）。
		// 无 waiter 时成本为一次 atomic.Load
		// Re-enqueue succeeded: size+1 cancels the dequeue's -1 (net 0 — the object is still
		// queued); the [state → signal] order: size.Add precedes wakeWaiter. This wake is a
		// load-bearing signal, not defensive redundancy: while the GC briefly holds the queue's
		// only (or last batch of) live element(s), a waiter just woken by Put/releaseSlot may
		// re-run the acquire path, gain nothing and re-suspend (its recheck sees Len==0 with
		// inFlight≥maxSize) — the re-enqueue is then the only capacity event it can ever observe,
		// so omitting this wake is a lost wakeup (the wake-completeness invariant: every Len
		// 0→positive transition point must signal). With no waiters the cost is one atomic.Load
		p.size.Add(1)
		p.wakeWaiter()
	}
}

// refill 异步补齐至多 deficit 个对象，把有效空闲数恢复到 minIdle 水位（P1-4，运行于独立的
// wg 跟踪 goroutine）。每轮创建前检查 p.ctx.Done（Stop 的 cancel 使其快速退出，零泄漏）；
// 任一轮创建失败即停止本轮补齐（失败退避的最简实现：不在本轮内重试到成功，依赖下个
// scanInterval 重算 deficit 自然重试——防止 newFunc 失败率高时反复补齐压垮下游）
// refill asynchronously creates up to deficit objects, restoring the effective idle count to the
// minIdle water level (P1-4, running in a dedicated wg-tracked goroutine). Every round checks
// p.ctx.Done before creating (Stop's cancel makes it exit promptly — zero leaks); any failed
// creation round stops the current refill (the simplest failure backoff: no retry-until-success
// within the round, relying on the next scanInterval recomputing the deficit to retry naturally —
// preventing a high-failure-rate newFunc from hammering the downstream with repeated refills)
func (p *Pool) refill(deficit int) {
	for range deficit {
		select {
		case <-p.ctx.Done():
			return
		default:
		}
		if !p.refillOne() {
			return
		}
	}
}

// refillOne 创建并入队一个补齐对象：镜像 initialize 的入队路径（stampEnqueue → SetData →
// queue.Put → size+1），但经 reserveSlot 走在途封顶（区别于 initialize 的裸 inFlight.Add——
// reserveSlot 的 CAS 成功即 +1，创建成功后不再重复计数）。补齐是池内部创建：不触发
// BeforeGet（钩子仅借出路径）、不触发 AfterPut（钩子仅外部 Put 路径），对象直接入队。
// 记账纪律（B3 准入条件）：
//   - 成功：totalCreated+1（在途已由 reserveSlot 计入）；入队后 size+1、wakeWaiter（补齐入队 =
//     queue.Len() 0→正迁移，唤醒完备性不变式的强制义务）；
//   - reserveSlot 失败（errNoCapacity 在途满 / ErrQueueClosed 池停止）：停止补齐（水位让位于
//     借出者与在途封顶）；
//   - newFunc 失败：partial value 由池关闭（补齐路径无调用方可移交所有权，镜像 initialize 的
//     失败路径；对象从未成功创建——不计数 totalCreated/totalDestroyed）；预留经 defer 恰好
//     释放一次；
//   - born-nil（D3）：视同创建失败，defer 释放预留；
//   - 入队失败（Stop 竞态/队列关闭）：新建 value 严禁丢弃——closeFunc + OnClose +
//     recordDestroyed(destroyedByStop) + releaseSlot（经 defer）+ wrapper 归还（镜像 initialize
//     的入队失败清理，满足 B3 准入条件 1）；
//   - newFunc panic：defer 在展开中恰好释放一次预留，panic 原样传播——补齐 goroutine 中的
//     panic 将终止进程（与 supervise goroutine 中 pingFunc/closeFunc panic 的既有行为一致，
//     NewFunc panic 策略为原样传播不 recover）。
//
// 返回 true 表示补齐成功，false 表示本轮补齐应停止
// refillOne creates and enqueues one refill object: mirroring initialize's enqueue path
// (stampEnqueue → SetData → queue.Put → size+1) but going through reserveSlot under the in-flight
// cap (unlike initialize's bare inFlight.Add — reserveSlot's successful CAS is the +1, and a
// successful creation never counts again). A refill is an internal creation: BeforeGet never fires
// (hooks belong to the borrow path) nor AfterPut (hooks belong to the external Put path); the
// object is enqueued directly. Accounting discipline (the B3 admission conditions):
//   - success: totalCreated+1 (the in-flight slot was already counted by reserveSlot); after the
//     enqueue, size+1 and wakeWaiter (a refill enqueue is a queue.Len() 0→positive transition —
//     the mandatory obligation of the wake-completeness invariant);
//   - reserveSlot failure (errNoCapacity: the in-flight quota is full; ErrQueueClosed: the pool is
//     stopping): the refill stops (the water level yields to borrowers and the in-flight cap);
//   - newFunc failure: the pool closes the partial value (a refill has no caller to hand ownership
//     to, mirroring initialize's failure path; the object was never successfully created — neither
//     totalCreated nor totalDestroyed is touched); the reservation is released exactly once by the
//     defer;
//   - born-nil (D3): treated as a creation failure; the defer releases the reservation;
//   - enqueue failure (a Stop race / the queue is closed): the freshly created value must never be
//     discarded — closeFunc + OnClose + recordDestroyed(destroyedByStop) + releaseSlot (via the
//     defer) + the wrapper is handed back (mirroring initialize's enqueue-failure cleanup,
//     satisfying B3 admission condition 1);
//   - newFunc panic: the defer releases the reservation exactly once during the unwinding and the
//     panic propagates untouched — a panic inside the refill goroutine terminates the process
//     (consistent with the existing behavior of pingFunc/closeFunc panics in the supervise
//     goroutine; the NewFunc panic policy is untouched propagation with no recover).
//
// True means the refill succeeded; false means the current refill round must stop
func (p *Pool) refillOne() bool {
	if err := p.reserveSlot(); err != nil {
		return false
	}
	// P0-A 纪律：defer 化配对释放——除“创建成功且已入队”外的任何路径（错误返回、panic 展开）
	// 都恰好释放一次在途预留（镜像 boundedCreate 已验证的 defer+created 模式，注入记忆
	// [reservation-leak-on-panic]）
	// P0-A discipline: the paired release is defer'd — every path other than "created and enqueued"
	// (an error return or a panic unwind) releases the in-flight reservation exactly once
	// (mirroring the battle-tested defer+created pattern of boundedCreate; the injected memory
	// [reservation-leak-on-panic])
	committed := false
	defer func() {
		if !committed {
			p.releaseSlot()
		}
	}()
	value, err := p.config.newFunc()
	if err != nil {
		// 创建失败：关闭 partial value（若有——补齐路径无调用方，池必须处置），不计数；
		// 预留由 defer 释放；本轮补齐到此为止（失败退避）
		// Creation failure: close the partial value if any (a refill has no caller — the pool must
		// dispose of it), without counting; the defer releases the reservation; the current refill
		// round stops here (failure backoff)
		if value != nil {
			_ = p.config.closeFunc(value)
		}
		return false
	}
	// D3：born-nil 视同创建失败（与 Put 拒绝 nil 的契约对称）：预留由 defer 恰好释放一次，
	// 绝不静默计数入队
	// D3: a born-nil value counts as a creation failure (symmetric with Put's nil rejection): the
	// defer releases the reservation exactly once, and it is never silently counted or enqueued
	if value == nil {
		return false
	}
	p.stats.totalCreated.Add(1)
	element := p.elementPool.Get()
	// E1/F5 结构纪律：与 initialize/Put 同序——先打时间戳再写 data（补齐对象必经 stampEnqueue
	// 打戳，否则 idleSince/expireAt 为 0 会被 supervise 的 fail-closed 判定立即销毁）
	// E1/F5 structural discipline: the same order as initialize/Put — stamp the timestamps before
	// writing data (a refill object must pass through stampEnqueue, otherwise idleSince/expireAt
	// stay 0 and supervise's fail-closed checks destroy it immediately)
	// GO-1：门禁提升至调用点——与原 stampEnqueue 函数内门禁逐字等价（见 ageEvictionOn 字段注释）；
	// F5 义务在守卫内不变：淘汰启用时补齐对象必打戳，未启用时 supervise 判定整体短路、无戳无害
	// GO-1: the gate is hoisted to the call site — verbatim equivalent to the original
	// in-stampEnqueue gate (see the ageEvictionOn field comment); the F5 obligation inside the guard
	// is unchanged: a refill object is always stamped once eviction is enabled, and with eviction
	// disabled supervise's checks short-circuit entirely so the missing stamps are harmless
	if p.ageEvictionOn {
		p.stampEnqueue(element)
	}
	element.SetData(value)
	if err := p.queue.Put(element); err != nil {
		// 入队失败（Stop 竞态/队列关闭）：B3 准入条件 1——新建 value 严禁丢弃：wrapper 归还 +
		// closeFunc + OnClose + recordDestroyed(destroyedByStop) + releaseSlot（经 defer）。
		// totalCreated 已 +1，本销毁与其配对（created-destroyed 净零，账本闭合）
		// Enqueue failure (a Stop race / the queue is closed): B3 admission condition 1 — the freshly
		// created value must never be discarded: hand the wrapper back + closeFunc + OnClose +
		// recordDestroyed(destroyedByStop) + releaseSlot (via the defer). totalCreated was already
		// +1'd, and this destruction pairs with it (created-destroyed nets to zero — the ledger
		// stays closed)
		p.elementPool.Put(element)
		closeErr := p.config.closeFunc(value)
		p.config.callback.OnClose(value, closeErr)
		p.stats.recordDestroyed(&p.stats.destroyedByStop)
		return false
	}
	committed = true
	// 入队成功：池内计数 +1（在途封顶已由 reserveSlot 保证）。size 语义（N1 修正后的准确
	// 归因）：补齐入队是免 size 门禁的运行期入队点——A3 既有语义只是“tombstone 占 size 且
	// size≤maxSize 由 Put 单门禁保证”；补齐叠加 tombstone 堆积曾使 size 持续无界超过
	// maxSize（N1 墓碑泵），现由 supervise 末尾的 tombstone GC 每轮回收既有墓碑，把 size
	// 上界收敛到 ~maxSize+minIdle 的瞬时窗口（销毁轮墓碑未回收期间），Put 的 ErrPoolFull
	// 门禁据此计数。锁序核验：element.mu（SetData，叶子锁）已释放，queue.Put 持
	// queue.lock，wakeWaiter 持 waitersMu——三者依次获取无嵌套，[状态→信号] 顺序满足双序协议
	//（注入记忆 [双序协议]：先 queue.Put/size.Add 后 wakeWaiter，与 Put 的 wake-after-enqueue 同序）
	// Enqueue succeeded: the pool count +1 (the in-flight cap was already guaranteed by
	// reserveSlot). The size semantics (the accurate attribution after the N1 fix): the refill
	// enqueue is a runtime enqueue point exempt from the size gate — the existing A3 semantics
	// were only "tombstones occupy size while size≤maxSize is guaranteed by Put's single gate";
	// refills stacked on tombstone pile-ups once drove size persistently and unboundedly past
	// maxSize (the N1 tombstone pump), and now the tombstone GC at the end of supervise reclaims
	// the pre-existing tombstones every pass, converging the upper bound of size to a transient
	// ~maxSize+minIdle window (while a destruction round's tombstones await reclaim) — Put's
	// ErrPoolFull gate counts against this. Lock-order check: element.mu (SetData, a leaf lock)
	// is already released, queue.Put holds queue.lock, wakeWaiter holds waitersMu — the three are
	// acquired in sequence without nesting, and the [state → signal] order satisfies the
	// dual-order protocol (the injected memory [双序协议]: queue.Put/size.Add precede
	// wakeWaiter, the same order as Put's wake-after-enqueue)
	p.size.Add(1)
	// B3 最隐蔽义务：补齐入队 = queue.Len() 0→正迁移 = 必须伴 wakeWaiter（唤醒完备性不变式：
	// 每个入队点必发信号，否则丢失唤醒证明与 tryAcquire tombstone 唤醒的“不可达挂起者”注释
	// 同时失效）。无 waiter 时成本为一次 atomic.Load
	// B3's most subtle obligation: a refill enqueue is a queue.Len() 0→positive transition and must
	// pair with wakeWaiter (the wake-completeness invariant: every enqueue point signals, otherwise
	// the lost-wakeup proof and the "unreachable suspended waiter" note of tryAcquire's tombstone
	// wake both break). With no waiters the cost is a single atomic.Load
	p.wakeWaiter()
	return true
}
