package internal

import "sync"

// Element 表示由 Group 和 Queue 使用的结构体
// Element represents a struct used by Group and Queue
type Element struct {
	data  interface{} // 数据
	value int64       // 值
}

// GetData 返回 Element 的数据
// GetData returns the data of Element
func (e *Element) GetData() interface{} {
	return e.data
}

// GetValue 返回 Element 的值
// GetValue returns the value of Element
func (e *Element) GetValue() int64 {
	return e.value
}

// SetData 设置 Element 的数据
// SetData sets the data of Element
func (e *Element) SetData(data interface{}) {
	e.data = data
}

// SetValue 设置 Element 的值
// SetValue sets the value of Element
func (e *Element) SetValue(value int64) {
	e.value = value
}

// Reset 重置 Element 的数据和值
// Reset resets the data and value of Element
func (e *Element) Reset() {
	e.data = nil
	e.value = 0
}

// ElementPool 表示一个对象池
// ElementPool represents an object pool
type ElementPool struct {
	pool *sync.Pool // 对象池
}

// NewElementPool 创建一个新的对象池
// NewElementPool creates a new object pool
func NewElementPool() *ElementPool {
	return &ElementPool{
		pool: &sync.Pool{
			New: func() interface{} {
				return &Element{} // 创建一个新的 Element 对象
			},
		},
	}
}

// Get 从对象池中获取一个 Element
// Get gets an Element from the object pool
func (p *ElementPool) Get() *Element {
	return p.pool.Get().(*Element)
}

// Put 将一个 Element 放入对象池
// Put puts an Element into the object pool
func (p *ElementPool) Put(e *Element) {
	if e != nil {
		e.Reset() // 重置 Element
		p.pool.Put(e)
	}
}
