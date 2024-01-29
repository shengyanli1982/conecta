package internal

import "sync"

// 工作元素, 用于 Group 和 Queue
// Worker Element, used by Group and Queue
type Element struct {
	data  any
	value int64
}

// 获取数据
// get data.
func (e *Element) GetData() any {
	return e.data
}

// 获取值
// get value.
func (e *Element) GetValue() int64 {
	return e.value
}

// 设置数据
// set data.
func (e *Element) SetData(data any) {
	e.data = data
}

// 设置值
// set value.
func (e *Element) SetValue(value int64) {
	e.value = value
}

// 重置
// reset.
func (e *Element) Reset() {
	e.data = nil
	e.value = 0
}

// 对象池
// object ElementPool.
type ElementPool struct {
	pool *sync.Pool
}

// 新建对象池
// new ObjectPool.
func NewElementPool() *ElementPool {
	return &ElementPool{
		pool: &sync.Pool{
			New: func() any {
				return &Element{}
			},
		},
	}
}

// 从对象池中获取一个元素
// Get an element from the object pool.
func (p *ElementPool) Get() *Element {
	return p.pool.Get().(*Element)
}

// 将元素放入对象池
// Put an element into the object pool.
func (p *ElementPool) Put(e *Element) {
	if e != nil {
		e.Reset()
		p.pool.Put(e)
	}
}
