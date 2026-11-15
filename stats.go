package conecta

import (
	"time"

	"github.com/shengyanli1982/conecta/internal/pool"
)

// Stats 是池统计指标的不可变快照（P1-6，命名对齐 database/sql 的 DBStats 与 pgxpool 的 Stat）：
// 瞬时水位 + 累计计数 + 五分类销毁原因 + 拒绝与等待指标。纯数据结构（值语义，无锁无接口），
// 用户自行接入指标系统（Prometheus/OpenTelemetry 示例见 README），主模块保持零依赖。
// 快照一致性：各字段来自独立的 atomic 读取（不追求跨字段强一致——并发操作期间不同字段可能
// 对应略有先后的时刻）；但五分类恒等式 TotalDestroyed == DestroyedByPing + DestroyedByLifetime +
// DestroyedByIdle + DestroyedByBorrow + DestroyedByStop 由 recordDestroyed 单入口在结构上保证，
// 池静止时快照精确满足该恒等式（并发销毁期间可能出现瞬时偏差：分类计数与总计是两次独立的
// atomic 自增）。ActiveCount == TotalBorrowed - TotalReturned 恒成立（派生值，无独立计数漂移）
// Stats is an immutable snapshot of the pool's statistics (P1-6, named after database/sql's DBStats
// and pgxpool's Stat): instantaneous water levels + cumulative counters + the five destruction
// categories + rejection and waiting metrics. A pure data structure (value semantics, no locks, no
// interfaces) that users wire into their own metrics systems (see the README for the
// Prometheus/OpenTelemetry example), keeping the main module dependency-free.
// Snapshot consistency: the fields come from independent atomic reads (no cross-field strong
// consistency is pursued — during concurrent operations different fields may correspond to moments
// slightly apart); however the five-category identity TotalDestroyed == DestroyedByPing +
// DestroyedByLifetime + DestroyedByIdle + DestroyedByBorrow + DestroyedByStop is structurally
// guaranteed by the single recordDestroyed entry point, and a snapshot of a quiescent pool
// satisfies it exactly (during concurrent destructions a transient deviation is possible: the
// category counter and the total are two independent atomic increments). ActiveCount ==
// TotalBorrowed - TotalReturned always holds (a derived value — no independent-counter drift)
type Stats struct {
	// ------ 瞬时水位 / Instantaneous water levels ------

	// MaxSize 是配置的在途对象总数上限（归一化后）
	// MaxSize is the configured cap of the total in-flight object count (after normalization)
	MaxSize int

	// MinIdle 是配置的最小空闲水位（归一化后；0 表示水位保持关闭）
	// MinIdle is the configured minimum idle water level (after normalization; 0 means
	// water-level maintenance is disabled)
	MinIdle int

	// ActiveCount 是已移交调用方且尚未归还的对象数（= TotalBorrowed - TotalReturned，派生值）。
	// 纯池自建对象负载下恒 ≥ 0；AfterPut 钩子拒绝的归还计入 TotalReturned（N2：所有权已
	// 移交回池即归还义务已履行，钩子拒绝不使该值漂移）；外部对象经 Put 收编会使
	// TotalReturned 无配对借出而令该值偏小甚至为负（Put(data any) 无来源信息的固有边界，
	// 见 README 的 D2 说明）
	// ActiveCount is the number of objects handed to callers and not yet returned
	// (= TotalBorrowed - TotalReturned, a derived value). Always ≥ 0 on a workload of purely
	// pool-created objects; a return rejected by the AfterPut hook counts into TotalReturned
	// (N2: the ownership has already transferred back to the pool, so the return obligation is
	// fulfilled and a hook rejection never drifts this value); foreign objects adopted via Put
	// increment TotalReturned without a paired borrow, which can push this value down or even
	// negative (an inherent boundary of the provenance-free Put(data any) — see the D2 note in
	// the README)
	ActiveCount int

	// IdleCount 是队列中当前可借出的有效空闲对象数（不含 tombstone）
	// IdleCount is the number of effective idle objects currently borrowable from the queue
	// (tombstones excluded)
	IdleCount int

	// TombstoneCount 是队列中当前已被销毁、等待回收的墓碑数（占用 size 容量，A3 语义；
	// 回收入口 = Get ∪ supervise 末尾的 tombstone GC——N1 后墓碑至多存活到下一轮扫描，
	// 不再无界堆积）
	// TombstoneCount is the number of tombstones currently in the queue — destroyed objects
	// waiting to be reclaimed (occupying size capacity, the A3 semantics; the reclaim entries =
	// Get ∪ the tombstone GC at the end of supervise — after N1 a tombstone lives at most until
	// the next pass and never piles up unboundedly)
	TombstoneCount int

	// ------ 累计计数 / Cumulative counters ------

	// TotalCreated 是累计成功创建的对象数（initialize 预建、GetOrCreate/GetWithContext 按需
	// 创建、MinIdle 补齐三类来源；创建失败与 born-nil 拒绝不计数）
	// TotalCreated is the cumulative number of successfully created objects (from the three sources
	// of initialize pre-creation, GetOrCreate/GetWithContext on-demand creation and MinIdle refill;
	// creation failures and born-nil rejections are not counted)
	TotalCreated int64

	// TotalDestroyed 是累计销毁的对象数，恒等于下面五个 DestroyedBy* 分类之和（见类型文档）
	// TotalDestroyed is the cumulative number of destroyed objects, always equal to the sum of the
	// five DestroyedBy* categories below (see the type documentation)
	TotalDestroyed int64

	// TotalBorrowed 是累计移交调用方的对象数：队列借出（Get/GetOrCreate/GetWithContext）与
	// 创建直交（GetOrCreate/GetWithContext 的按需创建路径——创建出的对象直接移交调用方持有，
	// 同样负有归还义务）
	// TotalBorrowed is the cumulative number of objects handed over to callers: queue borrows
	// (Get/GetOrCreate/GetWithContext) and direct creation hand-overs (the on-demand creation paths
	// of GetOrCreate/GetWithContext — a freshly created object goes straight into the caller's
	// hands, equally owing a return)
	TotalBorrowed int64

	// TotalReturned 是累计归还处理成功次数：Put 入队成功，或 AfterPut 钩子拒绝后由池销毁
	//（N2：钩子拒绝时所有权已移交回池，调用方的归还义务已履行，虽销毁仍计一次归还处理——
	// 与入队成功路径口径对称，保证 ActiveCount 不因钩子拒绝漂移）；被 ErrPoolFull 拒绝的
	// 归还不计数（所有权未转移，对象与配额仍归调用方处置）
	// TotalReturned is the cumulative number of successfully processed returns: a Put enqueued
	// successfully, or a Put rejected by the AfterPut hook and destroyed by the pool (N2: on a
	// hook rejection the ownership has already transferred back to the pool and the caller has
	// fulfilled its return obligation, so the destruction still books one processed return —
	// symmetric with the successful-enqueue path, keeping ActiveCount from drifting on hook
	// rejections); returns rejected by ErrPoolFull are not counted (ownership never transfers —
	// the value and its quota stay the caller's to dispose of)
	TotalReturned int64

	// ------ 销毁原因分类（五分类恒等式的分量）/ Destruction categories (the identity's terms) ------

	// DestroyedByPing 是后台扫描中 ping 连续失败达到 PingMaxRetries 上限而销毁的对象数
	// DestroyedByPing is the number of objects destroyed by the background scan after ping failures
	// reached the PingMaxRetries cap
	DestroyedByPing int64

	// DestroyedByLifetime 是对象年龄达到 MaxLifetime 而销毁的对象数（后台扫描与借出判定两条路径）
	// DestroyedByLifetime is the number of objects destroyed because their age reached MaxLifetime
	// (both the background scan and the borrow check)
	DestroyedByLifetime int64

	// DestroyedByIdle 是持续空闲达到 MaxIdleTime 而被后台扫描回收的对象数（尊重 MinIdle 下限）
	// DestroyedByIdle is the number of objects reclaimed by the background scan after idling
	// continuously for MaxIdleTime (respecting the MinIdle floor)
	DestroyedByIdle int64

	// DestroyedByBorrow 是借出/归还校验销毁的对象数（BeforeGet/AfterPut 钩子拒绝与
	// TestOnBorrow 预设校验失败统一计入）
	// DestroyedByBorrow is the number of objects destroyed by the borrow/return checks (BeforeGet /
	// AfterPut hook rejections and TestOnBorrow preset failures all count here)
	DestroyedByBorrow int64

	// DestroyedByStop 是四类来源销毁的对象数（G2 完整枚举，与 poolStats.destroyedByStop 注释
	// 同口径）：Stop/Cleanup、初始化失败清理、MinIdle 补齐入队失败（Stop 竞态），以及
	// tombstone GC 的 live 重入队失败（仅 BYO 队列被并发关闭时可达）
	// DestroyedByStop is the number of objects destroyed by the four sources (the complete G2
	// enumeration, in lockstep with the poolStats.destroyedByStop comment): Stop/Cleanup, the
	// failed-initialization cleanup, MinIdle refill enqueue failures (Stop races), and the tombstone
	// GC's live re-enqueue failure (reachable only when a BYO queue is closed concurrently)
	DestroyedByStop int64

	// ------ 拒绝与等待 / Rejections and waits ------

	// EmptyCount 是空池事件计数（P2-3 精确语义）：Get 返回 ErrPoolEmpty 时计入（队列暂空、
	// 到期销毁达 maxRetries 上限、借出校验拒绝达上限——三个出口）；GetOrCreate 的空池探测
	// 同样计数（countEmpty=true）但随后转入按需创建，从不向调用方返回该哨兵；GetWithContext
	// 的内部重试不计数（acquireOnce→tryAcquire(false)，等待循环不污染本计数）
	// EmptyCount is the empty-pool event count (the precise P2-3 semantics): incremented when Get
	// returns ErrPoolEmpty (three exits — the queue momentarily empty, expiry destructions reaching
	// the maxRetries cap, or borrow-check rejections reaching it); GetOrCreate's empty-pool probe
	// counts too (countEmpty=true), but it then falls through to on-demand creation and never
	// returns the sentinel to its caller; GetWithContext's internal retries do not count
	// (acquireOnce→tryAcquire(false) — the wait loop never pollutes this counter)
	EmptyCount int64

	// FullPutCount 是 Put 返回 ErrPoolFull 的次数
	// FullPutCount is the number of times Put returned ErrPoolFull
	FullPutCount int64

	// WaitCount 是 GetWithContext 注册 waiter 并实际进入挂起的累计次数（spurious wake 后重新
	// 挂起会再次计数）
	// WaitCount is the cumulative number of times GetWithContext registered a waiter and actually
	// suspended (re-suspending after a spurious wake counts again)
	WaitCount int64

	// WaitTimeoutCount 是 GetWithContext 挂起等待后因超时返回 context.DeadlineExceeded 的次数
	//（取消不计入；入口 fast-fail 与循环头 ctx 检查的返回也不计入）
	// WaitTimeoutCount is the number of times GetWithContext timed out with
	// context.DeadlineExceeded after having suspended (cancellations do not count, and neither do
	// returns from the entry fast-fail or the loop-head ctx check)
	WaitTimeoutCount int64

	// TotalWaitDuration 是 GetWithContext 注册到醒来（被唤醒、超时或取消）的累计等待时长
	//（配合 WaitCount 可得平均等待；P99 需用户侧直方图）
	// TotalWaitDuration is the cumulative time GetWithContext waiters spent from registration to
	// wake-up (woken, timed out or cancelled); divided by WaitCount it yields the average wait —
	// a P99 requires a user-side histogram
	TotalWaitDuration time.Duration
}

// Stats 返回池统计指标的当前快照。只读操作：累计计数经 atomic 读取（O(1)），
// IdleCount/TombstoneCount 经 queue.Range 遍历一次队列统计（O(N)，对 maxSize ≤ 数千的
// 常规场景可接受；N1 后 tombstone GC 每轮扫描把队列长度收敛回真实活对象数（上界
// ~maxSize+minIdle 的瞬时窗口），零流量下 N 亦不随时间增长，该声明前提恒成立；
// 遍历持队列锁并在元素锁内读取 data/retries 的一致对——锁序沿用既有边
// queue.lock→element.mu，与 closeAllIdle 同构，无新增边）。可在任意 goroutine 调用，但禁止在
// 销毁/健康检查回调（closeFunc、OnClose、OnPingSuccess、OnPingFailure）内调用（P2-4）：
// Cleanup 触发的 OnClose 位于 queue.Range 回调内、持有 queue.lock，Stats() 的 Range/Len 重入
// 即死锁；其余 OnClose 触发路径与 ping 回调当前虽不持队列锁，但那是实现细节而非契约（且 O(N)
// 遍历在 supervise goroutine 内会拖慢后台监督）——统一禁止，与 README Ownership 节的既有警告
// 对齐。BeforeGet/AfterPut 钩子在调用方 goroutine 锁外执行，钩子内调用 Stats/Len 不会死锁
// （TestPool_Hook_RunsWithoutPoolLocks 钉住；写操作 API 的重入仍被禁止）。
// Stop 之后调用同样安全（关闭队列的遍历为空，返回归零后的水位与保留的累计计数）。
// 快照非跨字段原子：并发负载下各字段可能对应略有先后的时刻（见 Stats 类型文档的一致性说明）
// Stats returns the pool's current statistics snapshot. A read-only operation: the cumulative
// counters are read atomically (O(1)), while IdleCount/TombstoneCount are tallied by one
// queue.Range traversal (O(N), acceptable for the usual maxSize ≤ a few thousand; after N1 the
// tombstone GC converges the queue length back to the true live-object count every pass (an
// upper bound of ~maxSize+minIdle in transient windows), so N never grows with time even under
// zero traffic and that premise always holds; the traversal
// holds the queue lock and reads the consistent data/retries pair under each element lock — the
// lock order reuses the existing queue.lock→element.mu edge, isomorphic to closeAllIdle, with no
// new edge). Safe to call from any goroutine, except inside the destruction/health callbacks
// (closeFunc, OnClose, OnPingSuccess, OnPingFailure) (P2-4): the OnClose fired by Cleanup runs
// inside the queue.Range callback holding queue.lock, so re-entering through Stats()'s Range/Len
// deadlocks; the other OnClose trigger paths and the ping callbacks hold no queue lock today, but
// that is an implementation detail rather than a contract (and an O(N) traversal inside the
// supervise goroutine would delay supervision) — hence the uniform ban, aligned with the existing
// Ownership warning in the README. The BeforeGet/AfterPut hooks run in the caller's goroutine with
// no pool lock held, so calling Stats/Len inside them cannot deadlock (pinned by
// TestPool_Hook_RunsWithoutPoolLocks; re-entering mutating APIs remains forbidden). Calling it
// after Stop is equally safe (traversing a closed queue is empty, returning the zeroed water
// levels with the cumulative counters retained). The snapshot is not cross-field atomic: under a
// concurrent load the fields may correspond to moments slightly apart (see the consistency note
// in the Stats type documentation)
func (p *Pool) Stats() Stats {
	s := Stats{
		MaxSize:             p.config.maxSize,
		MinIdle:             p.config.minIdle,
		TotalCreated:        p.stats.totalCreated.Load(),
		TotalDestroyed:      p.stats.totalDestroyed.Load(),
		TotalBorrowed:       p.stats.totalBorrowed.Load(),
		TotalReturned:       p.stats.totalReturned.Load(),
		DestroyedByPing:     p.stats.destroyedByPing.Load(),
		DestroyedByLifetime: p.stats.destroyedByLifetime.Load(),
		DestroyedByIdle:     p.stats.destroyedByIdle.Load(),
		DestroyedByBorrow:   p.stats.destroyedByBorrow.Load(),
		DestroyedByStop:     p.stats.destroyedByStop.Load(),
		EmptyCount:          p.stats.emptyCount.Load(),
		FullPutCount:        p.stats.fullPutCount.Load(),
		WaitCount:           p.stats.waitCount.Load(),
		WaitTimeoutCount:    p.stats.waitTimeoutCount.Load(),
		TotalWaitDuration:   time.Duration(p.stats.totalWaitDurationNs.Load()),
	}
	// ActiveCount 派生（报告要点 3）：借出账本减归还账本，避免独立计数漂移；
	// 外部收编对象的归还无配对借出，混合负载下可为负（D2 固有边界，见字段文档）
	// ActiveCount derived (report point 3): the borrow ledger minus the return ledger, avoiding an
	// independent counter's drift; a foreign adopted object's return has no paired borrow, so mixed
	// workloads may push it negative (the inherent D2 boundary — see the field documentation)
	s.ActiveCount = int(s.TotalBorrowed - s.TotalReturned)
	// IdleCount/TombstoneCount 经一次 Range 遍历统计（报告要点 4）：data!=nil 且 retries>=0 为
	// 有效空闲；其余（认领后置 nil 的 tombstone）计入墓碑。data/retries 在元素锁内成对读取，
	// 与销毁路径的锁内认领互斥，不会读到撕裂的中间态
	// IdleCount/TombstoneCount are tallied by one Range traversal (report point 4): data!=nil with
	// retries>=0 is an effective idle object; the rest (tombstones claimed to nil) count as
	// tombstones. data/retries are read as a pair under the element lock, mutually exclusive with
	// the destruction paths' under-lock claims, so no torn intermediate state is ever observed
	p.queue.Range(func(item any) bool {
		element := item.(*pool.Element)
		element.Lock()
		value := element.GetDataNoLock()
		retries := element.GetRetriesNoLock()
		element.Unlock()
		if value != nil && retries >= 0 {
			s.IdleCount++
		} else {
			s.TombstoneCount++
		}
		return true
	})
	return s
}
