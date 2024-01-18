<div align="center">
	<h1>Conecta</h1>
	<img src="assets/logo.png" alt="logo" width="300px">
    <h4>A lightweight module connection/session pool</h4>
</div>

# Introduction

**Conecta** is a lightweight module connection/session pool written in Go. It is designed to be simple and easy to use, and has no third-party dependencies.

In `Conecta` world, any object can be a connection/session, such as `net.Conn`, `*sql.DB`, `*redis.Client`, `*grpc.ClientConn`, etc. Such use any wrapper object can be used as a connection/session. So far as to say, `Conecta` is a generic connection/session pool.

You can also customize the creation, destruction and validation functions of these objects. Then use `Conecta` to manage these objects. `Conecta` will create, destroy and validate these objects for you.

# Advantage

-   Simple and easy to use
-   No third-party dependencies
-   Low memory usage

# Features

-   [x] Custom object creation function
-   [x] Custom object destruction function
-   [x] Custom object validation function
-   [x] Custom minimum number of objects when initializing the pool
-   [x] Custom maximum number of object validation failures before destroying the object

# Installation

```bash
$ go get github.com/shengyanli1982/conecta
```

# Quick Start

`Conecta` is very simple to use. You only need to implement the `NewFunc`, `CloseFunc` and `PingFunc` functions, and then call the `New` function to create a `Conecta` object.

## Config

`Conecta` has a config object, which can be used to configure the batch process behavior. The config object can be used following methods to set.

-   `WithCallback` set the callback functions. The default is `&emptyCallback{}`.
-   `WithInitialize` set the minimum number of objects when initializing the pool. The default is `1`.
-   `WithPingMaxRetries` set the maximum number of object validation failures before destroying the object. The default is `3`.
-   `WithNewFunc` set the object creation function. The default is `DefaultNewFunc`.
-   `WithPingFunc` set the object validation function. The default is `DefaultPingFunc`
-   `WithCloseFunc` set the object destruction function. The default is `DefaultCloseFunc`.
-   `WithScanInterval` set the interval between two scans. The default is `10000ms`.

> [!NOTE]
> The **Conecta** start a goroutine to scan the elements. The scan interval is set by `WithScanInterval`. The scan process will block the objects that have not been used for a long time, if so many objects in the pool.
>
> So if you want to use **Conecta** in a long-running program, you need to set the scan interval to a reasonable value. May be more than **10 seconds** is a good choice.

## Methods

-   `New` create a `Conecta` object.
-   `Get` get a object from the pool.
-   `GetOrCreate` get a object from the pool, if the pool is empty, create a new object.
-   `Put` put a object back to the pool.
-   `Stop` close the pool.

## Create

`Conecta`'s `New` function is used to create a `Conecta` object. The `New` function receives a `Config` object and a `QInterface` interface as parameters.

The `QInterface` interface is used to define the queue which is used to store the objects.

Following is the `QInterface` interface.

```go
// 队列方法接口
// Queue interface
type QInterface interface {
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
```

## Callback

`Callback` interface is used to define the callback functions. The `Callback` interface has three methods, `OnPingSuccess`, `OnPingFailure` and `OnClose`.

-   `OnPingSuccess` is called when the object validation is successful.
-   `OnPingFailure` is called when the object validation fails.
-   `OnClose` is called when the object is destroyed.

## Example

```go

```

**Result**

```bash
$ go run demo.go
*** [TCPClient] write: [109 115 103 49]
>>> [TCPServer] new client: 127.0.0.1:53511
>>> [TCPServer] read: msg1
>>> [TCPServer] write: msg1
*** [TCPClient] read: msg1
!! [main] please wait for the client msg processed and once scan to be executed... (6 seconds)
*** [TCPClient] write: [112 105 110 103]
>>> [TCPServer] read: ping
>>> [TCPServer] write: ping
*** [TCPClient] read: ping
$$ OnPingSuccess &{0xc000186038 {0 {0 0}}}
>>> [TCPServer] read error: read tcp 127.0.0.1:13134->127.0.0.1:53511: use of closed network connection
*** [TCPClient] write: [109 115 103 50]
*** [TCPClient] read error: read tcp 127.0.0.1:53511->127.0.0.1:13134: read: connection reset by peer
!! [main] please wait for the scan to be executed... (11 seconds)
*** [TCPClient] write error: write tcp 127.0.0.1:53511->127.0.0.1:13134: write: broken pipe
$$ OnPingFailure &{0xc000186038 {0 {0 0}}}
*** [TCPClient] close
$$ OnClose &{0xc000186038 {1 {0 0}}} <nil>
```
