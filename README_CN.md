[English](./README.md) | 中文

<div align="center">
	<img src="assets/logo.png" alt="logo" width="500px">
</div>

# 简介

**Conecta** 是一个轻量级的用于管理连接/会话池的 Go 模块。

在 `Conecta` 的世界中，任何对象都可以作为连接/会话，例如 `net.Conn`、`*sql.DB`、`*redis.Client`、`*grpc.ClientConn` 等。您可以使用任何包装对象作为连接/会话。换句话说，`Conecta` 是一个多功能且通用的连接/会话池。

通过 `Conecta`，您可以轻松处理这些对象的创建、销毁和验证。它提供了可自定义的函数用于创建、销毁和验证对象，使您能够根据特定需求定制池的行为。

# 优势

-   简单且易于使用
-   无需外部依赖
-   内存使用效率高

# 特性

-   [x] 自定义对象创建函数
-   [x] 自定义对象销毁函数
-   [x] 自定义对象验证函数
-   [x] 自定义池初始化期间的最小对象数量
-   [x] 自定义对象验证失败的最大次数

# 安装

```bash
go get github.com/shengyanli1982/conecta
```

# 快速入门

要快速开始使用 `Conecta`，请按照以下步骤进行操作：

1. 实现 `NewFunc`、`CloseFunc` 和 `PingFunc` 函数。
2. 调用 `New` 函数创建一个 `Conecta` 对象。

## 配置

`Conecta` 提供了一个配置对象，允许您自定义其行为。您可以使用以下方法来配置配置对象：

-   `WithCallback`：设置回调函数。默认值为 `&emptyCallback{}`。
-   `WithInitialize`：设置初始化池时的最小对象数。默认值为 `0`。
-   `WithPingMaxRetries`：设置对象验证失败的最大次数，超过该次数将销毁对象。默认值为 `3`。
-   `WithNewFunc`：设置对象创建函数。默认值为 `DefaultNewFunc`。
-   `WithPingFunc`：设置对象验证函数。默认值为 `DefaultPingFunc`。
-   `WithCloseFunc`：设置对象销毁函数。默认值为 `DefaultCloseFunc`。
-   `WithScanInterval`：设置两次扫描之间的间隔时间。默认值为 `10000ms`。

> [!NOTE] > **Conecta** 使用一个 goroutine 定期扫描池中的元素。可以使用 `WithScanInterval` 方法自定义扫描间隔时间。此扫描过程有助于识别和阻塞长时间处于空闲状态的对象，特别是当池中包含大量对象时。
>
> 对于长时间运行的程序，为了获得最佳性能，建议将扫描间隔设置为合理的值，例如超过 **10 秒**。

## 方法

-   `New`：创建一个 `Conecta` 对象。
-   `Get`：从池中获取一个对象。
-   `GetOrCreate`：从池中获取一个对象，如果池为空，则创建一个新对象。
-   `Put`：将一个对象放回池中。
-   `Stop`：关闭池。

## 创建

`Conecta` 的 `New` 函数用于创建一个 `Conecta` 对象。它接受一个 `Config` 对象和一个 `QueueInterface` 接口作为参数。

`QueueInterface` 接口定义了用于存储对象的队列。

以下是 `QueueInterface` 接口的定义：

```go
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
```

## 回调函数

`Callback` 接口用于定义 `Conecta` 的回调函数。它包括以下方法：

-   `OnPingSuccess`：当对象验证成功时调用。
-   `OnPingFailure`：当对象验证失败时调用。
-   `OnClose`：当对象销毁时调用。

## 示例

以下是一个简单的示例。

```go
package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/shengyanli1982/conecta"
	"github.com/shengyanli1982/workqueue"
)

var (
	// 监听地址
	// Listen address
	listenAddr = "0.0.0.0"

	// 服务器地址
	// Server address
	serverAddr = "127.0.0.1"

	// 服务器端口
	// Server port
	serverPort = 13134
)

// =================== TCP Server ===================

// TCPServer 表示一个 TCP 服务器
// TCPServer represents a TCP server
type TCPServer struct {
	// 监听器
	// Listener
	listener *net.TCPListener

	// 客户端连接列表
	// Client connection list
	clients []*net.TCPConn

	// 同步等待组，用于等待所有 goroutine 结束
	// Synchronization wait group, used to wait for all goroutines to end
	wg sync.WaitGroup

	// 用于确保某个操作只执行一次
	// Used to ensure that an operation is only performed once
	once sync.Once
}

// NewTCPServer 创建一个新的 TCP 服务器
// NewTCPServer creates a new TCP server
func NewTCPServer(port int) (*TCPServer, error) {
	// 创建一个 TCP 监听器
	// Create a TCP listener
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{
		IP:   net.ParseIP(listenAddr),
		Port: serverPort,
	})
	if err != nil {
		return nil, err
	}

	// 创建一个 TCP 服务器
	// Create a TCP server
	server := TCPServer{
		listener: listener,
		clients:  make([]*net.TCPConn, 0),
		wg:       sync.WaitGroup{},
		once:     sync.Once{},
	}

	// 返回创建的 TCP 服务器
	// Return the created TCP server
	return &server, nil
}

// Start 启动 TCP 服务器
// Start starts the TCP server
func (s *TCPServer) Start() {
	// 增加等待组的计数
	// Increase the count of the wait group
	s.wg.Add(1)

	// 启动一个 goroutine 来监听连接
	// Start a goroutine to listen for connections
	go s.listen()
}

// Stop 停止 TCP 服务器
// Stop stops the TCP server
func (s *TCPServer) Stop() {
	// 确保停止操作只执行一次
	// Ensure that the stop operation is only performed once
	s.once.Do(func() {
		fmt.Println(">>> [TCPServer] stop")

		// 关闭所有客户端连接
		// Close all client connections
		for _, c := range s.clients {
			_ = c.Close()
		}

		fmt.Println(">>> [TCPServer] close listener")

		// 关闭监听器
		// Close the listener
		_ = s.listener.Close()

		fmt.Println(">>> [TCPServer] close all clients")

		// 等待所有 goroutine 结束
		// Wait for all goroutines to end
		s.wg.Wait()
	})
}

// listen 方法用于监听传入的连接
// The listen method is used to listen for incoming connections
func (s *TCPServer) listen() {
	// 使用 defer 语句来确保在函数结束时调用 Done 方法
	// Use the defer statement to ensure that the Done method is called when the function ends
	defer s.wg.Done()

	// 使用 for 循环来不断接受新的连接
	// Use a for loop to continuously accept new connections
	for {
		// 使用 AcceptTCP 方法来接受新的 TCP 连接
		// Use the AcceptTCP method to accept a new TCP connection
		conn, err := s.listener.AcceptTCP()
		if err != nil {
			// 如果出现错误，检查是否是因为监听器已经关闭
			// If an error occurs, check if it is because the listener has been closed
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				fmt.Println(">>> [TCPServer] EOF")
				break
			} else {
				// 如果不是因为监听器已经关闭，打印错误信息并继续监听
				// If it is not because the listener has been closed, print the error message and continue listening
				fmt.Println(">>> [TCPServer] accept error:", err)
				continue
			}
		}
		// 如果成功接受了一个新的连接，增加等待组的计数
		// If a new connection is successfully accepted, increase the count of the wait group
		s.wg.Add(1)

		// 启动一个新的 goroutine 来处理这个连接
		// Start a new goroutine to handle this connection
		go s.handle(conn)

		// 将这个新的连接添加到客户端连接列表中
		// Add this new connection to the client connection list
		s.clients = append(s.clients, conn)

		// 打印新客户端的地址
		// Print the address of the new client
		fmt.Println(">>> [TCPServer] new client:", conn.RemoteAddr().String())
	}
}

// handle 方法用于处理客户端连接
// The handle method is used to handle client connections
func (s *TCPServer) handle(conn *net.TCPConn) {
	// 使用 defer 语句来确保在函数结束时调用 Done 方法
	// Use the defer statement to ensure that the Done method is called when the function ends
	defer s.wg.Done()

	// 使用 for 循环来不断读取客户端发送的数据
	// Use a for loop to continuously read data sent by the client
	for {
		// 创建一个缓冲区来存储读取的数据
		// Create a buffer to store the read data
		buf := make([]byte, 1024)

		// 使用 Read 方法来读取数据
		// Use the Read method to read data
		_, err := conn.Read(buf)
		if err != nil {
			// 如果出现错误，检查是否是因为连接已经关闭
			// If an error occurs, check if it is because the connection has been closed
			if !errors.Is(err, io.EOF) {
				fmt.Println(">>> [TCPServer] read error:", err)
			} else {
				fmt.Println(">>> [TCPServer] EOF")
			}
			break
		}

		// 打印读取到的数据
		// Print the read data
		fmt.Println(">>> [TCPServer] <- read:", string(buf))

		// 使用 Write 方法来发送数据
		// Use the Write method to send data
		_, err = conn.Write(buf)
		if err != nil {
			// 如果出现错误，打印错误信息并结束处理
			// If an error occurs, print the error message and end the processing
			fmt.Println(">>> [TCPServer] write error:", err)
			break
		}

		// 打印发送的数据
		// Print the sent data
		fmt.Println(">>> [TCPServer] -> write:", string(buf))
	}
}

// =================== TCP Client ===================

// TCPClient 表示一个 TCP 客户端
// TCPClient represents a TCP client.
type TCPClient struct {
	// TCP 连接
	// TCP connection
	conn *net.TCPConn

	// 用于确保某个操作只执行一次
	// Used to ensure that an operation is only performed once
	once sync.Once
}

// NewTCPClient 创建一个新的 TCP 客户端
// NewTCPClient creates a new TCP client.
func NewTCPClient(addr string, port int) (*TCPClient, error) {
	// 创建一个 TCP 连接
	// Create a TCP connection
	conn, err := net.DialTCP("tcp", nil, &net.TCPAddr{
		IP:   net.ParseIP(addr),
		Port: port,
	})
	if err != nil {
		// 如果出现错误，打印错误信息并返回
		// If an error occurs, print the error message and return
		fmt.Println("*** [TCPClient] create client error:", err)
		return nil, err
	}

	// 返回创建的 TCP 客户端
	// Return the created TCP client
	return &TCPClient{
		conn: conn,
		once: sync.Once{},
	}, nil
}

// Send 方法向服务器发送一条消息
// The Send method sends a message to the server.
func (c *TCPClient) Send(msg []byte) bool {
	// 使用 Write 方法发送消息
	// Use the Write method to send the message
	_, err := c.conn.Write(msg)
	if err != nil {
		// 如果出现错误，打印错误信息并返回 false
		// If an error occurs, print the error message and return false
		fmt.Println("*** [TCPClient] write error:", err)
		return false
	}
	// 打印发送的消息
	// Print the sent message
	fmt.Println("*** [TCPClient] -> write:", string(msg))

	// 创建一个缓冲区来存储读取的数据
	// Create a buffer to store the read data
	buf := make([]byte, 1024)

	// 使用 Read 方法读取服务器的响应
	// Use the Read method to read the server's response
	_, err = c.conn.Read(buf)
	if err != nil {
		// 如果出现错误，打印错误信息并返回 false
		// If an error occurs, print the error message and return false
		if err != io.EOF {
			fmt.Println("*** [TCPClient] read error:", err)
		} else {
			// 如果是 EOF 错误，打印 EOF 信息
			// If it is an EOF error, print EOF information
			fmt.Println("*** [TCPClient] EOF")
		}
		return false
	}
	// 打印读取到的响应
	// Print the read response
	fmt.Println("*** [TCPClient] <- read:", string(buf))

	// 如果没有错误，返回 true
	// If there is no error, return true
	return true
}

// Close 方法关闭 TCP 客户端连接
// The Close method closes the TCP client connection.
func (c *TCPClient) Close() {
	// 使用 once.Do 确保关闭操作只执行一次
	// Use once.Do to ensure that the close operation is only performed once
	c.once.Do(func() {
		// 打印关闭信息
		// Print close information
		fmt.Println("*** [TCPClient] close")

		// 关闭连接
		// Close the connection
		_ = c.conn.Close()
	})
}

// Ping 方法向服务器发送 ping 消息
// The Ping method sends a ping message to the server.
func (c *TCPClient) Ping() bool {
	// 使用 Send 方法发送 ping 消息，并返回结果
	// Use the Send method to send a ping message and return the result
	return c.Send([]byte("ping"))
}

// =================== Conecta ===================

// TestCallback 是 Conecta 的回调实现
// TestCallback is a callback implementation for Conecta.
type TestCallback struct{}

// OnPingSuccess 在 ping 成功时被调用
// OnPingSuccess is called when the ping is successful.
func (c *TestCallback) OnPingSuccess(data any) {
	// 打印 ping 成功的数据
	// Print the data when ping is successful
	fmt.Println("$$ OnPingSuccess", data)
}

// OnPingFailure 在 ping 失败时被调用
// OnPingFailure is called when the ping fails.
func (c *TestCallback) OnPingFailure(data any) {
	// 打印 ping 失败的数据
	// Print the data when ping fails
	fmt.Println("$$ OnPingFailure", data)
}

// OnClose 在连接关闭时被调用
// OnClose is called when the connection is closed.
func (c *TestCallback) OnClose(data any, err error) {
	// 打印连接关闭时的数据和错误
	// Print the data and error when the connection is closed
	fmt.Println("$$ OnClose", data, err)
}

// NewFunc 是一个用于创建 TCP 客户端的工厂函数
// NewFunc is a factory function for creating TCP clients.
func NewFunc() (any, error) {
	// 返回一个新的 TCP 客户端
	// Return a new TCP client
	return NewTCPClient(serverAddr, serverPort)
}

// PingFunc 是一个用于 ping 服务器的函数
// PingFunc is a function for pinging the server.
func PingFunc(any any, c int) bool {
	// 获取 TCP 客户端并执行 ping 操作
	// Get the TCP client and perform the ping operation
	client := any.(*TCPClient)
	return client.Ping()
}

// CloseFunc 是一个用于关闭 TCP 客户端连接的函数
// CloseFunc is a function for closing the TCP client connection.
func CloseFunc(any any) error {
	// 获取 TCP 客户端并关闭连接
	// Get the TCP client and close the connection
	client := any.(*TCPClient)
	client.Close()
	// 返回 nil 表示没有错误
	// Return nil to indicate no error
	return nil
}

// =================== Application ===================

func main() {
	// 设置扫描间隔为5秒
	// Set the scan interval to 5 seconds.
	scanInterval := 5000

	// 创建一个工作队列
	// Create a work queue.
	baseQ := workqueue.NewSimpleQueue(nil)

	// 创建一个 Conecta 池
	// Create a Conecta pool.
	conf := conecta.NewConfig().WithNewFunc(NewFunc).WithPingFunc(PingFunc).WithCloseFunc(CloseFunc).WithPingMaxRetries(1).WithScanInterval(scanInterval).WithCallback(&TestCallback{})
	pool, err := conecta.New(baseQ, conf)
	if err != nil {
		// 如果创建池时出错，打印错误并返回
		// If an error occurs while creating the pool, print the error and return
		fmt.Println("!! [main] create pool error:", err)
		return
	}
	// 使用 defer 关键字确保在函数结束时停止池
	// Use the defer keyword to ensure that the pool is stopped when the function ends
	defer pool.Stop()

	// 创建一个 TCP 服务器
	// Create a TCP server.
	server, err := NewTCPServer(serverPort)
	if err != nil {
		// 如果创建服务器时出错，打印错误并返回
		// If an error occurs while creating the server, print the error and return
		fmt.Println("!! [main] create server error:", err)
		return
	}
	// 使用 defer 关键字确保在函数结束时停止服务器
	// Use the defer keyword to ensure that the server is stopped when the function ends
	defer server.Stop()
	server.Start()

	// 从池中获取一个 TCP 客户端
	// Get a TCP client from the pool.
	client, err := pool.GetOrCreate()
	if err != nil {
		// 如果获取客户端时出错，打印错误并返回
		// If an error occurs while getting the client, print the error and return
		fmt.Println("!! [main] get client error:", err)
		return
	}

	// 向服务器发送一条消息
	// Send a message to the server.
	_ = client.(*TCPClient).Send([]byte("msg1"))

	// 将 TCP 客户端放回池中
	// Put the TCP client back to the pool.
	_ = pool.Put(client)

	fmt.Println("!! [main] please wait for the client msg processed and once scan to be executed... (6 seconds)")
	time.Sleep(6 * time.Second)

	// 停止 TCP 服务器。当 TCP 客户端将被关闭时，TCP 客户端将从池中移除
	// Stop the TCP server. When TCP client will be closed, the TCP client will be removed from the pool.
	server.Stop()

	// 从池中获取一个 TCP 客户端
	// Get a TCP client from the pool.
	client, err = pool.GetOrCreate()
	if err != nil {
		// 如果获取客户端时出错，打印错误并返回
		// If an error occurs while getting the client, print the error and return
		fmt.Println("!! [main] get client error:", err)
		return
	}

	// 向服务器发送一条消息。消息将无法发送
	// Send a message to the server. The message will fail to send.
	_ = client.(*TCPClient).Send([]byte("msg2"))

	// 将 TCP 客户端放回池中
	// Put the TCP client back to the pool.
	_ = pool.Put(client)

	fmt.Println("!! [main] please wait for the scan to be executed... (11 seconds)")
	time.Sleep(11 * time.Second)
}
```

**执行结果**

```bash
$ go run demo.go
*** [TCPClient] -> write: msg1
>>> [TCPServer] new client: 127.0.0.1:62009
>>> [TCPServer] <- read: msg1
>>> [TCPServer] -> write: msg1
*** [TCPClient] <- read: msg1
!! [main] please wait for the client msg processed and once scan to be executed... (6 seconds)
*** [TCPClient] -> write: ping
>>> [TCPServer] <- read: ping
>>> [TCPServer] -> write: ping
*** [TCPClient] <- read: ping
$$ OnPingSuccess &{0xc0000b6038 {0 {0 0}}}
>>> [TCPServer] stop
>>> [TCPServer] close listener
>>> [TCPServer] read error: read tcp 127.0.0.1:13134->127.0.0.1:62009: use of closed network connection
>>> [TCPServer] EOF
>>> [TCPServer] close all clients
*** [TCPClient] -> write: msg2
*** [TCPClient] EOF
!! [main] please wait for the scan to be executed... (11 seconds)
*** [TCPClient] write error: write tcp 127.0.0.1:62009->127.0.0.1:13134: write: broken pipe
$$ OnPingFailure &{0xc0000b6038 {0 {0 0}}}
*** [TCPClient] close
$$ OnClose &{0xc0000b6038 {1 {0 0}}} <nil>
```
