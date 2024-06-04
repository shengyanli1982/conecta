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

// Queue 接口定义了一个队列应该具备的基本操作。
// The Queue interface defines the basic operations that a queue should have.
type Queue = interface {
	// Put 方法用于将元素放入队列。
	// The Put method is used to put an element into the queue.
	Put(value interface{}) error

	// Get 方法用于从队列中获取元素。
	// The Get method is used to get an element from the queue.
	Get() (value interface{}, err error)

	// Done 方法用于标记元素处理完成。
	// The Done method is used to mark the element as done.
	Done(value interface{})

	// Len 方法用于获取队列的长度。
	// The Len method is used to get the length of the queue.
	Len() int

	// Values 方法用于获取队列中的所有元素。
	// The Values method is used to get all the elements in the queue.
	Values() []interface{}

	// Range 方法用于遍历队列中的所有元素。
	// The Range method is used to traverse all elements in the queue.
	Range(fn func(value interface{}) bool)

	// Shutdown 方法用于关闭队列。
	// The Shutdown method is used to shut down the queue.
	Shutdown()

	// IsClosed 方法用于检查队列是否已关闭。
	// The IsClosed method is used to check if the queue is closed.
	IsClosed() bool
}
