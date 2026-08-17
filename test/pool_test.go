package conecta

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shengyanli1982/conecta"
	wkq "github.com/shengyanli1982/workqueue/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testCallback struct {
	t *testing.T
}

func (c *testCallback) OnPingSuccess(data any) {
	assert.Equal(c.t, "success", data.(string))
	fmt.Println(">>>> OnPingSuccess")
}
func (c *testCallback) OnPingFailure(data any) {
	assert.Equal(c.t, "fail", data.(string))
	fmt.Println(">>>> OnPingFailure")
}
func (c *testCallback) OnClose(data any, err error) {
	fmt.Println(">>>> OnClose", data.(string), err)
}

func testCallbackPingFunc(data any, c int) bool {
	fmt.Println("# testCallbackPingFunc", data.(string), c)
	return data.(string) == "success"
}

func testCallbackCloseFunc(data any) error {
	fmt.Println("# testCallbackCloseFunc", data.(string))
	return nil
}

func testNewFunc() (any, error) {
	fmt.Println("# testNewFunc")
	return "success", nil
}

func TestPool_Put(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	_ = p.Put("item1")
	_ = p.Put("item2")
	_ = p.Put("item3")

	assert.Equal(t, 3, p.Len())
}

func TestPool_Get(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	_ = p.Put("item1")

	assert.Equal(t, 1, p.Len())

	data, err := p.Get()

	assert.Nil(t, err)
	assert.Equal(t, "item1", data.(string))

	assert.Equal(t, 0, p.Len())

	_, err = p.Get()

	assert.NotNil(t, err)
	// 空池 Get 返回裸哨兵 ErrPoolEmpty（零分配热路径，不包装底层队列错误），errors.Is 对其直接成立
	// Get on an empty pool returns the bare sentinel ErrPoolEmpty (zero-allocation hot path, no wrapping of the underlying queue error); errors.Is matches it directly
	assert.True(t, errors.Is(err, conecta.ErrPoolEmpty))
}

func TestPool_GetOrCreate(t *testing.T) {
	queue := wkq.NewQueue(nil)

	conf := conecta.NewConfig().WithNewFunc(testNewFunc)
	assert.NotNil(t, conf)

	p, err := conecta.New(queue, conf)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	data, err := p.GetOrCreate()
	assert.NotNil(t, data)
	assert.Nil(t, err)
	assert.Equal(t, "success", data.(string))
}

func TestPool_Stop(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	_ = p.Put("item1")
	_ = p.Put("item2")
	_ = p.Put("item3")

	assert.Equal(t, 3, p.Len())

	p.Stop()

	assert.Equal(t, 0, p.Len())

	data, err := p.Get()
	assert.Nil(t, data)
	assert.Equal(t, conecta.ErrQueueClosed, err)
}

func TestPool_Len(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	_ = p.Put("item1")
	_ = p.Put("item2")
	_ = p.Put("item3")

	assert.Equal(t, 3, p.Len())
}

func TestPool_Callback(t *testing.T) {
	scanInterval := 300

	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().WithCallback(&testCallback{t: t}).WithPingFunc(testCallbackPingFunc).WithCloseFunc(testCallbackCloseFunc).WithPingMaxRetries(1).WithScanInterval(scanInterval)
	assert.NotNil(t, conf)

	p, err := conecta.New(queue, conf)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	_ = p.Put("success")
	_ = p.Put("fail")

	fmt.Println("Please wait for the callback to be executed... (about 1.6 seconds)")

	time.Sleep(time.Millisecond * time.Duration(scanInterval*2+1000))
}

func TestPool_Initialize(t *testing.T) {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().WithInitialize(2)
	assert.NotNil(t, conf)

	// 未配置 newFunc 且 initialize>0 时，初始化失败并返回 newFunc 未配置错误
	// When newFunc is not configured and initialize>0, initialization fails with the "newFunc not configured" error
	p, err := conecta.New(queue, conf)
	assert.Nil(t, p)
	assert.EqualError(t, err, "newFunc not configured")
}

func TestPool_InitializeWithNewFunc(t *testing.T) {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().WithInitialize(2).WithNewFunc(testNewFunc)
	assert.NotNil(t, conf)

	p, err := conecta.New(queue, conf)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	assert.Equal(t, 2, p.Len())
}

func TestPool_PutWithParallel(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	// 使用 WaitGroup 等待所有并发 Put 完成
	// Use a WaitGroup to wait for all concurrent Puts to complete
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = p.Put("item1")
		}()
	}
	wg.Wait()

	assert.Equal(t, 100, p.Len())
}

func TestPool_GetWithParallel(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	// 使用 WaitGroup 等待所有并发 Put 完成
	// Use a WaitGroup to wait for all concurrent Puts to complete
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = p.Put("item1")
		}()
	}
	wg.Wait()

	// 使用 WaitGroup 等待所有并发 Get 完成
	// Use a WaitGroup to wait for all concurrent Gets to complete
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = p.Get()
		}()
	}
	wg.Wait()

	assert.Equal(t, 0, p.Len())
}

func TestPool_GetOrCreateWithParallel(t *testing.T) {
	queue := wkq.NewQueue(nil)
	// 使用原子计数器统计 newFunc 的调用次数
	// Use an atomic counter to track the number of newFunc invocations
	var createCount int64
	conf := conecta.NewConfig().WithNewFunc(func() (any, error) {
		atomic.AddInt64(&createCount, 1)
		return "success", nil
	})
	assert.NotNil(t, conf)

	p, err := conecta.New(queue, conf)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	// 使用 WaitGroup 等待所有并发 GetOrCreate 完成
	// Use a WaitGroup to wait for all concurrent GetOrCreate calls to complete
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = p.GetOrCreate()
		}()
	}
	wg.Wait()

	// 空池下 20 个并发 GetOrCreate 各自创建一次，创建出的元素均被借出未归还
	// 20 concurrent GetOrCreate calls on an empty pool each create once; created elements are all lent out and not returned
	assert.Equal(t, int64(20), atomic.LoadInt64(&createCount))
	assert.Equal(t, 0, p.Len())
}

func TestPool_Cleanup(t *testing.T) {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().WithCloseFunc(testCallbackCloseFunc).WithCallback(&testCallback{t: t})
	assert.NotNil(t, conf)

	p, err := conecta.New(queue, conf)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	_ = p.Put("item1")
	_ = p.Put("item2")
	_ = p.Put("item3")

	assert.Equal(t, 3, p.Len())

	p.Cleanup()

	assert.Equal(t, 0, p.Len())
}

// TestPool_Put_NilItem 测试放入空值的情况
func TestPool_Put_NilItem(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	err = p.Put(nil)
	// Put(nil) 返回 ErrValueIsNil，且不入队
	// Put(nil) returns ErrValueIsNil and nothing is enqueued
	assert.Equal(t, conecta.ErrValueIsNil, err)
	assert.Equal(t, 0, p.Len())
}

// TestPool_Get_EmptyPool 测试从空池中获取元素
func TestPool_Get_EmptyPool(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	item, err := p.Get()
	assert.Error(t, err)
	// 空池 Get 返回裸哨兵 ErrPoolEmpty（零分配热路径，不包装底层队列错误），errors.Is 对其直接成立
	// Get on an empty pool returns the bare sentinel ErrPoolEmpty (zero-allocation hot path, no wrapping of the underlying queue error); errors.Is matches it directly
	assert.True(t, errors.Is(err, conecta.ErrPoolEmpty))
	assert.Nil(t, item)
}

// TestPool_GetOrCreate_ErrorCase 测试创建新元素失败的情况
func TestPool_GetOrCreate_ErrorCase(t *testing.T) {
	queue := wkq.NewQueue(nil)
	expectedErr := errors.New("creation failed")

	conf := conecta.NewConfig().WithNewFunc(func() (any, error) {
		return nil, expectedErr
	})

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	item, err := p.GetOrCreate()
	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, item)
}

// TestPool_Put_AfterStop 测试在停止后放入元素
func TestPool_Put_AfterStop(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	require.NoError(t, err)
	require.NotNil(t, p)

	p.Stop()
	err = p.Put("test")
	assert.Error(t, err)
	assert.Equal(t, conecta.ErrQueueClosed, err)
}

// TestPool_Initialize_ZeroSize 测试初始化大小为0的情况
func TestPool_Initialize_ZeroSize(t *testing.T) {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().WithInitialize(0)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	assert.Equal(t, 0, p.Len())
}

// TestPool_Initialize_NegativeSize 测试初始化大小为负数的情况
func TestPool_Initialize_NegativeSize(t *testing.T) {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().WithInitialize(-1)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	assert.Equal(t, 0, p.Len())
}

// TestPool_Callback_NilCallback 测试回调函数为nil的情况
func TestPool_Callback_NilCallback(t *testing.T) {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().
		WithPingFunc(testCallbackPingFunc).
		WithCloseFunc(testCallbackCloseFunc).
		WithPingMaxRetries(1).
		WithScanInterval(300)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	err = p.Put("success")
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 300)
}

// TestPool_ConcurrentOperations 测试并发操作的正确性
func TestPool_ConcurrentOperations(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	const (
		numProducers     = 5
		numConsumers     = 5
		itemsPerProducer = 100
	)

	// 等待所有 goroutine 完成的 WaitGroup
	// WaitGroup for waiting all goroutines to complete
	var wg sync.WaitGroup
	wg.Add(numProducers + numConsumers)

	// 生产者专用 WaitGroup：精确同步生产完成时刻，替代固定 sleep 的竞态窗口
	// Dedicated WaitGroup for producers: synchronize the exact moment production
	// completes, replacing the racy fixed sleep
	var producerWG sync.WaitGroup
	producerWG.Add(numProducers)

	// 启动生产者
	// Start the producers
	for i := range numProducers {
		go func(producerID int) {
			defer wg.Done()
			defer producerWG.Done()
			for j := range itemsPerProducer {
				item := fmt.Sprintf("producer_%d_item_%d", producerID, j)
				_ = p.Put(item)
			}
		}(i)
	}

	// 用于记录消费的元素数量
	// Counter for the number of consumed elements
	var consumedCount int32

	// 启动消费者
	// Start the consumers
	for range numConsumers {
		go func() {
			defer wg.Done()
			for {
				_, err := p.Get()
				if err != nil {
					// 池已关闭，退出 goroutine 以避免泄漏
					// Pool is closed, exit the goroutine to avoid leaking
					if errors.Is(err, conecta.ErrQueueClosed) {
						return
					}
					time.Sleep(time.Millisecond)
					continue
				}
				atomic.AddInt32(&consumedCount, 1)
			}
		}()
	}

	// 等待生产者全部完成（精确同步，无固定 sleep 竞态窗口）
	// Wait for all producers to complete (exact synchronization, no fixed-sleep race window)
	producerWG.Wait()

	// 生产者完成后，所有元素最终必然被消费殆尽；用 Eventually 轮询等待该不变式成立
	// After producers finish, every element must eventually be consumed; poll for
	// this invariant with Eventually
	totalProduced := numProducers * itemsPerProducer
	assert.Eventually(t, func() bool {
		return int(atomic.LoadInt32(&consumedCount)) == totalProduced
	}, 5*time.Second, 10*time.Millisecond,
		"Consumed count should reach total produced (%d)", totalProduced)

	// 消费完毕后队列必然为空：总生产量全部出队后不再有新增
	// The queue must be empty once everything is consumed: no new items after
	// the total produced count has been fully dequeued
	assert.Equal(t, 0, p.Len())

	// 停止池，确保清理
	// Stop the pool to ensure cleanup
	p.Stop()
	assert.Equal(t, 0, p.Len())

	// 等待所有 goroutine 退出，确认无泄漏（race 超时时会暴露残留 goroutine）
	// Wait for all goroutines to exit, confirming no leak (leftover goroutines surface as race timeouts)
	wg.Wait()
}

func TestPool_GetOrCreate_AfterStop(t *testing.T) {
	queue := wkq.NewQueue(nil)
	var createCount int64
	conf := conecta.NewConfig().WithNewFunc(func() (any, error) {
		atomic.AddInt64(&createCount, 1)
		return "new-item", nil
	})

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)

	p.Stop()

	item, err := p.GetOrCreate()
	assert.Error(t, err)
	// Stop 后 Get 统一返回 ErrQueueClosed，GetOrCreate 不会创建新元素
	// After Stop, Get uniformly returns ErrQueueClosed and GetOrCreate does not create new elements
	assert.Equal(t, conecta.ErrQueueClosed, err)
	assert.Nil(t, item)
	assert.Equal(t, int64(0), atomic.LoadInt64(&createCount), "newFunc should not be called after Stop()")
}

// TestPool_Maintain_HealthyConnection 测试健康连接的维护
func TestPool_Maintain_HealthyConnection(t *testing.T) {
	queue := wkq.NewQueue(nil)
	var pingCount int64
	var closeCount int64

	conf := conecta.NewConfig().
		WithPingFunc(func(data any, retryCount int) bool {
			atomic.AddInt64(&pingCount, 1)
			return true // 返回 true 表示连接健康
		}).
		WithCloseFunc(func(data any) error {
			atomic.AddInt64(&closeCount, 1)
			return nil
		}).
		WithScanInterval(300)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	// 添加一个测试连接
	err = p.Put("test-connection")
	require.NoError(t, err)

	// 验证连接被 ping 至少 5 次但没有被关闭；等待完全由 Eventually 承担
	// （5s 窗口覆盖首个 300ms+ 扫描周期，无需固定 sleep），同时消除计时器粒度抖动
	// Verify the connection is pinged at least 5 times but not closed; the wait is
	// carried entirely by Eventually (the 5s window covers the first 300ms+ scan
	// cycle, no fixed sleep needed), which also absorbs timer granularity jitter
	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&pingCount) >= 5
	}, 5*time.Second, 50*time.Millisecond, "Ping should be called at least 5 times for healthy connection")
	assert.Equal(t, int64(0), atomic.LoadInt64(&closeCount), "Close should not be called for healthy connection")
	assert.Equal(t, 1, p.Len(), "Connection should remain in pool")
}

// TestPool_Maintain_UnhealthyConnection 测试不健康连接的维护
func TestPool_Maintain_UnhealthyConnection(t *testing.T) {
	queue := wkq.NewQueue(nil)
	var pingCount int64
	var closeCount int64

	conf := conecta.NewConfig().
		WithPingFunc(func(data any, retryCount int) bool {
			atomic.AddInt64(&pingCount, 1)
			return false // 返回 false 表示连接不健康
		}).
		WithCloseFunc(func(data any) error {
			atomic.AddInt64(&closeCount, 1)
			return nil
		}).
		WithScanInterval(300)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	// 添加一个测试连接
	err = p.Put("test-connection")
	require.NoError(t, err)

	// 验证连接被 ping 至少 3 次后被关闭；等待完全由 Eventually 承担
	// （5s 窗口覆盖首个 300ms+ 扫描周期，无需固定 sleep）。closeCount 与第 3 次
	// ping 存在先后顺序，须一并等待以避免微秒级竞态
	// Verify the connection is pinged at least 3 times then closed; the wait is
	// carried entirely by Eventually (the 5s window covers the first 300ms+ scan
	// cycle, no fixed sleep needed). closeCount follows the 3rd ping and must be
	// awaited together to avoid microsecond-level races
	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&pingCount) >= 3 && atomic.LoadInt64(&closeCount) >= 1
	}, 5*time.Second, 50*time.Millisecond, "Ping should be called at least 3 times and close once for unhealthy connection")
	assert.Equal(t, int64(1), atomic.LoadInt64(&closeCount), "Close should be called once for unhealthy connection")
	assert.Equal(t, 1, p.Len(), "Connection should remain in pool")
}

// TestPool_Maintain_RetryMechanism 测试重试机制
func TestPool_Maintain_RetryMechanism(t *testing.T) {
	queue := wkq.NewQueue(nil)
	var pingAttempts int64

	conf := conecta.NewConfig().
		WithPingFunc(func(data any, retryCount int) bool {
			current := atomic.AddInt64(&pingAttempts, 1)
			return current >= 3 // 第三次尝试时返回成功
		}).
		WithCloseFunc(func(data any) error {
			return nil
		}).
		WithPingMaxRetries(3).
		WithScanInterval(300)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	// 添加一个测试连接
	err = p.Put("test-connection")
	require.NoError(t, err)

	// 验证重试机制；等待完全由 Eventually 承担（5s 窗口覆盖首个 300ms+ 扫描
	// 周期，无需固定 sleep），同时消除计时器粒度抖动
	// Verify the retry mechanism; the wait is carried entirely by Eventually
	// (the 5s window covers the first 300ms+ scan cycle, no fixed sleep needed),
	// which also absorbs timer granularity jitter
	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&pingAttempts) >= 3
	}, 5*time.Second, 50*time.Millisecond, "Ping should be attempted at least 3 times")
	assert.Equal(t, 1, p.Len(), "Connection should remain in pool after successful retry")
}

// TestPool_Maintain_MultipleConnections 测试多个连接的维护
func TestPool_Maintain_MultipleConnections(t *testing.T) {
	queue := wkq.NewQueue(nil)
	var pingCount int64
	var closeCount int64

	conf := conecta.NewConfig().
		WithPingFunc(func(data any, retryCount int) bool {
			atomic.AddInt64(&pingCount, 1)
			return true
		}).
		WithCloseFunc(func(data any) error {
			atomic.AddInt64(&closeCount, 1)
			return nil
		}).
		WithScanInterval(300)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	// 添加测试连接
	err = p.Put("test-connection-1")
	require.NoError(t, err)
	err = p.Put("test-connection-2")
	require.NoError(t, err)

	// 验证两个连接各自被 ping 至少 5 次但没有被关闭；等待完全由 Eventually
	// 承担（5s 窗口覆盖首个 300ms+ 扫描周期，无需固定 sleep），同时消除计时器粒度抖动
	// Verify both connections are pinged at least 5 times each but not closed; the
	// wait is carried entirely by Eventually (the 5s window covers the first 300ms+
	// scan cycle, no fixed sleep needed), which also absorbs timer granularity jitter
	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&pingCount) >= 10
	}, 5*time.Second, 50*time.Millisecond, "Ping should be called at least 10 times for two healthy connections")
	assert.Equal(t, int64(0), atomic.LoadInt64(&closeCount), "Close should not be called for healthy connections")
	assert.Equal(t, 2, p.Len(), "Connections should remain in pool")
}

// TestPool_MaxSize_Default 测试默认最大容量锚点：连续 Put 1024 个成功，第 1025 个返回 ErrPoolFull
// TestPool_MaxSize_Default tests the default capacity anchor: 1024 Puts succeed and the 1025th returns ErrPoolFull
func TestPool_MaxSize_Default(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	// 默认容量为 1024：前 1024 次 Put 全部成功（使用轻量 int 值）
	// The default capacity is 1024: all of the first 1024 Puts succeed (using lightweight int values)
	for i := range 1024 {
		require.NoError(t, p.Put(i))
	}

	// 第 1025 次 Put 触发容量上限，返回预分配哨兵 ErrPoolFull
	// The 1025th Put hits the capacity limit and returns the pre-allocated sentinel ErrPoolFull
	err = p.Put(0)
	assert.True(t, errors.Is(err, conecta.ErrPoolFull))
	assert.Equal(t, 1024, p.Len())
}

// TestPool_MaxSize_WithMaxSize 测试 WithMaxSize 配置生效：容量满后 Put 返回 ErrPoolFull
// TestPool_MaxSize_WithMaxSize tests the WithMaxSize setting takes effect: Put returns ErrPoolFull when full
func TestPool_MaxSize_WithMaxSize(t *testing.T) {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().WithMaxSize(2)
	assert.NotNil(t, conf)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	require.NoError(t, p.Put("item1"))
	require.NoError(t, p.Put("item2"))

	// 容量已满：第 3 次 Put 返回 ErrPoolFull，队列长度保持为 2
	// Capacity is full: the 3rd Put returns ErrPoolFull and the queue length stays at 2
	err = p.Put("item3")
	assert.True(t, errors.Is(err, conecta.ErrPoolFull))
	assert.Equal(t, 2, p.Len())
}

// TestPool_MaxSize_RecoveryAfterGet 测试容量恢复：借出元素释放容量后 Put 重新成功
// TestPool_MaxSize_RecoveryAfterGet tests capacity recovery: after a Get frees capacity, Put succeeds again
func TestPool_MaxSize_RecoveryAfterGet(t *testing.T) {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().WithMaxSize(2)
	assert.NotNil(t, conf)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	require.NoError(t, p.Put("item1"))
	require.NoError(t, p.Put("item2"))
	assert.True(t, errors.Is(p.Put("item3"), conecta.ErrPoolFull))

	// Get 借出 1 个元素，释放 1 单位容量
	// Get borrows one element and frees one unit of capacity
	data, err := p.Get()
	require.NoError(t, err)
	assert.Equal(t, "item1", data)

	// 容量恢复：Put 重新成功，池回到满容量状态
	// Capacity recovered: Put succeeds again and the pool returns to full capacity
	require.NoError(t, p.Put("item3"))
	assert.Equal(t, 2, p.Len())
}

// TestPool_MaxSize_TombstoneOccupiesCapacity 测试 tombstone 占用容量：被销毁未回收的元素继续占容量直至 Get 回收
// TestPool_MaxSize_TombstoneOccupiesCapacity tests tombstones occupy capacity: destroyed but unreclaimed elements keep occupying capacity until Get reclaims them
func TestPool_MaxSize_TombstoneOccupiesCapacity(t *testing.T) {
	queue := wkq.NewQueue(nil)
	var closeCount int64

	conf := conecta.NewConfig().
		WithMaxSize(2).
		WithInitialize(2).
		WithScanInterval(300).
		WithPingMaxRetries(1).
		WithNewFunc(func() (any, error) { return "conn", nil }).
		WithPingFunc(func(any, int) bool { return false }).
		WithCloseFunc(func(any) error {
			atomic.AddInt64(&closeCount, 1)
			return nil
		})
	assert.NotNil(t, conf)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	// 等待 supervise 销毁两个元素（恒 false 的 pingFunc + 单次重试上限）
	// Wait for supervise to destroy both elements (always-false pingFunc + retry limit of 1)
	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&closeCount) == 2
	}, 5*time.Second, 50*time.Millisecond, "Both elements should be destroyed")

	// tombstone 仍留在队列中占用容量
	// Tombstones remain in the queue and still occupy capacity
	assert.Equal(t, 2, p.Len())
	assert.True(t, errors.Is(p.Put("intruder"), conecta.ErrPoolFull))

	// 单次 Get 回收两个 tombstone 后返回 ErrPoolEmpty
	// A single Get reclaims both tombstones and then returns ErrPoolEmpty
	_, err = p.Get()
	assert.True(t, errors.Is(err, conecta.ErrPoolEmpty))

	// 容量已释放：Put 重新成功
	// Capacity has been released: Put succeeds again
	require.NoError(t, p.Put("fresh"))
	assert.Equal(t, 1, p.Len())
}

// TestPool_MaxSize_Fallback 测试归一化回落：maxSize<=0 时回落为默认容量 1024
// TestPool_MaxSize_Fallback tests normalization fallback: maxSize <= 0 falls back to the default capacity 1024
func TestPool_MaxSize_Fallback(t *testing.T) {
	for _, maxSize := range []int{0, -1} {
		t.Run(fmt.Sprintf("maxSize=%d", maxSize), func(t *testing.T) {
			queue := wkq.NewQueue(nil)
			p, err := conecta.New(queue, conecta.NewConfig().WithMaxSize(maxSize))
			require.NoError(t, err)
			require.NotNil(t, p)
			defer p.Stop()

			// 行为与默认一致：1024 次成功，第 1025 次返回 ErrPoolFull
			// Behaves like the default: 1024 successes, the 1025th returns ErrPoolFull
			for i := range 1024 {
				require.NoError(t, p.Put(i))
			}
			assert.True(t, errors.Is(p.Put(0), conecta.ErrPoolFull))
		})
	}
}

// TestPool_MaxSize_InitializeClamped 测试归一化顺序：initialize 钳制发生在 maxSize 回落之后
// TestPool_MaxSize_InitializeClamped tests normalization order: the initialize clamp happens after the maxSize fallback
func TestPool_MaxSize_InitializeClamped(t *testing.T) {
	newFunc := func() (any, error) { return "conn", nil }

	t.Run("initialize clamped to maxSize", func(t *testing.T) {
		queue := wkq.NewQueue(nil)
		conf := conecta.NewConfig().WithMaxSize(3).WithInitialize(5).WithNewFunc(newFunc)
		assert.NotNil(t, conf)

		p, err := conecta.New(queue, conf)
		require.NoError(t, err)
		require.NotNil(t, p)
		defer p.Stop()

		// initialize(5) 被钳制到 maxSize(3)
		// initialize(5) is clamped to maxSize(3)
		assert.Equal(t, 3, p.Len())
	})

	t.Run("maxSize fallback before clamp", func(t *testing.T) {
		queue := wkq.NewQueue(nil)
		conf := conecta.NewConfig().WithMaxSize(0).WithInitialize(5).WithNewFunc(newFunc)
		assert.NotNil(t, conf)

		p, err := conecta.New(queue, conf)
		require.NoError(t, err)
		require.NotNil(t, p)
		defer p.Stop()

		// maxSize(0) 先回落为 1024，initialize(5) 保持不变
		// maxSize(0) falls back to 1024 first, so initialize(5) is kept as-is
		assert.Equal(t, 5, p.Len())
	})
}

// TestPool_MaxSize_NilBeatsFull 测试错误优先级：Put(nil) 返回 ErrValueIsNil 而非 ErrPoolFull
// TestPool_MaxSize_NilBeatsFull tests error priority: Put(nil) returns ErrValueIsNil instead of ErrPoolFull
func TestPool_MaxSize_NilBeatsFull(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, conecta.NewConfig().WithMaxSize(1))
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	// 先填满池
	// Fill the pool first
	require.NoError(t, p.Put("item1"))

	// nil 检查优先于容量检查
	// The nil check takes priority over the capacity check
	err = p.Put(nil)
	assert.Equal(t, conecta.ErrValueIsNil, err)
}

// TestPool_MaxSize_StopBeatsFull 测试错误优先级：满池 Stop 后 Put 返回 ErrQueueClosed 而非 ErrPoolFull
// TestPool_MaxSize_StopBeatsFull tests error priority: after Stop on a full pool, Put returns ErrQueueClosed instead of ErrPoolFull
func TestPool_MaxSize_StopBeatsFull(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, conecta.NewConfig().WithMaxSize(1))
	require.NoError(t, err)
	require.NotNil(t, p)

	// 先填满池，再停止
	// Fill the pool first, then stop it
	require.NoError(t, p.Put("item1"))
	p.Stop()

	// 已停止检查优先于容量检查
	// The stopped check takes priority over the capacity check
	err = p.Put("item2")
	assert.Equal(t, conecta.ErrQueueClosed, err)
	assert.False(t, errors.Is(err, conecta.ErrPoolFull))
}

// TestPool_MaxSize_GetOrCreateUnlimited 测试 GetOrCreate 的按需创建不受 maxSize 约束
// TestPool_MaxSize_GetOrCreateUnlimited tests that GetOrCreate's on-demand creation is not constrained by maxSize
func TestPool_MaxSize_GetOrCreateUnlimited(t *testing.T) {
	queue := wkq.NewQueue(nil)
	var createCount int64

	conf := conecta.NewConfig().
		WithMaxSize(1).
		WithInitialize(1).
		WithNewFunc(func() (any, error) {
			atomic.AddInt64(&createCount, 1)
			return "created", nil
		})
	assert.NotNil(t, conf)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	// 初始化阶段 newFunc 已被调用一次（创建唯一的初始化元素）
	// newFunc has already been called once during initialization (creating the single initialized element)
	assert.Equal(t, int64(1), atomic.LoadInt64(&createCount))

	// 首次 GetOrCreate 借出已初始化元素，不再调用 newFunc
	// The first GetOrCreate borrows the initialized element without calling newFunc
	data, err := p.GetOrCreate()
	require.NoError(t, err)
	assert.Equal(t, "created", data)
	assert.Equal(t, int64(1), atomic.LoadInt64(&createCount))
	assert.Equal(t, 0, p.Len())

	// 池空时 GetOrCreate 按需创建成功，不受 maxSize=1 约束
	// On an empty pool, GetOrCreate creates on demand successfully, unconstrained by maxSize=1
	data, err = p.GetOrCreate()
	require.NoError(t, err)
	assert.Equal(t, "created", data)
	assert.Equal(t, int64(2), atomic.LoadInt64(&createCount))
	assert.Equal(t, 0, p.Len())
}

// TestPool_MaxSize_ConcurrentHardLimit 测试并发硬上限：CAS 预留语义保证成功数恰好等于 maxSize
// TestPool_MaxSize_ConcurrentHardLimit tests the concurrent hard limit: CAS reservation guarantees the success count equals exactly maxSize
func TestPool_MaxSize_ConcurrentHardLimit(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, conecta.NewConfig().WithMaxSize(50))
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	// 8 个 goroutine 各 Put 20 次，共 160 次并发 Put
	// 8 goroutines each Put 20 times, 160 concurrent Puts in total
	var wg sync.WaitGroup
	var success, full, other int64
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				err := p.Put("conn")
				if err == nil {
					atomic.AddInt64(&success, 1)
				} else if errors.Is(err, conecta.ErrPoolFull) {
					atomic.AddInt64(&full, 1)
				} else {
					atomic.AddInt64(&other, 1)
				}
			}
		}()
	}
	wg.Wait()

	// 成功数恰好为 50，其余 110 次全部为 ErrPoolFull，无其他错误
	// Exactly 50 succeed; the other 110 all return ErrPoolFull with no other errors
	assert.Equal(t, int64(50), atomic.LoadInt64(&success))
	assert.Equal(t, int64(110), atomic.LoadInt64(&full))
	assert.Equal(t, int64(0), atomic.LoadInt64(&other))
	assert.Equal(t, 50, p.Len())
}

// TestPool_MaxSize_FullNoOwnershipTransfer 测试满池 Put 失败不转移所有权：被拒 value 不会被池关闭
// TestPool_MaxSize_FullNoOwnershipTransfer tests a full-pool Put failure does not transfer ownership: the rejected value is not closed by the pool
func TestPool_MaxSize_FullNoOwnershipTransfer(t *testing.T) {
	queue := wkq.NewQueue(nil)
	// closeFunc 记录器：记录所有被池关闭的 value
	// closeFunc recorder: records every value closed by the pool
	var mu sync.Mutex
	var closed []string

	conf := conecta.NewConfig().WithMaxSize(1).WithCloseFunc(func(v any) error {
		mu.Lock()
		closed = append(closed, v.(string))
		mu.Unlock()
		return nil
	})
	assert.NotNil(t, conf)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)

	require.NoError(t, p.Put("resident"))
	// 池满：Put 失败，所有权不转移
	// Pool is full: Put fails and ownership is not transferred
	assert.True(t, errors.Is(p.Put("intruder"), conecta.ErrPoolFull))

	p.Stop()

	// Stop 后只有成功入队的 resident 被关闭；intruder 未被池关闭（仍由调用者负责）
	// After Stop only the successfully enqueued resident is closed; the intruder is not closed by the pool (still the caller's responsibility)
	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, closed, "resident")
	assert.NotContains(t, closed, "intruder")
}
