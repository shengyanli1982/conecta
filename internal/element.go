package internal

import "sync"

// Element 是一个结构体，用于 Group 和 Queue
// Element is a struct, used by Group and Queue
type Element struct {
	data  any   // 数据
	value int64 // 值
}

// GetData 是一个方法，用于获取 Element 的数据
// GetData is a method to get the data of Element
func (e *Element) GetData() any {
	return e.data
}

// GetValue 是一个方法，用于获取 Element 的值
// GetValue is a method to get the value of Element
func (e *Element) GetValue() int64 {
	return e.value
}

// SetData 是一个方法，用于设置 Element 的数据
// SetData is a method to set the data of Element
func (e *Element) SetData(data any) {
	e.data = data
}

// SetValue 是一个方法，用于设置 Element 的值
// SetValue is a method to set the value of Element
func (e *Element) SetValue(value int64) {
	e.value = value
}

// Reset 是一个方法，用于重置 Element 的数据和值
// Reset is a method to reset the data and value of Element
func (e *Element) Reset() {
	e.data = nil
	e.value = 0
}

// ElementPool 是一个结构体，表示对象池
// ElementPool is a struct, representing an object pool
type ElementPool struct {
	pool *sync.Pool // 对象池
}

// NewElementPool 是一个函数，用于新建对象池
// NewElementPool is a function to create a new object pool
func NewElementPool() *ElementPool {
	return &ElementPool{
		pool: &sync.Pool{
			New: func() any {
				return &Element{} // 新建一个 Element 对象
			},
		},
	}
}

// Get 是一个方法，用于从对象池中获取一个 Element
// Get is a method to get an Element from the object pool
func (p *ElementPool) Get() *Element {
	return p.pool.Get().(*Element)
}

// Put 是一个方法，用于将 Element 放入对象池
// Put is a method to put an Element into the object pool
func (p *ElementPool) Put(e *Element) {
	if e != nil {
		e.Reset() // 重置 Element
		p.pool.Put(e)
	}
}
