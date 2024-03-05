package conecta

import (
	"context"
	"errors"
	"sync"
	"time"

	itl "github.com/shengyanli1982/conecta/internal"
)

// ErrorQueueClosed 表示队列已经关闭
// ErrorQueueClosed indicates that the queue has been closed.
var ErrorQueueClosed = errors.New("queue is closed")

// ErrorQueueInterfaceIsNil 表示队列接口为空
// ErrorQueueInterfaceIsNil indicates that the queue interface is empty.
var ErrorQueueInterfaceIsNil = errors.New("queue interface is nil")

// Pool 是连接池的结构体
// Pool is the struct of the connection pool
type Pool struct {
	queue       QueueInterface     // 队列接口
	config      *Config            // 配置
	wg          sync.WaitGroup     // 同步等待组
	once        sync.Once          // 同步一次
	ctx         context.Context    // 上下文
	cancel      context.CancelFunc // 取消函数
	elementpool *itl.ElementPool   // 元素池
}

// New 创建一个新的连接池
// New creates a new connection pool
func New(queue QueueInterface, conf *Config) (*Pool, error) {
	// 如果队列为空，则返回 nil 和错误
	// If the queue is empty, return nil and an error
	if queue == nil {
		return nil, ErrorQueueInterfaceIsNil
	}

	// 如果配置为空，则使用默认配置
	// If the configuration is empty, use the default configuration
	conf = isConfigValid(conf)

	// 创建连接池
	// Create a connection pool
	pool := Pool{
		queue:       queue,
		config:      conf,
		wg:          sync.WaitGroup{},
		once:        sync.Once{},
		elementpool: itl.NewElementPool(),
	}
	pool.ctx, pool.cancel = context.WithCancel(context.Background())

	// 初始化连接池
	// Initialize the connection pool
	err := pool.initialize()
	if err != nil {
		return nil, err
	}

	// 启动执行器
	// Start the executor
	pool.wg.Add(1)
	go pool.executor()

	// 返回连接池
	// Return the connection pool
	return &pool, nil
}

// Stop 停止连接池
// Stop stops the connection pool
func (p *Pool) Stop() {
	p.once.Do(func() {
		// 发送关闭信号
		// Send the close signal
		p.cancel()
		p.wg.Wait()

		// 关闭队列
		// Close the queue
		p.queue.Stop()
	})
}

// initialize 是连接池的初始化方法
// initialize is the initialization method for the connection pool
func (p *Pool) initialize() error {
	// 创建一个元素列表，长度为配置中的初始化元素数量
	// Create an element list with a length of the initial element count in the configuration
	elements := make([]any, 0, p.config.initialize)

	// 循环创建 p.config.initialize 个元素
	// Loop to create p.config.initialize elements
	for i := 0; i < p.config.initialize; i++ {
		// 使用配置中的 newFunc 方法创建新的元素
		// Use the newFunc method in the configuration to create a new element
		value, err := p.config.newFunc()
		if err != nil {
			// 如果创建元素失败，则返回错误
			// If the creation of the element fails, return the error
			return err
		}

		// 如果创建元素成功，则将元素添加到元素列表中
		// If the creation of the element is successful, add the element to the element list
		elements = append(elements, value)
	}

	// 将元素列表中的元素添加到队列中
	// Add the elements in the element list to the queue
	for _, value := range elements {
		err := p.queue.Add(value)
		if err != nil {
			// 如果添加元素到队列失败，则返回错误
			// If adding the element to the queue fails, return the error
			return err
		}
	}

	// 如果所有元素都成功添加到队列，返回 nil
	// If all elements are successfully added to the queue, return nil
	return nil
}

// executor 是连接池的执行器，定期检测连接状态
// executor is the executor of the connection pool, periodically checks the connection status
func (p *Pool) executor() {
	// 创建一个定时器，每 p.config.scanInterval 毫秒触发一次
	// Create a timer that triggers every p.config.scanInterval milliseconds
	ticker := time.NewTicker(time.Millisecond * time.Duration(p.config.scanInterval))

	// 使用 defer 语句确保在函数退出时停止定时器并完成等待组的计数
	// Use a defer statement to ensure that the timer is stopped and the wait group count is completed when the function exits
	defer func() {
		p.wg.Done()
		ticker.Stop()
	}()

	// 使用 for 循环和 select 语句来定期检测连接状态
	// Use a for loop and select statement to periodically check the connection status
	for {
		select {
		// 当收到关闭信号时，退出循环
		// Exit the loop when a shutdown signal is received
		case <-p.ctx.Done():
			return

		// 当定时器触发时，遍历队列中的元素并对每个元素进行 Ping 检测
		// When the timer triggers, traverse the elements in the queue and perform a Ping check on each element
		case <-ticker.C:
			p.queue.Range(func(data any) bool {
				// 获取元素和元素的值
				// Get the element and its value
				element := data.(*itl.Element)
				value := element.GetData()
				retryCount := int(element.GetValue())

				// 如果元素的 Ping 次数超过最大重试次数，则关闭连接
				// If the number of Ping times of the element exceeds the maximum number of retries, the connection is closed
				if retryCount >= p.config.maxRetries {
					// 如果元素的值不为 nil，则关闭连接
					// If the value of the element is not nil, close the connection
					if value != nil {
						// 关闭连接并调用回调函数
						// Close the connection and call the callback function
						err := p.config.closeFunc(value)
						p.config.callback.OnClose(value, err)

						// 将元素的数据置为空
						// Set the data of the element to nil
						element.SetData(nil)
					}
				} else {
					// 对元素进行 Ping 检测
					// Perform a Ping check on the element
					if ok := p.config.pingFunc(value, retryCount); ok {
						// 如果 Ping 检测成功，则重置 Ping 次数并调用回调函数
						// If the Ping check is successful, reset the number of Ping times and call the callback function
						element.SetValue(0)
						p.config.callback.OnPingSuccess(value)
					} else {
						// 如果 Ping 检测失败，则将 Ping 次数加 1 并调用回调函数
						// If the Ping check fails, increment the number of Ping times by 1 and call the callback function
						element.SetValue(int64(retryCount) + 1)
						p.config.callback.OnPingFailure(value)
					}
				}

				// 返回 true 以继续遍历队列中的其他元素
				// Return true to continue traversing other elements in the queue
				return true
			})
		}
	}
}

// Put 方法将数据放入连接池
// The Put method puts data into the connection pool
func (p *Pool) Put(data any) error {
	// 如果队列已经关闭，则返回 ErrorQueueClosed 错误
	// If the queue is closed, return the ErrorQueueClosed error
	if p.queue.IsClosed() {
		return ErrorQueueClosed
	}

	// 从对象池中获取一个元素
	// Get an element from the object pool
	element := p.elementpool.Get()

	// 设置元素的数据
	// Set the data of the element
	element.SetData(data)
	element.SetValue(0)

	// 将元素放入队列
	// Put the element into the queue
	return p.queue.Add(element)
}

// Get 方法从连接池中获取数据
// The Get method retrieves data from the connection pool
func (p *Pool) Get() (any, error) {
	// 如果队列已经关闭，则返回 ErrorQueueClosed 错误
	// If the queue is closed, return the ErrorQueueClosed error
	if p.queue.IsClosed() {
		return nil, ErrorQueueClosed
	}

	// 从队列中获取一个元素，如果元素的值不为 nil，则返回元素的值，否则继续获取
	// Get an element from the queue. If the value of the element is not nil, return the value of the element, otherwise continue to get it
	for {
		// 从队列中获取一个元素
		// Get an element from the queue
		element, err := p.queue.Get()
		if err != nil {
			return nil, err
		}
		p.queue.Done(element)

		// 从元素中获取数据
		// Get data from the element
		data := element.(*itl.Element)
		value := data.GetData()

		// 如果元素的值不为 nil，则返回元素的值
		// If the value of the element is not nil, return the value of the element
		if value != nil {
			p.elementpool.Put(data)
			return value, nil
		}

		// 如果元素的值为 nil，则将元素放回对象池，继续获取
		// If the value of the element is nil, put the element back into the object pool and continue to get it
		p.elementpool.Put(data)
	}
}

// GetOrCreate 方法从连接池中获取数据，如果连接池已经关闭，则返回 ErrorQueueClosed 错误，否则创建新的数据
// The GetOrCreate method retrieves data from the connection pool. If the connection pool is already closed, it returns the ErrorQueueClosed error, otherwise it creates new data
func (p *Pool) GetOrCreate() (any, error) {
	// 调用 Get 方法获取一个元素，如果没有错误，则返回元素的值
	// Call the Get method to get an element. If there is no error, return the value of the element
	value, err := p.Get()

	// 如果获取元素时出现错误
	// If an error occurs when getting the element
	if err != nil {
		// 如果错误是 ErrorQueueClosed，则直接返回错误
		// If the error is ErrorQueueClosed, return the error directly
		if errors.Is(err, ErrorQueueClosed) {
			return nil, err
		} else {
			// 如果错误不是 ErrorQueueClosed，则调用配置中的 newFunc 方法创建新的数据
			// If the error is not ErrorQueueClosed, call the newFunc method in the configuration to create new data
			return p.config.newFunc()
		}
	}

	// 如果没有错误，则返回元素的值
	// If there is no error, return the value of the element
	return value, nil
}

// Len 方法返回连接池中的元素数量
// The Len method returns the number of elements in the connection pool
func (p *Pool) Len() int {
	// 调用队列的 Len 方法获取队列中的元素数量
	// Call the Len method of the queue to get the number of elements in the queue
	return p.queue.Len()
}
