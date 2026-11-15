package conecta

// 定义函数类型 PingFunc，接收两个参数，返回一个布尔值
// PingFunc 是用于检查元素是否可用的函数，如果元素可用，返回 true，否则返回 false
// Define function type PingFunc, which takes two parameters and returns a boolean value
// PingFunc is a function used to check whether an element is available. If the element is available, it returns true, otherwise it returns false
type PingFunc = func(element any, retryCount int) bool

// 定义函数类型 NewFunc，返回一个任意类型的元素和一个错误
// NewFunc 是用于创建新元素的函数。契约：成功（err == nil）时必须返回非 nil 元素——
// 池视 nil 为非法值（Put 拒绝 nil），创建路径对 (nil, nil) 同样显式拒绝并返回 ErrValueIsNil，
// 绝不静默计数（born-nil 对象会打破“nil ⟹ 配额已释放”的销毁豁免前提，永久泄漏在途配额）
// Define function type NewFunc, which returns an element of any type and an error
// NewFunc is a function used to create new elements. Contract: on success (err == nil) it must
// return a non-nil element — the pool treats nil as an illegal value (Put rejects nil), and the
// creation paths equally reject (nil, nil) explicitly with ErrValueIsNil instead of silently
// counting it (a born-nil object would break the "nil ⟹ quota already released" destruction
// exemption premise and leak its in-flight slot permanently)
type NewFunc = func() (element any, err error)

// 定义函数类型 CloseFunc，接收一个任意类型的元素，返回一个错误
// CloseFunc 是用于关闭元素的函数
// Define function type CloseFunc, which takes an element of any type and returns an error
// CloseFunc is a function used to close elements
type CloseFunc = func(element any) error

// 定义函数类型 BeforeGetFunc，在借出前对元素执行校验/激活，返回一个错误
// BeforeGetFunc 是借出路径的决策钩子（区别于纯观察者的 Callback）：元素出队后、返回调用方前，
// 在池外（不持任何池内锁）调用；返回 nil 则正常借出，返回 error（或 panic——由池 recover 并
// 转为携带 panic 值的 error）则该元素被销毁（closeFunc + OnClose + 释放在途配额）并继续尝试
// 队列中的下一个，销毁数达到 PingMaxRetries 上限后返回 ErrPoolEmpty。
// 典型用途：会话级激活、凭据刷新、借出前健康校验（TestOnBorrow 即其 pingFunc 预设）。
// 约束：钩子内禁止重入池的写操作 API（Get/Put/GetWithContext/Stop 等）；应快速返回，
// 钩子耗时直接叠加在借出路径上。nil（默认）表示关闭，借出热路径零开销
// Define function type BeforeGetFunc, which validates/activates an element before lending and returns an error
// BeforeGetFunc is the decision hook of the borrow path (as opposed to the pure-observer Callback):
// it is invoked outside the pool (holding no pool lock) after the element is dequeued and before it
// is returned to the caller; returning nil lends the element normally, while returning an error (or
// panicking — the pool recovers and converts the panic into an error carrying the panic value)
// destroys the element (closeFunc + OnClose + in-flight quota release) and continues with the next
// queued element, returning ErrPoolEmpty once the destruction count reaches the PingMaxRetries cap.
// Typical uses: session activation, credential refresh, borrow-time health checks (TestOnBorrow is
// precisely its pingFunc preset). Constraints: re-entering the pool's mutating APIs
// (Get/Put/GetWithContext/Stop etc.) from the hook is forbidden; it must return quickly since its
// latency lands directly on the borrow path. nil (the default) disables the hook at zero cost on
// the borrow hot path
type BeforeGetFunc = func(element any) error

// 定义函数类型 AfterPutFunc，在归还入队前对元素执行校验/钝化，返回一个错误
// AfterPutFunc 是归还路径的决策钩子：Put 收到元素后、入队之前，在池外（不持任何池内锁、
// 且已通过停止快检——Stop 后不执行）调用；返回 nil 则继续正常入队，返回 error（或 panic——
// 由池 recover 并转为携带 panic 值的 error）则池主动拒绝该归还：元素被销毁（closeFunc +
// OnClose + 释放在途配额）、不入队，且 Put 将该 error 原样透传给调用方——语义为“对象被池
// 主动拒绝并销毁”，区别于 ErrPoolFull（容量拒绝，对象仍归调用方处置）；被拒绝的对象已关闭，
// 调用方不得复用或再次归还。典型用途：会话级钝化（重置连接状态）、归还前校验。
// 约束与 BeforeGetFunc 相同：禁止重入池的写操作 API、应快速返回。nil（默认）表示关闭，
// 归还热路径零开销
// Define function type AfterPutFunc, which validates/passivates an element before it is returned to the queue, and returns an error
// AfterPutFunc is the decision hook of the return path: it is invoked outside the pool (holding no
// pool lock, and only after the lock-free stop fast check — it never runs after Stop) once Put has
// received the element and before it is enqueued; returning nil proceeds with the normal enqueue,
// while returning an error (or panicking — the pool recovers and converts the panic into an error
// carrying the panic value) makes the pool actively reject the return: the element is destroyed
// (closeFunc + OnClose + in-flight quota release), never enqueued, and Put passes that error
// through to the caller as-is — the semantics being "the object was actively rejected and destroyed
// by the pool", distinct from ErrPoolFull (a capacity rejection where the object stays the
// caller's to dispose of); a rejected object has been closed and must not be reused or returned
// again. Typical uses: session passivation (resetting connection state), return-time validation.
// The constraints match BeforeGetFunc: re-entering the pool's mutating APIs is forbidden and it
// must return quickly. nil (the default) disables the hook at zero cost on the return hot path
type AfterPutFunc = func(element any) error

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
	Put(value any) error

	// Get 方法用于从队列中获取元素。
	// The Get method is used to get an element from the queue.
	Get() (value any, err error)

	// Done 方法用于标记元素处理完成。
	// The Done method is used to mark the element as done.
	Done(value any)

	// Len 方法用于获取队列的长度。
	// The Len method is used to get the length of the queue.
	Len() int

	// Values 方法用于获取队列中的所有元素。
	// The Values method is used to get all the elements in the queue.
	Values() []any

	// Range 方法用于遍历队列中的所有元素。
	// The Range method is used to traverse all elements in the queue.
	Range(fn func(value any) bool)

	// Shutdown 方法用于关闭队列。
	// The Shutdown method is used to shut down the queue.
	Shutdown()

	// IsClosed 方法用于检查队列是否已关闭。
	// The IsClosed method is used to check if the queue is closed.
	IsClosed() bool
}
