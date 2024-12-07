package conecta

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/shengyanli1982/conecta/internal/pool"
)

const (
	// Default batch size for processing elements
	// 默认批处理大小
	DefaultBatchSize = 8

	// Default worker pool size for limiting concurrent operations
	// 默认工作池大小
	DefaultWorkerPoolSize = 16
)

var (
	// Error definitions
	// 错误定义
	ErrorQueueClosed         = errors.New("queue is closed")
	ErrorQueueInterfaceIsNil = errors.New("queue interface is nil")
)

// Pool structure for managing connection pool
// 连接池结构体
type Pool struct {
	queue       Queue              // Queue interface / 队列接口
	config      *Config            // Configuration / 配置信息
	wg          sync.WaitGroup     // Wait group for goroutines / 等待组
	once        sync.Once          // Ensure Stop is called only once / 确保Stop只执行一次
	ctx         context.Context    // Context for cancellation / 上下文
	cancel      context.CancelFunc // Cancel function / 取消函数
	elementpool *pool.ElementPool  // Pool for elements / 元素池
	workerpool  chan struct{}      // Worker pool for limiting concurrent operations / 工作池
}

// Create a new connection pool
// 创建新的连接池
func New(queue Queue, conf *Config) (*Pool, error) {
	// Check if queue is nil
	// 检查队列是否为空
	if queue == nil {
		return nil, ErrorQueueInterfaceIsNil
	}

	// Validate and get valid configuration
	// 验证并获取有效的配置
	conf = isConfigValid(conf)
	// Create context with cancellation
	// 创建带取消功能的上下文
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize pool instance
	// 初始化连接池实例
	p := &Pool{
		queue:       queue,
		config:      conf,
		elementpool: pool.NewElementPool(),
		ctx:         ctx,
		cancel:      cancel,
		workerpool:  make(chan struct{}, DefaultWorkerPoolSize),
	}

	// Initialize the pool
	// 初始化连接池
	if err := p.initialize(); err != nil {
		cancel()
		return nil, err
	}

	// Start executor goroutine
	// 启动执行器协程
	p.wg.Add(1)
	go p.executor()

	return p, nil
}

// Stop the connection pool
// 停止连接池
func (p *Pool) Stop() {
	// Ensure stop operation is executed only once
	// 确保只执行一次停止操作
	p.once.Do(func() {
		p.cancel()          // Cancel context / 取消上下文
		p.wg.Wait()         // Wait for all goroutines to complete / 等待所有协程完成
		p.cleanupElements() // Clean up all elements / 清理所有元素
		p.queue.Shutdown()  // Shutdown the queue / 关闭队列
	})
}

// Initialize the connection pool with initial connections
// 初始化连接池
func (p *Pool) initialize() error {
	// Create slices for initial elements
	// 创建初始元素切片
	elements := make([]any, 0, p.config.initialize)
	successElements := make([]*pool.Element, 0, p.config.initialize)

	// Create initial connections
	// 创建初始连接
	for i := 0; i < p.config.initialize; i++ {
		// Create new connection
		// 创建新的连接
		if value, err := p.config.newFunc(); err != nil {
			// If creation fails, clean up existing connections
			// 如果创建失败，清理已创建的连接
			for _, element := range successElements {
				if v := element.GetData(); v != nil {
					p.config.closeFunc(v)
				}
				p.elementpool.Put(element)
			}
			return err
		} else {
			elements = append(elements, value)
		}
	}

	// Put created connections into queue
	// 将创建的连接放入队列
	for _, value := range elements {
		element := p.elementpool.Get()
		element.SetData(value)
		successElements = append(successElements, element)

		// Try to put element into queue
		// 尝试将元素放入队列
		if err := p.queue.Put(element); err != nil {
			// If putting fails, clean up all elements
			// 如果放入失败，清理所有元素
			for _, e := range successElements {
				if v := e.GetData(); v != nil {
					p.config.closeFunc(v)
				}
				p.elementpool.Put(e)
			}
			return err
		}
	}
	return nil
}

// Executor that periodically processes connections
// 执行器，定期处理连接
func (p *Pool) executor() {
	// Create ticker for periodic execution
	// 创建定时器
	ticker := time.NewTicker(time.Millisecond * time.Duration(p.config.scanInterval))
	defer func() {
		ticker.Stop() // Stop ticker / 停止定时器
		p.wg.Done()   // Mark goroutine as done / 标记协程完成
	}()

	// Loop for processing connections
	// 循环处理连接
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.processElements()
		}
	}
}

// Process connection elements in batches
// 处理连接元素
func (p *Pool) processElements() {
	// Create slice for batch processing
	// 创建批处理元素切片
	elements := make([]*pool.Element, 0, DefaultBatchSize)

	// Iterate through elements in queue
	// 遍历队列中的元素
	p.queue.Range(func(data any) bool {
		element := data.(*pool.Element)
		elements = append(elements, element)

		// Process current batch when reaching batch size
		// 当达到批处理大小时处理当前批��
		if len(elements) >= DefaultBatchSize {
			p.processBatch(elements)
			elements = elements[:0]
		}
		return true
	})

	// Process remaining elements
	// 处理剩余的元素
	if len(elements) > 0 {
		p.processBatch(elements)
	}
}

// Process a batch of connections concurrently
// 批量处理连接
func (p *Pool) processBatch(elements []*pool.Element) {
	var wg sync.WaitGroup

	// Process each element concurrently
	// 并发处理每个元素
	for _, element := range elements {
		wg.Add(1)
		p.workerpool <- struct{}{} // Acquire worker slot / 获取工作协程槽位

		go func(element *pool.Element) {
			defer func() {
				<-p.workerpool // Release worker slot / 释放工作协程槽位
				wg.Done()      // Mark current processing as done / 标记当前处理完成
			}()

			value := element.GetData()
			retryCount := int(element.GetValue())

			// Check if exceeded max retry attempts
			// 检查是否超过最大重试次数
			if retryCount >= p.config.maxRetries {
				p.handleMaxRetries(element, value)
				return
			}

			// Perform ping operation and handle result
			// 执行ping操作并处理结果
			if ok := p.config.pingFunc(value, retryCount); ok {
				element.SetValue(0) // Reset retry count / 重置重试次数
				p.config.callback.OnPingSuccess(value)
			} else {
				element.SetValue(int64(retryCount) + 1) // Increment retry count / 增加重试次数
				p.config.callback.OnPingFailure(value)
			}
		}(element)
	}

	wg.Wait() // Wait for all processing to complete / 等待所有处理完成
}

// Clean up all connections in the pool
// 清理所有连接
func (p *Pool) cleanupElements() {
	// Clean up all elements in queue
	// 清理队列中的所有元素
	p.queue.Range(func(data any) bool {
		element := data.(*pool.Element)
		if value := element.GetData(); value != nil {
			err := p.config.closeFunc(value)
			p.config.callback.OnClose(value, err)
			element.Reset()
		}
		return true
	})

	// Try to clean up possible remaining elements
	// 尝试清理可能的残留元素
	maxAttempts := p.queue.Len() * 2
	attempts := 0
	for {
		if _, err := p.Get(); err != nil || attempts >= maxAttempts {
			break
		}
		attempts++
	}
}

// Handle connections that have reached maximum retry attempts
// 处理达到最大重试次数的连接
func (p *Pool) handleMaxRetries(element *pool.Element, value any) {
	// Close and clean up connection that exceeded retry attempts
	// 关闭并清理超过重试次数的连接
	if value != nil {
		err := p.config.closeFunc(value)
		p.config.callback.OnClose(value, err)
		element.SetData(nil)
	}
}

// Put a connection into the pool
// 将连接放入池中
func (p *Pool) Put(data any) error {
	// Check if queue is closed
	// 检查队列是否已关闭
	if p.queue.IsClosed() {
		return ErrorQueueClosed
	}

	// Get element and set data
	// 获取元素并设置数据
	element := p.elementpool.Get()
	element.SetData(data)

	// Put element into queue
	// 将元素放入队列
	if err := p.queue.Put(element); err != nil {
		p.elementpool.Put(element)
		return err
	}

	return nil
}

// Get a connection from the pool
// 从池中获取连接
func (p *Pool) Get() (any, error) {
	// Check if queue is closed
	// 检查队列是否已关闭
	if p.queue.IsClosed() {
		return nil, ErrorQueueClosed
	}

	// Loop to get valid connection
	// 循环尝试获取有效连接
	for {
		// Get element from queue
		// 从队列中获取元素
		element, err := p.queue.Get()
		if err != nil {
			return nil, err
		}

		// Mark element as done
		// 标记元素已处理
		p.queue.Done(element)
		data := element.(*pool.Element)
		value := data.GetData()

		// Return valid connection
		// 返回有效连接
		if value != nil {
			p.elementpool.Put(data)
			return value, nil
		}

		// Recycle invalid element
		// 回收无效元素
		p.elementpool.Put(data)
	}
}

// Get a connection from the pool or create a new one if none available
// 从池中获取连接，如果没有则创建新的
func (p *Pool) GetOrCreate() (any, error) {
	// Try to get connection from pool
	// 尝试从池中获取连接
	value, err := p.Get()
	if err == nil {
		return value, nil
	}

	// Check if error is due to closed queue
	// 检查是否因为队列关闭导致的错误
	if errors.Is(err, ErrorQueueClosed) {
		return nil, err
	}

	// Create new connection
	// 创建新的连接
	return p.config.newFunc()
}

// Get the number of connections in the pool
// 获取池中连接数量
func (p *Pool) Len() int {
	return p.queue.Len()
}

// Clean up connections in the pool
// 清理池中的连接
func (p *Pool) Cleanup() {
	p.cleanupElements()
}
