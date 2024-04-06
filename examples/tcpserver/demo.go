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
