package main

import (
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

type TCPServer struct {
	listener *net.TCPListener
	clients  []*net.TCPConn
	wg       sync.WaitGroup
	once     sync.Once
}

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
		once:     sync.Once{},
	}
	return &server, nil
}

func (s *TCPServer) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		for {
			conn, err := s.listener.AcceptTCP()
			if err != nil {
				continue
			}
			s.wg.Add(1)
			go s.handleConnection(conn)
			s.clients = append(s.clients, conn)
			fmt.Println(">>> [TCPServer] new client:", conn.RemoteAddr().String())
		}
	}()
}

func (s *TCPServer) Stop() {
	s.once.Do(func() {
		_ = s.listener.Close()
		for _, c := range s.clients {
			_ = c.Close()
		}
		s.wg.Wait()
	})
}

func (s *TCPServer) handleConnection(conn *net.TCPConn) {
	defer s.wg.Done()
	for {
		buf := make([]byte, 1024)
		_, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				fmt.Println(">>> [TCPServer] read error:", err)
			} else {
				fmt.Println(">>> [TCPServer] EOF")
			}
			return
		}
		fmt.Println(">>> [TCPServer] read:", string(buf))
		_, err = conn.Write(buf)
		if err != nil {
			fmt.Println(">>> [TCPServer] write error:", err)
			return
		}
		fmt.Println(">>> [TCPServer] write:", string(buf))
	}
}

// =================== TCP Client ===================

type TCPClient struct {
	conn *net.TCPConn
	once sync.Once
}

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

func (c *TCPClient) Send(msg []byte) bool {
	_, err := c.conn.Write(msg)
	if err != nil {
		fmt.Println("*** [TCPClient] write error:", err)
		return false
	}
	fmt.Println("*** [TCPClient] write:", msg)
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
	fmt.Println("*** [TCPClient] read:", string(buf))
	return true
}

func (c *TCPClient) Close() {
	c.once.Do(func() {
		fmt.Println("*** [TCPClient] close")
		_ = c.conn.Close()
	})
}

func (c *TCPClient) Ping() bool {
	return c.Send([]byte("ping"))
}

// =================== Conecta ===================

type TestCallback struct{}

func (c *TestCallback) OnPingSuccess(data any) {
	fmt.Println("$$ OnPingSuccess", data)
}
func (c *TestCallback) OnPingFailure(data any) {
	fmt.Println("$$ OnPingFailure", data)
}
func (c *TestCallback) OnClose(data any, err error) {
	fmt.Println("$$ OnClose", data, err)
}

func NewFunc() (any, error) {
	return NewTCPClient(serverAddr, serverPort)
}

func PingFunc(any any, c int) bool {
	client := any.(*TCPClient)
	return client.Ping()
}

func CloseFunc(any any) error {
	client := any.(*TCPClient)
	client.Close()
	return nil
}

// =================== Application ===================

func main() {
	scanInterval := 5000

	baseQ := workqueue.NewSimpleQueue(nil)
	conf := conecta.NewConfig().WithNewFunc(NewFunc).WithPingFunc(PingFunc).WithCloseFunc(CloseFunc).WithPingMaxRetries(1).WithScanInterval(scanInterval).WithCallback(&TestCallback{})

	pool := conecta.New(baseQ, conf)
	defer pool.Stop()

	server, err := NewTCPServer(serverPort)
	if err != nil {
		fmt.Println("!! [main] create server error:", err)
		return
	}
	defer server.Stop()
	server.Start()

	client, err := pool.GetOrCreate()
	if err != nil {
		fmt.Println("!! [main] get client error:", err)
		return
	}

	_ = client.(*TCPClient).Send([]byte("msg1"))

	_ = pool.Put(client)

	fmt.Println("!! [main] please wait for the client msg processed and once scan to be executed... (6 seconds)")
	time.Sleep(6 * time.Second)
	server.Stop()

	client, err = pool.GetOrCreate()
	if err != nil {
		fmt.Println("!! [main] get client error:", err)
		return
	}

	_ = client.(*TCPClient).Send([]byte("msg2"))

	_ = pool.Put(client)

	fmt.Println("!! [main] please wait for the scan to be executed... (11 seconds)")
	time.Sleep(11 * time.Second)
}
