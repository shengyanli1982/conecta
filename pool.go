package conecta

import (
	"context"
	"errors"
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
	size        atomic.Int64       // 当前池内元素计数，镜像 queue.Len() 语义（含 tombstone）；无锁原子量，不参与 stopGate→queue.lock→element.mu 锁序 / current element count in the pool, mirroring queue.Len() semantics (including tombstones); lock-free atomic outside the stopGate→queue.lock→element.mu lock order
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

		// Wrap value in element and enqueue
		// 将 value 包装到 element 并入队
		element := p.elementPool.Get()
		element.SetData(value)
		if err := p.queue.Put(element); err != nil {
			// queue.Put 失败：归还 wrapper 并关闭本次创建的 value，并清理已入队的元素
			// queue.Put failed: return wrapper, close this value (never enqueued), and drain already enqueued elements
			p.elementPool.Put(element)
			if value != nil {
				_ = p.config.closeFunc(value)
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
			_ = p.config.closeFunc(v)
		}
		p.elementPool.Put(wrapper)
	}
}

// Stop gracefully shuts down the pool
// 优雅地关闭连接池
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
		p.Cleanup()
		p.queue.Shutdown()
	})
}

// Cleanup cleans up all elements in the pool
// 清理连接池中的所有元素
func (p *Pool) Cleanup() {
	// Phase 1: Close all values but do NOT return wrappers to elementPool
	// 第一阶段：关闭所有 value，但不归还 wrapper 到 elementPool
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
		return true
	})
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
		p.elementPool.Put(wrapper)
	}
}

// Get retrieves an element from the pool
// 从连接池中获取一个元素
func (p *Pool) Get() (any, error) {
	// Check if queue is closed
	// 检查队列是否已关闭
	if p.queue.IsClosed() {
		return nil, ErrQueueClosed
	}

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
			continue
		}
		wrapper.SetRetriesNoLock(0)
		// Clear the reference so the wrapper returned to sync.Pool does not retain the user value
		// 清除引用，防止归还 sync.Pool 的 wrapper 滞留用户 value
		wrapper.SetDataNoLock(nil)
		wrapper.Unlock()

		p.elementPool.PutRaw(wrapper)
		return value, nil
	}
}

// GetOrCreate gets an element from the pool or creates a new one if none is available
// 从连接池获取元素，如果没有可用元素则创建新的
func (p *Pool) GetOrCreate() (any, error) {
	value, err := p.Get()
	if err == nil {
		return value, nil
	}

	// 池已关闭：不创建新元素
	// Pool is closed: do not create new elements
	if errors.Is(err, ErrQueueClosed) {
		return nil, err
	}

	return p.config.newFunc()
}

// Put adds a new element to the pool
// 向连接池中添加新元素
func (p *Pool) Put(data any) error {
	// 拒绝 nil 值
	// Reject nil values
	if data == nil {
		return ErrValueIsNil
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

	// CAS 预留一个容量槽位；池满时返回预分配哨兵 ErrPoolFull（零分配，容量检查位于 closed 检查之后）
	// Reserve a capacity slot via CAS; return the pre-allocated sentinel ErrPoolFull when full (zero allocation, the capacity check runs after the closed check)
	for {
		cur := p.size.Load()
		if cur >= int64(p.config.maxSize) {
			return ErrPoolFull
		}
		if p.size.CompareAndSwap(cur, cur+1) {
			break
		}
	}

	element := p.elementPool.Get()
	// wrapper 来自 elementPool（sync.Pool 返回的独占对象），在 queue.Put 发布前为当前 goroutine 独占，无需加锁
	// The wrapper obtained from elementPool (an exclusive object returned by sync.Pool) is exclusively owned by this goroutine before being published via queue.Put, no lock needed
	element.SetDataNoLock(data)
	if err := p.queue.Put(element); err != nil {
		// 入队失败：先释放已预留的容量槽位，再归还 wrapper
		// Enqueue failed: release the reserved capacity slot first, then return the wrapper
		p.size.Add(-1)
		p.elementPool.Put(element)
		// 队列被并发关闭时统一映射为 ErrQueueClosed；其余错误（如幂等队列的 AlreadyExist）原样透传
		// Map a concurrently closed queue to ErrQueueClosed; pass through other errors (e.g. AlreadyExist from idempotent queues) as-is
		if p.queue.IsClosed() {
			return ErrQueueClosed
		}
		return err
	}
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
// 检查并维护连接池元素状态
func (p *Pool) supervise() {
	// 使用 Values() 获取队列短锁快照，不再持有队列锁执行用户 I/O
	// Take a short-lock snapshot of the queue via Values() instead of holding the queue lock during user I/O
	for _, item := range p.queue.Values() {
		element := item.(*pool.Element)
		element.Lock()
		value := element.GetDataNoLock()
		if value == nil {
			element.Unlock()
			continue
		}
		retryCount := int(element.GetRetriesNoLock())
		if retryCount < 0 {
			element.Unlock()
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
		} else {
			element.SetRetriesNoLock(int64(retryCount))
			element.Unlock()
			p.config.callback.OnPingFailure(value)
		}
	}
}
