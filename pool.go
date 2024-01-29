package conecta

import (
	"context"
	"errors"
	"sync"
	"time"

	itl "github.com/shengyanli1982/conecta/internal"
)

// 队列已经关闭
// ErrorQueueClosed indicates that the queue has been closed.
var ErrorQueueClosed = errors.New("queue is closed")

// 队列接口为空
// ErrorQueueInterfaceIsNil indicates that the queue interface is empty.
var ErrorQueueInterfaceIsNil = errors.New("queue interface is nil")

// 连接池结构体
// Connection pool structure.
type Pool struct {
	queue       QueueInterface
	config      *Config
	wg          sync.WaitGroup
	once        sync.Once
	ctx         context.Context
	cancel      context.CancelFunc
	elementpool *itl.ElementPool
}

// New 创建一个新的连接池
// New creates a new connection pool.
func New(queue QueueInterface, conf *Config) (*Pool, error) {
	// 如果队列为空，则返回 nil
	// If the queue is empty, return nil.
	if queue == nil {
		return nil, ErrorQueueInterfaceIsNil
	}

	// 如果配置为空，则使用默认配置
	// If the configuration is empty, use the default configuration.
	conf = isConfigValid(conf)

	// 创建连接池
	// Create a connection pool.
	pool := Pool{
		queue:       queue,
		config:      conf,
		wg:          sync.WaitGroup{},
		once:        sync.Once{},
		elementpool: itl.NewElementPool(),
	}
	pool.ctx, pool.cancel = context.WithCancel(context.Background())

	// 初始化连接池
	// Initialize the connection pool.
	err := pool.initialize()
	if err != nil {
		return nil, err
	}

	// 启动执行器
	// Start the executor.
	pool.wg.Add(1)
	go pool.executor()

	// 返回连接池
	// Return the connection pool.
	return &pool, nil
}

// Stop 停止连接池
// Stop stops the connection pool.
func (p *Pool) Stop() {
	p.once.Do(func() {
		// 发送关闭信号
		// Send the close signal.
		p.cancel()
		p.wg.Wait()

		// 关闭队列
		// Close the queue.
		p.queue.Stop()
	})
}

// 初始化连接池
// initializes the connection pool.
func (p *Pool) initialize() error {
	// 创建一个元素列表
	// Create an element list.
	elements := make([]any, 0)

	// 创建 p.config.initialize 个元素
	// Create p.config.initialize elements.
	for i := 0; i < p.config.initialize; i++ {
		// 创建新的元素
		// Create a new element.
		value, err := p.config.newFunc()
		if err != nil {
			// 如果创建元素失败，则直接退出
			// If the creation of the element fails, exit directly.
			return err
		}

		// 创建元素成功，则将元素列表中
		// If the creation of the element is successful, put the element in the list.
		elements = append(elements, value)
	}

	// 将元素列表放入队列
	// Put the element list into the queue.
	for _, value := range elements {
		err := p.queue.Add(value)
		if err != nil {
			return err
		}
	}

	// 返回 nil
	// Return nil.
	return nil
}

// executor 是连接池的执行器，定期检测连接状态
// executor is the executor of the connection pool, periodically checks the connection status.
func (p *Pool) executor() {
	// 每 p.config.scanInterval 毫秒检测一次
	// Check every p.config.scanInterval milliseconds.
	ticker := time.NewTicker(time.Millisecond * time.Duration(p.config.scanInterval))

	defer func() {
		p.wg.Done()
		ticker.Stop()
	}()

	for {
		select {
		case <-p.ctx.Done():
			return

		case <-ticker.C:
			// 遍历 queue 中的元素，然后对元素做 Ping 的检测
			// Traverse the elements in the queue and perform Ping checks on them.
			p.queue.Range(func(data any) bool {
				element := data.(*itl.Element)
				value := element.GetData()
				retryCount := int(element.GetValue())
				// 如果元素的 Ping 次数超过最大重试次数，则关闭连接
				// If the number of Ping times of the element exceeds the maximum number of retries, the connection is closed.
				if retryCount >= p.config.maxRetries {
					// 如果元素的值不为 nil，则关闭连接
					// If the value of the element is not nil, close the connection.
					if value != nil {
						// 关闭连接
						// Close the connection.
						err := p.config.closeFunc(value)
						p.config.callback.OnClose(value, err)

						// 对象中的数据置空
						// Empty the data in the object.
						element.SetData(nil)
					}
				} else {
					// 执行 Ping 检测
					// Perform Ping checks.
					if ok := p.config.pingFunc(value, retryCount); ok {
						// 重置 Ping 次数
						// Reset the number of Ping times.
						element.SetValue(0)
						p.config.callback.OnPingSuccess(value)
					} else {
						// Ping 次数加 1
						// The number of Ping times plus 1.
						element.SetValue(int64(retryCount) + 1)
						p.config.callback.OnPingFailure(value)
					}
				}

				// 一定要返回 true，否则会导致 Range 函数提前退出
				// Be sure to return true, otherwise it will cause the Range function to exit early.
				return true
			})
		}
	}
}

// Put 将数据放入连接池
// Put puts data into the connection pool.
func (p *Pool) Put(data any) error {
	// 如果队列已经关闭，则返回 ErrorQueueClosed
	// If the queue is closed, return ErrorQueueClosed.
	if p.queue.IsClosed() {
		return ErrorQueueClosed
	}

	// 从对象池中获取一个元素
	// Get an element from the object pool.
	element := p.elementpool.Get()

	// 设置元素的数据
	// Set the data of the element.
	element.SetData(data)
	element.SetValue(0)

	// 将元素放入队列
	// Put the element into the queue.
	return p.queue.Add(element)
}

// Get 从连接池中获取数据
// Get retrieves data from the connection pool.
func (p *Pool) Get() (any, error) {
	// 如果队列已经关闭，则返回 ErrorQueueClosed
	// If the queue is closed, return ErrorQueueClosed.
	if p.queue.IsClosed() {
		return nil, ErrorQueueClosed
	}

	// 从队列中获取一个元素，如果元素的值不为 nil，则返回元素的值，否则继续获取
	// Get an element from the queue. If the value of the element is not nil, return the value of the element, otherwise continue to get it.
	for {
		// 从队列中获取一个元素
		// Get an element from the queue.
		element, err := p.queue.Get()
		if err != nil {
			return nil, err
		}
		p.queue.Done(element)

		// 从元素中获取数据
		// Get data from the element.
		data := element.(*itl.Element)
		value := data.GetData()

		// 如果元素的值不为 nil，则返回元素的值
		// If the value of the element is not nil, return the value of the element.
		if value != nil {
			p.elementpool.Put(data)
			return value, nil
		}

		//如果元素的值为 nil，则将元素放回对象池，继续获取
		// If the value of the element is nil, put the element back into the object pool and continue to get it.
		p.elementpool.Put(data)
	}
}

// GetOrCreate 从连接池中获取数据，如果连接已经关闭，返回 ErrorQueueClosed。否则创建新的数据
// GetOrCreate retrieves data from the connection pool, if the pool is already closed, return ErrorQueueClosed. Otherwise, create new data.
func (p *Pool) GetOrCreate() (any, error) {
	// 获取一个元素，如果没有错误，则返回元素的值
	// Get an element, if there is no error, return the value of the element.
	value, err := p.Get()

	// 如果有错误，且 ErrorQueueClosed，则直接返回错误，否则创建新的数据
	// If there is an error and ErrorQueueClosed, return the error directly, otherwise create new data.
	if err != nil {
		// 如果错误是 ErrorQueueClosed，则直接返回错误
		// If the error is ErrorQueueClosed, return the error directly.
		if errors.Is(err, ErrorQueueClosed) {
			return nil, err
		} else {
			// 如果错误不是 ErrorQueueClosed，则创建新的数据
			// If the error is not ErrorQueueClosed, create new data.
			return p.config.newFunc()
		}
	}

	// 返回元素的值
	// Return the value of the element.
	return value, nil
}

// Len 返回连接池中的元素数量
// Len returns the number of elements in the connection pool.
func (p *Pool) Len() int {
	return p.queue.Len()
}
