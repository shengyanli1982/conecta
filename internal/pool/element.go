package pool

import "sync"

// Element 表示由 Group 和 Queue 使用的结构体，包含数据和值两个字段
// Element represents a struct used by Group and Queue, containing two fields: data and value
type Element struct {
	// mu 保护 data 和 value 字段的并发访问
	// mu protects data and value fields for concurrent access
	mu sync.Mutex

	// data 是 Element 的数据字段，可以存储任何类型的数据
	// data is the data field of Element, which can store data of any type
	data interface{}

	// value 是 Element 的值字段，存储一个 64 位的整数
	// value is the value field of Element, storing a 64-bit integer
	value int64
}

// GetData 返回 Element 的数据字段
// GetData returns the data field of Element
func (e *Element) GetData() interface{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.data
}

// GetValue 返回 Element 的值字段
// GetValue returns the value field of Element
func (e *Element) GetValue() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.value
}

// SetData 设置 Element 的数据字段
// SetData sets the data field of Element
func (e *Element) SetData(data interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.data = data
}

// SetValue 设置 Element 的值字段
// SetValue sets the value field of Element
func (e *Element) SetValue(value int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.value = value
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

// GetDataNoLock 在不加锁的情况下返回 Element 的数据字段，调用方必须已持有 mu 锁
// GetDataNoLock returns the data field of Element without acquiring the lock, caller must hold mu
func (e *Element) GetDataNoLock() interface{} {
	return e.data
}

// GetValueNoLock 在不加锁的情况下返回 Element 的值字段，调用方必须已持有 mu 锁
// GetValueNoLock returns the value field of Element without acquiring the lock, caller must hold mu
func (e *Element) GetValueNoLock() int64 {
	return e.value
}

// SetDataNoLock 在不加锁的情况下设置 Element 的数据字段，调用方必须已持有 mu 锁
// SetDataNoLock sets the data field of Element without acquiring the lock, caller must hold mu
func (e *Element) SetDataNoLock(data interface{}) {
	e.data = data
}

// SetValueNoLock 在不加锁的情况下设置 Element 的值字段，调用方必须已持有 mu 锁
// SetValueNoLock sets the value field of Element without acquiring the lock, caller must hold mu
func (e *Element) SetValueNoLock(value int64) {
	e.value = value
}

// Reset 重置 Element 的数据和值字段，将数据字段设置为 nil，将值字段设置为 0
// Reset resets the data and value fields of Element, setting the data field to nil and the value field to 0
func (e *Element) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.data = nil
	e.value = 0
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
			New: func() interface{} {
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

func (p *ElementPool) PutRaw(e *Element) {
	if e != nil {
		p.pool.Put(e)
	}
}
