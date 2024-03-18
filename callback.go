package conecta

// emptyCallback 是 Callback 的空实现，不执行任何操作
// emptyCallback is a no-op implementation of Callback.
type emptyCallback struct{}

// OnPingSuccess 是 Ping 成功时的空回调，不执行任何操作
// OnPingSuccess is a no-op callback when Ping is successful.
func (c *emptyCallback) OnPingSuccess(any) {}

// OnPingFailure 是 Ping 失败时的空回调，不执行任何操作
// OnPingFailure is a no-op callback when Ping fails.
func (c *emptyCallback) OnPingFailure(any) {}

// OnClose 是关闭时的空回调，不执行任何操作
// OnClose is a no-op callback when closing.
func (c *emptyCallback) OnClose(any, error) {}

// newEmptyCallback 返回一个新的 emptyCallback 实例
// newEmptyCallback returns a new emptyCallback instance.
func newEmptyCallback() *emptyCallback { return &emptyCallback{} }
