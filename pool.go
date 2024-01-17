package conecta

import (
	"context"
	"errors"
	"sync"
	"time"

	itl "github.com/shengyanli1982/conecta/internal"
)

var ErrorQueueClosed = errors.New("queue is closed")

// 元素内存池
// Element memory pool.
var elementPool = itl.NewElementPool()

type Pool struct {
	queue  QInterface
	config *Config
	wg     sync.WaitGroup
	once   sync.Once
	ctx    context.Context
	cancel context.CancelFunc
}

// NewPool 创建一个新的连接池
// NewPool creates a new connection pool.
func NewPool(queue QInterface, conf *Config) *Pool {
	// 如果队列为空，则返回 nil
	// If the queue is empty, return nil.
	if queue == nil {
		return nil
	}

	// 如果配置为空，则使用默认配置
	// If the configuration is empty, use the default configuration.
	conf = isConfigValid(conf)

	// 创建连接池
	// Create a connection pool.
	pool := Pool{
		queue:  queue,
		config: conf,
		wg:     sync.WaitGroup{},
		once:   sync.Once{},
	}
	pool.ctx, pool.cancel = context.WithCancel(context.Background())

	// 启动执行器
	// Start the executor.
	pool.wg.Add(1)
	go pool.executor()

	// 返回连接池
	// Return the connection pool.
	return &pool
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

// executor 是连接池的执行器，定期检测连接状态
// executor is the executor of the connection pool, periodically checks the connection status.
func (p *Pool) executor() {
	// 每 5 秒检测一次
	// Check every 5 seconds.
	ticker := time.NewTicker(time.Second * 5)

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
				item := data.(*itl.Element)
				value := item.GetData()
				retryCount := int(item.GetValue())
				// 如果元素的 Ping 次数超过最大重试次数，则关闭连接
				// If the number of Ping times of the element exceeds the maximum number of retries, the connection is closed.
				if retryCount >= p.config.maxRetries {
					// 关闭连接
					// Close the connection.
					err := p.config.closeFunc(value)
					p.config.callback.OnClose(value, err)
					// 对象中的数据置空
					// Empty the data in the object.
					item.SetData(nil)
				} else {
					// 执行 Ping 检测
					// Perform Ping checks.
					if ok := p.config.pingFunc(value, retryCount); ok {
						// 重置 Ping 次数
						// Reset the number of Ping times.
						item.SetValue(0)
						p.config.callback.OnPingSuccess(value)
					} else {
						// Ping 次数加 1
						// The number of Ping times plus 1.
						item.SetValue(int64(retryCount) + 1)
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
	item := elementPool.Get()

	// 设置元素的数据
	// Set the data of the element.
	item.SetData(data)
	item.SetValue(0)

	// 将元素放入队列
	// Put the element into the queue.
	return p.queue.Add(item)
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
		item, err := p.queue.Get()
		if err != nil {
			return nil, err
		}
		p.queue.Done(item)

		// 从元素中获取数据
		// Get data from the element.
		data := item.(*itl.Element)
		value := data.GetData()

		// 如果元素的值不为 nil，则返回元素的值
		// If the value of the element is not nil, return the value of the element.
		if value != nil {
			elementPool.Put(data)
			return value, nil
		}

		//如果元素的值为 nil，则将元素放回对象池，继续获取
		// If the value of the element is nil, put the element back into the object pool and continue to get it.
		elementPool.Put(data)
	}
}

// GetOrCreate 从连接池中获取数据，如果连接已经关闭，返回 ErrorQueueClosed。否则创建新的数据
// GetOrCreate retrieves data from the connection pool, if the pool is already closed, return ErrorQueueClosed. Otherwise, create new data.
func (p *Pool) GetOrCreate() (any, error) {
	// 获取一个元素，如果没有错误，则返回元素的值
	// Get an element, if there is no error, return the value of the element.
	value, err := p.Get()
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
