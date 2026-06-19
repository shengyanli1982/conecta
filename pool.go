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
	go p.monitor()
	return p, nil
}

// initialize creates and adds initial elements to the pool
// 初始化连接池，创建并添加初始元素
func (p *Pool) initialize() error {
	// Create and enqueue initial elements one by one
	// 逐个创建并入队初始元素
	for i := 0; i < p.config.initialize; i++ {
		value, err := p.config.newFunc()
		if err != nil {
			// newFunc 失败：关闭本次创建的 value（从未入队），并清理已入队的元素
			// newFunc failed: close this value (never enqueued) and drain already enqueued elements
			if value != nil {
				_ = p.config.closeFunc(value)
			}
			p.drainAndClose()
			return err
		}

		// Wrap value in element and enqueue
		// 将 value 包装到 element 并入队
		element := p.elementpool.Get()
		element.SetData(value)
		if err := p.queue.Put(element); err != nil {
			// queue.Put 失败：归还 wrapper 并关闭本次创建的 value，并清理已入队的元素
			// queue.Put failed: return wrapper, close this value (never enqueued), and drain already enqueued elements
			p.elementpool.Put(element)
			if value != nil {
				_ = p.config.closeFunc(value)
			}
			p.drainAndClose()
			return err
		}
	}

	// All elements successfully enqueued; caller (Stop/Cleanup) is responsible for cleanup
	// 所有元素已成功入队；调用方（Stop/Cleanup）负责后续清理
	return nil
}

// drainAndClose drains all queued elements, closes their values, and returns wrappers to the element pool.
// 排空队列中所有已入队的元素，关闭它们的 value，并将 wrapper 归还给 elementpool。
func (p *Pool) drainAndClose() {
	for {
		elem, err := p.queue.Get()
		if err != nil {
			break
		}
		p.queue.Done(elem)
		wrapper := elem.(*pool.Element)
		if v := wrapper.GetData(); v != nil {
			_ = p.config.closeFunc(v)
		}
		p.elementpool.Put(wrapper)
	}
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
	// Phase 1: Close all values but do NOT return wrappers to elementpool
	// 第一阶段：关闭所有 value，但不归还 wrapper 到 elementpool
	p.queue.Range(func(data any) bool {
		element := data.(*pool.Element)
		if value := element.GetData(); value != nil {
			err := p.config.closeFunc(value)
			p.config.callback.OnClose(value, err)
			element.Reset()
		}
		return true
	})
	// Phase 2: Drain queue and return all wrappers to elementpool
	// 第二阶段：排空队列，回收所有 wrapper 到 elementpool
	for {
		element, err := p.queue.Get()
		if err != nil {
			break
		}
		p.queue.Done(element)
		p.elementpool.Put(element.(*pool.Element))
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

	for {
		// Check if pool context is cancelled
		// 检查连接池上下文是否已取消
		if p.ctx.Err() != nil {
			return nil, p.ctx.Err()
		}

		// Try to get element from queue
		// 尝试从队列中获取元素
		element, err := p.queue.Get()
		if err != nil {
			return nil, err
		}
		p.queue.Done(element)

		wrapper := element.(*pool.Element)
		wrapper.Lock()
		value := wrapper.GetDataNoLock()
		if value == nil {
			wrapper.SetValueNoLock(0)
			wrapper.Unlock()
			p.elementpool.PutRaw(wrapper)
			continue
		}
		wrapper.SetValueNoLock(0)
		wrapper.Unlock()

		p.elementpool.PutRaw(wrapper)
		return value, nil
	}
}

// GetOrCreate gets an element from the pool or creates a new one if none is available
// 从连接池获取元素，如果没有可用元素则创建新的
func (p *Pool) GetOrCreate() (any, error) {
	value, err := p.Get()
	if err == nil {
		return value, nil
	}

	// 池已关闭（ctx 已取消或队列已关闭）：不创建新连接
	// Pool is closed (ctx cancelled or queue shutdown): do not create new connections
	if p.ctx.Err() != nil || errors.Is(err, ErrorQueueClosed) {
		if p.ctx.Err() != nil {
			return nil, p.ctx.Err()
		}
		return nil, err
	}

	return p.config.newFunc()
}

// Put adds a new element to the pool
// 向连接池中添加新元素
func (p *Pool) Put(data any) error {
	if p.queue.IsClosed() {
		return ErrorQueueClosed
	}
	element := p.elementpool.Get()
	element.SetDataNoLock(data)
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

// monitor runs the pool monitoring loop
// 运行连接池监控循环
func (p *Pool) monitor() {
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
			p.supervise()
		}
	}
}

// supervise checks and maintains pool elements status
// 检查并维护连接池元素状态
func (p *Pool) supervise() {
	p.queue.Range(func(data any) bool {
		element := data.(*pool.Element)
		element.Lock()
		defer element.Unlock()

		value := element.GetDataNoLock()
		if value == nil {
			return true
		}

		retryCount := int(element.GetValueNoLock())
		if retryCount < 0 {
			return true
		}

		if ok := p.config.pingFunc(value, retryCount); ok {
			element.SetValueNoLock(0)
			p.config.callback.OnPingSuccess(value)
			return true
		}

		retryCount++
		if retryCount >= p.config.maxRetries {
			err := p.config.closeFunc(value)
			p.config.callback.OnClose(value, err)
			element.SetDataNoLock(nil)
			element.SetValueNoLock(-1)
		} else {
			element.SetValueNoLock(int64(retryCount))
			p.config.callback.OnPingFailure(value)
		}

		return true
	})
}
