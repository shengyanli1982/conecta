package conecta

// 回调接口
// Callback is the interface that receives callbacks from the queue.
type Callback interface {
	OnPingSuccess(any)  // 在 Ping 成功时调用 (called when a validate is successful)
	OnPingFailure(any)  // 在 Ping 失败时调用 (called when a validate fails)
	OnClose(any, error) // 在销毁对象时调用 (called when an object is destroyed)
}

// 空回调实现
// emptyCallback is a no-op implementation of Callback.
type emptyCallback struct{}

func (c *emptyCallback) OnPingSuccess(any)  {} // 空实现 (no-op)
func (c *emptyCallback) OnPingFailure(any)  {} // 空实现 (no-op)
func (c *emptyCallback) OnClose(any, error) {} // 空实现 (no-op)

// 新建空回调
// newEmptyCallback returns a new emptyCallback.
func newEmptyCallback() *emptyCallback {
	return &emptyCallback{}
}

// 队列方法接口
// Queue interface
type QueueInterface interface {
	// 添加一个元素到队列
	// Add adds an element to the queue.
	Add(element any) error

	// 获得 queue 的长度
	// Len returns the number of elements in the queue.
	Len() int

	// 遍历队列中的元素，如果 fn 返回 false，则停止遍历
	// Range iterates over each element in the queue and calls the provided function.
	// If the function returns false, the iteration stops.
	Range(fn func(element any) bool)

	// 获得 queue 中的一个元素，如果 queue 为空，返回 ErrorQueueEmpty
	// Get retrieves an element from the queue.
	Get() (element any, err error)

	// 标记元素已经处理完成
	// Done marks an element as processed and removes it from the queue.
	Done(element any)

	// 关闭队列
	// Stop stops the queue and releases any resources.
	Stop()

	// 判断队列是否已经关闭
	// IsClosed returns true if the queue is closed, false otherwise.
	IsClosed() bool
}
