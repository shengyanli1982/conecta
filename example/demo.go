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
