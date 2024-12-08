package conecta

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/shengyanli1982/conecta/internal/pool"
)

var (
	// Error definitions for the pool package
	// 连接池包的错误定义
	ErrorQueueClosed         = errors.New("queue is closed")
	ErrorQueueInterfaceIsNil = errors.New("queue interface is nil")
)

const (
	// Default timeout for Get operation
	// Get 操作的默认超时时间
	defaultItemGetTimeout = 15 * time.Second
)

// Pool represents a connection pool that manages a collection of elements
// Pool 表示一个管理元素集合的连接池
type Pool struct {
	queue       Queue              // The underlying queue for storing elements / 用于存储元素的底层队列
	config      *Config            // Configuration for the pool / 连接池的配置
	wg          sync.WaitGroup     // WaitGroup for goroutine synchronization / 用于goroutine同步的等待组
	once        sync.Once          // Ensures Stop() is called only once / 确保Stop()只被调用一次
	ctx         context.Context    // Context for cancellation / 用于取消操作的上下文
	cancel      context.CancelFunc // Context cancellation function / 上下文取消函数
	elementpool *pool.ElementPool  // Pool for element objects / 元素对象池
}

// New creates a new Pool instance with the given queue and configuration
// 使用给定的队列和配置创建一个新的Pool实例
func New(queue Queue, conf *Config) (*Pool, error) {
	// Check if queue interface is valid
	// 检查队列接口是否有效
	if queue == nil {
		return nil, ErrorQueueInterfaceIsNil
	}
	// Validate and normalize configuration
	// 验证并规范化配置
	conf = isConfigValid(conf)
	ctx, cancel := context.WithCancel(context.Background())

	// Create new pool instance with initialized fields
	// 创建新的连接池实例并初始化字段
	p := &Pool{
		queue:       queue,
		config:      conf,
		elementpool: pool.NewElementPool(),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Initialize pool with initial elements
	// 初始化连接池的初始元素
	if err := p.initialize(); err != nil {
		cancel()
		return nil, err
	}

	// Start the executor goroutine
	// 启动执行器goroutine
	p.wg.Add(1)
	go p.executor()
	return p, nil
}

// initialize creates and adds initial elements to the pool
// 初始化连接池，创建并添加初始元素
func (p *Pool) initialize() error {
	// Create a slice to hold initial elements
	// 创建切片来存储初始元素
	elements := make([]any, 0, p.config.initialize)

	// Create initial elements using newFunc
	// 使用newFunc创建初始元素
	for i := 0; i < p.config.initialize; i++ {
		if value, err := p.config.newFunc(); err != nil {
			// Cleanup on error: close all created elements
			// 错误清理：关闭所有已创建的元素
			for _, v := range elements {
				if v != nil {
					_ = p.config.closeFunc(v)
				}
			}
			return err
		} else {
			elements = append(elements, value)
		}
	}

	// Add elements to the queue
	// 将元素添加到队列中
	for _, value := range elements {
		element := p.elementpool.Get()
		element.SetData(value)
		if err := p.queue.Put(element); err != nil {
			// Cleanup on error: return element to pool and close all elements
			// 错误清理：将元素返回到池中并关闭所有元素
			p.elementpool.Put(element)
			for _, v := range elements {
				if v != nil {
					_ = p.config.closeFunc(v)
				}
			}
			return err
		}
	}
	return nil
}

// Stop gracefully shuts down the pool
// 优雅地关闭连接池
func (p *Pool) Stop() {
	p.once.Do(func() {
		p.cancel()
		p.wg.Wait()
		p.Cleanup()
		p.queue.Shutdown()
	})
}

// Cleanup cleans up all elements in the pool
// 清理连接池中的所有元素
func (p *Pool) Cleanup() {
	p.queue.Range(func(data any) bool {
		element := data.(*pool.Element)
		if value := element.GetData(); value != nil {
			err := p.config.closeFunc(value)
			p.config.callback.OnClose(value, err)
			element.Reset()
			p.elementpool.Put(element)
		}
		return true
	})
	for {
		if _, err := p.Get(); err != nil {
			break
		}
	}
}

// Get retrieves an element from the pool
// 从连接池中获取一个元素
func (p *Pool) Get() (any, error) {
	// Check if queue is closed
	// 检查队列是否已关闭
	if p.queue.IsClosed() {
		return nil, ErrorQueueClosed
	}

	// Create timeout context for get operation
	// 为获取操作创建超时上下文
	ctx, cancel := context.WithTimeout(p.ctx, defaultItemGetTimeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			// Try to get element from queue
			// 尝试从队列中获取元素
			element, err := p.queue.Get()
			if err != nil {
				return nil, err
			}
			p.queue.Done(element)

			// Process the retrieved element
			// 处理获取到的元素
			data := element.(*pool.Element)
			value := data.GetData()
			p.elementpool.Put(data)

			if value != nil {
				return value, nil
			}
		}
	}
}

// GetOrCreate gets an element from the pool or creates a new one if none is available
// 从连接池获取元素，如果没有可用元素则创建新的
func (p *Pool) GetOrCreate() (any, error) {
	// Try to get existing element first
	// 首先尝试获取现有元素
	value, err := p.Get()
	if err == nil {
		return value, nil
	}

	// Return error if queue is closed
	// 如果队列已关闭则返回错误
	if errors.Is(err, ErrorQueueClosed) {
		return nil, err
	}

	// Create new element if none available
	// 如果没有可用元素则创建新元素
	return p.config.newFunc()
}

// Put adds a new element to the pool
// 向连接池中添加新元素
func (p *Pool) Put(data any) error {
	if p.queue.IsClosed() {
		return ErrorQueueClosed
	}
	element := p.elementpool.Get()
	element.SetData(data)
	if err := p.queue.Put(element); err != nil {
		p.elementpool.Put(element)
		return err
	}
	return nil
}

// Len returns the current number of elements in the pool
// 返回连接池中当前的元素数量
func (p *Pool) Len() int {
	return p.queue.Len()
}

// executor runs the main pool maintenance loop
// 运行连接池的主要维护循环
func (p *Pool) executor() {
	ticker := time.NewTicker(time.Millisecond * time.Duration(p.config.scanInterval))
	defer func() {
		ticker.Stop()
		p.wg.Done()
	}()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.maintain()
		}
	}
}

// maintain checks and maintains the health of pool elements by:
// 1. Checking each element's health status using pingFunc
// 2. Managing retry counts for failed health checks
// 3. Cleaning up elements that exceed max retry attempts
// 4. Triggering appropriate callbacks for different scenarios
//
// maintain 函数检查并维护连接池元素的健康状态，主要功能包括：
// 1. 使用 pingFunc 检查每个元素的健康状态
// 2. 管理健康检查失败时的重试计数
// 3. 清理超过最大重试次数的元素
// 4. 触发不同场景下的回调函数
func (p *Pool) maintain() {
	p.queue.Range(func(data any) bool {
		// Convert the data to an Element type and get its value
		// 将数据转换为 Element 类型并获取其值
		element := data.(*pool.Element)
		value := element.GetData()

		// Skip if the element value is nil
		// 如果元素值为空，直接跳过
		if value == nil {
			return true
		}

		// Get the retry count for this element
		// 获取该元素的重试计数
		retryCount := int(element.GetValue())

		// Skip if the element has been marked as invalid (retry count < 0)
		// 如果重试计数为负数，表示元素已被标记为失效，直接跳过
		if retryCount < 0 {
			return true
		}

		// Check element's health status using pingFunc
		// 使用 pingFunc 检查元素的健康状态
		if ok := p.config.pingFunc(value, retryCount); ok {
			// On successful ping: reset retry count and trigger success callback
			// Ping 成功：重置重试计数并触发成功回调
			element.SetValue(0)
			p.config.callback.OnPingSuccess(value)
			return true
		}

		// On ping failure: increment retry count
		// Ping 失败：增加重试计数
		retryCount++

		// If max retries exceeded, close and cleanup the element
		// 如果超过最大重试次数，关闭并清理元素
		if retryCount >= p.config.maxRetries {
			// Close the element, trigger close callback, and mark as invalid
			// 关闭元素，触发关闭回调，并标记为无效
			err := p.config.closeFunc(value)
			p.config.callback.OnClose(value, err)
			element.SetData(nil)
			element.SetValue(-1)
		} else {
			// Update retry count and trigger failure callback if within retry limit
			// 在重试次数限制内，更新重试计数并触发失败回调
			element.SetValue(int64(retryCount))
			p.config.callback.OnPingFailure(value)
		}

		return true
	})
}
