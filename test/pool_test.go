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
