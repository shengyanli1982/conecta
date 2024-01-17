package conecta

import "sync"

// 工作元素, 用于 Group 和 Queue
// Worker element, used by Group and Queue
type element struct {
	data  any
	value int64
}

// 获取数据
// get data.
func (e *element) GetData() any {
	return e.data
}

// 获取值
// get value.
func (e *element) GetValue() int64 {
	return e.value
}

// 设置数据
// set data.
func (e *element) SetData(data any) {
	e.data = data
}

// 设置值
// set value.
func (e *element) SetValue(value int64) {
	e.value = value
}

// 重置
// reset.
func (e *element) Reset() {
	e.data = nil
	e.value = 0
}

// 对象池
// object ObjectPool.
type ObjectPool struct {
	pool *sync.Pool
}

// 新建对象池
// new ObjectPool.
func NewElementPool() *ObjectPool {
	return &ObjectPool{
		pool: &sync.Pool{
			New: func() any {
				return &element{}
			},
		},
	}
}

// 从对象池中获取一个元素
// Get an element from the object pool.
func (p *ObjectPool) Get() *element {
	return p.pool.Get().(*element)
}

// 将元素放入对象池
// Put an element into the object pool.
func (p *ObjectPool) Put(e *element) {
	if e != nil {
		e.Reset()
		p.pool.Put(e)
	}
}
