package pool

import (
	"sync"
	"sync/atomic"
)

// Element 表示由 Group 和 Queue 使用的结构体，包含数据、重试计数与三个时间戳字段
// Element represents a struct used by Group and Queue, containing the data, retry count and three timestamp fields
type Element struct {
	// mu 保护 data 和 retries 字段的并发访问
	// mu protects data and retries fields for concurrent access
	mu sync.Mutex

	// data 是 Element 的数据字段，可以存储任何类型的数据
	// data is the data field of Element, which can store data of any type
	data any

	// retries 是 Element 的重试计数字段，存储重试计数，-1 表示已销毁
	// retries is the retries field of Element, storing retry count, -1 marks destroyed
	retries int64

	// createdAt 是元素（wrapper）本次入池时刻的 UnixNano 时间戳；atomic 读写，不受 mu 保护、不参与锁序。
	// 用途声明（P2-5）：stampEnqueue 在每次入队时重打本戳，故当前数值恒等于 idleSince，且生产
	// 判定路径无读取（GetCreatedAt 仅被复用清零契约测试调用）。受 wrapper/value 解耦限制——
	// 借出时 wrapper 归还 sync.Pool、归还时 value 获得新 wrapper——本字段不追踪 value 跨借还
	// 循环的首次创建时刻；保留作未来"对象年龄自首次创建起算"语义（需引入 value→wrapper 映射）
	// 的预留原点字段
	// createdAt is the UnixNano timestamp when the element (wrapper) entered the pool this time; read/written atomically, not guarded by mu and outside the lock order.
	// Purpose note (P2-5): stampEnqueue re-stamps it on every enqueue, so its value currently always equals idleSince, and no production decision path reads it (GetCreatedAt is called only by the reuse-clearing contract tests). Limited by the wrapper/value decoupling — the wrapper goes back to sync.Pool on borrow and the value gets a fresh wrapper on return — this field does not track the value's first-creation moment across borrow/return cycles; it is kept as the reserved origin for a future "age counted from first creation" semantic (which would require a value→wrapper mapping)
	createdAt atomic.Int64

	// expireAt 是元素到期时刻的 UnixNano 时间戳，入池时一次性算好（含抖动）。0 表示未打时间戳：
	// 年龄淘汰关闭时判定侧短路、该值不会被读取；年龄淘汰开启时等同于“立即到期”（fail-closed）
	// expireAt is the UnixNano timestamp at which the element expires, computed once on enqueue (jitter included).
	// 0 means unstamped: the check short-circuits and never reads it while age eviction is disabled, while with age
	// eviction enabled it counts as "expired immediately" (fail-closed)
	expireAt atomic.Int64

	// idleSince 是元素最近一次入队（开始空闲）时刻的 UnixNano 时间戳；atomic 读写，不受 mu 保护、不参与锁序
	// idleSince is the UnixNano timestamp of the element's most recent enqueue (idle start); read/written atomically, not guarded by mu and outside the lock order
	idleSince atomic.Int64
}

// GetData 返回 Element 的数据字段
// GetData returns the data field of Element
func (e *Element) GetData() any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.data
}

// GetRetries 返回 Element 的重试计数字段
// GetRetries returns the retries field of Element
func (e *Element) GetRetries() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.retries
}

// SetData 设置 Element 的数据字段
// SetData sets the data field of Element
func (e *Element) SetData(data any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.data = data
}

// SetRetries 设置 Element 的重试计数字段
// SetRetries sets the retries field of Element
func (e *Element) SetRetries(retries int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.retries = retries
}

// Lock 锁定 Element 的互斥锁
// Lock locks the mutex of Element
func (e *Element) Lock() {
	e.mu.Lock()
}

// Unlock 解锁 Element 的互斥锁
// Unlock unlocks the mutex of Element
func (e *Element) Unlock() {
	e.mu.Unlock()
}

// GetDataNoLock 在不加锁的情况下返回 Element 的数据字段，调用方必须已持有 mu 锁，或对尚未发布（未入队）的 Element 拥有排他所有权
// GetDataNoLock returns the data field of Element without acquiring the lock, caller must hold mu, or exclusively own the element before it is published (enqueued)
func (e *Element) GetDataNoLock() any {
	return e.data
}

// GetRetriesNoLock 在不加锁的情况下返回 Element 的重试计数字段，调用方必须已持有 mu 锁，或对尚未发布（未入队）的 Element 拥有排他所有权
// GetRetriesNoLock returns the retries field of Element without acquiring the lock, caller must hold mu, or exclusively own the element before it is published (enqueued)
func (e *Element) GetRetriesNoLock() int64 {
	return e.retries
}

// SetDataNoLock 在不加锁的情况下设置 Element 的数据字段，调用方必须已持有 mu 锁，或对尚未发布（未入队）的 Element 拥有排他所有权
// SetDataNoLock sets the data field of Element without acquiring the lock, caller must hold mu, or exclusively own the element before it is published (enqueued)
func (e *Element) SetDataNoLock(data any) {
	e.data = data
}

// SetRetriesNoLock 在不加锁的情况下设置 Element 的重试计数字段，调用方必须已持有 mu 锁，或对尚未发布（未入队）的 Element 拥有排他所有权
// SetRetriesNoLock sets the retries field of Element without acquiring the lock, caller must hold mu, or exclusively own the element before it is published (enqueued)
func (e *Element) SetRetriesNoLock(retries int64) {
	e.retries = retries
}

// GetCreatedAt 返回元素本次入池时刻的 UnixNano 时间戳；atomic 读取，不加 mu 锁，持有 mu 时调用同样安全
// GetCreatedAt returns the UnixNano timestamp when the element entered the pool this time; atomic read without acquiring mu, safe to call while holding mu
func (e *Element) GetCreatedAt() int64 {
	return e.createdAt.Load()
}

// SetCreatedAt 设置元素本次入池时刻的 UnixNano 时间戳；atomic 写入，不加 mu 锁，持有 mu 时调用同样安全
// SetCreatedAt sets the UnixNano timestamp when the element entered the pool this time; atomic write without acquiring mu, safe to call while holding mu
func (e *Element) SetCreatedAt(createdAt int64) {
	e.createdAt.Store(createdAt)
}

// GetExpireAt 返回元素到期时刻的 UnixNano 时间戳，0 表示未打时间戳（语义见字段说明）；atomic 读取，不加 mu 锁，持有 mu 时调用同样安全
// GetExpireAt returns the UnixNano timestamp at which the element expires, 0 meaning unstamped (see the field doc for the semantics); atomic read without acquiring mu, safe to call while holding mu
func (e *Element) GetExpireAt() int64 {
	return e.expireAt.Load()
}

// SetExpireAt 设置元素到期时刻的 UnixNano 时间戳；atomic 写入，不加 mu 锁，持有 mu 时调用同样安全
// SetExpireAt sets the UnixNano timestamp at which the element expires; atomic write without acquiring mu, safe to call while holding mu
func (e *Element) SetExpireAt(expireAt int64) {
	e.expireAt.Store(expireAt)
}

// GetIdleSince 返回元素最近一次入队时刻的 UnixNano 时间戳；atomic 读取，不加 mu 锁，持有 mu 时调用同样安全
// GetIdleSince returns the UnixNano timestamp of the element's most recent enqueue; atomic read without acquiring mu, safe to call while holding mu
func (e *Element) GetIdleSince() int64 {
	return e.idleSince.Load()
}

// SetIdleSince 设置元素最近一次入队时刻的 UnixNano 时间戳；atomic 写入，不加 mu 锁，持有 mu 时调用同样安全
// SetIdleSince sets the UnixNano timestamp of the element's most recent enqueue; atomic write without acquiring mu, safe to call while holding mu
func (e *Element) SetIdleSince(idleSince int64) {
	e.idleSince.Store(idleSince)
}

// Reset 重置 Element 的数据、重试计数与全部时间戳字段：数据置 nil、重试计数置 0、时间戳清零。
// wrapper 经 ElementPool 复用，时间戳必须在此清零，杜绝陈旧到期时刻污染下一个使用者
// Reset resets the data, retry count and all timestamp fields of Element: data to nil, retries to 0, timestamps to zero.
// Wrappers are reused through ElementPool, so the timestamps must be cleared here to prevent a stale expiry from polluting the next user
func (e *Element) Reset() {
	e.mu.Lock()
	e.data = nil
	e.retries = 0
	e.mu.Unlock()
	// 时间戳走 atomic，不属于 mu 的保护范围
	// Timestamps are atomic and outside mu's protection
	e.createdAt.Store(0)
	e.expireAt.Store(0)
	e.idleSince.Store(0)
}

// ElementPool 表示一个对象池，用于存储和管理 Element 对象
// ElementPool represents an object pool, used for storing and managing Element objects
type ElementPool struct {
	// pool 是一个同步的对象池，用于存储 Element 对象
	// pool is a synchronous object pool, used for storing Element objects
	pool *sync.Pool
}

// NewElementPool 创建一个新的对象池，该对象池用于存储 Element 对象
// NewElementPool creates a new object pool, this object pool is used for storing Element objects
func NewElementPool() *ElementPool {
	return &ElementPool{
		// 初始化一个同步的对象池，该对象池的新建函数返回一个新的 Element 对象
		// Initialize a synchronous object pool, the new function of this object pool returns a new Element object
		pool: &sync.Pool{
			// New 返回一个新的 Element 对象
			// New returns a new Element object
			New: func() any {
				return &Element{}
			},
		},
	}
}

// Get 从对象池中获取一个 Element 对象，如果对象池为空，则新建一个 Element 对象
// Get gets an Element object from the object pool, if the object pool is empty, a new Element object is created
func (p *ElementPool) Get() *Element {
	return p.pool.Get().(*Element)
}

// Put 将一个 Element 对象放入对象池，如果 Element 对象不为空，则重置 Element 对象并放入对象池
// Put puts an Element object into the object pool, if the Element object is not null, reset the Element object and put it into the object pool
func (p *ElementPool) Put(e *Element) {
	// 如果 Element 对象不为空
	// If the Element object is not null
	if e != nil {
		// 重置 Element 对象
		// Reset the Element object
		e.Reset()

		// 将 Element 对象放入对象池
		// Put the Element object into the object pool
		p.pool.Put(e)
	}
}

// PutRaw 将一个 Element 对象放入对象池但跳过加锁的 Reset：data 与 retries 须由调用方在元素锁内清理完毕，
// 而三个 atomic 时间戳在此统一清零（免锁、开销恒定），使陈旧时间戳泄漏在结构上不可能发生
// PutRaw puts an Element object into the object pool but skips the locking Reset: data and retries must already
// have been cleared by the caller under the element lock, while the three atomic timestamps are always cleared
// here (lock-free, constant cost), making a stale-timestamp leak structurally impossible
func (p *ElementPool) PutRaw(e *Element) {
	if e != nil {
		e.createdAt.Store(0)
		e.expireAt.Store(0)
		e.idleSince.Store(0)
		p.pool.Put(e)
	}
}
