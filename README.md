English | [中文](./README_CN.md)

<div align="center">
	<img src="assets/logo.png" alt="logo" width="500px">
</div>

[![Go Report Card](https://goreportcard.com/badge/github.com/shengyanli1982/conecta)](https://goreportcard.com/report/github.com/shengyanli1982/conecta)
[![Build Status](https://github.com/shengyanli1982/conecta/actions/workflows/test.yaml/badge.svg)](https://github.com/shengyanli1982/conecta/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/shengyanli1982/conecta.svg)](https://pkg.go.dev/github.com/shengyanli1982/conecta)

# Introduction

**Conecta** is a lightweight module for managing connection/session pools in Go.

In the world of `Conecta`, any object can serve as a connection/session, such as `net.Conn`, `*sql.DB`, `*redis.Client`, `*grpc.ClientConn`, and more. You can use any wrapper object as a connection/session. In other words, `Conecta` is a versatile and generic connection/session pool.

With `Conecta`, you can easily handle the creation, destruction, and validation of these objects. It provides customizable functions for creating, destroying, and validating objects, allowing you to tailor the behavior of the pool to your specific requirements.

# Advantages

-   Simple and user-friendly
-   No external dependencies required
-   Efficient memory usage

# Features

-   [x] Custom object creation function
-   [x] Custom object destruction function
-   [x] Custom object validation function
-   [x] Custom minimum number of objects during pool initialization
-   [x] Custom maximum number of object validation failures before object destruction

# Installation

```bash
go get github.com/shengyanli1982/conecta
```

# Quick Start

To quickly get started with `Conecta`, follow these steps:

1. Implement the `NewFunc`, `CloseFunc`, and `PingFunc` functions.
2. Call the `New` function to create a `Conecta` object.

## 1. Config

`Conecta` provides a config object that allows you to customize its behavior. You can use the following methods to configure the config object:

-   `WithCallback`: Set the callback functions. The default is `&emptyCallback{}`.
-   `WithInitialize`: Set the minimum number of objects when initializing the pool. The default is `0`.
-   `WithPingMaxRetries`: Set the maximum number of object validation failures before destroying the object. The default is `3`.
-   `WithNewFunc`: Set the object creation function. The default is `DefaultNewFunc`.
-   `WithPingFunc`: Set the object validation function. The default is `DefaultPingFunc`.
-   `WithCloseFunc`: Set the object destruction function. The default is `DefaultCloseFunc`.
-   `WithScanInterval`: Set the interval between two scans. The default is `10000ms`.

> [!NOTE]
> The **Conecta** utilizes a goroutine to periodically scan the elements in the pool. The scan interval can be customized using the `WithScanInterval` method. This scan process helps identify and block objects that have been idle for a long time, especially when the pool contains a large number of objects.
>
> For optimal performance in long-running programs, it is recommended to set the scan interval to a reasonable value, such as more than **10 seconds**.

## 2. Methods

-   `New`: Creates a `Conecta` object.
-   `Get`: Retrieves an object from the pool.
-   `GetOrCreate`: Retrieves an object from the pool, and if the pool is empty, creates a new object.
-   `Put`: Puts an object back into the pool.
-   `Stop`: Closes the pool.

## 3. Create

The `New` function of `Conecta` is used to create a `Conecta` object. It takes a `Config` object and a `QueueInterface` interface as parameters.

The `QueueInterface` interface defines the queue used to store the objects.

Here is the `QueueInterface` interface:

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

## 4. Callback

The `Callback` interface is used to define the callback functions for `Conecta`. It includes the following methods:

-   `OnPingSuccess`: Called when the object validation is successful.
-   `OnPingFailure`: Called when the object validation fails.
-   `OnClose`: Called when the object is destroyed.

## 5. Example

You can find the code for each case in the `examples` directory.

### 5.1. Simple Example

Here is a simple example that demonstrates how to use `Conecta`:

```go
package main

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/shengyanli1982/conecta"
	"github.com/shengyanli1982/workqueue"
)

// Demo 是一个包含 value 字段的结构体
// Demo is a struct that contains a value field
type Demo struct {
	// value 是一个字符串
	// value is a string
	value string

	// id 是一个字符串
	// id is a string
	id string
}

// GetValue 是一个方法，它返回 Demo 结构体中的 value 字段
// GetValue is a method that returns the value field in the Demo struct
func (d *Demo) GetValue() string {
	// 返回 value 字段
	// Return the value field
	return d.value
}

// GetID 是一个方法，它返回 Demo 结构体中的 id 字段
// GetID is a method that returns the id field in the Demo struct
func (d *Demo) GetID() string {
	// 返回 id 字段
	// Return the id field
	return d.id
}

// NewFunc 是一个函数，它创建并返回一个新的 Demo 结构体
// NewFunc is a function that creates and returns a new Demo struct
func NewFunc() (any, error) {
	// 创建一个新的 Demo 结构体，其 value 字段被设置为 "test"
	// Create a new Demo struct with its value field set to "test"
	return &Demo{value: "test", id: uuid.NewString()}, nil
}

func main() {
	// 创建一个工作队列
	// Create a work queue.
	baseQ := workqueue.NewSimpleQueue(nil)

	// 创建一个 Conecta 池
	// Create a Conecta pool.
	conf := conecta.NewConfig().
		// 使用 NewFunc 函数作为新建连接的函数
		// Use the NewFunc function as the function to create a new connection.
		WithNewFunc(NewFunc)

	// 使用 conecta.New 函数创建一个新的连接池
	// Use the conecta.New function to create a new connection pool.
	pool, err := conecta.New(baseQ, conf)

	// 检查是否在创建连接池时出现错误
	// Check if there was an error while creating the connection pool.
	if err != nil {
		// 如果创建连接池时出错，打印错误并返回
		// If an error occurs while creating the connection pool, print the error and return.
		fmt.Println("!! [main] create pool error:", err)
		return
	}
	// 使用 defer 关键字确保在函数结束时停止池
	// Use the defer keyword to ensure that the pool is stopped when the function ends
	defer pool.Stop()

	// 使用 for 循环从池中获取数据
	// Use a for loop to get data from the pool
	for i := 0; i < 10; i++ {
		// 使用 GetOrCreate 方法从池中获取数据
		// Use the GetOrCreate method to get data from the pool
		if data, err := pool.GetOrCreate(); err != nil {
			// 如果从池中获取数据时出错，打印错误并返回
			// If an error occurs while getting data from the pool, print the error and return
			fmt.Println("!! [main] get data error:", err)
			return

		} else {
			// 打印从池中获取的数据
			// Print the data obtained from the pool
			fmt.Printf(">> [main] get data: %s, id: %s\n", fmt.Sprintf("%s_%v", data.(*Demo).GetValue(), i), data.(*Demo).GetID())

			// 使用 Put 方法将数据放回池中
			// Use the Put method to put the data back into the pool
			if err := pool.Put(data); err != nil {
				// 如果将数据放回池中时出错，打印错误并返回
				// If an error occurs while putting the data back into the pool, print the error and return
				fmt.Println("!! [main] put data error:", err)
				return
			}
		}
	}
}
```

**Result**

```bash
$ go run demo.go
>> [main] get data: test_0, id: 7b781fde-b392-470a-9c12-2e495429c1a0
>> [main] get data: test_1, id: 7b781fde-b392-470a-9c12-2e495429c1a0
>> [main] get data: test_2, id: 7b781fde-b392-470a-9c12-2e495429c1a0
>> [main] get data: test_3, id: 7b781fde-b392-470a-9c12-2e495429c1a0
>> [main] get data: test_4, id: 7b781fde-b392-470a-9c12-2e495429c1a0
>> [main] get data: test_5, id: 7b781fde-b392-470a-9c12-2e495429c1a0
>> [main] get data: test_6, id: 7b781fde-b392-470a-9c12-2e495429c1a0
>> [main] get data: test_7, id: 7b781fde-b392-470a-9c12-2e495429c1a0
>> [main] get data: test_8, id: 7b781fde-b392-470a-9c12-2e495429c1a0
>> [main] get data: test_9, id: 7b781fde-b392-470a-9c12-2e495429c1a0
```

### 5.2. Full Example

Here is a complete example that demonstrates how to use `Conecta`:

1. Create a TCP server and client.
2. Use `Conecta` to manage the TCP sessions between the server and client.
3. Implement the `NewFunc`, `CloseFunc`, and `PingFunc` functions.
4. Set `PingMaxRetries` to `1` to simulate a failed ping operation.
5. Utilize the `Callback` interface to handle the callback functions.

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
	// 使用 net.ListenTCP 创建一个新的 TCP 监听器
	// Use net.ListenTCP to create a new TCP listener
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{
		// 解析监听地址
		// Parse the listen address
		IP: net.ParseIP(listenAddr),

		// 设置服务器端口
		// Set the server port
		Port: serverPort,
	})

	// 检查是否在创建监听器时出现错误
	// Check if there was an error while creating the listener
	if err != nil {
		// 如果创建监听器时出错，返回错误
		// If an error occurs while creating the listener, return the error
		return nil, err
	}

	// 创建一个 TCP 服务器
	// Create a TCP server
	server := TCPServer{
		// 设置监听器
		// Set the listener
		listener: listener,

		// 初始化客户端连接列表
		// Initialize the client connection list
		clients: make([]*net.TCPConn, 0),

		// 初始化等待组
		// Initialize the wait group
		wg: sync.WaitGroup{},

		// 初始化 once
		// Initialize once
		once: sync.Once{},
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

// Stop 方法用于停止 TCP 服务器
// The Stop method is used to stop the TCP server.
func (s *TCPServer) Stop() {
	// 使用 sync.Once 来确保停止操作只执行一次
	// Use sync.Once to ensure that the stop operation is only performed once.
	s.once.Do(func() {
		// 打印停止服务器的消息
		// Print the message of stopping the server.
		fmt.Println(">>> [TCPServer] stop")

		// 使用 for 循环来关闭所有客户端连接
		// Use a for loop to close all client connections.
		for _, c := range s.clients {
			// 使用 Close 方法来关闭连接
			// Use the Close method to close the connection.
			_ = c.Close()
		}

		// 打印关闭监听器的消息
		// Print the message of closing the listener.
		fmt.Println(">>> [TCPServer] close listener")

		// 使用 Close 方法来关闭监听器
		// Use the Close method to close the listener.
		_ = s.listener.Close()

		// 打印关闭所有客户端的消息
		// Print the message of closing all clients.
		fmt.Println(">>> [TCPServer] close all clients")

		// 使用 Wait 方法来等待所有 goroutine 结束
		// Use the Wait method to wait for all goroutines to end.
		s.wg.Wait()
	})
}

// listen 方法用于监听传入的连接
// The listen method is used to listen for incoming connections.
func (s *TCPServer) listen() {
	// 使用 defer 语句来确保在函数结束时调用 Done 方法
	// Use the defer statement to ensure that the Done method is called when the function ends.
	defer s.wg.Done()

	// 使用 for 循环来不断接受新的连接
	// Use a for loop to continuously accept new connections.
	for {
		// 使用 AcceptTCP 方法来接受新的 TCP 连接
		// Use the AcceptTCP method to accept a new TCP connection.
		conn, err := s.listener.AcceptTCP()

		// 检查是否在接受新连接时出现错误
		// Check if there was an error while accepting the new connection.
		if err != nil {
			// 如果接受新连接时出错，检查是否是 EOF 错误或网络关闭错误
			// If an error occurs while accepting the new connection, check if it is an EOF error or a network closed error.
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				// 如果是 EOF 错误或网络关闭错误，打印 EOF 信息并跳出循环
				// If it is an EOF error or a network closed error, print EOF information and break the loop.
				fmt.Println(">>> [TCPServer] EOF")
				break
			} else {
				// 如果是其他错误，打印错误并继续循环
				// If it is another error, print the error and continue the loop.
				fmt.Println(">>> [TCPServer] accept error:", err)
				continue
			}
		}

		// 如果成功接受了一个新的连接，增加等待组的计数
		// If a new connection is successfully accepted, increase the count of the wait group.
		s.wg.Add(1)

		// 启动一个新的 goroutine 来处理这个连接
		// Start a new goroutine to handle this connection.
		go s.handle(conn)

		// 将这个新的连接添加到客户端连接列表中
		// Add this new connection to the client connection list.
		s.clients = append(s.clients, conn)

		// 打印新客户端的地址
		// Print the address of the new client.
		fmt.Println(">>> [TCPServer] new client:", conn.RemoteAddr().String())
	}
}

// handle 方法用于处理客户端连接
// The handle method is used to handle client connections.
func (s *TCPServer) handle(conn *net.TCPConn) {
	// 使用 defer 语句来确保在函数结束时调用 Done 方法
	// Use the defer statement to ensure that the Done method is called when the function ends.
	defer s.wg.Done()

	// 使用 for 循环来不断读取客户端发送的数据
	// Use a for loop to continuously read data sent by the client.
	for {
		// 创建一个缓冲区来存储读取的数据
		// Create a buffer to store the read data.
		buf := make([]byte, 1024)

		// 使用 Read 方法来读取数据
		// Use the Read method to read data.
		_, err := conn.Read(buf)

		// 检查是否在读取数据时出现错误
		// Check if there was an error while reading the data.
		if err != nil {
			// 如果读取数据时出错，检查是否是 EOF 错误
			// If an error occurs while reading the data, check if it is an EOF error.
			if !errors.Is(err, io.EOF) {
				// 如果不是 EOF 错误，打印错误并跳出循环
				// If it is not an EOF error, print the error and break the loop.
				fmt.Println(">>> [TCPServer] read error:", err)
			} else {
				// 如果是 EOF 错误，打印 EOF 信息并跳出循环
				// If it is an EOF error, print EOF information and break the loop.
				fmt.Println(">>> [TCPServer] EOF")
			}
			break
		}

		// 打印读取到的数据
		// Print the read data.
		fmt.Println(">>> [TCPServer] <- read:", string(buf))

		// 使用 Write 方法来发送数据
		// Use the Write method to send data.
		_, err = conn.Write(buf)

		// 检查是否在发送数据时出现错误
		// Check if there was an error while sending the data.
		if err != nil {
			// 如果发送数据时出错，打印错误并跳出循环
			// If an error occurs while sending the data, print the error and break the loop.
			fmt.Println(">>> [TCPServer] write error:", err)
			break
		}

		// 打印发送的数据
		// Print the sent data.
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
	// 使用 net.DialTCP 方法创建一个 TCP 连接
	// Use the net.DialTCP method to create a TCP connection
	conn, err := net.DialTCP("tcp", nil, &net.TCPAddr{
		// 解析 IP 地址
		// Parse the IP address
		IP: net.ParseIP(addr),

		// 设置端口号
		// Set the port number
		Port: port,
	})

	// 检查是否在创建 TCP 连接时出现错误
	// Check if there was an error while creating the TCP connection
	if err != nil {
		// 如果创建 TCP 连接时出错，打印错误并返回 nil 和错误
		// If an error occurs while creating the TCP connection, print the error and return nil and the error
		fmt.Println("*** [TCPClient] create client error:", err)
		return nil, err
	}

	// 创建一个新的 TCP 客户端并返回
	// Create a new TCP client and return
	return &TCPClient{
		// 设置 TCP 连接
		// Set the TCP connection
		conn: conn,

		// 初始化 sync.Once 结构体
		// Initialize the sync.Once struct
		once: sync.Once{},
	}, nil
}

// Send 方法向服务器发送一条消息
// The Send method sends a message to the server.
func (c *TCPClient) Send(msg []byte) bool {
	// 使用 Write 方法发送消息
	// Use the Write method to send the message
	_, err := c.conn.Write(msg)

	// 检查是否在发送消息时出现错误
	// Check if there was an error while sending the message
	if err != nil {
		// 如果发送消息时出错，打印错误并返回 false
		// If an error occurs while sending the message, print the error and return false
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

	// 检查是否在读取响应时出现错误
	// Check if there was an error while reading the response
	if err != nil {
		// 如果读取响应时出错，检查是否是 EOF 错误
		// If an error occurs while reading the response, check if it is an EOF error
		if err != io.EOF {
			// 如果不是 EOF 错误，打印错误并返回 false
			// If it is not an EOF error, print the error and return false
			fmt.Println("*** [TCPClient] read error:", err)
		} else {
			// 如果是 EOF 错误，打印 EOF 信息并返回 false
			// If it is an EOF error, print EOF information and return false
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

	// 返回 ping 操作的结果
	// Return the result of the ping operation
	return client.Ping()
}

// CloseFunc 是一个用于关闭 TCP 客户端连接的函数
// CloseFunc is a function for closing the TCP client connection.
func CloseFunc(any any) error {
	// 获取 TCP 客户端并关闭连接
	// Get the TCP client and close the connection
	client := any.(*TCPClient)

	// 执行关闭操作
	// Perform the close operation
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
	conf := conecta.NewConfig().

		// 设置新建连接的函数
		// Set the function to create a new connection.
		WithNewFunc(NewFunc).

		// 设置检查连接的函数
		// Set the function to check the connection.
		WithPingFunc(PingFunc).

		// 设置关闭连接的函数
		// Set the function to close the connection.
		WithCloseFunc(CloseFunc).

		// 设置检查连接的最大重试次数
		// Set the maximum number of retries to check the connection.
		WithPingMaxRetries(1).

		// 设置扫描间隔
		// Set the scan interval.
		WithScanInterval(scanInterval).

		// 设置回调函数
		// Set the callback function.
		WithCallback(&TestCallback{})

	// 使用基础队列和配置创建新的 Conecta 池
	// Create a new Conecta pool using the base queue and configuration
	pool, err := conecta.New(baseQ, conf)

	// 检查是否在创建池时出现错误
	// Check if there was an error while creating the pool
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

	// 检查是否在创建服务器时出现错误
	// Check if there was an error while creating the server
	if err != nil {
		// 如果创建服务器时出错，打印错误并返回
		// If an error occurs while creating the server, print the error and return
		fmt.Println("!! [main] create server error:", err)
		return
	}
	// 使用 defer 关键字确保在函数结束时停止服务器
	// Use the defer keyword to ensure that the server is stopped when the function ends
	defer server.Stop()

	// 启动 TCP 服务器
	// Start the TCP server.
	server.Start()

	// 从池中获取一个 TCP 客户端
	// Get a TCP client from the pool.
	client, err := pool.GetOrCreate()

	// 检查是否在获取客户端时出现错误
	// Check if there was an error while getting the client
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

	// 打印一条消息，让用户等待客户端消息处理和一次扫描执行（需要 6 秒）
	// Print a message asking the user to wait for the client message to be processed and a scan to be executed (takes 6 seconds)
	fmt.Println("!! [main] please wait for the client msg processed and once scan to be executed... (6 seconds)")

	// 使程序暂停 6 秒，等待客户端消息处理和扫描完成
	// Pause the program for 6 seconds to wait for the client message processing and scan to complete
	time.Sleep(6 * time.Second)

	// 停止 TCP 服务器。当 TCP 客户端将被关闭时，TCP 客户端将从池中移除
	// Stop the TCP server. When TCP client will be closed, the TCP client will be removed from the pool.
	server.Stop()

	// 从池中获取一个 TCP 客户端
	// Get a TCP client from the pool.
	client, err = pool.GetOrCreate()

	// 检查是否在获取客户端时出现错误
	// Check if there was an error while getting the client
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

	// 打印一条消息，让用户等待扫描执行（需要 11 秒）
	// Print a message asking the user to wait for the scan to be executed (takes 11 seconds)
	fmt.Println("!! [main] please wait for the scan to be executed... (11 seconds)")

	// 使程序暂停 11 秒，等待扫描完成
	// Pause the program for 11 seconds to wait for the scan to complete
	time.Sleep(11 * time.Second)
}
```

**Result**

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
