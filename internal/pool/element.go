package pool

import "sync"

// Element 表示由 Group 和 Queue 使用的结构体，包含数据和重试计数两个字段
// Element represents a struct used by Group and Queue, containing two fields: data and retries
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

// Reset 重置 Element 的数据和重试计数字段，将数据字段设置为 nil，将重试计数字段设置为 0
// Reset resets the data and retries fields of Element, setting the data field to nil and the retries field to 0
func (e *Element) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.data = nil
	e.retries = 0
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

// PutRaw 将一个 Element 对象放入对象池但跳过 Reset，调用方必须保证元素状态合法（字段已在元素锁内清理完毕）
// PutRaw puts an Element object into the object pool but skips Reset; the caller must guarantee the element state is valid (fields already cleared under the element lock)
func (p *ElementPool) PutRaw(e *Element) {
	if e != nil {
		p.pool.Put(e)
	}
}
