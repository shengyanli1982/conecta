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
	listenAddr = "0.0.0.0"
	serverAddr = "127.0.0.1"
	serverPort = 13134
)

// =================== TCP Server ===================

// TCPServer represents a TCP server.
type TCPServer struct {
	listener *net.TCPListener
	clients  []*net.TCPConn
	wg       sync.WaitGroup
	once     sync.Once
}

// NewTCPServer creates a new TCP server.
func NewTCPServer(port int) (*TCPServer, error) {
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{
		IP:   net.ParseIP(listenAddr),
		Port: serverPort,
	})
	if err != nil {
		return nil, err
	}
	server := TCPServer{
		listener: listener,
		clients:  make([]*net.TCPConn, 0),
		wg:       sync.WaitGroup{},
		once:     sync.Once{},
	}
	return &server, nil
}

// Start starts the TCP server.
func (s *TCPServer) Start() {
	s.wg.Add(1)
	go s.listen()
}

// Stop stops the TCP server.
func (s *TCPServer) Stop() {
	s.once.Do(func() {
		fmt.Println(">>> [TCPServer] stop")
		for _, c := range s.clients {
			_ = c.Close()
		}
		fmt.Println(">>> [TCPServer] close listener")
		_ = s.listener.Close()
		fmt.Println(">>> [TCPServer] close all clients")
		s.wg.Wait()
	})
}

// listen listens for incoming connections.
func (s *TCPServer) listen() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.AcceptTCP()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				fmt.Println(">>> [TCPServer] EOF")
				break
			} else {
				fmt.Println(">>> [TCPServer] accept error:", err)
				continue
			}
		}
		s.wg.Add(1)
		go s.handle(conn)
		s.clients = append(s.clients, conn)
		fmt.Println(">>> [TCPServer] new client:", conn.RemoteAddr().String())
	}
}

// handle handles the client connection.
func (s *TCPServer) handle(conn *net.TCPConn) {
	defer s.wg.Done()

	for {
		buf := make([]byte, 1024)
		_, err := conn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				fmt.Println(">>> [TCPServer] read error:", err)
			} else {
				fmt.Println(">>> [TCPServer] EOF")
			}
			break
		}
		fmt.Println(">>> [TCPServer] <- read:", string(buf))
		_, err = conn.Write(buf)
		if err != nil {
			fmt.Println(">>> [TCPServer] write error:", err)
			break
		}
		fmt.Println(">>> [TCPServer] -> write:", string(buf))
	}
}

// =================== TCP Client ===================

// TCPClient represents a TCP client.
type TCPClient struct {
	conn *net.TCPConn
	once sync.Once
}

// NewTCPClient creates a new TCP client.
func NewTCPClient(addr string, port int) (*TCPClient, error) {
	conn, err := net.DialTCP("tcp", nil, &net.TCPAddr{
		IP:   net.ParseIP(addr),
		Port: port,
	})
	if err != nil {
		fmt.Println("*** [TCPClient] create client error:", err)
		return nil, err
	}

	return &TCPClient{
		conn: conn,
		once: sync.Once{},
	}, nil
}

// Send sends a message to the server.
func (c *TCPClient) Send(msg []byte) bool {
	_, err := c.conn.Write(msg)
	if err != nil {
		fmt.Println("*** [TCPClient] write error:", err)
		return false
	}
	fmt.Println("*** [TCPClient] -> write:", string(msg))
	buf := make([]byte, 1024)
	_, err = c.conn.Read(buf)
	if err != nil {
		if err != io.EOF {
			fmt.Println("*** [TCPClient] read error:", err)
		} else {
			fmt.Println("*** [TCPClient] EOF")
		}
		return false
	}
	fmt.Println("*** [TCPClient] <- read:", string(buf))
	return true
}

// Close closes the TCP client connection.
func (c *TCPClient) Close() {
	c.once.Do(func() {
		fmt.Println("*** [TCPClient] close")
		_ = c.conn.Close()
	})
}

// Ping pings the server.
func (c *TCPClient) Ping() bool {
	return c.Send([]byte("ping"))
}

// =================== Conecta ===================

// TestCallback is a callback implementation for Conecta.
type TestCallback struct{}

// OnPingSuccess is called when the ping is successful.
func (c *TestCallback) OnPingSuccess(data any) {
	fmt.Println("$$ OnPingSuccess", data)
}

// OnPingFailure is called when the ping fails.
func (c *TestCallback) OnPingFailure(data any) {
	fmt.Println("$$ OnPingFailure", data)
}

// OnClose is called when the connection is closed.
func (c *TestCallback) OnClose(data any, err error) {
	fmt.Println("$$ OnClose", data, err)
}

// NewFunc is a factory function for creating TCP clients.
func NewFunc() (any, error) {
	return NewTCPClient(serverAddr, serverPort)
}

// PingFunc is a function for pinging the server.
func PingFunc(any any, c int) bool {
	client := any.(*TCPClient)
	return client.Ping()
}

// CloseFunc is a function for closing the TCP client connection.
func CloseFunc(any any) error {
	client := any.(*TCPClient)
	client.Close()
	return nil
}

// =================== Application ===================

func main() {
	// Set the scan interval to 5 seconds.
	scanInterval := 5000

	// Create a work queue.
	baseQ := workqueue.NewSimpleQueue(nil)

	// Create a Conecta pool.
	conf := conecta.NewConfig().WithNewFunc(NewFunc).WithPingFunc(PingFunc).WithCloseFunc(CloseFunc).WithPingMaxRetries(1).WithScanInterval(scanInterval).WithCallback(&TestCallback{})
	pool := conecta.New(baseQ, conf)
	defer pool.Stop()

	// Create a TCP server.
	server, err := NewTCPServer(serverPort)
	if err != nil {
		fmt.Println("!! [main] create server error:", err)
		return
	}
	defer server.Stop()
	server.Start()

	// Get a TCP client from the pool.
	client, err := pool.GetOrCreate()
	if err != nil {
		fmt.Println("!! [main] get client error:", err)
		return
	}

	// Send a message to the server.
	_ = client.(*TCPClient).Send([]byte("msg1"))

	// Put the TCP client back to the pool.
	_ = pool.Put(client)

	fmt.Println("!! [main] please wait for the client msg processed and once scan to be executed... (6 seconds)")
	time.Sleep(6 * time.Second)

	// Stop the TCP server. When TCP client will be closed, the TCP client will be removed from the pool.
	server.Stop()

	// Get a TCP client from the pool.
	client, err = pool.GetOrCreate()
	if err != nil {
		fmt.Println("!! [main] get client error:", err)
		return
	}

	// Send a message to the server. The message will fail to send.
	_ = client.(*TCPClient).Send([]byte("msg2"))

	// Put the TCP client back to the pool.
	_ = pool.Put(client)

	fmt.Println("!! [main] please wait for the scan to be executed... (11 seconds)")
	time.Sleep(11 * time.Second)
}
```

**Result**

```bash
$ go run demo.go
*** [TCPClient] -> write: msg1
>>> [TCPServer] new client: 127.0.0.1:58021
>>> [TCPServer] <- read: msg1
>>> [TCPServer] -> write: msg1
*** [TCPClient] <- read: msg1
!! [main] please wait for the client msg processed and once scan to be executed... (6 seconds)
*** [TCPClient] -> write: ping
>>> [TCPServer] <- read: ping
>>> [TCPServer] -> write: ping
*** [TCPClient] <- read: ping
$$ OnPingSuccess &{0xc000012048 {0 {0 0}}}
>>> [TCPServer] stop
>>> [TCPServer] read error: read tcp 127.0.0.1:13134->127.0.0.1:58021: use of closed network connection
>>> [TCPServer] close listener
>>> [TCPServer] EOF
>>> [TCPServer] close all clients
*** [TCPClient] -> write: msg2
*** [TCPClient] EOF
!! [main] please wait for the scan to be executed... (11 seconds)
*** [TCPClient] write error: write tcp 127.0.0.1:58021->127.0.0.1:13134: write: broken pipe
$$ OnPingFailure &{0xc000012048 {0 {0 0}}}
*** [TCPClient] close
$$ OnClose &{0xc000012048 {1 {0 0}}} <nil>
```
