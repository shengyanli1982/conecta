package conecta

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/shengyanli1982/conecta/internal/pool"
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
	// 队列接口，用于管理连接池中的连接
	// QueueInterface is the interface for managing connections in the pool
	queue Queue

	// 配置，包含了连接池的所有配置信息
	// Config contains all the configuration information of the connection pool
	config *Config

	// 同步等待组，用于等待所有的连接都被正确处理
	// WaitGroup is used to wait for all connections to be properly handled
	wg sync.WaitGroup

	// 同步一次，确保某个操作只执行一次
	// Once is used to ensure that an operation is performed only once
	once sync.Once

	// 上下文，用于传递必要的信息和取消信号
	// Context is used to pass necessary information and cancellation signals
	ctx context.Context

	// 取消函数，用于取消上下文中的操作
	// CancelFunc is used to cancel operations in the context
	cancel context.CancelFunc

	// 元素池，用于存储和管理连接池中的元素
	// ElementPool is used to store and manage elements in the connection pool
	elementpool *pool.ElementPool
}

// New 创建一个新的连接池
// New creates a new connection pool
func New(queue Queue, conf *Config) (*Pool, error) {
	// 如果队列为空，则返回 nil 和错误
	// If the queue is null, return nil and an error
	if queue == nil {
		return nil, ErrorQueueInterfaceIsNil
	}
	// 如果配置为空，则使用默认配置
	// If the configuration is null, use the default configuration
	conf = isConfigValid(conf)

	// 创建一个 Pool 对象，包含一个队列，配置，同步等待组，同步一次执行，和一个元素池
	// Create a Pool object, including a queue, configuration, synchronous wait group, synchronous once execution, and an element pool
	pool := Pool{
		// 初始化一个队列
		// Initialize a queue
		queue: queue,

		// 使用验证过的配置
		// Use the validated configuration
		config: conf,

		// 初始化一个同步等待组
		// Initialize a synchronous wait group
		wg: sync.WaitGroup{},

		// 初始化一个同步一次执行
		// Initialize a synchronous once execution
		once: sync.Once{},

		// 创建一个新的元素池
		// Create a new element pool
		elementpool: pool.NewElementPool(),
	}

	// 创建一个可以被取消的上下文
	// Create a context that can be cancelled
	pool.ctx, pool.cancel = context.WithCancel(context.Background())

	// 初始化连接池
	// Initialize the connection pool
	err := pool.initialize()
	// 如果初始化过程中出现错误，则返回 nil 和错误
	// If an error occurs during initialization, return nil and the error
	if err != nil {
		return nil, err
	}

	// 启动执行器
	// Start the executor
	pool.wg.Add(1)

	// 在一个新的 goroutine 中运行执行器
	// Run the executor in a new goroutine
	go pool.executor()

	// 返回连接池
	// Return the connection pool
	return &pool, nil
}

// Stop 停止连接池
// Stop stops the connection pool
func (p *Pool) Stop() {
	// 使用 sync.Once 确保 Stop 方法只被执行一次
	// Use sync.Once to ensure that the Stop method is only executed once
	p.once.Do(func() {
		// 调用 cancel 函数来发送关闭信号
		// Call the cancel function to send a close signal
		p.cancel()

		// 使用 WaitGroup 等待所有的 goroutine 完成
		// Use WaitGroup to wait for all goroutines to complete
		p.wg.Wait()

		// 调用 Reset 方法来重置 Pool，这将清空 Pool 中的所有元素
		// Call the Reset method to reset the Pool, this will clear all elements in the Pool
		p.Cleanup()

		// 调用队列的 Stop 方法来关闭队列
		// Call the Stop method of the queue to close the queue
		p.queue.Shutdown()
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

		// 如果创建元素过程中出现错误
		// If an error occurs during the creation of the element
		if err != nil {
			// 则返回错误信息
			// Then return the error message
			return err
		}

		// 如果元素创建成功
		// If the element is created successfully
		// 则将元素添加到元素列表中
		// Then add the element to the element list
		elements = append(elements, value)
	}

	// 遍历 elements 列表中的每一个元素
	// Iterate over each element in the elements list
	for _, value := range elements {
		// 从元素池中获取一个元素
		// Get an element from the element pool
		element := p.elementpool.Get()

		// 设置元素的数据字段为当前遍历到的值
		// Set the data field of the element to the current value being iterated
		element.SetData(value)

		// 设置元素的值字段为 0
		// Set the value field of the element to 0
		element.SetValue(0)

		// 尝试将元素添加到队列中
		// Try to add the element to the queue
		err := p.queue.Put(element)

		// 如果添加失败
		// If the addition fails
		if err != nil {
			// 将元素放回元素池
			// Put the element back into the element pool
			p.elementpool.Put(element)

			// 返回错误
			// Return the error
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
				element := data.(*pool.Element)

				// 获取元素中的数据
				// Get the data from the element
				value := element.GetData()

				// 获取元素的值，这里的值通常用于表示重试的次数
				// Get the value of the element, which is usually used to represent the number of retries
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

						// 调用 OnClose 回调函数，传入 value 和 err
						// Call the OnClose callback function, passing in value and err
						p.config.callback.OnClose(value, err)

						// 将元素的数据置为空
						// Set the data of the element to nil
						element.SetData(nil)
					}
				} else {
					// 对元素进行 Ping 检测
					// Perform a Ping check on the element
					if ok := p.config.pingFunc(value, retryCount); ok {
						// 如果 Ping 检测成功，将元素的值设为 0
						// If the Ping check is successful, set the value of the element to 0
						element.SetValue(0)

						// 调用 OnPingSuccess 回调函数，传入 value
						// Call the OnPingSuccess callback function, passing in value
						p.config.callback.OnPingSuccess(value)
					} else {
						// 如果 Ping 检测失败，将元素的值加 1
						// If the Ping check fails, increment the value of the element by 1
						element.SetValue(int64(retryCount) + 1)

						// 调用 OnPingFailure 回调函数，传入 value
						// Call the OnPingFailure callback function, passing in value
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
	// If the queue is already closed, return the ErrorQueueClosed error
	if p.queue.IsClosed() {
		return ErrorQueueClosed
	}

	// 从元素池中获取一个元素
	// Get an element from the element pool
	element := p.elementpool.Get()

	// 设置元素的数据为传入的数据
	// Set the data of the element to the passed-in data
	element.SetData(data)

	// 设置元素的值为 0
	// Set the value of the element to 0
	element.SetValue(0)

	// 将元素放入队列
	// Put the element into the queue
	return p.queue.Put(element)
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

		// 如果获取过程中出现错误，直接返回错误
		// If an error occurs during the acquisition, return the error directly
		if err != nil {
			return nil, err
		}

		// 标记队列中的这个元素已经处理完毕
		// Mark this element in the queue as processed
		p.queue.Done(element)

		// 将获取到的元素转换为 Element 类型
		// Convert the obtained element to the Element type
		data := element.(*pool.Element)

		// 获取元素中的数据
		// Get the data from the element
		value := data.GetData()

		// 如果元素的数据不为 nil
		// If the data of the element is not nil
		if value != nil {
			// 将元素放回元素池
			// Put the element back into the element pool
			p.elementpool.Put(data)

			// 返回元素的数据和 nil 错误
			// Return the data of the element and a nil error
			return value, nil
		}

		// 如果元素的数据为 nil，则将元素放回元素池，然后继续获取
		// If the data of the element is nil, put the element back into the element pool and continue to get it
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

// Cleanup 是 Pool 结构体的一个方法，用于重置连接池
// Cleanup is a method of the Pool struct, used to reset the connection pool
func (p *Pool) Cleanup() {
	// 遍历连接池中的所有连接
	// Traverse all connections in the connection pool
	p.queue.Range(func(data any) bool {
		// 将数据转换为 Element 类型
		// Convert the data to Element type
		element := data.(*pool.Element)

		// 获取 Element 中的数据
		// Get the data in Element
		value := element.GetData()

		// 如果数据不为空
		// If the data is not null
		if value != nil {
			// 使用配置中的关闭函数关闭连接
			// Use the close function in the configuration to close the connection
			err := p.config.closeFunc(value)

			// 调用配置中的回调函数的 OnClose 方法处理关闭连接后的操作
			// Call the OnClose method of the callback function in the configuration to handle the operation after closing the connection
			p.config.callback.OnClose(value, err)

			// 重置 Element
			// Reset Element
			element.Reset()
		}

		// 返回 true 继续遍历
		// Return true to continue traversing
		return true
	})

	// 这个循环会一直尝试从池中获取连接，直到无法获取（可能是因为池已经空了）
	// This loop will keep trying to get a connection from the pool until it can't (possibly because the pool is empty)
	for {
		// 尝试从池中获取连接
		// Try to get a connection from the pool
		if _, err := p.Get(); err != nil {
			// 如果出现错误，就跳出循环
			// If there is an error, break the loop
			break
		}
	}
}
