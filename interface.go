package conecta

// 定义函数类型 PingFunc，接收两个参数，返回一个布尔值
// PingFunc 是用于检查元素是否可用的函数，如果元素可用，返回 true，否则返回 false
// Define function type PingFunc, which takes two parameters and returns a boolean value
// PingFunc is a function used to check whether an element is available. If the element is available, it returns true, otherwise it returns false
type PingFunc = func(element any, retryCount int) bool

// 定义函数类型 NewFunc，返回一个任意类型的元素和一个错误
// NewFunc 是用于创建新元素的函数
// Define function type NewFunc, which returns an element of any type and an error
// NewFunc is a function used to create new elements
type NewFunc = func() (element any, err error)

// 定义函数类型 CloseFunc，接收一个任意类型的元素，返回一个错误
// CloseFunc 是用于关闭元素的函数
// Define function type CloseFunc, which takes an element of any type and returns an error
// CloseFunc is a function used to close elements
type CloseFunc = func(element any) error

// 定义函数类型 RangeFunc，接收一个任意类型的元素，返回一个布尔值
// RangeFunc 是用于遍历元素的函数，如果返回 true，则继续遍历，否则停止遍历
// Define function type RangeFunc, which takes an element of any type and returns a boolean value
// RangeFunc is a function used to traverse elements. If it returns true, the traversal continues, otherwise the traversal stops
type RangeFunc = func(element any) bool

// Callback 是一个接口，定义了三个方法，分别在 Ping 成功、失败和关闭时调用
// Callback is an interface that defines three methods, which are called when Ping is successful, fails, and closes, respectively
type Callback = interface {
	// 在 Ping 成功时调用
	// Called when a validate is successful
	OnPingSuccess(any)

	// 在 Ping 失败时调用
	// Called when a validate fails
	OnPingFailure(any)

	// 在销毁对象时调用
	// Called when an object is destroyed
	OnClose(any, error)
}

// QueueInterface 是一个接口，定义了队列的基本操作，如添加元素、获取长度、遍历元素、获取元素、标记元素处理完成、关闭队列和判断队列是否已关闭
// QueueInterface is an interface that defines the basic operations of a queue, such as adding elements, getting the length, iterating over elements, getting elements, marking elements as processed, closing the queue, and determining whether the queue is closed
type QueueInterface = interface {
	// 添加一个元素到队列
	// Add an element to the queue
	Add(element any) error

	// 获得 queue 的长度
	// Get the length of the queue
	Len() int

	// 遍历队列中的元素，如果 fn 返回 false，则停止遍历
	// Iterate over the elements in the queue, if fn returns false, stop iterating
	Range(fn func(element any) bool)

	// 获得 queue 中的一个元素，如果 queue 为空，返回 ErrorQueueEmpty
	// Get an element from the queue, if the queue is empty, return ErrorQueueEmpty
	Get() (element any, err error)

	// 标记元素已经处理完成
	// Mark an element as processed
	Done(element any)

	// 关闭队列
	// Close the queue
	Stop()

	// 判断队列是否已经关闭
	// Determine whether the queue is closed
	IsClosed() bool
}
