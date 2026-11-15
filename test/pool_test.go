package conecta

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shengyanli1982/conecta"
	ipool "github.com/shengyanli1982/conecta/internal/pool"
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

// TestPool_Supervise_RealTickPingsAllElements 真实 supervise 链路端到端冒烟：最小
// scanInterval（300ms）下，10 个元素应在 ~2×scanInterval+裕量内被真实 tick 全部
// ping 到。由 BenchmarkSupervise_10_Real 转化而来（P2-3）：原基准读数由定时器周期
// 主导（288ms/op ≈ scanInterval，supervise 真实遍历+ping 派发占比 <0.001%），对
// supervise CPU 成本零敏感，属伪性能信号；其真实价值——tick → queue.Range →
// pingFunc 派发 → 计数同步的端到端可用性——由本冒烟测试承接并带有回归牙齿。
// TestPool_Supervise_RealTickPingsAllElements is an end-to-end smoke test of the
// real supervise chain: with the minimum scanInterval (300ms), all 10 elements
// must be pinged by real ticks within ~2×scanInterval + margin. Converted from
// BenchmarkSupervise_10_Real (P2-3): the old benchmark's reading was dominated
// by the timer period (288ms/op ≈ scanInterval; the real traversal + ping
// dispatch was <0.001% of it) and totally insensitive to supervise's CPU cost —
// a pseudo performance signal. Its genuine value (the end-to-end tick →
// queue.Range → pingFunc dispatch → counter sync chain) is carried over here
// with regression teeth.
func TestPool_Supervise_RealTickPingsAllElements(t *testing.T) {
	queue := wkq.NewQueue(nil)
	var pingCount int64

	conf := conecta.NewConfig().
		WithPingFunc(func(_ any, _ int) bool {
			atomic.AddInt64(&pingCount, 1)
			return true // 健康连接：只计数不触发销毁 / healthy connection: count only, never destroyed
		}).
		WithCloseFunc(func(_ any) error { return nil }).
		WithScanInterval(conecta.MinScanInterval) // =300ms，normalizeConfig 允许的最小值 / the minimum allowed by normalizeConfig

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	const numElements = 10
	for range numElements {
		require.NoError(t, p.Put("conn"))
	}

	// 首个 tick 最晚 300ms 触发，一次 Range 覆盖全部 10 个元素；1s 窗口
	//（~2×scanInterval + 400ms 裕量）吸收定时器粒度抖动。防真空臂：阈值恰为元素
	// 总数 10（计数型 pingFunc 累计）而非恒真条件——supervise 停摆或漏扫任一元素
	// 都会超时失败。
	// The first tick fires no later than 300ms and one Range pass covers all 10
	// elements; the 1s window (~2×scanInterval + 400ms margin) absorbs timer
	// granularity jitter. Anti-vacuity: the threshold is exactly the element
	// count 10 (cumulative via the counting pingFunc), not a trivially true
	// condition — a stalled supervise or any missed element fails on timeout.
	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&pingCount) >= numElements
	}, time.Second, 50*time.Millisecond,
		"all %d elements should be pinged by real supervise ticks", numElements)

	// 健康元素全部存活：supervise 不应销毁任何一个
	// All healthy elements survive: supervise must not destroy any of them
	assert.Equal(t, numElements, p.Len())
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

// ----------------------------------------------------------------------------
// 对象年龄管理（MaxLifetime + Jitter）与内部计数插桩
// Object lifetime management (MaxLifetime + Jitter) and internal counter instrumentation
//
// 本节用例只使用标准库 testing（不引入断言库）；时间相关用例采用
// “短寿命 + MinScanInterval 扫描 + 轮询等待”的组合，不引入 clock 抽象。
// The cases in this section use only the stdlib testing package (no assertion library);
// time-related cases combine a short lifetime with the MinScanInterval scan and polling
// instead of introducing a clock abstraction.
// ----------------------------------------------------------------------------

// countingCallback 统计三个生命周期回调的触发次数，用于断言销毁路径恰好触发一次 OnClose
// countingCallback counts the three lifecycle callbacks, used to assert a destroy path fires OnClose exactly once
type countingCallback struct {
	pingSuccess atomic.Int64
	pingFailure atomic.Int64
	close       atomic.Int64
}

// OnPingSuccess 实现 Callback 接口并累计 ping 成功次数
// OnPingSuccess implements the Callback interface and counts ping successes
func (c *countingCallback) OnPingSuccess(any) { c.pingSuccess.Add(1) }

// OnPingFailure 实现 Callback 接口并累计 ping 失败次数
// OnPingFailure implements the Callback interface and counts ping failures
func (c *countingCallback) OnPingFailure(any) { c.pingFailure.Add(1) }

// OnClose 实现 Callback 接口并累计销毁次数
// OnClose implements the Callback interface and counts destructions
func (c *countingCallback) OnClose(any, error) { c.close.Add(1) }

// closedRecorder 记录池关闭过的 value，用于断言“关闭了哪一个、各关闭了几次”
// closedRecorder records the values closed by the pool to assert which value was closed and how often
type closedRecorder struct {
	mu     sync.Mutex
	values []any
}

// add 记录一次关闭
// add records one close
func (r *closedRecorder) add(v any) {
	r.mu.Lock()
	r.values = append(r.values, v)
	r.mu.Unlock()
}

// total 返回累计关闭次数
// total returns the cumulative number of closes
func (r *closedRecorder) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.values)
}

// count 返回指定 value 被关闭的次数
// count returns how many times the given value was closed
func (r *closedRecorder) count(v any) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, item := range r.values {
		if item == v {
			n++
		}
	}
	return n
}

// waitUntil 轮询等待条件成立，超时则以 Fatal 结束（标准库版 Eventually）
// waitUntil polls until the condition holds and fails the test on timeout (a stdlib Eventually)
func waitUntil(t *testing.T, timeout, tick time.Duration, cond func() bool, format string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf(format, args...)
		}
		time.Sleep(tick)
	}
}

// mustNewPool 创建池并在失败时终止测试
// mustNewPool creates a pool and fails the test on error
func mustNewPool(t *testing.T, conf *conecta.Config) *conecta.Pool {
	t.Helper()
	p, err := conecta.New(wkq.NewQueue(nil), conf)
	if err != nil {
		t.Fatalf("conecta.New() error = %v", err)
	}
	if p == nil {
		t.Fatal("conecta.New() returned a nil pool")
	}
	return p
}

// TestPool_MaxLifetime_SuperviseEvictsExpired 验证后台扫描按年龄淘汰到期对象：
// 年龄判定先于 pingFunc 执行，销毁复用既有 tombstone 路径（closeFunc 与 OnClose 各一次，tombstone 仍占容量）
// TestPool_MaxLifetime_SuperviseEvictsExpired verifies the background scan evicts objects by age:
// the age check runs before pingFunc and destruction reuses the existing tombstone path
// (closeFunc and OnClose fire once each; the tombstone keeps occupying capacity)
func TestPool_MaxLifetime_SuperviseEvictsExpired(t *testing.T) {
	t.Run("element enqueued by Put", func(t *testing.T) {
		var pingCount atomic.Int64
		closed := &closedRecorder{}
		cb := &countingCallback{}

		conf := conecta.NewConfig().
			WithMaxLifetime(50 * time.Millisecond).
			WithScanInterval(conecta.MinScanInterval).
			WithPingFunc(func(any, int) bool {
				pingCount.Add(1)
				return true // 始终健康：只有年龄判定能淘汰它 / always healthy, so only the age check can evict it
			}).
			WithCloseFunc(func(v any) error {
				closed.add(v)
				return nil
			}).
			WithCallback(cb)

		p := mustNewPool(t, conf)
		defer p.Stop()

		if err := p.Put("conn"); err != nil {
			t.Fatalf("Put() error = %v", err)
		}

		// 首个扫描周期（300ms）远晚于 50ms 寿命：对象必然在这一轮被年龄判定淘汰
		// The first scan (300ms) runs long after the 50ms lifetime, so the object must be evicted in that pass
		waitUntil(t, 5*time.Second, 20*time.Millisecond,
			func() bool { return closed.total() == 1 && cb.close.Load() == 1 },
			"expired element was not evicted by supervise: closeFunc=%d OnClose=%d", closed.total(), cb.close.Load())

		if got := pingCount.Load(); got != 0 {
			t.Errorf("pingFunc calls = %d, want 0: the age check must run before pingFunc", got)
		}
		if got := closed.count("conn"); got != 1 {
			t.Errorf("closeFunc calls for the expired element = %d, want exactly 1", got)
		}
		// 与 ping 失败销毁同语义：tombstone 留在队列中继续占用容量，直到 Get 回收
		// Same semantics as a ping-failure destroy: the tombstone stays in the queue occupying capacity until a Get reclaims it
		if got := p.Len(); got != 1 {
			t.Errorf("Len() after eviction = %d, want 1 (the tombstone still occupies capacity)", got)
		}
		if _, err := p.Get(); !errors.Is(err, conecta.ErrPoolEmpty) {
			t.Errorf("Get() after eviction error = %v, want ErrPoolEmpty", err)
		}
		if got := p.Len(); got != 0 {
			t.Errorf("Len() after the tombstone reclaim = %d, want 0", got)
		}
	})

	t.Run("elements created by initialize", func(t *testing.T) {
		var pingCount, closeCount atomic.Int64

		// 寿命 1s + 扫描间隔 300ms：前三轮扫描（300/600/900ms）对象都未到期，必须照常 ping；
		// 第四轮（约 1.2s）到期后被年龄判定淘汰。双向断言可区分“真实 expireAt”与“从未打时间戳”
		// （后者 expireAt=0，在启用年龄淘汰的池中等同于立即到期，会在第一轮扫描就被销毁）
		// Lifetime 1s with a 300ms scan interval: during the first three scans (300/600/900ms) the element has not
		// expired and must be pinged as usual; the fourth scan (~1.2s) evicts it by age. Asserting both directions
		// distinguishes a real expireAt from a missing stamp (which leaves expireAt=0 — equivalent to instantly
		// expired once age eviction is on — and would be destroyed in the very first scan)
		conf := conecta.NewConfig().
			WithInitialize(1).
			WithNewFunc(func() (any, error) { return "precreated", nil }).
			WithMaxLifetime(time.Second).
			WithScanInterval(conecta.MinScanInterval).
			WithPingFunc(func(any, int) bool {
				pingCount.Add(1)
				return true
			}).
			WithCloseFunc(func(v any) error {
				closeCount.Add(1)
				return nil
			})

		p := mustNewPool(t, conf)
		defer p.Stop()

		if got := p.Len(); got != 1 {
			t.Fatalf("Len() after initialize = %d, want 1", got)
		}

		waitUntil(t, 5*time.Second, 20*time.Millisecond,
			func() bool { return pingCount.Load() >= 2 },
			"pre-created element was never pinged: pingCount=%d closeCount=%d", pingCount.Load(), closeCount.Load())

		// 到期前不得被淘汰：证明 initialize 路径确实按 maxLifetime 打了未来的 expireAt
		// It must not be evicted before expiry: proof that the initialize path stamped a future expireAt from maxLifetime
		if got := closeCount.Load(); got != 0 {
			t.Errorf("closeFunc calls before the lifetime elapsed = %d, want 0", got)
		}

		waitUntil(t, 5*time.Second, 20*time.Millisecond,
			func() bool { return closeCount.Load() == 1 },
			"pre-created element was not evicted after its lifetime elapsed: closeCount=%d", closeCount.Load())

		if got := pingCount.Load(); got < 2 {
			t.Errorf("pingFunc calls = %d, want >= 2 (the element should have survived several scans)", got)
		}
	})
}

// TestPool_MaxLifetime_GetDestroysExpiredBeforeLending 验证借出路径的年龄判定：
// 到期对象被就地销毁并跳过，继续返回队列中下一个未到期对象；单次 Get 的销毁次数受 PingMaxRetries 上限约束
// TestPool_MaxLifetime_GetDestroysExpiredBeforeLending verifies the age check on the borrow path:
// an expired object is destroyed in place and skipped, the next unexpired one is returned, and the number of
// destructions inside a single Get is capped by PingMaxRetries
func TestPool_MaxLifetime_GetDestroysExpiredBeforeLending(t *testing.T) {
	t.Run("expired element is destroyed and the next one is lent", func(t *testing.T) {
		closed := &closedRecorder{}
		cb := &countingCallback{}

		conf := conecta.NewConfig().
			WithMaxLifetime(30 * time.Millisecond).
			WithScanInterval(10000). // 抑制后台扫描，单独考察借出路径 / suppress the background scan to isolate the borrow path
			WithCloseFunc(func(v any) error {
				closed.add(v)
				return nil
			}).
			WithCallback(cb)

		p := mustNewPool(t, conf)
		defer p.Stop()

		if err := p.Put("stale"); err != nil {
			t.Fatalf("Put(stale) error = %v", err)
		}
		// 等 stale 到期后再放入 fresh；队列 FIFO，stale 必然先被取出
		// Let stale expire before enqueuing fresh; the queue is FIFO so stale is dequeued first
		time.Sleep(60 * time.Millisecond)
		if err := p.Put("fresh"); err != nil {
			t.Fatalf("Put(fresh) error = %v", err)
		}

		value, err := p.Get()
		if err != nil {
			t.Fatalf("Get() error = %v, want the fresh element", err)
		}
		if value != "fresh" {
			t.Errorf("Get() = %v, want %q: the expired element must be skipped", value, "fresh")
		}
		if got := closed.count("stale"); got != 1 {
			t.Errorf("closeFunc calls for the expired element = %d, want exactly 1", got)
		}
		if got := closed.count("fresh"); got != 0 {
			t.Errorf("closeFunc calls for the lent element = %d, want 0", got)
		}
		if got := cb.close.Load(); got != 1 {
			t.Errorf("OnClose calls = %d, want exactly 1", got)
		}
		// stale 已销毁、fresh 已借出：两者都已离队
		// stale is destroyed and fresh is lent out: both have left the queue
		if got := p.Len(); got != 0 {
			t.Errorf("Len() = %d, want 0", got)
		}
		if _, err := p.Get(); !errors.Is(err, conecta.ErrPoolEmpty) {
			t.Errorf("second Get() error = %v, want ErrPoolEmpty", err)
		}
	})

	t.Run("expired destructions per Get are capped by PingMaxRetries", func(t *testing.T) {
		closed := &closedRecorder{}

		conf := conecta.NewConfig().
			WithMaxLifetime(20 * time.Millisecond).
			WithPingMaxRetries(1).
			WithScanInterval(10000).
			WithCloseFunc(func(v any) error {
				closed.add(v)
				return nil
			})

		p := mustNewPool(t, conf)
		defer p.Stop()

		for _, v := range []string{"a", "b", "c"} {
			if err := p.Put(v); err != nil {
				t.Fatalf("Put(%q) error = %v", v, err)
			}
		}
		time.Sleep(60 * time.Millisecond) // 三个对象全部到期 / all three are now expired

		// maxRetries=1：单次 Get 只销毁 1 个到期对象即返回 ErrPoolEmpty，剩余对象留给后续调用
		// maxRetries=1: a single Get destroys one expired object and then returns ErrPoolEmpty, leaving the rest to later calls
		for want := 1; want <= 3; want++ {
			if _, err := p.Get(); !errors.Is(err, conecta.ErrPoolEmpty) {
				t.Fatalf("Get() #%d error = %v, want ErrPoolEmpty", want, err)
			}
			if got := closed.total(); got != want {
				t.Errorf("destroyed count after Get() #%d = %d, want %d", want, got, want)
			}
			if got, wantLen := p.Len(), 3-want; got != wantLen {
				t.Errorf("Len() after Get() #%d = %d, want %d", want, got, wantLen)
			}
		}

		// 队列已排空：后续 Get 不再销毁任何对象
		// The queue is drained: later Gets destroy nothing more
		if _, err := p.Get(); !errors.Is(err, conecta.ErrPoolEmpty) {
			t.Errorf("Get() on the drained pool error = %v, want ErrPoolEmpty", err)
		}
		if got := closed.total(); got != 3 {
			t.Errorf("destroyed count = %d, want 3", got)
		}
	})
}

// TestPool_MaxLifetime_Normalization 验证配置归一化：负值归零（关闭年龄淘汰）、抖动钳制到 maxLifetime/2
// TestPool_MaxLifetime_Normalization verifies config normalization: negative values fall back to zero
// (age eviction disabled) and the jitter is clamped to maxLifetime/2
func TestPool_MaxLifetime_Normalization(t *testing.T) {
	t.Run("negative lifetime disables age eviction", func(t *testing.T) {
		var pingCount, closeCount atomic.Int64

		conf := conecta.NewConfig().
			WithMaxLifetime(-5 * time.Second).
			WithMaxLifetimeJitter(-time.Second).
			WithScanInterval(conecta.MinScanInterval).
			WithPingFunc(func(any, int) bool {
				pingCount.Add(1)
				return true
			}).
			WithCloseFunc(func(any) error {
				closeCount.Add(1)
				return nil
			})

		p := mustNewPool(t, conf)
		defer p.Stop()

		if err := p.Put("conn"); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		// 覆盖两个以上扫描周期：负值归一化为 0 后年龄淘汰整体关闭，健康检查照常运行
		// Covers more than two scan periods: after the negative value normalizes to 0, age eviction is off
		// while health supervision keeps running
		time.Sleep(700 * time.Millisecond)

		if got := closeCount.Load(); got != 0 {
			t.Errorf("closeFunc calls = %d, want 0 (a negative lifetime must normalize to disabled)", got)
		}
		if got := pingCount.Load(); got < 2 {
			t.Errorf("pingFunc calls = %d, want >= 2", got)
		}
		if value, err := p.Get(); err != nil || value != "conn" {
			t.Errorf("Get() = (%v, %v), want (conn, nil)", value, err)
		}
	})

	t.Run("negative jitter behaves as no jitter", func(t *testing.T) {
		closed := &closedRecorder{}

		conf := conecta.NewConfig().
			WithMaxLifetime(100 * time.Millisecond).
			WithMaxLifetimeJitter(-time.Hour).
			WithScanInterval(10000).
			WithCloseFunc(func(v any) error {
				closed.add(v)
				return nil
			})

		p := mustNewPool(t, conf)
		defer p.Stop()

		if err := p.Put("conn"); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		time.Sleep(400 * time.Millisecond) // 远超 100ms 寿命 / far beyond the 100ms lifetime

		if _, err := p.Get(); !errors.Is(err, conecta.ErrPoolEmpty) {
			t.Errorf("Get() error = %v, want ErrPoolEmpty (the element must have expired)", err)
		}
		if got := closed.count("conn"); got != 1 {
			t.Errorf("closeFunc calls = %d, want exactly 1", got)
		}
	})

	t.Run("jitter is clamped to half of the lifetime", func(t *testing.T) {
		// jitter=1h 远超 maxLifetime/2=500ms；归一化后实际寿命必然落在 [1s, 1.5s] 区间内
		// jitter=1h far exceeds maxLifetime/2=500ms; after normalization the effective lifetime must fall in [1s, 1.5s]
		closed := &closedRecorder{}

		conf := conecta.NewConfig().
			WithMaxLifetime(time.Second).
			WithMaxLifetimeJitter(time.Hour).
			WithScanInterval(10000).
			WithCloseFunc(func(v any) error {
				closed.add(v)
				return nil
			})

		p := mustNewPool(t, conf)
		defer p.Stop()

		if err := p.Put("lower"); err != nil {
			t.Fatalf("Put(lower) error = %v", err)
		}
		if err := p.Put("upper"); err != nil {
			t.Fatalf("Put(upper) error = %v", err)
		}

		// 下界：200ms 时对象必然未到期（实际寿命 >= maxLifetime = 1s）
		// Lower bound: at 200ms the object cannot have expired (effective lifetime >= maxLifetime = 1s)
		time.Sleep(200 * time.Millisecond)
		if value, err := p.Get(); err != nil || value != "lower" {
			t.Fatalf("Get() at 200ms = (%v, %v), want (lower, nil): the lifetime must be at least maxLifetime", value, err)
		}

		// 上界：约 2.2s 时对象必然已到期（实际寿命 <= 1.5s）；若抖动未被钳制，此处对象仍会存活
		// Upper bound: at ~2.2s the object must have expired (effective lifetime <= 1.5s); with an unclamped
		// jitter it would still be alive here
		time.Sleep(2 * time.Second)
		if _, err := p.Get(); !errors.Is(err, conecta.ErrPoolEmpty) {
			t.Errorf("Get() at ~2.2s error = %v, want ErrPoolEmpty: the jitter must be clamped to maxLifetime/2", err)
		}
		if got := closed.count("upper"); got != 1 {
			t.Errorf("closeFunc calls for the expired element = %d, want exactly 1", got)
		}
	})
}

// TestElementPool_ReuseClearsTimestamps 验证 wrapper 回收时时间戳被清零——sync.Pool 复用场景的最高频缺陷点：
// Reset（ElementPool.Put）与 PutRaw（Pool.Get 的借出与 tombstone 回收）两条路径都不得残留
// createdAt/expireAt/idleSince，否则陈旧到期时刻会让下一个使用者一入池就被判定为过期。
// 断言直接针对被回收的 wrapper：清零发生在归还之前，因此不依赖 sync.Pool 是否还回同一个对象
// （-race 下 Put→Get 的对象同一性没有保证，用它断言会引入抖动）。
// TestElementPool_ReuseClearsTimestamps verifies the timestamps are cleared when a wrapper is recycled — the most
// frequent defect point of sync.Pool reuse: neither Reset (ElementPool.Put) nor PutRaw (the borrow and tombstone
// reclaim paths of Pool.Get) may leave createdAt/expireAt/idleSince behind, otherwise a stale expiry would make the
// next user look expired the moment it enters the pool. Assertions target the recycled wrapper itself: clearing
// happens before the hand-back, so they do not depend on sync.Pool returning the same object (object identity
// across Put→Get is not guaranteed under -race and would make the test flaky).
func TestElementPool_ReuseClearsTimestamps(t *testing.T) {
	recyclers := []struct {
		name    string
		recycle func(*ipool.ElementPool, *ipool.Element)
	}{
		{"Put runs Reset", func(ep *ipool.ElementPool, e *ipool.Element) { ep.Put(e) }},
		{"PutRaw skips Reset", func(ep *ipool.ElementPool, e *ipool.Element) { ep.PutRaw(e) }},
	}

	// assertClean 断言 wrapper 不残留任何字段值
	// assertClean asserts the wrapper retains no field value at all
	assertClean := func(t *testing.T, e *ipool.Element, stage string) {
		t.Helper()
		if got := e.GetCreatedAt(); got != 0 {
			t.Errorf("%s: stale createdAt leaked through reuse: %d", stage, got)
		}
		if got := e.GetExpireAt(); got != 0 {
			t.Errorf("%s: stale expireAt leaked through reuse: %d", stage, got)
		}
		if got := e.GetIdleSince(); got != 0 {
			t.Errorf("%s: stale idleSince leaked through reuse: %d", stage, got)
		}
		if got := e.GetData(); got != nil {
			t.Errorf("%s: stale data leaked through reuse: %v", stage, got)
		}
		if got := e.GetRetries(); got != 0 {
			t.Errorf("%s: stale retries leaked through reuse: %d", stage, got)
		}
	}

	for _, rec := range recyclers {
		t.Run(rec.name, func(t *testing.T) {
			ep := ipool.NewElementPool()
			e := ep.Get()

			// 模拟一次完整入池：数据、重试计数与三个时间戳全部置为非零值
			// Simulate a full enqueue: data, retry count and all three timestamps set to non-zero values
			e.SetData("conn")
			e.SetRetries(2)
			e.SetCreatedAt(1_700_000_000_000_000_000)
			e.SetExpireAt(1_700_000_060_000_000_000)
			e.SetIdleSince(1_700_000_030_000_000_000)

			// PutRaw 的契约要求调用方已在元素锁内清理 data/retries，此处按真实调用点（Pool.Get）的方式清理
			// PutRaw's contract requires the caller to have cleared data/retries under the element lock; mirror the real call site (Pool.Get)
			e.Lock()
			e.SetDataNoLock(nil)
			e.SetRetriesNoLock(0)
			e.Unlock()
			rec.recycle(ep, e)

			assertClean(t, e, "recycled wrapper")
			// 端到端：从复用池再取出的 wrapper 同样不得带任何陈旧字段
			// End to end: the next wrapper handed out by the reuse pool must be clean as well
			assertClean(t, ep.Get(), "wrapper from the next Get")
		})
	}
}

// TestPool_Stats_AccountingProxies 以可观测代理量钉住内部 stats 插桩的完整性。
// 本批次不暴露 Stats()，内部计数器无法从外部测试模块读取；但每个销毁分支都与“一次 closeFunc +
// 一次 OnClose”一一对应，且分类计数与总计由单一 recordDestroyed 入口成对自增，因此恒等式
// totalDestroyed == destroyedByPing + destroyedByLifetime + destroyedByStop 在结构上成立。
// 下列用例断言的正是这些一一对应关系，以及借出/归还/池空/池满四类可观测次数。
// TestPool_Stats_AccountingProxies pins the completeness of the internal stats instrumentation through
// observable proxies. This batch does not expose Stats(), so the internal counters cannot be read from the
// external test module; however every destroy branch maps one-to-one onto "one closeFunc + one OnClose", and the
// category counter plus the total are incremented in pairs through the single recordDestroyed entry point, so the
// identity totalDestroyed == destroyedByPing + destroyedByLifetime + destroyedByStop holds structurally.
// The cases below assert exactly those one-to-one relations plus the observable borrow/return/empty/full counts.
func TestPool_Stats_AccountingProxies(t *testing.T) {
	t.Run("ping and lifetime destroys each fire closeFunc and OnClose once", func(t *testing.T) {
		closed := &closedRecorder{}
		cb := &countingCallback{}

		conf := conecta.NewConfig().
			WithMaxLifetime(50 * time.Millisecond).
			WithPingMaxRetries(1).
			WithScanInterval(conecta.MinScanInterval).
			WithPingFunc(func(v any, _ int) bool { return v != "bad" }).
			WithCloseFunc(func(v any) error {
				closed.add(v)
				return nil
			}).
			WithCallback(cb)

		p := mustNewPool(t, conf)
		defer p.Stop()

		if err := p.Put("bad"); err != nil { // 由 ping 失败销毁 / destroyed by ping failure
			t.Fatalf("Put(bad) error = %v", err)
		}
		if err := p.Put("aging"); err != nil { // 由年龄判定销毁 / destroyed by the age check
			t.Fatalf("Put(aging) error = %v", err)
		}

		waitUntil(t, 5*time.Second, 20*time.Millisecond,
			func() bool { return closed.total() == 2 && cb.close.Load() == 2 },
			"both elements should be destroyed exactly once: closeFunc=%d OnClose=%d", closed.total(), cb.close.Load())

		for _, v := range []string{"bad", "aging"} {
			if got := closed.count(v); got != 1 {
				t.Errorf("closeFunc calls for %q = %d, want exactly 1", v, got)
			}
		}
	})

	t.Run("Stop destroys every idle element once", func(t *testing.T) {
		closed := &closedRecorder{}
		cb := &countingCallback{}

		conf := conecta.NewConfig().
			WithScanInterval(10000).
			WithCloseFunc(func(v any) error {
				closed.add(v)
				return nil
			}).
			WithCallback(cb)

		p := mustNewPool(t, conf)
		for _, v := range []string{"x", "y", "z"} {
			if err := p.Put(v); err != nil {
				t.Fatalf("Put(%q) error = %v", v, err)
			}
		}

		p.Stop()

		if got := closed.total(); got != 3 {
			t.Errorf("closeFunc calls after Stop = %d, want 3", got)
		}
		if got := cb.close.Load(); got != 3 {
			t.Errorf("OnClose calls after Stop = %d, want 3", got)
		}
		for _, v := range []string{"x", "y", "z"} {
			if got := closed.count(v); got != 1 {
				t.Errorf("closeFunc calls for %q = %d, want exactly 1", v, got)
			}
		}
	})

	t.Run("borrow return empty and full counts are observable", func(t *testing.T) {
		p := mustNewPool(t, conecta.NewConfig().WithMaxSize(2).WithScanInterval(10000))
		defer p.Stop()

		var putOK, putFull, getOK, getEmpty int
		for range 3 {
			err := p.Put("conn")
			switch {
			case errors.Is(err, conecta.ErrPoolFull):
				putFull++
			case err == nil:
				putOK++
			default:
				t.Fatalf("Put() unexpected error = %v", err)
			}
		}
		for range 3 {
			_, err := p.Get()
			switch {
			case errors.Is(err, conecta.ErrPoolEmpty):
				getEmpty++
			case err != nil:
				t.Fatalf("Get() unexpected error = %v", err)
			default:
				getOK++
			}
		}

		// 这四组可观测数量正是 totalReturned/fullPutCount/totalBorrowed/emptyCount 的期望值
		// These four observable quantities are exactly the expected values of totalReturned/fullPutCount/totalBorrowed/emptyCount
		if putOK != 2 || putFull != 1 || getOK != 2 || getEmpty != 1 {
			t.Errorf("counts = (putOK %d, putFull %d, getOK %d, getEmpty %d), want (2, 1, 2, 1)",
				putOK, putFull, getOK, getEmpty)
		}
	})

	t.Run("concurrent lifetime eviction and Cleanup close each object exactly once", func(t *testing.T) {
		const objects = 20
		closed := &closedRecorder{}
		cb := &countingCallback{}

		conf := conecta.NewConfig().
			WithMaxLifetime(350 * time.Millisecond).
			WithScanInterval(conecta.MinScanInterval).
			WithPingFunc(func(any, int) bool { return true }).
			WithCloseFunc(func(v any) error {
				time.Sleep(2 * time.Millisecond) // 拉长销毁窗口，制造与 Cleanup 的真实交叠 / widen the destroy window to overlap with Cleanup
				closed.add(v)
				return nil
			}).
			WithCallback(cb)

		p := mustNewPool(t, conf)
		defer p.Stop()

		for i := range objects {
			if err := p.Put(fmt.Sprintf("obj-%d", i)); err != nil {
				t.Fatalf("Put() error = %v", err)
			}
		}

		// 第 2 轮扫描（约 600ms）开始按年龄淘汰，此时调用 Cleanup 与 closeAllIdle 交叠。
		// 断言与交叠顺序无关：元素锁内的认领语义保证每个对象恰好被关闭一次
		// The second scan (~600ms) starts evicting by age; Cleanup is called right then to overlap with closeAllIdle.
		// The assertion is independent of the interleaving: the claim under the element lock guarantees each object
		// is closed exactly once
		time.Sleep(620 * time.Millisecond)
		p.Cleanup()
		// Stop 等待 supervise goroutine 退出，确保在途的 closeFunc 全部记录完毕后再断言（消除时序抖动）
		// Stop waits for the supervise goroutine to exit so every in-flight closeFunc has been recorded before
		// asserting (removing timing jitter)
		p.Stop()

		if got := closed.total(); got != objects {
			t.Errorf("closeFunc calls = %d, want %d (every object closed exactly once)", got, objects)
		}
		if got := cb.close.Load(); got != int64(objects) {
			t.Errorf("OnClose calls = %d, want %d", got, objects)
		}
		for i := range objects {
			v := fmt.Sprintf("obj-%d", i)
			if got := closed.count(v); got != 1 {
				t.Errorf("closeFunc calls for %q = %d, want exactly 1", v, got)
			}
		}
	})
}

// TestPool_MaxLifetime_DisabledByDefault 回归验证：默认配置（maxLifetime=0）下行为与引入年龄淘汰之前完全一致，
// 且仅设置抖动不会开启年龄淘汰
// TestPool_MaxLifetime_DisabledByDefault is the regression guard: with the default config (maxLifetime=0) behavior is
// identical to before age eviction existed, and setting the jitter alone does not enable it
func TestPool_MaxLifetime_DisabledByDefault(t *testing.T) {
	// 导出的默认常量必须为 0（关闭态），且可直接作为 time.Duration 传入 Builder
	// The exported defaults must be 0 (disabled) and must be directly usable as time.Duration in the builders
	var (
		defaultLifetime time.Duration = conecta.DefaultMaxLifetime
		defaultJitter   time.Duration = conecta.DefaultMaxLifetimeJitter
	)
	if defaultLifetime != 0 || defaultJitter != 0 {
		t.Errorf("defaults = (lifetime %v, jitter %v), want (0s, 0s)", defaultLifetime, defaultJitter)
	}

	cases := []struct {
		name string
		conf *conecta.Config
	}{
		{"default config", conecta.NewConfig()},
		// 仅设置抖动不得开启年龄淘汰：maxLifetime=0 时抖动被归一化为 0
		// Setting the jitter alone must not enable age eviction: with maxLifetime=0 the jitter normalizes to 0
		{"jitter without lifetime", conecta.NewConfig().WithMaxLifetimeJitter(time.Hour)},
		{"explicit zero lifetime", conecta.NewConfig().
			WithMaxLifetime(conecta.DefaultMaxLifetime).
			WithMaxLifetimeJitter(time.Hour)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var pingCount, closeCount atomic.Int64

			conf := tc.conf.
				WithScanInterval(conecta.MinScanInterval).
				WithPingFunc(func(any, int) bool {
					pingCount.Add(1)
					return true
				}).
				WithCloseFunc(func(any) error {
					closeCount.Add(1)
					return nil
				})

			p := mustNewPool(t, conf)
			defer p.Stop()

			if err := p.Put("conn"); err != nil {
				t.Fatalf("Put() error = %v", err)
			}
			// 覆盖两个以上扫描周期：默认配置下对象不会因年龄被淘汰，健康检查与容量语义保持不变
			// Covers more than two scan periods: with the default config nothing is evicted by age, and health
			// supervision plus capacity semantics stay unchanged
			time.Sleep(700 * time.Millisecond)

			if got := closeCount.Load(); got != 0 {
				t.Errorf("closeFunc calls = %d, want 0 (age eviction must stay disabled by default)", got)
			}
			if got := pingCount.Load(); got < 2 {
				t.Errorf("pingFunc calls = %d, want >= 2", got)
			}
			if got := p.Len(); got != 1 {
				t.Errorf("Len() = %d, want 1", got)
			}
			if value, err := p.Get(); err != nil || value != "conn" {
				t.Errorf("Get() = (%v, %v), want (conn, nil)", value, err)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// 阻塞式获取（GetWithContext）：ctx 超时/取消传播 + waiter 机制 + WithAcquireTimeout
// Blocking acquisition (GetWithContext): ctx timeout/cancel propagation + waiter mechanism + WithAcquireTimeout
//
// 本节用例只使用标准库 testing（复用上方年龄淘汰小节的 waitUntil/mustNewPool 辅助函数）；
// 所有挂起窗口由 gatedCreator 的创建门控确定性构造，不依赖 sleep 竞态命中。
// waitCount/waitTimeoutCount/totalWaitDurationNs 是未导出 stats 字段（本批次不暴露 Stats()，
// 直接断言按 B1 A4 裁决延至 B3），此处以其行为投影钉住：返回错误身份、耗时下界与唤醒因果序。
// The cases in this section use only the stdlib testing package (reusing the waitUntil/mustNewPool
// helpers from the age-eviction section above); every suspension window is constructed deterministically
// through gatedCreator's creation gating instead of racing against sleeps.
// waitCount/waitTimeoutCount/totalWaitDurationNs are unexported stats fields (this batch does not expose
// Stats(); per B1's A4 ruling direct assertions are deferred to B3), so the tests pin their behavioral
// projections: error identity, elapsed-time lower bounds and the causal order of wake-ups.
// ----------------------------------------------------------------------------

// errGatedCreate 是门控失败模式的预分配错误（断言用 == 直判证明错误原样透传、无包装）
// errGatedCreate is the pre-allocated error of the gated fail mode (asserted with == to prove the error
// is passed through unwrapped)
var errGatedCreate = errors.New("gated creation failure")

// gatedPartialValue 是门控失败模式随错误透传的 value（验证 value 所有权随错误移交调用方的契约）
// gatedPartialValue is the value that travels with the error in the gated fail mode (verifying the
// contract that value ownership passes to the caller together with the error)
const gatedPartialValue = "partial-value"

// gatedCreator 是门控创建函数：arm/armFail 之后下一次 newFunc 调用阻塞在创建窗口内（此时它持有
// 唯一的在途 CAS 预留，inFlight 被钉在 maxSize），并通过 entered 通知测试；测试向 release
// 发信号后调用返回（arm=成功返回 "created"；armFail=返回 (gatedPartialValue, errGatedCreate)，
// 用于钉住失败路径的预留释放与错误透传）。用于确定性地构造“在途满 → waiter 挂起”窗口
// gatedCreator is a gated creation function: once armed via arm/armFail, the next newFunc call blocks
// inside the creation window (holding the only in-flight CAS reservation, pinning inFlight at maxSize)
// and announces itself via entered; the call returns once the test signals release (arm = success
// returning "created"; armFail = returning (gatedPartialValue, errGatedCreate), pinning the failure
// path's reservation release and error passthrough). It deterministically constructs the
// "in-flight quota full → waiter suspends" window
type gatedCreator struct {
	entered chan struct{}
	release chan struct{}
	armed   atomic.Bool
	fail    atomic.Bool
	calls   atomic.Int64
}

// newGatedCreator 创建门控创建器（entered 带缓冲，创建方可先行通知）
// newGatedCreator creates a gated creator (entered is buffered so the creator can announce ahead)
func newGatedCreator() *gatedCreator {
	return &gatedCreator{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

// newFunc 实现门控创建：仅当已 arm 时阻塞（arm 被本次调用消费），其余调用立即返回
// newFunc implements the gated creation: it blocks only while armed (the arm is consumed by this
// call); every other call returns immediately
func (g *gatedCreator) newFunc() (any, error) {
	g.calls.Add(1)
	if g.armed.CompareAndSwap(true, false) {
		g.entered <- struct{}{}
		<-g.release
		if g.fail.Load() {
			return gatedPartialValue, errGatedCreate
		}
	}
	return "created", nil
}

// arm 装填成功门控：下一次 newFunc 调用将阻塞在创建窗口内，放行后成功返回
// arm loads the success gate: the next newFunc call will block inside the creation window and succeed
// once released
func (g *gatedCreator) arm() {
	g.fail.Store(false)
	g.armed.Store(true)
}

// armFail 装填失败门控：下一次 newFunc 调用将阻塞在创建窗口内，放行后返回
// (gatedPartialValue, errGatedCreate)——创建失败路径（预留必须释放并唤醒 waiter）
// armFail loads the failure gate: the next newFunc call will block inside the creation window and, once
// released, return (gatedPartialValue, errGatedCreate) — the creation-failure path (the reservation must
// be released and a waiter woken)
func (g *gatedCreator) armFail() {
	g.fail.Store(true)
	g.armed.Store(true)
}

// awaitPinned 等待被门控的创建调用确认进入（此刻在途预留已生效，inFlight 钉在 maxSize）
// awaitPinned waits until the gated creation call confirms entry (the in-flight reservation is active,
// pinning inFlight at maxSize)
func (g *gatedCreator) awaitPinned() { <-g.entered }

// open 释放被门控的创建调用（会合式，接收方就绪前阻塞）
// open releases the gated creation call (rendezvous; blocks until the receiver is ready)
func (g *gatedCreator) open() { g.release <- struct{}{} }

// tryOpen 非阻塞地尝试释放门控（测试清理兜底：无被阻塞调用时立即返回）
// tryOpen releases the gate without blocking (test-cleanup safety net: returns at once when no
// call is blocked)
func (g *gatedCreator) tryOpen() {
	select {
	case g.release <- struct{}{}:
	default:
	}
}

// acquireResult 承载一次 GetWithContext 的跨 goroutine 结果
// acquireResult carries one GetWithContext outcome across goroutines
type acquireResult struct {
	value any
	err   error
}

// startAcquire 在独立 goroutine 中调用一次 GetWithContext，结果经带缓冲通道送回
// （缓冲为 1：即使测试提前结束，goroutine 也能发送后退出，无泄漏）
// startAcquire runs one GetWithContext call in a fresh goroutine and delivers the outcome through a
// buffered channel (capacity 1: the goroutine can always send and exit even if the test ends early,
// so nothing leaks)
func startAcquire(p *conecta.Pool, ctx context.Context) <-chan acquireResult {
	ch := make(chan acquireResult, 1)
	go func() {
		v, err := p.GetWithContext(ctx)
		ch <- acquireResult{value: v, err: err}
	}()
	return ch
}

// assertSuspended 断言结果通道在宽限窗口内没有产出——即调用方确实挂起为 waiter。
// 挂起由容量预留钉死保证，与调度时序无关，窗口只用于排除“已返回”
// assertSuspended asserts the result channel yields nothing within the grace window — i.e. the caller
// really suspended as a waiter. Suspension is guaranteed by the pinned capacity reservation rather
// than by scheduling luck; the window only rules out "already returned"
func assertSuspended(t *testing.T, ch <-chan acquireResult) {
	t.Helper()
	select {
	case res := <-ch:
		t.Fatalf("GetWithContext returned early: value=%v err=%v, want suspension", res.value, res.err)
	case <-time.After(50 * time.Millisecond):
	}
}

// awaitResult 在上限时间内等待一次获取结果，超时以 Fatal 结束
// awaitResult waits for one acquisition outcome within the bound and fails the test on timeout
func awaitResult(t *testing.T, ch <-chan acquireResult, timeout time.Duration) acquireResult {
	t.Helper()
	select {
	case res := <-ch:
		return res
	case <-time.After(timeout):
		t.Fatalf("GetWithContext did not return within %v", timeout)
		return acquireResult{}
	}
}

// TestPool_GetWithContext_ImmediatePaths 验证不挂起的三条快速路径：
// 队列有对象直接借出；空池且 inFlight<maxSize 立即有界创建（创建裸值不入队——F6）；
// 入口 ctx 已 done 快速失败（D9）；池停止返回 ErrQueueClosed
// TestPool_GetWithContext_ImmediatePaths verifies the non-suspending fast paths: borrowing directly
// from a non-empty queue; immediate bounded creation on an empty pool with inFlight<maxSize (the created
// raw value is not enqueued — F6); fast failure for an already-done ctx at entry (D9); and
// ErrQueueClosed after the pool stopped
func TestPool_GetWithContext_ImmediatePaths(t *testing.T) {
	t.Run("borrows from a non-empty queue", func(t *testing.T) {
		p := mustNewPool(t, conecta.NewConfig().WithScanInterval(10000))
		defer p.Stop()

		if err := p.Put("item1"); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		value, err := p.GetWithContext(context.Background())
		if err != nil || value != "item1" {
			t.Fatalf("GetWithContext() = (%v, %v), want (item1, nil)", value, err)
		}
		if got := p.Len(); got != 0 {
			t.Errorf("Len() = %d, want 0", got)
		}
	})

	t.Run("bounded creation on an empty pool", func(t *testing.T) {
		var createCount atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(2).
			WithScanInterval(10000).
			WithNewFunc(func() (any, error) {
				createCount.Add(1)
				return "fresh", nil
			})
		p := mustNewPool(t, conf)
		defer p.Stop()

		// 空池 + inFlight(0) < maxSize(2)：同步调用直接创建返回，不进入挂起路径
		// Empty pool with inFlight(0) < maxSize(2): the synchronous call creates and returns directly,
		// never entering the suspension path
		value, err := p.GetWithContext(context.Background())
		if err != nil || value != "fresh" {
			t.Fatalf("GetWithContext() = (%v, %v), want (fresh, nil)", value, err)
		}
		if got := createCount.Load(); got != 1 {
			t.Errorf("newFunc calls = %d, want 1", got)
		}
		// F6：创建出的裸值不入队，池内计数不漂移
		// F6: the created raw value is not enqueued and the pool count does not drift
		if got := p.Len(); got != 0 {
			t.Errorf("Len() after bounded creation = %d, want 0 (created value is not enqueued)", got)
		}
		// 调用方 Put 归还时才被包装入池
		// The value is wrapped into the pool only when the caller returns it via Put
		if err := p.Put(value); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		if got := p.Len(); got != 1 {
			t.Errorf("Len() after Put = %d, want 1", got)
		}
	})

	t.Run("already-done ctx fails fast", func(t *testing.T) {
		p := mustNewPool(t, conecta.NewConfig().WithScanInterval(10000))
		defer p.Stop()

		if err := p.Put("item1"); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		// D9：入口 ctx 已 done → 立即返回 ctx.Err()，即使池内有可用对象
		// D9: an already-done ctx at entry returns ctx.Err() immediately, even with an object available
		value, err := p.GetWithContext(ctx)
		if !errors.Is(err, context.Canceled) || value != nil {
			t.Fatalf("GetWithContext(done ctx) = (%v, %v), want (nil, context.Canceled)", value, err)
		}
		if got := p.Len(); got != 1 {
			t.Errorf("Len() = %d, want 1 (fast-fail must not borrow)", got)
		}
	})

	t.Run("stopped pool returns ErrQueueClosed", func(t *testing.T) {
		p := mustNewPool(t, conecta.NewConfig().WithScanInterval(10000))
		p.Stop()

		value, err := p.GetWithContext(context.Background())
		if err != conecta.ErrQueueClosed || value != nil {
			t.Fatalf("GetWithContext() after Stop = (%v, %v), want (nil, ErrQueueClosed)", value, err)
		}
	})
}

// TestPool_GetWithContext_WakesOnPutReturn 验证 criterion b（P0-B 在途封顶语义）：唯一在途配额被
// 门控创建占满 → W 挂起；G1 创建成功后在途配额保持占用（成功不释放预留——旧语义在此会释放并唤醒
// W 再创建第 2 个对象，突破 maxSize=1，正是 P0-B 修复的缺陷），W 必须继续挂起；G1 通过 Put 归还 →
// wake-after-enqueue（D7）唤醒 W → W 借出的正是归还的那个对象（value 同一性断言），newFunc 全程
// 只被调用 1 次
// TestPool_GetWithContext_WakesOnPutReturn verifies criterion b (the P0-B in-flight cap semantics): the
// only in-flight slot is pinned by a gated creation → W suspends; when G1's creation succeeds the
// in-flight slot stays occupied (success never releases the reservation — the old semantics released it
// here, waking W to create a 2nd object past maxSize=1, exactly the defect P0-B fixes), so W must stay
// suspended; G1's Put return → wake-after-enqueue (D7) wakes W → W borrows precisely the returned object
// (value identity assertion), with newFunc called exactly once throughout
func TestPool_GetWithContext_WakesOnPutReturn(t *testing.T) {
	g := newGatedCreator()
	conf := conecta.NewConfig().WithMaxSize(1).WithScanInterval(10000).WithNewFunc(g.newFunc)
	p := mustNewPool(t, conf)
	defer p.Stop()
	defer g.tryOpen()

	// G1 占住唯一在途配额：门控创建阻塞在 newFunc 内，持有在途 CAS 预留（inFlight=1=maxSize）
	// G1 pins the only in-flight slot: the gated creation blocks inside newFunc holding the in-flight CAS
	// reservation (inFlight=1=maxSize)
	g.arm()
	g1 := startAcquire(p, context.Background())
	g.awaitPinned()

	// 在途配额已满：W 的 CAS 预留失败 → 注册 waiter 挂起
	// The in-flight quota is full: W's CAS reservation fails → it registers as a waiter and suspends
	w := startAcquire(p, context.Background())
	assertSuspended(t, w)

	// 放行门控：G1 创建成功返回——预留转为正式在途（inFlight 保持 1），不产生任何唤醒，
	// W 必须仍然挂起（在途封顶：绝不突破创建第 2 个对象）
	// Open the gate: G1's creation succeeds and returns — the reservation converts into a formal
	// in-flight slot (inFlight stays 1), no wake is produced, and W must still be suspended (the
	// in-flight cap: never create a 2nd object past it)
	g.open()
	resG1 := awaitResult(t, g1, 5*time.Second)
	if resG1.err != nil || resG1.value == nil {
		t.Fatalf("G1 result = (%v, %v), want an acquired object", resG1.value, resG1.err)
	}
	assertSuspended(t, w)

	// Put 归还 → wake-after-enqueue 唤醒 W → W 借出归还的对象（同一性断言：唤醒不携带元素，
	// W 重跑获取路径从队列借出的正是刚入队的 value）
	// Put return → wake-after-enqueue wakes W → W borrows the returned object (identity assertion: the
	// wake carries no element; re-running the acquire path W borrows from the queue exactly the value
	// just enqueued)
	if err := p.Put(resG1.value); err != nil {
		t.Fatalf("Put(returned) error = %v", err)
	}
	resW := awaitResult(t, w, 5*time.Second)
	if resW.err != nil || resW.value == nil {
		t.Fatalf("waiter result = (%v, %v), want an acquired object", resW.value, resW.err)
	}
	if resW.value != resG1.value {
		t.Errorf("waiter got %v, want the returned object %v (the woken waiter must borrow the returned value)", resW.value, resG1.value)
	}
	if got := p.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0 (the returned object was borrowed by the woken waiter)", got)
	}
	if got := g.calls.Load(); got != 1 {
		t.Errorf("newFunc calls = %d, want 1 (only G1 created; the in-flight cap forbids the waiter a 2nd creation)", got)
	}
}

// TestPool_GetWithContext_WakesOnCapacityRelease 验证 criterion c（P0-B 语义下的“容量释放唤醒”）：
// G1 的门控创建失败（newFunc 返回错误）→ 在途预留经 releaseSlot 释放（inFlight-1，先状态后信号）→
// 挂起的 W 被唤醒，重跑获取路径并有界创建成功。同时钉住失败路径的错误透传契约：G1 收到裸的
// errGatedCreate（== 直判，无包装）与随错误移交所有权的 value。
// （旧语义下本用例的释放源是“成功创建的预留释放 + tombstone 回收”——P0-B 后成功创建不再释放配额，
// 释放唤醒的唯一确定性构造是创建失败/销毁路径，此处取创建失败）
// TestPool_GetWithContext_WakesOnCapacityRelease verifies criterion c (the "capacity release wake" under
// P0-B semantics): G1's gated creation fails (newFunc returns an error) → the in-flight reservation is
// released through releaseSlot (inFlight-1, state before signal) → the suspended W is woken, re-runs the
// acquire path and succeeds with a bounded creation. It also pins the failure path's error-passthrough
// contract: G1 receives the bare errGatedCreate (== comparison, unwrapped) together with the value whose
// ownership travels with the error.
// (Under the old semantics this case's release source was "a successful creation's reservation release +
// tombstone reclaim" — after P0-B a successful creation never releases quota, so the only deterministic
// constructions of a release wake are the creation-failure and destruction paths; this one uses the
// creation failure)
func TestPool_GetWithContext_WakesOnCapacityRelease(t *testing.T) {
	g := newGatedCreator()
	conf := conecta.NewConfig().WithMaxSize(1).WithScanInterval(10000).WithNewFunc(g.newFunc)
	p := mustNewPool(t, conf)
	defer p.Stop()
	defer g.tryOpen()

	// G1 占住唯一在途配额：门控失败创建阻塞在 newFunc 内（inFlight=1=maxSize）
	// G1 pins the only in-flight slot: the gated failing creation blocks inside newFunc (inFlight=1=maxSize)
	g.armFail()
	g1 := startAcquire(p, context.Background())
	g.awaitPinned()

	// W：在途 CAS 预留失败 → 挂起
	// W: the in-flight CAS reservation fails → it suspends
	w := startAcquire(p, context.Background())
	assertSuspended(t, w)

	// 放行门控：G1 的 newFunc 返回错误 → 预留释放（inFlight 1→0）+ 定向唤醒 W →
	// W 重跑获取路径：tryAcquire 空 → 有界创建（未 arm，立即成功）
	// Open the gate: G1's newFunc returns an error → the reservation is released (inFlight 1→0) and W is
	// woken in a targeted fashion → W re-runs the acquire path: tryAcquire finds nothing → bounded
	// creation (not armed, succeeds immediately)
	g.open()

	// G1：错误原样透传（== 直判证明无包装），value 所有权随错误移交
	// G1: the error is passed through as-is (the == comparison proves no wrapping), and value ownership
	// travels with the error
	resG1 := awaitResult(t, g1, 5*time.Second)
	if resG1.err != errGatedCreate {
		t.Fatalf("G1 error = %v, want the bare errGatedCreate (unwrapped, == identity)", resG1.err)
	}
	if resG1.value != gatedPartialValue {
		t.Errorf("G1 value = %v, want %v (ownership travels with the error)", resG1.value, gatedPartialValue)
	}

	resW := awaitResult(t, w, 5*time.Second)
	if resW.err != nil || resW.value == nil {
		t.Fatalf("waiter result = (%v, %v), want an acquired object", resW.value, resW.err)
	}
	if got := g.calls.Load(); got != 2 {
		t.Errorf("newFunc calls = %d, want 2 (G1's failed attempt + the woken waiter's creation)", got)
	}
	// F6：创建成功的裸值不入队；失败创建的 value 已随错误移交调用方
	// F6: the successfully created raw value is not enqueued; the failed creation's value already traveled
	// to the caller with the error
	if got := p.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0", got)
	}
}

// TestPool_GetWithContext_TimeoutReturnsDeadlineExceeded 验证 criterion d：挂起等待中 ctx 超时
// → 返回裸的 context.DeadlineExceeded（D1 不引入新哨兵）。行为投影：waitCount=1（确实挂起过）、
// waitTimeoutCount=1（该错误身份唯一来源）、totalWaitDuration>0（耗时下界）
// TestPool_GetWithContext_TimeoutReturnsDeadlineExceeded verifies criterion d: a ctx timeout while
// suspended returns the bare context.DeadlineExceeded (D1 introduces no new sentinel). Behavioral
// projections: waitCount=1 (it really suspended), waitTimeoutCount=1 (the unique source of this error
// identity) and totalWaitDuration>0 (the elapsed-time lower bound)
func TestPool_GetWithContext_TimeoutReturnsDeadlineExceeded(t *testing.T) {
	g := newGatedCreator()
	conf := conecta.NewConfig().WithMaxSize(1).WithScanInterval(10000).WithNewFunc(g.newFunc)
	p := mustNewPool(t, conf)
	defer p.Stop()
	defer g.tryOpen()

	g.arm()
	g1 := startAcquire(p, context.Background())
	g.awaitPinned()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	w := startAcquire(p, ctx)
	// 超时（150ms）前的宽限窗口内确认确实挂起（waitCount=1 的行为投影）
	// Within the grace window before the 150ms timeout, confirm real suspension (the behavioral
	// projection of waitCount=1)
	assertSuspended(t, w)

	res := awaitResult(t, w, 5*time.Second)
	elapsed := time.Since(start)
	if !errors.Is(res.err, context.DeadlineExceeded) || res.value != nil {
		t.Fatalf("GetWithContext() = (%v, %v), want (nil, context.DeadlineExceeded)", res.value, res.err)
	}
	// totalWaitDuration>0 的行为投影：确实等待了不短于超时时长的时间
	// Behavioral projection of totalWaitDuration>0: it really waited no less than the timeout
	if elapsed < 150*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 150ms", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed = %v, want < 3s (the timeout must fire on schedule)", elapsed)
	}

	g.open()
	if resG1 := awaitResult(t, g1, 5*time.Second); resG1.err != nil {
		t.Errorf("G1 result err = %v, want nil", resG1.err)
	}
}

// TestPool_GetWithContext_CancelAndDeregister 验证 criterion e：ctx 取消 → 返回裸的
// context.Canceled；且注销无泄漏——取消的 waiter 从 FIFO 列表移除，下一次定向唤醒（由 Put 归还
// 触发）必须到达仍存活的 waiter（若注销泄漏，唤醒会打到已死的通道上，存活 waiter 睡死）
// TestPool_GetWithContext_CancelAndDeregister verifies criterion e: a ctx cancellation returns the
// bare context.Canceled, and deregistration never leaks — the cancelled waiter is removed from the
// FIFO list, so the next targeted wake (triggered by a Put return) must reach the surviving waiter
// (with a deregistration leak the wake would hit the dead channel and the survivor would sleep forever)
func TestPool_GetWithContext_CancelAndDeregister(t *testing.T) {
	g := newGatedCreator()
	conf := conecta.NewConfig().WithMaxSize(1).WithScanInterval(10000).WithNewFunc(g.newFunc)
	p := mustNewPool(t, conf)
	defer p.Stop()
	defer g.tryOpen()

	g.arm()
	g1 := startAcquire(p, context.Background())
	g.awaitPinned()

	// W1 挂起后被取消：返回 context.Canceled（且注销完成先于调用返回）
	// W1 suspends and is then cancelled: it returns context.Canceled (deregistration completes
	// before the call returns)
	ctx1, cancel1 := context.WithCancel(context.Background())
	w1 := startAcquire(p, ctx1)
	assertSuspended(t, w1)
	cancel1()
	res1 := awaitResult(t, w1, 5*time.Second)
	if !errors.Is(res1.err, context.Canceled) || res1.value != nil {
		t.Fatalf("cancelled waiter result = (%v, %v), want (nil, context.Canceled)", res1.value, res1.err)
	}

	// W2 在 W1 注销之后注册：下一次唤醒必须到达 W2（FIFO 头部已无死通道）
	// W2 registers after W1 deregistered: the next wake must reach W2 (no dead channel at the FIFO head)
	w2 := startAcquire(p, context.Background())
	assertSuspended(t, w2)

	// P0-B：放行门控后 G1 创建成功（预留转为正式在途，不产生唤醒）；唤醒源是随后的 Put 归还
	//（wake-after-enqueue）——若 W1 注销泄漏，唤醒会打到死通道上，W2 睡死、此处超时
	// P0-B: opening the gate lets G1's creation succeed (the reservation converts into a formal in-flight
	// slot, producing no wake); the wake source is the subsequent Put return (wake-after-enqueue) — had
	// W1's deregistration leaked, the wake would hit the dead channel and W2 would sleep forever, timing
	// out here
	g.open()
	resG1 := awaitResult(t, g1, 5*time.Second)
	if resG1.err != nil || resG1.value == nil {
		t.Fatalf("G1 result = (%v, %v), want an acquired object", resG1.value, resG1.err)
	}
	if err := p.Put(resG1.value); err != nil {
		t.Fatalf("Put(returned) error = %v", err)
	}

	res2 := awaitResult(t, w2, 2*time.Second) // 定向唤醒 FIFO 头部 = W2 / targeted wake of the FIFO head = W2
	if res2.err != nil || res2.value == nil {
		t.Fatalf("surviving waiter result = (%v, %v), want an acquired object (the cancelled waiter must have been deregistered)", res2.value, res2.err)
	}
}

// TestPool_GetWithContext_StopBroadcastsErrQueueClosed 验证 criterion f：Stop 广播唤醒全部
// 挂起 waiter，统一收到 ErrQueueClosed（D1：池停止复用现有哨兵）
// TestPool_GetWithContext_StopBroadcastsErrQueueClosed verifies criterion f: Stop broadcast-wakes
// every suspended waiter and all of them receive ErrQueueClosed (D1: the stopped pool reuses the
// existing sentinel)
func TestPool_GetWithContext_StopBroadcastsErrQueueClosed(t *testing.T) {
	g := newGatedCreator()
	conf := conecta.NewConfig().WithMaxSize(3).WithScanInterval(10000).WithNewFunc(g.newFunc)
	p := mustNewPool(t, conf)
	defer g.tryOpen()

	// 三个门控创建把在途配额钉满（inFlight=3=maxSize）
	// Three gated creations pin the in-flight quota (inFlight=3=maxSize)
	pinners := make([]<-chan acquireResult, 0, 3)
	for range 3 {
		g.arm()
		pinners = append(pinners, startAcquire(p, context.Background()))
		g.awaitPinned()
	}

	// 三个 waiter 挂起（带 10s 兜底 deadline：广播若丢失，测试失败而非永久挂死）
	// Three waiters suspend (with a 10s safety deadline: a lost broadcast fails the test instead of
	// hanging forever)
	waiters := make([]<-chan acquireResult, 0, 3)
	for range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		waiters = append(waiters, startAcquire(p, ctx))
	}
	assertSuspended(t, waiters[0])
	assertSuspended(t, waiters[1])
	assertSuspended(t, waiters[2])

	p.Stop()

	// 全部挂起 waiter 收到 ErrQueueClosed
	// Every suspended waiter receives ErrQueueClosed
	for i, w := range waiters {
		if res := awaitResult(t, w, 5*time.Second); res.err != conecta.ErrQueueClosed {
			t.Errorf("waiter #%d result = (%v, %v), want ErrQueueClosed", i, res.value, res.err)
		}
	}

	// 清理：放行三个被门控的创建（Stop 不持有 stopGate 等待 newFunc，放行不阻塞）
	// Cleanup: release the three gated creations (Stop does not hold the stopGate waiting on newFunc,
	// so releasing never blocks)
	for range 3 {
		g.open()
	}
	for i, pin := range pinners {
		if res := awaitResult(t, pin, 5*time.Second); res.err != nil && res.err != conecta.ErrQueueClosed {
			t.Errorf("pinner #%d result err = %v, want nil or ErrQueueClosed", i, res.err)
		}
	}
}

// trackedObject 是并发风暴用例的标记对象：held 标志断言同一对象绝不被两个调用方同时持有
// trackedObject is the marked object of the concurrent-storm case: the held flag asserts the same
// object is never held by two callers at once
type trackedObject struct {
	id   int64
	held atomic.Bool
}

// TestPool_GetWithContext_ConcurrentStorm 验证 criterion g：≥8 个 goroutine 的 GetWithContext
// 借还循环 + 混入短超时取消者，-race 下无死锁、无丢失唤醒，对象互斥（同一对象不被两人同时
// 持有）。waitCount ≥ 成功等待数 属内部计数器一致性，直接断言延至 B3 的公开 Stats()（A4）
// TestPool_GetWithContext_ConcurrentStorm verifies criterion g: a borrow-and-return storm of ≥8
// GetWithContext goroutines mixed with short-timeout cancellers — under -race there is no deadlock
// and no lost wake-up, and objects are mutually exclusive (never held by two callers at once).
// "waitCount ≥ successful waits" is an internal-counter invariant whose direct assertion is deferred
// to B3's public Stats() (A4)
func TestPool_GetWithContext_ConcurrentStorm(t *testing.T) {
	const (
		workers    = 8
		cancellers = 2
		loops      = 30
		maxSize    = 4
	)

	var nextID atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(maxSize).
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) {
			// 拉长有界创建的预留窗口，制造真实的容量挤兑与挂起/唤醒 churn
			// Stretch the bounded-creation reservation window to produce real capacity contention
			// and suspend/wake churn
			time.Sleep(100 * time.Microsecond)
			return &trackedObject{id: nextID.Add(1)}, nil
		})
	p := mustNewPool(t, conf)

	var (
		wg         sync.WaitGroup
		violations atomic.Int64
		acquired   atomic.Int64
		timeouts   atomic.Int64
	)

	workerCtx, workerCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer workerCancel()

	hold := func(v any) *trackedObject {
		obj := v.(*trackedObject)
		if !obj.held.CompareAndSwap(false, true) {
			violations.Add(1) // 互斥被打破：同一对象被两个调用方同时持有 / mutual exclusion broken
		}
		return obj
	}
	release := func(obj *trackedObject) {
		obj.held.Store(false)
		// 容量满时 Put 拒绝属既有契约：对象由调用方处置（此处丢弃），不得误报
		// A full-pool Put rejection is part of the existing contract: the caller disposes of the
		// object (dropped here) and it must not be reported as an error
		if err := p.Put(obj); err != nil && !errors.Is(err, conecta.ErrPoolFull) {
			t.Errorf("Put() error = %v", err)
		}
	}

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range loops {
				v, err := p.GetWithContext(workerCtx)
				if err != nil {
					t.Errorf("worker GetWithContext() error = %v, want nil", err)
					return
				}
				obj := hold(v)
				acquired.Add(1)
				// 持有 200µs：8 个 worker 争抢 4 个槽位，保证挂起/唤醒路径被真实覆盖
				// Hold for 200µs: 8 workers contend for 4 slots, guaranteeing the suspend/wake path
				// is genuinely exercised
				time.Sleep(200 * time.Microsecond)
				release(obj)
			}
		}()
	}
	for range cancellers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range loops {
				ctx, cancel := context.WithTimeout(context.Background(), 300*time.Microsecond)
				v, err := p.GetWithContext(ctx)
				cancel()
				switch {
				case err == nil:
					release(hold(v))
				case errors.Is(err, context.DeadlineExceeded):
					timeouts.Add(1) // 短超时取消者：超时是合法结局 / a legitimate outcome for short-timeout cancellers
				default:
					t.Errorf("canceller GetWithContext() error = %v, want nil or DeadlineExceeded", err)
					return
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("concurrent storm did not complete within 60s (deadlock or lost wake-up)")
	}

	if got := violations.Load(); got != 0 {
		t.Errorf("mutual-exclusion violations = %d, want 0", got)
	}
	if got := acquired.Load(); got != workers*loops {
		t.Errorf("worker acquisitions = %d, want %d", got, workers*loops)
	}
	p.Stop()
}

// TestPool_GetWithContext_AcquireTimeoutConfig 验证 criterion h：acquireTimeout 配置生效——
// 无 deadline 的 ctx + 短 acquireTimeout → 派生超时上下文并按期返回 DeadlineExceeded；
// 负值归一化为 0（不设默认超时）
// TestPool_GetWithContext_AcquireTimeoutConfig verifies criterion h: the acquireTimeout setting
// takes effect — a deadline-free ctx plus a short acquireTimeout derives a timeout context and
// returns DeadlineExceeded on schedule; negative values normalize to 0 (no default timeout)
func TestPool_GetWithContext_AcquireTimeoutConfig(t *testing.T) {
	t.Run("acquireTimeout caps a deadline-free ctx", func(t *testing.T) {
		g := newGatedCreator()
		conf := conecta.NewConfig().
			WithMaxSize(1).
			WithScanInterval(10000).
			WithAcquireTimeout(120 * time.Millisecond).
			WithNewFunc(g.newFunc)
		p := mustNewPool(t, conf)
		defer p.Stop()
		defer g.tryOpen()

		g.arm()
		g1 := startAcquire(p, context.Background())
		g.awaitPinned()

		start := time.Now()
		w := startAcquire(p, context.Background()) // 无 deadline → 由 acquireTimeout 派生 / deadline-free → derived from acquireTimeout
		res := awaitResult(t, w, 5*time.Second)
		elapsed := time.Since(start)

		if !errors.Is(res.err, context.DeadlineExceeded) {
			t.Fatalf("GetWithContext() error = %v, want context.DeadlineExceeded", res.err)
		}
		if elapsed < 120*time.Millisecond || elapsed > 3*time.Second {
			t.Errorf("elapsed = %v, want [120ms, 3s] (the derived timeout must fire on schedule)", elapsed)
		}

		g.open()
		if resG1 := awaitResult(t, g1, 5*time.Second); resG1.err != nil {
			t.Errorf("G1 result err = %v, want nil", resG1.err)
		}
	})

	t.Run("negative acquireTimeout normalizes to no default timeout", func(t *testing.T) {
		g := newGatedCreator()
		conf := conecta.NewConfig().
			WithMaxSize(1).
			WithScanInterval(10000).
			WithAcquireTimeout(-time.Second).
			WithNewFunc(g.newFunc)
		p := mustNewPool(t, conf)
		defer p.Stop()
		defer g.tryOpen()

		g.arm()
		g1 := startAcquire(p, context.Background())
		g.awaitPinned()

		// 负值若未归一化为 0，派生的 WithTimeout(-1s) 会立即到期、~0ms 就返回；
		// 归一化后不设默认超时，等待由 ctx 自身的 150ms deadline 终结
		// Had the negative value not normalized to 0, the derived WithTimeout(-1s) would expire
		// instantly and return in ~0ms; after normalization there is no default timeout and the wait
		// ends at the ctx's own 150ms deadline
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()

		start := time.Now()
		w := startAcquire(p, ctx)
		res := awaitResult(t, w, 5*time.Second)
		elapsed := time.Since(start)

		if !errors.Is(res.err, context.DeadlineExceeded) {
			t.Fatalf("GetWithContext() error = %v, want context.DeadlineExceeded", res.err)
		}
		if elapsed < 100*time.Millisecond {
			t.Errorf("elapsed = %v, want >= 100ms (a negative acquireTimeout must not derive an expired context)", elapsed)
		}

		g.open()
		if resG1 := awaitResult(t, g1, 5*time.Second); resG1.err != nil {
			t.Errorf("G1 result err = %v, want nil", resG1.err)
		}
	})
}

// TestPool_GetWithContext_ExistingDeadlineTakesPrecedence 验证 criterion i：ctx 自带 deadline
// 时不派生新上下文，acquireTimeout 不生效——远短于 ctx deadline 的 acquireTimeout 不会提前
// 触发超时
// TestPool_GetWithContext_ExistingDeadlineTakesPrecedence verifies criterion i: a ctx that already
// carries a deadline gets no derived context and acquireTimeout has no effect — an acquireTimeout far
// shorter than the ctx deadline never fires early
func TestPool_GetWithContext_ExistingDeadlineTakesPrecedence(t *testing.T) {
	g := newGatedCreator()
	conf := conecta.NewConfig().
		WithMaxSize(1).
		WithScanInterval(10000).
		WithAcquireTimeout(20 * time.Millisecond). // 远短于观察窗口 / far shorter than the observation window
		WithNewFunc(g.newFunc)
	p := mustNewPool(t, conf)
	defer p.Stop()
	defer g.tryOpen()

	g.arm()
	g1 := startAcquire(p, context.Background())
	g.awaitPinned()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	w := startAcquire(p, ctx)
	// 100ms 宽限窗口 >> acquireTimeout(20ms)：若错误地派生了 20ms 超时，此刻早已返回
	// DeadlineExceeded；仍在挂起即证明 ctx 自身 deadline 优先（不派生）
	// The 100ms grace window >> acquireTimeout(20ms): had a 20ms timeout been wrongly derived, the
	// call would long have returned DeadlineExceeded; still being suspended proves the ctx's own
	// deadline takes precedence (no derivation)
	select {
	case res := <-w:
		t.Fatalf("GetWithContext returned within 100ms: (%v, %v), want suspension (acquireTimeout must not override the ctx deadline)", res.value, res.err)
	case <-time.After(100 * time.Millisecond):
	}

	// 放行门控 → G1 创建成功（P0-B：预留转为正式在途，无唤醒）→ Put 归还唤醒 W → W 借出成功
	//（远晚于 acquireTimeout(20ms) 仍成功获取，即“ctx 自身 deadline 优先、不派生”的铁证）
	// Open the gate → G1's creation succeeds (P0-B: the reservation converts into a formal in-flight
	// slot, no wake) → the Put return wakes W → W borrows successfully (acquiring far later than the
	// 20ms acquireTimeout is conclusive proof that the ctx's own deadline takes precedence and nothing
	// was derived)
	g.open()
	resG1 := awaitResult(t, g1, 5*time.Second)
	if resG1.err != nil || resG1.value == nil {
		t.Fatalf("G1 result = (%v, %v), want an acquired object", resG1.value, resG1.err)
	}
	if err := p.Put(resG1.value); err != nil {
		t.Fatalf("Put(returned) error = %v", err)
	}
	res := awaitResult(t, w, 5*time.Second)
	if res.err != nil || res.value == nil {
		t.Fatalf("GetWithContext() = (%v, %v), want an acquired object", res.value, res.err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 100ms", elapsed)
	}
}

// TestPool_GetWithContext_WaiterFIFO 验证公平唤醒：多个 waiter 按注册顺序（FIFO）被逐个唤醒，
// 每次归还只唤醒队首一个（单点唤醒，D7）。P0-B 语义下唤醒由链式 Put 归还驱动：G1 创建的对象
// 归还 → 唤醒队首 W1；W1 借出后其结果再归还 → 唤醒次位 W2。全程只有 G1 一次创建（在途封顶
// maxSize=1：旧语义下 G1/W1/W2 各创建一次共 3 次——那正是 P0-B 修复的突破缺陷）
// TestPool_GetWithContext_WaiterFIFO verifies fair waking: multiple waiters are woken strictly in
// registration order (FIFO), and each return wakes exactly one waiter at the head (single-point wake,
// D7). Under P0-B semantics the wakes are driven by chained Put returns: G1's created object is
// returned → the head W1 is woken; after W1 borrows it, its result is returned → the next waiter W2 is
// woken. There is exactly one creation throughout (the in-flight cap at maxSize=1: under the old
// semantics G1/W1/W2 each created once, 3 in total — precisely the break-through defect P0-B fixes)
func TestPool_GetWithContext_WaiterFIFO(t *testing.T) {
	g := newGatedCreator()
	conf := conecta.NewConfig().WithMaxSize(1).WithScanInterval(10000).WithNewFunc(g.newFunc)
	p := mustNewPool(t, conf)
	defer p.Stop()
	defer g.tryOpen()

	var (
		orderMu sync.Mutex
		order   []string
	)
	record := func(name string) {
		orderMu.Lock()
		order = append(order, name)
		orderMu.Unlock()
	}

	g.arm()
	g1 := startAcquire(p, context.Background())
	g.awaitPinned()

	// W1 先注册、W2 后注册（宽限窗口确保注册顺序确定）
	// W1 registers first, W2 second (the grace windows make the registration order certain)
	w1 := make(chan acquireResult, 1)
	go func() {
		v, err := p.GetWithContext(context.Background())
		record("W1")
		w1 <- acquireResult{v, err}
	}()
	assertSuspended(t, w1)
	w2 := make(chan acquireResult, 1)
	go func() {
		v, err := p.GetWithContext(context.Background())
		record("W2")
		w2 <- acquireResult{v, err}
	}()
	assertSuspended(t, w2)

	// 放行门控：G1 创建成功（预留转为正式在途，无唤醒）；G1 归还 → 唤醒队首 W1；
	// W1 借出后其结果再归还 → 唤醒次位 W2。完成顺序必须是 [W1, W2]（LIFO 实现会得到 [W2, W1]）
	// Open the gate: G1's creation succeeds (the reservation converts into a formal in-flight slot, no
	// wake); G1's return wakes the head W1; after W1 borrows, returning its result wakes the next waiter
	// W2. The completion order must be [W1, W2] (a LIFO implementation would yield [W2, W1])
	g.open()
	resG1 := awaitResult(t, g1, 5*time.Second)
	if resG1.err != nil || resG1.value == nil {
		t.Fatalf("G1 result = (%v, %v), want an acquired object", resG1.value, resG1.err)
	}
	if err := p.Put(resG1.value); err != nil {
		t.Fatalf("Put(G1) error = %v", err)
	}
	res1 := awaitResult(t, w1, 5*time.Second)
	if res1.err != nil || res1.value == nil {
		t.Fatalf("W1 result = (%v, %v), want an acquired object", res1.value, res1.err)
	}
	if err := p.Put(res1.value); err != nil {
		t.Fatalf("Put(W1) error = %v", err)
	}
	res2 := awaitResult(t, w2, 5*time.Second)
	if res2.err != nil || res2.value == nil {
		t.Fatalf("W2 result = (%v, %v), want an acquired object", res2.value, res2.err)
	}

	orderMu.Lock()
	defer orderMu.Unlock()
	if len(order) != 2 || order[0] != "W1" || order[1] != "W2" {
		t.Errorf("wake order = %v, want [W1 W2] (FIFO fairness)", order)
	}
	if got := g.calls.Load(); got != 1 {
		t.Errorf("newFunc calls = %d, want 1 (only G1 created; the in-flight cap hands the same object along the FIFO wake chain)", got)
	}
}

// ----------------------------------------------------------------------------
// P0-B 在途封顶验收（maxSize 封顶在途对象总数）+ P0-A panic 安全 + P1-B 错误透传
// P0-B in-flight cap acceptance (maxSize bounds the total in-flight objects) + P0-A panic safety +
// P1-B error passthrough
//
// 本节用例钉住用户裁决选项 A 的核心语义：在途对象（已借出 + 池内空闲 + 在途创建预留）总数被
// maxSize 封顶，配额耗尽后 GetWithContext 挂起等待归还/销毁，绝不突破创建；GetOrCreate 保留 A1
// 突破语义但突破对象同样计入在途。inFlight 是未导出字段（F7：test/ 独立模块读不到），直接断言按
// A4 裁决延至 B3 公开 Stats()，此处以行为投影钉住：挂起/创建时机、newFunc 调用次数、value 同一性
// 与销毁后的配额再生。
// The cases in this section pin the core semantics of user-ruled option A: the total in-flight objects
// (borrowed + idle in the pool + in-flight creation reservations) are capped by maxSize; once the quota
// is exhausted GetWithContext suspends and waits for a return/destruction instead of ever creating past
// the cap, while GetOrCreate keeps the A1 break-through semantics with break-through objects also
// counted as in-flight. inFlight is unexported (F7: the separate test/ module cannot read it), so per
// the A4 ruling direct assertions are deferred to B3's public Stats(); here the semantics are pinned by
// behavioral projections: suspend/creation timing, newFunc call counts, value identity and quota
// regeneration after destructions.
// ----------------------------------------------------------------------------

// TestPool_GetWithContext_InFlightCapAndReturnWake 验收准则 1+2（P0-B 核心证据）：maxSize=2、
// initialize=0，前两个 GetWithContext 各创建并持有一个对象后，第 3 个必须挂起——newFunc 调用
// 次数恰好为 2（绝不是 3）；其中之一 Put 归还后，挂起者被唤醒并且获得的正是归还的那个对象
// （value 同一性），newFunc 次数保持 2（唤醒走借出，不新建）
// TestPool_GetWithContext_InFlightCapAndReturnWake verifies acceptance criteria 1+2 (the core P0-B
// evidence): with maxSize=2 and initialize=0, after the first two GetWithContext calls each create and
// hold one object, the third must suspend — newFunc is called exactly 2 times (never 3); once one of
// the holders returns its object via Put, the suspended caller is woken and receives precisely the
// returned object (value identity), with the newFunc count still at 2 (the wake leads to a borrow, not
// a creation)
func TestPool_GetWithContext_InFlightCapAndReturnWake(t *testing.T) {
	var calls atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(2).
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", calls.Add(1)), nil
		})
	p := mustNewPool(t, conf)
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 前两个调用各创建并持有一个对象：inFlight=2=maxSize
	// The first two calls each create and hold one object: inFlight=2=maxSize
	v1, err := p.GetWithContext(ctx)
	if err != nil || v1 != "obj-1" {
		t.Fatalf("GetWithContext #1 = (%v, %v), want (obj-1, nil)", v1, err)
	}
	v2, err := p.GetWithContext(ctx)
	if err != nil || v2 != "obj-2" {
		t.Fatalf("GetWithContext #2 = (%v, %v), want (obj-2, nil)", v2, err)
	}

	// 第 3 个必须挂起：在途配额已耗尽，绝不突破创建第 3 个对象（达成报告 [P0-1] 场景 A）
	// The third must suspend: the in-flight quota is exhausted and a third object is never created
	// (fulfilling the report's [P0-1] scenario A)
	w3ctx, w3cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer w3cancel()
	w3 := startAcquire(p, w3ctx)
	assertSuspended(t, w3)
	if got := calls.Load(); got != 2 {
		t.Fatalf("newFunc calls = %d, want 2 (never 3: the in-flight cap forbids the break-through creation)", got)
	}

	// 归还唤醒：Put(v1) → w3 被唤醒并借到归还的对象（同一性断言），newFunc 不再被调用
	// Return wake: Put(v1) → w3 is woken and borrows the returned object (identity assertion); newFunc
	// is never called again
	if err := p.Put(v1); err != nil {
		t.Fatalf("Put(v1) error = %v", err)
	}
	res3 := awaitResult(t, w3, 5*time.Second)
	if res3.err != nil || res3.value != v1 {
		t.Fatalf("w3 result = (%v, %v), want the returned obj-1", res3.value, res3.err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("newFunc calls = %d, want 2 (the woken waiter borrows, never creates)", got)
	}

	// 归还义务收尾：全部在途对象回池，交由 Stop 销毁（不泄漏在途配额）
	// Return-obligation cleanup: every in-flight object goes back so Stop can destroy it (no leaked quota)
	if err := p.Put(v2); err != nil {
		t.Fatalf("Put(v2) error = %v", err)
	}
	if err := p.Put(res3.value); err != nil {
		t.Fatalf("Put(res3) error = %v", err)
	}
}

// TestPool_GetWithContext_AllBorrowedSuspendAllReturnedWake 验收准则 3：maxSize 个对象全部被
// GetWithContext 借出未还 → 后续调用全部挂起（绝不新建）；全部 Put 归还 → 挂起者全部被唤醒并
// 恰好取回这三个对象（无新建、无遗漏、无死锁）
// TestPool_GetWithContext_AllBorrowedSuspendAllReturnedWake verifies acceptance criterion 3: with all
// maxSize objects borrowed via GetWithContext and unreturned, every later call suspends (never
// creating); returning them all via Put wakes every suspended caller, and the three objects are
// re-acquired exactly (no creation, no loss, no deadlock)
func TestPool_GetWithContext_AllBorrowedSuspendAllReturnedWake(t *testing.T) {
	const maxSize = 3
	var calls atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(maxSize).
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", calls.Add(1)), nil
		})
	p := mustNewPool(t, conf)
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	held := make([]any, 0, maxSize)
	for i := range maxSize {
		v, err := p.GetWithContext(ctx)
		if err != nil {
			t.Fatalf("GetWithContext #%d error = %v", i+1, err)
		}
		held = append(held, v)
	}
	if got := calls.Load(); got != int64(maxSize) {
		t.Fatalf("newFunc calls = %d, want %d", got, maxSize)
	}

	// 全部借出：三个新调用全部挂起（在途封顶——报告 [P0-1] 场景 A 的完整形态）
	// All borrowed: three new calls all suspend (the in-flight cap — the full shape of scenario A)
	waiters := make([]<-chan acquireResult, 0, maxSize)
	for range maxSize {
		waiters = append(waiters, startAcquire(p, ctx))
	}
	for _, w := range waiters {
		assertSuspended(t, w)
	}
	if got := calls.Load(); got != int64(maxSize) {
		t.Fatalf("newFunc calls = %d, want %d (suspended waiters never create)", got, maxSize)
	}

	// 全部归还：每次 Put 定向唤醒一个 waiter（FIFO），三个挂起者全部醒来并借到对象
	// Return everything: each Put wakes one waiter (FIFO); all three suspended callers wake and borrow
	for _, v := range held {
		if err := p.Put(v); err != nil {
			t.Fatalf("Put(%v) error = %v", v, err)
		}
	}
	got := make(map[any]bool, maxSize)
	acquired := make([]any, 0, maxSize)
	for i, w := range waiters {
		res := awaitResult(t, w, 5*time.Second)
		if res.err != nil || res.value == nil {
			t.Fatalf("waiter #%d result = (%v, %v), want an acquired object", i, res.value, res.err)
		}
		got[res.value] = true
		acquired = append(acquired, res.value)
	}
	for _, v := range held {
		if !got[v] {
			t.Errorf("object %v was never re-acquired by a woken waiter", v)
		}
	}
	if c := calls.Load(); c != int64(maxSize) {
		t.Errorf("newFunc calls = %d, want %d (the wake chain borrows, never creates)", c, maxSize)
	}

	// 归还义务收尾
	// Return-obligation cleanup
	for _, v := range acquired {
		if err := p.Put(v); err != nil {
			t.Fatalf("Put(cleanup %v) error = %v", v, err)
		}
	}
}

// TestPool_GetWithContext_InFlightAccounting 验收准则 4（会计恒等式的行为投影）：任意混合操作
// 序列（GetWithContext/Get/GetOrCreate/Put/Cleanup/supervise 销毁）之后，在途配额始终精确等于
// “物理持有的活对象数”——配额耗尽即挂起、销毁即再生、绝不为负（外部收编对象的销毁被 0 下限
// 挡住，不虚增可创建容量）
// TestPool_GetWithContext_InFlightAccounting verifies acceptance criterion 4 (the behavioral projection
// of the accounting identity): after arbitrary mixed operation sequences
// (GetWithContext/Get/GetOrCreate/Put/Cleanup/supervise destructions) the in-flight quota always equals
// exactly the number of physically live objects — exhaustion suspends, destructions regenerate quota,
// and the counter never goes negative (destroying an adopted foreign object is stopped by the zero
// floor and never inflates the creatable capacity)
func TestPool_GetWithContext_InFlightAccounting(t *testing.T) {
	t.Run("mixed operations keep the in-flight total equal to live objects", func(t *testing.T) {
		var calls, closed atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(2).
			WithScanInterval(10000).
			WithNewFunc(func() (any, error) {
				return fmt.Sprintf("obj-%d", calls.Add(1)), nil
			}).
			WithCloseFunc(func(any) error {
				closed.Add(1)
				return nil
			})
		p := mustNewPool(t, conf)
		defer p.Stop()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// ① 纯 GWC 循环：两次创建占满在途配额（inFlight=2=maxSize）
		// ① A pure GWC cycle: two creations fill the in-flight quota (inFlight=2=maxSize)
		v1, err := p.GetWithContext(ctx)
		if err != nil || v1 != "obj-1" {
			t.Fatalf("GWC #1 = (%v, %v), want (obj-1, nil)", v1, err)
		}
		v2, err := p.GetWithContext(ctx)
		if err != nil || v2 != "obj-2" {
			t.Fatalf("GWC #2 = (%v, %v), want (obj-2, nil)", v2, err)
		}

		// ② 探针挂起（inFlight==2 的投影）；Put(v1) 唤醒并借到 v1（借出/归还不改变在途）
		// ② The probe suspends (projection of inFlight==2); Put(v1) wakes it and it borrows v1
		// (lending/returning never changes the in-flight count)
		probe3 := startAcquire(p, ctx)
		assertSuspended(t, probe3)
		if err := p.Put(v1); err != nil {
			t.Fatalf("Put(v1) error = %v", err)
		}
		res3 := awaitResult(t, probe3, 5*time.Second)
		if res3.err != nil || res3.value != v1 {
			t.Fatalf("probe3 = (%v, %v), want (obj-1, nil)", res3.value, res3.err)
		}

		// ③ 空队列 Get 快速失败（不触碰在途配额）
		// ③ Get on an empty queue fails fast (never touching the in-flight quota)
		if _, err := p.Get(); !errors.Is(err, conecta.ErrPoolEmpty) {
			t.Fatalf("Get() error = %v, want ErrPoolEmpty", err)
		}

		// ④ GetOrCreate 突破（A1）：创建第 3 个对象，inFlight=3 > maxSize——契约允许
		// ④ GetOrCreate break-through (A1): a third object is created, inFlight=3 > maxSize — allowed by
		// the contract
		v4, err := p.GetOrCreate()
		if err != nil || v4 != "obj-3" {
			t.Fatalf("GetOrCreate() = (%v, %v), want (obj-3, nil)", v4, err)
		}
		if got := calls.Load(); got != 3 {
			t.Fatalf("newFunc calls = %d, want 3", got)
		}

		// ⑤ 突破期间 GWC 必须挂起（在途配额耗尽——契约语义），Put(v2) 唤醒后借到 v2
		// ⑤ During the break-through GWC must suspend (quota exhausted — the contract semantics);
		// Put(v2) wakes the probe, which borrows v2
		probe4 := startAcquire(p, ctx)
		assertSuspended(t, probe4)
		if err := p.Put(v2); err != nil {
			t.Fatalf("Put(v2) error = %v", err)
		}
		res4 := awaitResult(t, probe4, 5*time.Second)
		if res4.err != nil || res4.value != v2 {
			t.Fatalf("probe4 = (%v, %v), want (obj-2, nil)", res4.value, res4.err)
		}

		// ⑥ 两个对象回池（size 门禁 ≤2 恰好容纳），Cleanup 全部销毁：inFlight 3→1
		//（销毁释放配额；第 3 个对象 obj-3 仍被测试持有——借出对象池无法销毁）
		// ⑥ Two objects go back (the size gate ≤2 fits exactly) and Cleanup destroys them all:
		// inFlight 3→1 (destructions release quota; the third object obj-3 is still held by the test —
		// the pool cannot destroy borrowed objects)
		if err := p.Put(res3.value); err != nil {
			t.Fatalf("Put(obj-1) error = %v", err)
		}
		if err := p.Put(res4.value); err != nil {
			t.Fatalf("Put(obj-2) error = %v", err)
		}
		p.Cleanup()
		if got := closed.Load(); got != 2 {
			t.Fatalf("closeFunc calls = %d, want 2", got)
		}

		// ⑦ 恒等式牙齿：此刻 inFlight=1（仅 obj-3 存活）<2 → GWC 必须能再次创建。
		// 若销毁未递减在途（inFlight 滞留 3），此调用将挂起直至超时——投影失败即记账漂移
		// ⑦ The identity's teeth: inFlight=1 now (only obj-3 lives) < 2 → GWC must create again. Had the
		// destructions not decremented the counter (inFlight stuck at 3), this call would suspend until
		// timeout — a failed projection means accounting drift
		v5, err := p.GetWithContext(ctx)
		if err != nil || v5 != "obj-4" {
			t.Fatalf("GWC after destructions = (%v, %v), want (obj-4, nil) — destructions must release in-flight quota", v5, err)
		}

		// ⑧ 收尾恒等式：全部活对象回池，Stop 恰好各销毁一次 → closed == calls == 4
		// （创建与销毁一一配对，inFlight 归零无泄漏、无双重销毁）
		// ⑧ Closing identity: every live object returns, Stop destroys each exactly once → closed ==
		// calls == 4 (creations and destructions pair one-to-one; inFlight ends at zero with no leak and
		// no double destruction)
		if err := p.Put(v4); err != nil {
			t.Fatalf("Put(obj-3) error = %v", err)
		}
		if err := p.Put(v5); err != nil {
			t.Fatalf("Put(obj-4) error = %v", err)
		}
		p.Stop()
		if got := closed.Load(); got != 4 {
			t.Errorf("closeFunc calls = %d, want 4 (every created object destroyed exactly once)", got)
		}
		if got := calls.Load(); got != 4 {
			t.Errorf("newFunc calls = %d, want 4", got)
		}
	})

	t.Run("supervise destruction releases in-flight quota (tombstone path)", func(t *testing.T) {
		var calls, closed atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(1).
			WithInitialize(1).
			WithScanInterval(conecta.MinScanInterval).
			WithPingMaxRetries(1).
			WithPingFunc(func(any, int) bool { return false }).
			WithNewFunc(func() (any, error) {
				return fmt.Sprintf("obj-%d", calls.Add(1)), nil
			}).
			WithCloseFunc(func(any) error {
				closed.Add(1)
				return nil
			})
		p := mustNewPool(t, conf)
		defer p.Stop()

		// initialize 创建 obj-1（calls=1，inFlight=1=maxSize）；supervise ping 失败销毁它：
		// inFlight 1→0 + 唤醒，tombstone 留队列占 size（A3 不变）
		// initialize creates obj-1 (calls=1, inFlight=1=maxSize); supervise destroys it on ping failure:
		// inFlight 1→0 + wake, the tombstone stays in the queue occupying size (A3 unchanged)
		waitUntil(t, 5*time.Second, 20*time.Millisecond,
			func() bool { return closed.Load() == 1 },
			"pre-created element was not destroyed: closed=%d", closed.Load())

		// 探针：tombstone 回收 → 空队列 → 有界创建必须成功（inFlight=0<1）。
		// 牙齿：若 supervise 销毁未递减在途，inFlight 滞留 1=maxSize → 挂起直至 ctx 超时
		// The probe: tombstone reclaim → empty queue → the bounded creation must succeed (inFlight=0<1).
		// Teeth: had the supervise destruction not decremented the counter, inFlight would stick at
		// 1=maxSize → suspension until the ctx timeout
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		v, err := p.GetWithContext(ctx)
		if err != nil || v != "obj-2" {
			t.Fatalf("GetWithContext() = (%v, %v), want (obj-2, nil) — supervise destruction must release the in-flight quota", v, err)
		}
		if got := calls.Load(); got != 2 {
			t.Errorf("newFunc calls = %d, want 2 (initialize + the probe's creation)", got)
		}
	})

	t.Run("destroying an adopted foreign value floors in-flight at zero", func(t *testing.T) {
		var calls, closed atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(1).
			WithScanInterval(10000).
			WithNewFunc(func() (any, error) {
				return fmt.Sprintf("obj-%d", calls.Add(1)), nil
			}).
			WithCloseFunc(func(any) error {
				closed.Add(1)
				return nil
			})
		p := mustNewPool(t, conf)
		defer p.Stop()

		// 外部对象经 Put 收编（从未经 newFunc +1），Cleanup 销毁它：releaseSlot 的 0 下限必须
		// 挡住 inFlight 变负（无下限时将落到 -1，虚增可创建容量、破坏在途封顶）
		// A foreign object is adopted via Put (never went through a newFunc +1) and Cleanup destroys it:
		// releaseSlot's zero floor must stop inFlight from going negative (without the floor it would
		// drop to -1, virtually inflating the creatable capacity and breaking the in-flight cap)
		if err := p.Put("foreign"); err != nil {
			t.Fatalf("Put(foreign) error = %v", err)
		}
		p.Cleanup()
		if got := closed.Load(); got != 1 {
			t.Fatalf("closeFunc calls = %d, want 1", got)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// 探针 1：在途配额未被外部销毁污染 → 正常创建（inFlight 0→1）
		// Probe 1: the quota was not polluted by the foreign destruction → a normal creation (0→1)
		v1, err := p.GetWithContext(ctx)
		if err != nil || v1 != "obj-1" {
			t.Fatalf("probe #1 = (%v, %v), want (obj-1, nil)", v1, err)
		}

		// 探针 2：inFlight=1=maxSize → 必须挂起。牙齿：若下限失守（inFlight=-1+1=0），探针 2 会
		// 错误地创建第 2 个对象（突破封顶），assertSuspended 与 calls==1 联合钉死
		// Probe 2: inFlight=1=maxSize → it must suspend. Teeth: had the floor failed (inFlight=-1+1=0),
		// probe 2 would wrongly create a second object (breaking the cap); assertSuspended together with
		// calls==1 pins this down
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		probe := startAcquire(p, pctx)
		assertSuspended(t, probe)
		if got := calls.Load(); got != 1 {
			t.Fatalf("newFunc calls = %d, want 1 (a negative in-flight drift must not inflate the creatable capacity)", got)
		}

		// 收尾：归还 v1 唤醒探针（借出归还义务闭环）
		// Cleanup: returning v1 wakes the probe (closing the return-obligation loop)
		if err := p.Put(v1); err != nil {
			t.Fatalf("Put(v1) error = %v", err)
		}
		res := awaitResult(t, probe, 5*time.Second)
		if res.err != nil || res.value != v1 {
			t.Fatalf("probe = (%v, %v), want the returned obj-1", res.value, res.err)
		}
		if err := p.Put(res.value); err != nil {
			t.Fatalf("Put(cleanup) error = %v", err)
		}
	})
}

// TestPool_GetWithContext_NewFuncPanicReleasesReservation 验收准则 5（P0-A panic 安全）：
// newFunc panic → 在途预留经 defer 在 panic 展开中恰好释放一次（inFlight 不泄漏）、panic 原样
// 传播给调用方（不吞用户 panic）、池后续完全可用（满配额创建 → 封顶挂起 → 归还唤醒）
// TestPool_GetWithContext_NewFuncPanicReleasesReservation verifies acceptance criterion 5 (P0-A panic
// safety): a panicking newFunc → the in-flight reservation is released exactly once by the defer during
// the panic unwind (no inFlight leak), the panic propagates to the caller untouched (the user's panic is
// never swallowed), and the pool stays fully functional afterwards (creation up to the full quota →
// capped suspension → return wake)
func TestPool_GetWithContext_NewFuncPanicReleasesReservation(t *testing.T) {
	const panicValue = "boom-in-newFunc"
	var calls atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(2).
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) {
			if calls.Add(1) == 1 {
				panic(panicValue)
			}
			return fmt.Sprintf("obj-%d", calls.Load()), nil
		})
	p := mustNewPool(t, conf)
	defer p.Stop()

	// panic 原样传播给调用方（recover 捕获到的必须是原始 panic 值——不吞、不替换）
	// The panic propagates to the caller untouched (what recover catches must be the original panic
	// value — never swallowed, never replaced)
	caught := func() (recovered any) {
		defer func() { recovered = recover() }()
		_, _ = p.GetWithContext(context.Background())
		return nil
	}()
	if caught != panicValue {
		t.Fatalf("recovered = %v, want %v (the user's panic must propagate untouched)", caught, panicValue)
	}

	// 预留无泄漏：两个在途配额完好可用。牙齿：若 panic 绕过释放（P0-A 缺陷形态），inFlight 滞留 1，
	// 下面第二次创建将因 inFlight=2=maxSize 挂起直至 ctx 超时
	// No reservation leak: both in-flight slots remain usable. Teeth: had the panic bypassed the release
	// (the P0-A defect shape), inFlight would stick at 1 and the second creation below would suspend at
	// inFlight=2=maxSize until the ctx timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	v1, err := p.GetWithContext(ctx)
	if err != nil || v1 != "obj-2" {
		t.Fatalf("GetWithContext #1 after panic = (%v, %v), want (obj-2, nil) — the reservation must not leak", v1, err)
	}
	v2, err := p.GetWithContext(ctx)
	if err != nil || v2 != "obj-3" {
		t.Fatalf("GetWithContext #2 after panic = (%v, %v), want (obj-3, nil) — the reservation must not leak", v2, err)
	}

	// 池完全可用：在途满（inFlight=2=maxSize）→ 新调用挂起；归还后唤醒并借到归还对象
	// The pool is fully functional: the quota is full (inFlight=2=maxSize) → a new call suspends; a
	// return wakes it and it borrows the returned object
	wctx, wcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer wcancel()
	w := startAcquire(p, wctx)
	assertSuspended(t, w)
	if err := p.Put(v1); err != nil {
		t.Fatalf("Put(v1) error = %v", err)
	}
	res := awaitResult(t, w, 5*time.Second)
	if res.err != nil || res.value != v1 {
		t.Fatalf("waiter result = (%v, %v), want the returned obj-2", res.value, res.err)
	}

	// 归还义务收尾
	// Return-obligation cleanup
	if err := p.Put(v2); err != nil {
		t.Fatalf("Put(v2) error = %v", err)
	}
	if err := p.Put(res.value); err != nil {
		t.Fatalf("Put(res) error = %v", err)
	}
}

// TestPool_GetWithContext_NewFuncErrorPassthrough P1-B：newFunc 错误透传契约——错误 == 直判
// 无包装、value 所有权随错误移交调用方、失败路径的在途预留已释放（无泄漏：maxSize=1 下第二次
// 获取仍能创建而非挂起）
// TestPool_GetWithContext_NewFuncErrorPassthrough covers P1-B: the newFunc error-passthrough contract —
// the error is bare under an == comparison (no wrapping), value ownership travels with the error back to
// the caller, and the failure path released the in-flight reservation (no leak: under maxSize=1 a second
// acquisition still creates instead of suspending)
func TestPool_GetWithContext_NewFuncErrorPassthrough(t *testing.T) {
	errCreate := errors.New("dial failure")
	const partial = "half-open-conn"
	var calls atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(1).
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) {
			if calls.Add(1) == 1 {
				return partial, errCreate
			}
			return "healthy", nil
		})
	p := mustNewPool(t, conf)
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 错误原样透传（== 直判证明无包装），value 所有权随错误移交
	// The error is passed through as-is (the == comparison proves no wrapping); value ownership travels
	// with the error
	v, err := p.GetWithContext(ctx)
	if err != errCreate {
		t.Fatalf("error = %v, want the bare errCreate (== identity proves no wrapping)", err)
	}
	if v != partial {
		t.Errorf("value = %v, want %v (ownership travels with the error)", v, partial)
	}

	// 预留已释放（无泄漏）：maxSize=1 下第二次获取必须仍能创建——若失败路径泄漏了在途预留，
	// inFlight 滞留 1=maxSize，此调用将挂起直至 ctx 超时
	// The reservation was released (no leak): under maxSize=1 the second acquisition must still create —
	// had the failure path leaked the reservation, inFlight would stick at 1=maxSize and this call would
	// suspend until the ctx timeout
	v2, err := p.GetWithContext(ctx)
	if err != nil || v2 != "healthy" {
		t.Fatalf("GetWithContext #2 = (%v, %v), want (healthy, nil) — the failure path must release the reservation", v2, err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("newFunc calls = %d, want 2", got)
	}

	// 归还义务收尾
	// Return-obligation cleanup
	if err := p.Put(v2); err != nil {
		t.Fatalf("Put(v2) error = %v", err)
	}
}

// TestPool_Put_RecycledWrapperVsSuperviseRace E1 回归钉住：supervise 的陈旧 Values() 快照 ×
// Put 的 wrapper 复用写 data × elementPool（sync.Pool）回收三者叠加的竞争形态。
// 根因：supervise 持快照遍历期间，快照中的 wrapper 可被 Get 离队 → PutRaw 回收 → 并发 Put 复用；
// 修复前 Put 以无锁 SetDataNoLock 写 data，与 supervise 的锁内读之间没有任何 happens-before 边，
// 构成内存模型意义上的数据竞争（interface 双字可撕裂）；且“data 已写、expireAt 未打”的中间态
// 在 lifetimeOn 下触发 fail-closed 误判，把刚归还的健康对象误销毁。
// 修复后（E1）：Put 先 stampEnqueue 再经 element.mu 带锁 SetData——带锁写与 supervise 的锁内读
// 互斥（TSan 竞争消除），先打戳保证 supervise 若见 data!=nil 必见新 expireAt（误判消除）。
// 检测臂：-race 下无锁写 vs 锁内读必被 TSan 报告（测试二进制失败）；行为臂：任何 closeFunc
// 触发（closed>0）即 fail-closed 误判或竞争撕读的后果，两条臂都不允许出现。
// TestPool_Put_RecycledWrapperVsSuperviseRace pins the E1 regression: the race shape formed by
// supervise's stale Values() snapshot × Put's wrapper-reuse data write × elementPool (sync.Pool)
// recycling. Root cause: while supervise walks a snapshot, a wrapper in it can be dequeued by Get →
// recycled via PutRaw → reused by a concurrent Put; before the fix Put wrote data with the lock-free
// SetDataNoLock, leaving no happens-before edge against supervise's under-lock read — a data race in
// the memory-model sense (an interface value is two words and can tear); moreover the intermediate
// state "data written, expireAt not yet stamped" triggers the fail-closed misjudgment under
// lifetimeOn, wrongly destroying a just-returned healthy object. After the fix (E1) Put runs
// stampEnqueue first and then SetData under element.mu — the locked write is mutually exclusive
// with supervise's under-lock read (the TSan race is gone), and stamping first guarantees supervise
// sees the new expireAt whenever it sees data!=nil (the misjudgment is gone). Detection arms: under
// -race TSan necessarily reports the lock-free write vs the under-lock read (failing the test
// binary); behaviorally, any closeFunc firing (closed>0) is a consequence of the fail-closed
// misjudgment or a torn read — neither arm may trigger.
func TestPool_Put_RecycledWrapperVsSuperviseRace(t *testing.T) {
	var closed, gets, pings atomic.Int64

	conf := conecta.NewConfig().
		WithMaxSize(24).
		WithInitialize(12).
		// 300ms 最小扫描间隔：1.3s 压力窗口内保证 ≥4 轮快照行走；12 个 wrapper × 1ms ping
		// ≈ 12ms 行走窗口，与 4 个循环 goroutine 的回收复用充分交叠
		// The 300ms minimum scan interval guarantees ≥4 snapshot walks within the 1.3s stress
		// window; 12 wrappers × 1ms ping ≈ a 12ms walk window, fully overlapping the recycling
		// of the 4 loop goroutines
		WithScanInterval(conecta.MinScanInterval).
		// lifetimeOn：开启 fail-closed（expireAt==0 视为到期）判定臂；1 小时寿命确保合法到期不发生
		// lifetimeOn enables the fail-closed (expireAt==0 counts as expired) arm; the 1-hour lifetime
		// ensures no legitimate expiry ever happens
		WithMaxLifetime(time.Hour).
		WithPingFunc(func(any, int) bool {
			// pingFunc 在 element.mu 内执行：拉长快照行走窗口，让其余 wrapper 在窗口内被高频回收复用
			// pingFunc runs under element.mu: it stretches the snapshot-walk window so the other
			// wrappers are recycled at high frequency inside it
			time.Sleep(time.Millisecond)
			pings.Add(1)
			return true
		}).
		WithNewFunc(func() (any, error) { return "conn", nil }).
		WithCloseFunc(func(any) error {
			closed.Add(1)
			return nil
		})
	p := mustNewPool(t, conf)
	defer p.Stop()

	// 多 goroutine 紧循环借出/归还：wrapper 以 Get→清零离队→PutRaw→elementPool→Put 的路径高频
	// 复用。≥2 个循环 goroutine 是复现的关键：supervise 按快照序在 element.mu 内 ping 队头元素
	// （1ms）时，部分 Get 会阻塞在该队头的 mu 上，而其余 goroutine 的 Get 恰好离队快照中的后续
	// wrapper 并完成整轮回收复用（修复前为无锁写 data）——supervise 随后按快照走到它并锁内读，
	// 写读之间不存在任何 happens-before 边（单 goroutine 时 FIFO 追逐会被 mu 交接意外序化，无法触发）
	// Multiple tight borrow/return loop goroutines: wrappers are reused at high frequency along the
	// Get→clear-on-dequeue→PutRaw→elementPool→Put path. ≥2 loop goroutines are the key to
	// reproduction: while supervise pings the head element under its element.mu in snapshot order
	// (1ms), the first Get blocks on that head's mu while the other goroutines' Gets dequeue the
	// subsequent wrappers from the snapshot and complete a full recycle (a lock-free data write
	// before the fix) — supervise then walks on to them and reads under the lock, with no
	// happens-before edge between the write and the read (with a single goroutine the FIFO chase is
	// accidentally ordered by the mu hand-off and never triggers)
	var wg sync.WaitGroup
	deadline := time.Now().Add(1300 * time.Millisecond)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				v, err := p.Get()
				if err != nil {
					if errors.Is(err, conecta.ErrPoolEmpty) {
						continue
					}
					t.Errorf("Get() error = %v", err)
					return
				}
				gets.Add(1)
				if err := p.Put(v); err != nil {
					t.Errorf("Put() error = %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// 防真空臂 1：循环必须真实推进（否则交叠窗口不存在，测试无意义）
	// Anti-vacuity arm 1: the loop must have made real progress (otherwise the overlap window never
	// existed and the test proves nothing)
	if got := gets.Load(); got < 10 {
		t.Fatalf("recycle loop made no progress: gets = %d, want >= 10", got)
	}
	// 诊断信息：pings 反映快照行走中读到 data!=nil 的次数。sync.Pool 在 -race/GC 压力下的
	// wrapper 身份换血（GC 清池 + P 迁移致 private 槽滞留）会使部分快照读取落在 nil 态
	//（pings 偏低属预期，不作断言）；竞争检出依赖 TSan 对 happens-before 的判定而非读取到
	// 的值——无序窗口窄且受调度影响，实测修复前单轮检出率 5%-40% 波动（曾捕获精确竞争对：
	// supervise 锁内读 pool.go × Put 无锁写 pool.go），-count=100 下回归累计检出概率 ≈ 1。
	// 修复后带锁写与锁内读恒被 element.mu 序化，本测试恒绿
	// Diagnostic: pings reflects how many snapshot reads saw data!=nil. Wrapper-identity churn in
	// sync.Pool under -race/GC pressure (GC pool flushes + P migrations stranding private slots)
	// lands some snapshot reads in the nil state (a low ping count is expected and not asserted);
	// race detection relies on TSan's happens-before verdict rather than the values read — the
	// unordered window is narrow and scheduling-sensitive, with measured pre-fix per-run detection
	// fluctuating between 5% and 40% (the exact race pair was captured: supervise's under-lock read
	// in pool.go × Put's lock-free write in pool.go); under -count=100 the cumulative regression
	// detection probability is ≈ 1. After the fix the locked write and the under-lock read are
	// always ordered by element.mu, so this test is permanently green
	t.Logf("stress done: gets=%d pings=%d closed=%d", gets.Load(), pings.Load(), closed.Load())
	// 行为臂：健康对象（ping 恒真、寿命 1 小时）在任何交错下都不得被销毁
	// Behavioral arm: a healthy object (ping always true, 1-hour lifetime) must never be destroyed
	// under any interleaving
	if got := closed.Load(); got != 0 {
		t.Fatalf("closeFunc calls = %d, want 0 — a healthy object must never be destroyed by a stale-snapshot misjudgment", got)
	}
}

// errCapturingCallback 在 countingCallback 基础上捕获 OnClose 的 error 参数：
// 用于断言钩子拒绝路径把钩子错误（而非 closeFunc 错误）随销毁传给 OnClose 的同一性
// errCapturingCallback extends countingCallback by capturing OnClose's error argument: used to
// assert the identity of the hook error (rather than the closeFunc error) that the hook-rejection
// paths hand to OnClose with each destruction
type errCapturingCallback struct {
	closeN    atomic.Int64
	pingS     atomic.Int64
	pingF     atomic.Int64
	mu        sync.Mutex
	closeArgs []error
}

func (c *errCapturingCallback) OnPingSuccess(any) { c.pingS.Add(1) }
func (c *errCapturingCallback) OnPingFailure(any) { c.pingF.Add(1) }
func (c *errCapturingCallback) OnClose(_ any, err error) {
	c.closeN.Add(1)
	c.mu.Lock()
	c.closeArgs = append(c.closeArgs, err)
	c.mu.Unlock()
}

func (c *errCapturingCallback) errs() []error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]error(nil), c.closeArgs...)
}

// TestPool_BeforeGet_RejectsDestroysAndTriesNext 验收准则 1：BeforeGet 返回 error → 该对象被
// 销毁（closeFunc + OnClose 各恰好一次，OnClose 收到钩子错误本体）且借出循环继续尝试队列
// 下一个，最终拿到合格对象；未设钩子（默认 nil）时行为与现状完全一致（零成本关闭态）
// TestPool_BeforeGet_RejectsDestroysAndTriesNext covers acceptance criterion 1: a BeforeGet error
// destroys that object (closeFunc and OnClose fire exactly once each, OnClose receiving the hook
// error itself) and the borrow loop continues with the next queued element until a qualified one is
// obtained; with no hook configured (the default nil) behavior is identical to the status quo (the
// zero-cost disabled state)
func TestPool_BeforeGet_RejectsDestroysAndTriesNext(t *testing.T) {
	t.Run("rejected objects are destroyed and the next queued one is lent", func(t *testing.T) {
		errRejected := errors.New("session dirty")
		closed := &closedRecorder{}
		cb := &errCapturingCallback{}
		var hookCalls atomic.Int64

		conf := conecta.NewConfig().
			WithMaxSize(8).
			WithScanInterval(10000).
			WithBeforeGet(func(v any) error {
				hookCalls.Add(1)
				if s, _ := v.(string); strings.HasPrefix(s, "bad") {
					return errRejected
				}
				return nil
			}).
			WithCloseFunc(func(v any) error {
				closed.add(v)
				return nil
			}).
			WithCallback(cb)
		p := mustNewPool(t, conf)
		defer p.Stop()

		for _, v := range []string{"bad-1", "bad-2", "good"} {
			if err := p.Put(v); err != nil {
				t.Fatalf("Put(%q) error = %v", v, err)
			}
		}

		v, err := p.Get()
		if err != nil || v != "good" {
			t.Fatalf("Get() = (%v, %v), want (good, nil)", v, err)
		}
		// 钩子对 bad-1、bad-2、good 各调用一次（FIFO 顺序）
		// The hook ran once each for bad-1, bad-2 and good (FIFO order)
		if got := hookCalls.Load(); got != 3 {
			t.Errorf("beforeGet calls = %d, want 3", got)
		}
		// 销毁三件套的行为投影：closeFunc 与 OnClose 对每个被拒对象恰好一次
		// The behavioral projection of the destruction triple: closeFunc and OnClose fire exactly
		// once per rejected object
		for _, want := range []string{"bad-1", "bad-2"} {
			if got := closed.count(want); got != 1 {
				t.Errorf("closeFunc calls for %q = %d, want exactly 1", want, got)
			}
		}
		if got := closed.count("good"); got != 0 {
			t.Errorf("closeFunc calls for the qualified object = %d, want 0", got)
		}
		if got := cb.closeN.Load(); got != 2 {
			t.Errorf("OnClose calls = %d, want 2", got)
		}
		// OnClose 收到钩子错误本体（== 同一性：不包装、不替换为 closeFunc 错误）
		// OnClose received the hook error itself (== identity: never wrapped, never replaced by the
		// closeFunc error)
		for i, got := range cb.errs() {
			if got != errRejected {
				t.Errorf("OnClose error #%d = %v, want the bare hook error (== identity)", i, got)
			}
		}
		// 被拒对象已离队销毁：池内只剩借出的 good
		// The rejected objects left the queue destroyed: only the lent-out good remains
		if got := p.Len(); got != 0 {
			t.Errorf("Len() = %d, want 0 (rejected objects must not stay in the queue)", got)
		}
	})

	t.Run("unset hook lends as-is (default zero-cost state)", func(t *testing.T) {
		closed := &closedRecorder{}
		conf := conecta.NewConfig().
			WithMaxSize(4).
			WithScanInterval(10000).
			WithCloseFunc(func(v any) error {
				closed.add(v)
				return nil
			})
		p := mustNewPool(t, conf)
		defer p.Stop()

		if err := p.Put("x"); err != nil {
			t.Fatalf("Put(x) error = %v", err)
		}
		v, err := p.Get()
		if err != nil || v != "x" {
			t.Fatalf("Get() = (%v, %v), want (x, nil)", v, err)
		}
		if got := closed.total(); got != 0 {
			t.Errorf("closeFunc calls = %d, want 0 with no hook configured", got)
		}
	})
}

// TestPool_BeforeGet_BudgetCappedAtMaxRetries 验收准则 1（预算臂）：借出校验拒绝的销毁数达到
// maxRetries 上限后返回裸 ErrPoolEmpty（防止一池坏对象把单次借出放大成大量 closeFunc 调用），
// 队列中剩余对象留给后续调用继续处理——与既有到期销毁的预算模式一致
// TestPool_BeforeGet_BudgetCappedAtMaxRetries covers acceptance criterion 1 (the budget arm): once
// borrow-check rejections reach the maxRetries cap the call returns the bare ErrPoolEmpty
// (preventing a pool full of bad objects from amplifying a single borrow into many closeFunc
// calls); the remaining queued objects are left for later calls — the same budget pattern as the
// existing expiry destructions
func TestPool_BeforeGet_BudgetCappedAtMaxRetries(t *testing.T) {
	errRejected := errors.New("unqualified")
	closed := &closedRecorder{}
	conf := conecta.NewConfig().
		WithMaxSize(8).
		WithScanInterval(10000).
		WithPingMaxRetries(2).
		WithBeforeGet(func(any) error { return errRejected }).
		WithCloseFunc(func(v any) error {
			closed.add(v)
			return nil
		})
	p := mustNewPool(t, conf)
	defer p.Stop()

	for _, v := range []string{"bad-1", "bad-2", "bad-3"} {
		if err := p.Put(v); err != nil {
			t.Fatalf("Put(%q) error = %v", v, err)
		}
	}

	// 第一次 Get：销毁 bad-1、bad-2 后预算（maxRetries=2）耗尽 → 裸 ErrPoolEmpty（== 直判）
	// The first Get destroys bad-1 and bad-2, exhausts the budget (maxRetries=2) and returns the
	// bare ErrPoolEmpty (== comparison)
	_, err := p.Get()
	if err != conecta.ErrPoolEmpty {
		t.Fatalf("Get() error = %v, want the bare ErrPoolEmpty", err)
	}
	if got := closed.total(); got != 2 {
		t.Fatalf("closeFunc calls = %d, want exactly 2 (the budget caps one borrow's destructions)", got)
	}
	// bad-3 仍在队列中（占容量），留给后续调用
	// bad-3 is still queued (occupying capacity), left for later calls
	if got := p.Len(); got != 1 {
		t.Errorf("Len() = %d, want 1", got)
	}

	// 第二次 Get：销毁 bad-3 后队列已空 → ErrPoolEmpty；总销毁数 3（每对象恰好一次）
	// The second Get destroys bad-3, finds the queue empty and returns ErrPoolEmpty; 3 destructions
	// in total (each object exactly once)
	_, err = p.Get()
	if err != conecta.ErrPoolEmpty {
		t.Fatalf("second Get() error = %v, want ErrPoolEmpty", err)
	}
	if got := closed.count("bad-3"); got != 1 {
		t.Errorf("closeFunc calls for bad-3 = %d, want exactly 1", got)
	}
	if got := p.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0", got)
	}
}

// TestPool_BeforeGet_InFlightReleasedOnDestroy 验收准则 5：BeforeGet 销毁释放在途配额——
// releaseSlot 的 inFlight-1 使 maxSize=1 的池能立即再创建；若销毁未递减在途（配额泄漏），
// 探针 GetWithContext 将因 inFlight 滞留 1=maxSize 而挂起直至 ctx 超时（测试的牙齿）
// TestPool_BeforeGet_InFlightReleasedOnDestroy covers acceptance criterion 5: a BeforeGet
// destruction releases the in-flight quota — releaseSlot's inFlight-1 lets a maxSize=1 pool create
// again immediately; had the destruction not decremented the counter (a quota leak), the probe
// GetWithContext would suspend at inFlight stuck on 1=maxSize until the ctx timeout (the test's
// teeth)
func TestPool_BeforeGet_InFlightReleasedOnDestroy(t *testing.T) {
	errRejected := errors.New("stale session")
	var calls, closed atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(1).
		WithInitialize(1).
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", calls.Add(1)), nil
		}).
		WithBeforeGet(func(v any) error {
			if v == "obj-1" {
				return errRejected
			}
			return nil
		}).
		WithCloseFunc(func(any) error {
			closed.Add(1)
			return nil
		})
	p := mustNewPool(t, conf)
	defer p.Stop()

	// initialize 预建 obj-1（inFlight=1=maxSize）；GWC 借出它 → 钩子拒绝 → 销毁 + releaseSlot
	//（inFlight 1→0）→ 队列空 → 有界创建 obj-2 成功返回。若销毁未释放在途配额，reserveSlot
	// 将见 inFlight=1=maxSize → 挂起直至 ctx 超时 → Fatalf
	// initialize pre-creates obj-1 (inFlight=1=maxSize); GWC borrows it → the hook rejects →
	// destruction + releaseSlot (inFlight 1→0) → the queue is empty → the bounded creation of obj-2
	// succeeds. Had the destruction not released the quota, reserveSlot would see inFlight=1=maxSize
	// → suspension until the ctx timeout → Fatalf
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	v, err := p.GetWithContext(ctx)
	if err != nil || v != "obj-2" {
		t.Fatalf("GetWithContext() = (%v, %v), want (obj-2, nil) — the borrow-check destruction must release the in-flight quota", v, err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("newFunc calls = %d, want 2", got)
	}
	if got := closed.Load(); got != 1 {
		t.Errorf("closeFunc calls = %d, want exactly 1 (obj-1 destroyed once)", got)
	}
	// 归还义务收尾 / Return-obligation cleanup
	if err := p.Put(v); err != nil {
		t.Fatalf("Put(obj-2) error = %v", err)
	}
}

// TestPool_AfterPut_RejectsDestroysAndPassthrough 验收准则 2：AfterPut 返回 error → Put 不入队
// + 销毁该对象（closeFunc + OnClose 各恰好一次，OnClose 收到钩子错误本体）+ 该 error 被 Put
// 原样透传（== 直判，且明确区别于 ErrPoolFull）；返回 nil → 正常入队。执行序钉住：nil 检查
// 先于钩子（钩子永远见不到 nil）、Stop 后不执行钩子（直接 ErrQueueClosed）
// TestPool_AfterPut_RejectsDestroysAndPassthrough covers acceptance criterion 2: an AfterPut error
// keeps Put from enqueuing, destroys the object (closeFunc and OnClose fire exactly once each,
// OnClose receiving the hook error itself) and the error is passed through by Put as-is (==
// identity, explicitly distinct from ErrPoolFull); a nil return enqueues normally. The execution
// order is pinned: the nil check precedes the hook (the hook never sees nil) and the hook never
// runs after Stop (ErrQueueClosed is returned directly)
func TestPool_AfterPut_RejectsDestroysAndPassthrough(t *testing.T) {
	errRejected := errors.New("txn still open")
	closed := &closedRecorder{}
	cb := &errCapturingCallback{}
	var hookCalls atomic.Int64

	conf := conecta.NewConfig().
		WithMaxSize(4).
		WithScanInterval(10000).
		WithAfterPut(func(v any) error {
			hookCalls.Add(1)
			if v == "dirty" {
				return errRejected
			}
			return nil
		}).
		WithCloseFunc(func(v any) error {
			closed.add(v)
			return nil
		}).
		WithCallback(cb)
	p := mustNewPool(t, conf)
	defer p.Stop()

	// nil 检查先于钩子：Put(nil) 返回 ErrValueIsNil 且钩子未被调用
	// The nil check precedes the hook: Put(nil) returns ErrValueIsNil without invoking the hook
	if err := p.Put(nil); err != conecta.ErrValueIsNil {
		t.Fatalf("Put(nil) error = %v, want ErrValueIsNil", err)
	}
	if got := hookCalls.Load(); got != 0 {
		t.Fatalf("afterPut calls after Put(nil) = %d, want 0 (the nil check must precede the hook)", got)
	}

	// 拒绝路径：不入队 + 销毁 + 透传钩子错误本体
	// The rejection path: never enqueued + destroyed + the hook error passed through itself
	err := p.Put("dirty")
	if err != errRejected {
		t.Fatalf("Put(dirty) error = %v, want the bare hook error (== identity proves passthrough)", err)
	}
	if errors.Is(err, conecta.ErrPoolFull) {
		t.Fatalf("the hook rejection must be distinguishable from ErrPoolFull")
	}
	if got := closed.count("dirty"); got != 1 {
		t.Errorf("closeFunc calls for dirty = %d, want exactly 1", got)
	}
	if got := cb.closeN.Load(); got != 1 {
		t.Errorf("OnClose calls = %d, want 1", got)
	}
	if errs := cb.errs(); len(errs) != 1 || errs[0] != errRejected {
		t.Errorf("OnClose errors = %v, want exactly [the bare hook error]", errs)
	}
	if got := p.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0 (a rejected object must not be enqueued)", got)
	}

	// 通过路径：正常入队 / The acceptance path: enqueued normally
	if err := p.Put("clean"); err != nil {
		t.Fatalf("Put(clean) error = %v", err)
	}
	if got := p.Len(); got != 1 {
		t.Errorf("Len() = %d, want 1", got)
	}

	// Stop 后不执行钩子：直接 ErrQueueClosed（钩子调用数不再增长）
	// The hook never runs after Stop: ErrQueueClosed is returned directly (the hook call count
	// stops growing)
	before := hookCalls.Load()
	p.Stop()
	if err := p.Put("post-stop"); err != conecta.ErrQueueClosed {
		t.Fatalf("Put after Stop error = %v, want ErrQueueClosed", err)
	}
	if got := hookCalls.Load(); got != before {
		t.Errorf("afterPut calls after Stop grew to %d from %d, want unchanged (no hook run after Stop)", got, before)
	}
}

// TestPool_AfterPut_InFlightReleasedOnDestroy 验收准则 5：AfterPut 销毁释放在途配额——
// maxSize=1 的池中借出对象被钩子拒绝归还并销毁后，下一次 GetWithContext 必须能立即创建；
// 若销毁未递减在途（配额泄漏），探针将挂起直至 ctx 超时（测试的牙齿）
// TestPool_AfterPut_InFlightReleasedOnDestroy covers acceptance criterion 5: an AfterPut
// destruction releases the in-flight quota — in a maxSize=1 pool, once a borrowed object is
// rejected on return and destroyed, the next GetWithContext must create immediately; had the
// destruction not decremented the counter (a quota leak), the probe would suspend until the ctx
// timeout (the test's teeth)
func TestPool_AfterPut_InFlightReleasedOnDestroy(t *testing.T) {
	errRejected := errors.New("conn broken by user")
	var calls, closed atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(1).
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", calls.Add(1)), nil
		}).
		WithAfterPut(func(v any) error {
			if v == "obj-1" {
				return errRejected
			}
			return nil
		}).
		WithCloseFunc(func(any) error {
			closed.Add(1)
			return nil
		})
	p := mustNewPool(t, conf)
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// GWC 创建 obj-1（inFlight=1=maxSize）；归还被钩子拒绝 → 销毁 + releaseSlot（inFlight 1→0）
	// GWC creates obj-1 (inFlight=1=maxSize); the return is rejected by the hook → destruction +
	// releaseSlot (inFlight 1→0)
	v, err := p.GetWithContext(ctx)
	if err != nil || v != "obj-1" {
		t.Fatalf("GetWithContext #1 = (%v, %v), want (obj-1, nil)", v, err)
	}
	if err := p.Put(v); err != errRejected {
		t.Fatalf("Put(obj-1) error = %v, want the bare hook error", err)
	}
	if got := closed.Load(); got != 1 {
		t.Errorf("closeFunc calls = %d, want exactly 1", got)
	}

	// 牙齿：配额已释放 → 第二次 GWC 立即创建 obj-2；若泄漏则挂起至 ctx 超时
	// Teeth: the quota was released → the second GWC creates obj-2 immediately; a leak would
	// suspend until the ctx timeout
	v2, err := p.GetWithContext(ctx)
	if err != nil || v2 != "obj-2" {
		t.Fatalf("GetWithContext #2 = (%v, %v), want (obj-2, nil) — the AfterPut destruction must release the in-flight quota", v2, err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("newFunc calls = %d, want 2", got)
	}
	// 归还义务收尾（obj-2 被钩子放行）/ Return-obligation cleanup (obj-2 passes the hook)
	if err := p.Put(v2); err != nil {
		t.Fatalf("Put(obj-2) error = %v", err)
	}
}

// TestPool_AfterPut_RejectionKeepsActiveCount N2（B3 审计 §3-N2）：AfterPut 钩子拒绝销毁后
// ActiveCount 不得漂移——钩子拒绝时调用方已履行归还义务、对象所有权已移交回池（池选择销毁
// 而非入队），该次归还必须计入 TotalReturned，否则借出→拒绝销毁的 ActiveCount=
// TotalBorrowed-TotalReturned 派生值永久 +1（ActiveCount 是泄漏检测核心指标，无界虚高 →
// 假泄漏告警/真泄漏被淹没）。对照 BeforeGet 拒绝（钩子在 totalBorrowed+1 之前执行、从未
// 入账故无漂移），本测试钉住 AfterPut 两侧的账本对称：钩子拒绝销毁计 returned（所有权已
// 转移）；ErrPoolFull 拒绝不计（所有权未转移——由既有子测试 "full put rejection is counted
// without polluting the ledger" 钉住）；外部对象拒绝与外部对象成功归还同口径（D2 既有边界：
// 无配对借出 → 派生值可为负，属文档化语义而非缺陷）。
// 牙齿（mutation）：删除拒绝分支的 totalReturned.Add(1) → TotalReturned==0、ActiveCount==1 → FAIL
// TestPool_AfterPut_RejectionKeepsActiveCount covers N2 (B3 audit §3-N2): ActiveCount must not
// drift after an AfterPut hook rejection destroys the object — on a hook rejection the caller has
// already fulfilled its return obligation and ownership has transferred back to the pool (which
// chose destruction over enqueue), so the return must book into TotalReturned; otherwise the
// derived ActiveCount=TotalBorrowed-TotalReturned drifts +1 permanently per borrow→reject cycle
// (ActiveCount is the core leak-detection metric; unbounded inflation means false leak alarms and
// drowned real leaks). Contrasting the BeforeGet rejection (the hook runs before totalBorrowed+1
// and never books, so it cannot drift), this test pins the ledger symmetry of both AfterPut
// outcomes: a hook-rejected destruction books the return (ownership transferred); an ErrPoolFull
// rejection books nothing (ownership never transfers — pinned by the existing subtest "full put
// rejection is counted without polluting the ledger"); a foreign-object rejection follows the
// same convention as a successful foreign return (the existing D2 boundary: no paired borrow, so
// the derived value may go negative — documented semantics, not a defect).
// Teeth (mutation): removing totalReturned.Add(1) from the rejection branch → TotalReturned==0
// and ActiveCount==1 → FAIL
func TestPool_AfterPut_RejectionKeepsActiveCount(t *testing.T) {
	errRejected := errors.New("dirty connection")
	var calls atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(2).
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", calls.Add(1)), nil
		}).
		WithAfterPut(func(v any) error {
			if v == "obj-1" || v == "foreign" {
				return errRejected
			}
			return nil
		}).
		WithCloseFunc(func(any) error { return nil })
	p := mustNewPool(t, conf)
	defer p.Stop()

	ctx := context.Background()
	// 池自建对象：GWC 创建直交 obj-1（borrowed+1）→ 归还被钩子拒绝销毁（returned 必须 +1）
	// A pool-created object: the GWC creation hands over obj-1 (borrowed+1) → the return is
	// rejected by the hook and destroyed (returned must +1)
	v, err := p.GetWithContext(ctx)
	if err != nil || v != "obj-1" {
		t.Fatalf("GetWithContext #1 = (%v, %v), want (obj-1, nil)", v, err)
	}
	if err := p.Put(v); err != errRejected {
		t.Fatalf("Put(obj-1) error = %v, want the bare hook error", err)
	}
	s := p.Stats()
	if s.TotalBorrowed != 1 || s.TotalReturned != 1 {
		t.Errorf("(TotalBorrowed, TotalReturned) = (%d, %d), want (1, 1): the hook-rejected return still books — ownership transferred back to the pool", s.TotalBorrowed, s.TotalReturned)
	}
	if s.ActiveCount != 0 {
		t.Errorf("ActiveCount = %d, want 0: a hook rejection must never drift the derived value (N2)", s.ActiveCount)
	}
	if s.DestroyedByBorrow != 1 || s.TotalDestroyed != 1 {
		t.Errorf("(DestroyedByBorrow, TotalDestroyed) = (%d, %d), want (1, 1)", s.DestroyedByBorrow, s.TotalDestroyed)
	}
	assertDestroyIdentity(t, s, "after the hook-rejected return")

	// 对照臂 1：钩子放行的归还同样入账（两条路径账本口径对称）
	// Control arm 1: a hook-accepted return books alike (both paths share one ledger convention)
	v2, err := p.GetWithContext(ctx)
	if err != nil || v2 != "obj-2" {
		t.Fatalf("GetWithContext #2 = (%v, %v), want (obj-2, nil)", v2, err)
	}
	if err := p.Put(v2); err != nil {
		t.Fatalf("Put(obj-2) error = %v", err)
	}
	if s := p.Stats(); s.TotalReturned != 2 || s.ActiveCount != 0 {
		t.Errorf("(TotalReturned, ActiveCount) = (%d, %d), want (2, 0)", s.TotalReturned, s.ActiveCount)
	}

	// 对照臂 2：外部对象被拒与外部对象成功归还同口径——同样计 returned；无配对借出使
	// ActiveCount 落到 -1（D2 既有文档化边界，与成功路径的负漂对称，非本修复缺陷）
	// Control arm 2: a rejected foreign object follows the same convention as a successful
	// foreign return — it equally books the return; with no paired borrow ActiveCount lands at
	// -1 (the existing documented D2 boundary, symmetric with the success path's negative drift,
	// not a defect of this fix)
	if err := p.Put("foreign"); err != errRejected {
		t.Fatalf("Put(foreign) error = %v, want the bare hook error", err)
	}
	s = p.Stats()
	if s.TotalReturned != 3 || s.ActiveCount != -1 {
		t.Errorf("(TotalReturned, ActiveCount) = (%d, %d), want (3, -1): the rejected foreign return books symmetrically with an accepted one (the documented D2 boundary)", s.TotalReturned, s.ActiveCount)
	}
	if s.DestroyedByBorrow != 2 || s.TotalDestroyed != 2 {
		t.Errorf("(DestroyedByBorrow, TotalDestroyed) = (%d, %d), want (2, 2)", s.DestroyedByBorrow, s.TotalDestroyed)
	}
	assertDestroyIdentity(t, s, "after the foreign hook-rejected return")
}

// TestPool_TestOnBorrow_PresetUsesPingFunc 验收准则 3：testOnBorrow=true 且未设 beforeGet →
// normalizeConfig 自动注入 pingFunc(element, 0) 校验作为 BeforeGet 预设：ping 失败的对象在
// 借出前被销毁并尝试队列下一个；OnClose 收到内部哨兵 errBorrowCheckFailed（非导出，经错误
// 文本断言）
// TestPool_TestOnBorrow_PresetUsesPingFunc covers acceptance criterion 3: testOnBorrow=true with
// no beforeGet set → normalizeConfig injects the pingFunc(element, 0) check as the BeforeGet
// preset: an object failing the ping is destroyed before lending and the next queued one is tried;
// OnClose receives the internal sentinel errBorrowCheckFailed (unexported, asserted via its error
// text)
func TestPool_TestOnBorrow_PresetUsesPingFunc(t *testing.T) {
	closed := &closedRecorder{}
	cb := &errCapturingCallback{}
	var pingCalls atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(8).
		WithScanInterval(10000).
		WithTestOnBorrow(true).
		WithPingFunc(func(v any, retry int) bool {
			pingCalls.Add(1)
			if retry != 0 {
				t.Errorf("preset ping retryCount = %d, want 0 (a borrow check is a one-shot verdict)", retry)
			}
			return v != "bad"
		}).
		WithCloseFunc(func(v any) error {
			closed.add(v)
			return nil
		}).
		WithCallback(cb)
	p := mustNewPool(t, conf)
	defer p.Stop()

	for _, v := range []string{"bad", "good"} {
		if err := p.Put(v); err != nil {
			t.Fatalf("Put(%q) error = %v", v, err)
		}
	}

	v, err := p.Get()
	if err != nil || v != "good" {
		t.Fatalf("Get() = (%v, %v), want (good, nil) — the failing object must be destroyed before lending", v, err)
	}
	if got := closed.count("bad"); got != 1 {
		t.Errorf("closeFunc calls for bad = %d, want exactly 1", got)
	}
	if got := closed.count("good"); got != 0 {
		t.Errorf("closeFunc calls for good = %d, want 0", got)
	}
	// 预设钩子的错误是内部哨兵（文本断言；不导出、不进入公共错误契约）
	// The preset hook's error is the internal sentinel (asserted by text; unexported, outside the
	// public error contract)
	if errs := cb.errs(); len(errs) != 1 || errs[0] == nil || errs[0].Error() != "borrow check failed" {
		t.Errorf("OnClose errors = %v, want exactly one \"borrow check failed\"", errs)
	}
	// 借出校验各一次（bad 失败、good 通过）
	// One borrow check each (bad fails, good passes)
	if got := pingCalls.Load(); got != 2 {
		t.Errorf("pingFunc calls = %d, want 2", got)
	}
}

// TestPool_TestOnBorrow_UserBeforeGetTakesPrecedence 验收准则 3（优先级臂）：beforeGet 已设置
// 时 testOnBorrow 不覆盖（预设不注入，pingFunc 不参与借出判定）——用户钩子优先
// TestPool_TestOnBorrow_UserBeforeGetTakesPrecedence covers acceptance criterion 3 (the precedence
// arm): once beforeGet is set, testOnBorrow never overrides it (no preset injection; pingFunc
// plays no part in the borrow decision) — the user hook takes precedence
func TestPool_TestOnBorrow_UserBeforeGetTakesPrecedence(t *testing.T) {
	closed := &closedRecorder{}
	var pingCalls, hookCalls atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(8).
		WithScanInterval(10000).
		WithTestOnBorrow(true).
		WithPingFunc(func(any, int) bool {
			pingCalls.Add(1)
			return false // 若预设被注入，所有对象都会被判不健康并销毁
			// had the preset been injected, every object would fail and be destroyed
		}).
		WithBeforeGet(func(any) error {
			hookCalls.Add(1)
			return nil // 用户钩子放行一切 / the user hook accepts everything
		}).
		WithCloseFunc(func(v any) error {
			closed.add(v)
			return nil
		})
	p := mustNewPool(t, conf)
	defer p.Stop()

	// scanInterval=10000 使 supervise 在测试窗口内不运行，pingCalls 只可能来自借出预设
	// scanInterval=10000 keeps supervise out of the test window, so pingCalls could only come from
	// the borrow preset
	if err := p.Put("x"); err != nil {
		t.Fatalf("Put(x) error = %v", err)
	}
	v, err := p.Get()
	if err != nil || v != "x" {
		t.Fatalf("Get() = (%v, %v), want (x, nil)", v, err)
	}
	if got := hookCalls.Load(); got != 1 {
		t.Errorf("beforeGet calls = %d, want 1 (the user hook ran)", got)
	}
	if got := pingCalls.Load(); got != 0 {
		t.Errorf("pingFunc calls = %d, want 0 (the preset must not be injected over a user hook)", got)
	}
	if got := closed.total(); got != 0 {
		t.Errorf("closeFunc calls = %d, want 0", got)
	}
}

// TestPool_Hook_PanicConvertedToError 验收准则 4（panic 臂）：BeforeGet/AfterPut panic →
// callHook 的 defer recover 把 panic 转为携带 panic 值的 error（不吞上下文）、对象走对应销毁
// 路径、池不崩溃且后续完全可用——钩子自身的 bug 不拖垮整个池
// TestPool_Hook_PanicConvertedToError covers acceptance criterion 4 (the panic arm): a panicking
// BeforeGet/AfterPut → callHook's deferred recover converts the panic into an error carrying the
// panic value (the context is never swallowed), the object goes down its destruction path, and the
// pool neither crashes nor degrades afterwards — a bug in the hook itself never takes the pool down
func TestPool_Hook_PanicConvertedToError(t *testing.T) {
	t.Run("panicking beforeGet destroys the object and the borrow continues", func(t *testing.T) {
		const panicValue = "boom-get"
		closed := &closedRecorder{}
		cb := &errCapturingCallback{}
		var hookCalls atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(8).
			WithScanInterval(10000).
			WithBeforeGet(func(any) error {
				if hookCalls.Add(1) == 1 {
					panic(panicValue)
				}
				return nil
			}).
			WithCloseFunc(func(v any) error {
				closed.add(v)
				return nil
			}).
			WithCallback(cb)
		p := mustNewPool(t, conf)
		defer p.Stop()

		for _, v := range []string{"p-1", "ok-2"} {
			if err := p.Put(v); err != nil {
				t.Fatalf("Put(%q) error = %v", v, err)
			}
		}

		// 第一次借出触发 panic → 转 error → 销毁 p-1 → 继续尝试 → 拿到 ok-2（池不崩溃）
		// The first borrow triggers the panic → converted to an error → p-1 destroyed → the loop
		// continues → ok-2 is obtained (the pool survives)
		v, err := p.Get()
		if err != nil || v != "ok-2" {
			t.Fatalf("Get() = (%v, %v), want (ok-2, nil)", v, err)
		}
		if got := closed.count("p-1"); got != 1 {
			t.Errorf("closeFunc calls for p-1 = %d, want exactly 1", got)
		}
		// OnClose 收到的错误必须携带 panic 值（真实上下文增量，不被吞掉）
		// The error OnClose received must carry the panic value (a real context increment, never
		// swallowed)
		errs := cb.errs()
		if len(errs) != 1 || errs[0] == nil || !strings.Contains(errs[0].Error(), panicValue) {
			t.Fatalf("OnClose errors = %v, want one error containing %q", errs, panicValue)
		}
		if !strings.Contains(errs[0].Error(), "beforeGet hook panicked") {
			t.Errorf("OnClose error = %q, want the \"beforeGet hook panicked\" context", errs[0].Error())
		}
		// 池仍完全可用：归还 + 再借出（钩子已不再 panic）
		// The pool is still fully functional: return + borrow again (the hook no longer panics)
		if err := p.Put(v); err != nil {
			t.Fatalf("Put(ok-2) error = %v", err)
		}
		if v2, err := p.Get(); err != nil || v2 != "ok-2" {
			t.Fatalf("second Get() = (%v, %v), want (ok-2, nil)", v2, err)
		}
	})

	t.Run("panicking afterPut destroys the object and Put passes the error through", func(t *testing.T) {
		const panicValue = "boom-put"
		closed := &closedRecorder{}
		var hookCalls atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(8).
			WithScanInterval(10000).
			WithAfterPut(func(any) error {
				if hookCalls.Add(1) == 1 {
					panic(panicValue)
				}
				return nil
			}).
			WithCloseFunc(func(v any) error {
				closed.add(v)
				return nil
			})
		p := mustNewPool(t, conf)
		defer p.Stop()

		// Put 触发 panic → 转 error → 销毁 d-1 → Put 透传该 error（池不崩溃）
		// Put triggers the panic → converted to an error → d-1 destroyed → Put passes the error
		// through (the pool survives)
		err := p.Put("d-1")
		if err == nil || !strings.Contains(err.Error(), panicValue) {
			t.Fatalf("Put(d-1) error = %v, want an error containing %q", err, panicValue)
		}
		if !strings.Contains(err.Error(), "afterPut hook panicked") {
			t.Errorf("Put error = %q, want the \"afterPut hook panicked\" context", err.Error())
		}
		if got := closed.count("d-1"); got != 1 {
			t.Errorf("closeFunc calls for d-1 = %d, want exactly 1", got)
		}
		if got := p.Len(); got != 0 {
			t.Errorf("Len() = %d, want 0 (a rejected object must not be enqueued)", got)
		}
		// 池仍完全可用：后续 Put 正常入队（钩子已不再 panic）
		// The pool is still fully functional: a later Put enqueues normally (the hook no longer
		// panics)
		if err := p.Put("d-2"); err != nil {
			t.Fatalf("Put(d-2) error = %v", err)
		}
		if got := p.Len(); got != 1 {
			t.Errorf("Len() = %d, want 1", got)
		}
	})
}

// TestPool_Hook_RunsWithoutPoolLocks 验收准则 4（不持锁臂）：钩子调用发生在所有池内锁之外
// （stopGate/queue.lock/element.mu/waitersMu 全释放）——钩子内调用只读池 API（Len）不死锁。
// 若实现回归为持锁调用钩子，本测试将因 Len 的队列锁重入而挂死（超时即失败）。
// 注意：这只钉住只读安全；钩子内重入写操作 API（Get/Put/Stop 等）仍被文档明确禁止
// TestPool_Hook_RunsWithoutPoolLocks covers acceptance criterion 4 (the no-lock arm): hook
// invocations happen outside every pool lock (stopGate/queue.lock/element.mu/waitersMu all
// released) — a read-only pool API call (Len) inside the hook cannot deadlock. Had the
// implementation regressed into calling hooks under a lock, this test would hang on Len's
// queue-lock re-entry (a timeout fails it). Note: this only pins read-only safety; re-entering
// mutating pool APIs (Get/Put/Stop etc.) from a hook remains explicitly forbidden by the docs
func TestPool_Hook_RunsWithoutPoolLocks(t *testing.T) {
	var p *conecta.Pool
	var beforeLen, afterLen atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(8).
		WithScanInterval(10000).
		WithBeforeGet(func(any) error {
			beforeLen.Store(int64(p.Len())) // 持锁调用则死锁 / deadlocks if invoked under a lock
			return nil
		}).
		WithAfterPut(func(any) error {
			afterLen.Store(int64(p.Len()))
			return nil
		})
	p = mustNewPool(t, conf)
	defer p.Stop()

	done := make(chan error, 1)
	go func() {
		if err := p.Put("x"); err != nil {
			done <- fmt.Errorf("Put: %w", err)
			return
		}
		if _, err := p.Get(); err != nil {
			done <- fmt.Errorf("Get: %w", err)
			return
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("hook round trip failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hooks deadlocked on a read-only pool API call — hooks must run with no pool lock held")
	}
	// Put 钩子执行时对象尚未入队（Len=0）；Get 钩子执行时对象已离队（Len=0）——顺带钉住
	// 两个钩子的触发时机（入队前 / 离队后）
	// When the Put hook runs the object is not yet enqueued (Len=0); when the Get hook runs it has
	// already left the queue (Len=0) — incidentally pinning both hook trigger points (before the
	// enqueue / after the dequeue)
	if got := afterLen.Load(); got != 0 {
		t.Errorf("Len() inside afterPut = %d, want 0 (the hook runs before the enqueue)", got)
	}
	if got := beforeLen.Load(); got != 0 {
		t.Errorf("Len() inside beforeGet = %d, want 0 (the hook runs after the dequeue)", got)
	}
}

// TestPool_NewFunc_NilValueRejected D3（验收准则 7）：newFunc 返回 (nil, nil) 时三条创建路径
// （initialize / GetOrCreate / boundedCreate）一致返回明确的 ErrValueIsNil，绝不静默计数——
// born-nil 对象会打破“nil ⟹ 配额已释放”的销毁豁免前提，令在途配额永久泄漏。
// 泄漏探针：每个子测试随后用 GetWithContext 验证在途配额未被 born-nil 污染
// （泄漏则挂起直至 ctx 超时——测试的牙齿）
// TestPool_NewFunc_NilValueRejected covers D3 (acceptance criterion 7): when newFunc returns
// (nil, nil), all three creation paths (initialize / GetOrCreate / boundedCreate) consistently
// return an explicit ErrValueIsNil and never count it silently — a born-nil object would break the
// "nil ⟹ quota already released" destruction exemption premise and leak its in-flight slot
// permanently. Leak probes: each subtest then uses GetWithContext to verify the in-flight quota
// was not polluted by the born-nil value (a leak would suspend until the ctx timeout — the test's
// teeth)
func TestPool_NewFunc_NilValueRejected(t *testing.T) {
	t.Run("initialize path fails New with ErrValueIsNil", func(t *testing.T) {
		var closed atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(4).
			WithInitialize(1).
			WithScanInterval(10000).
			WithNewFunc(func() (any, error) { return nil, nil }).
			WithCloseFunc(func(any) error {
				closed.Add(1)
				return nil
			})
		p, err := conecta.New(wkq.NewQueue(nil), conf)
		if err != conecta.ErrValueIsNil {
			t.Fatalf("New() error = %v, want the bare ErrValueIsNil", err)
		}
		if p != nil {
			t.Fatalf("New() returned a non-nil pool with an error")
		}
		// born-nil 从未计数入队，也不应触发 closeFunc（nil 无可关闭）
		// the born-nil value was never counted or enqueued, and closeFunc must not fire for it
		// (there is nothing to close in a nil)
		if got := closed.Load(); got != 0 {
			t.Errorf("closeFunc calls = %d, want 0", got)
		}
	})

	t.Run("GetOrCreate path returns ErrValueIsNil without leaking quota", func(t *testing.T) {
		var calls atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(1).
			WithScanInterval(10000).
			WithNewFunc(func() (any, error) {
				if calls.Add(1) == 1 {
					return nil, nil // born-nil / born-nil
				}
				return fmt.Sprintf("obj-%d", calls.Load()), nil
			})
		p := mustNewPool(t, conf)
		defer p.Stop()

		v, err := p.GetOrCreate()
		if err != conecta.ErrValueIsNil {
			t.Fatalf("GetOrCreate() error = %v, want the bare ErrValueIsNil", err)
		}
		if v != nil {
			t.Fatalf("GetOrCreate() value = %v, want nil", v)
		}

		// 泄漏探针：born-nil 未 +1 → maxSize=1 的配额完好 → GWC 立即创建成功；
		// 若泄漏（旧缺陷形态），inFlight 滞留 1=maxSize → 挂起至 ctx 超时
		// Leak probe: the born-nil value was never +1'd → the maxSize=1 quota is intact → GWC
		// creates immediately; a leak (the old defect shape) would stick inFlight at 1=maxSize →
		// suspension until the ctx timeout
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		v2, err := p.GetWithContext(ctx)
		if err != nil || v2 != "obj-2" {
			t.Fatalf("GetWithContext() = (%v, %v), want (obj-2, nil) — the born-nil value must not consume in-flight quota", v2, err)
		}
		if err := p.Put(v2); err != nil {
			t.Fatalf("Put(obj-2) error = %v", err)
		}
	})

	t.Run("boundedCreate path returns ErrValueIsNil and releases the reservation", func(t *testing.T) {
		var calls atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(1).
			WithScanInterval(10000).
			WithNewFunc(func() (any, error) {
				if calls.Add(1) == 1 {
					return nil, nil // born-nil / born-nil
				}
				return "healthy", nil
			})
		p := mustNewPool(t, conf)
		defer p.Stop()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// GWC 的有界创建路径：预留成功 → newFunc 返回 (nil,nil) → ErrValueIsNil，
		// 预留由 defer 恰好释放一次
		// GWC's bounded-creation path: the reservation succeeds → newFunc returns (nil,nil) →
		// ErrValueIsNil, and the defer releases the reservation exactly once
		v, err := p.GetWithContext(ctx)
		if err != conecta.ErrValueIsNil {
			t.Fatalf("GetWithContext #1 error = %v, want the bare ErrValueIsNil", err)
		}
		if v != nil {
			t.Fatalf("GetWithContext #1 value = %v, want nil", v)
		}

		// 泄漏探针：预留已释放 → 第二次 GWC 立即创建成功；若 defer 未释放（created 误置 true
		// 的形态），inFlight 滞留 1=maxSize → 挂起至 ctx 超时
		// Leak probe: the reservation was released → the second GWC creates immediately; had the
		// defer not released it (the created-wrongly-true shape), inFlight would stick at
		// 1=maxSize → suspension until the ctx timeout
		v2, err := p.GetWithContext(ctx)
		if err != nil || v2 != "healthy" {
			t.Fatalf("GetWithContext #2 = (%v, %v), want (healthy, nil) — the born-nil failure must release the reservation", v2, err)
		}
		if got := calls.Load(); got != 2 {
			t.Errorf("newFunc calls = %d, want 2", got)
		}
		if err := p.Put(v2); err != nil {
			t.Fatalf("Put(healthy) error = %v", err)
		}
	})
}

// TestPool_GetOrCreate_InFlightCounted D4（验收准则 8）：GetOrCreate 的按需创建对象计入在途
// 配额（A1 突破语义下 inFlight 仍 +1）——杀死“删除 GetOrCreate 的 inFlight.Add(1)”这一
// mutation：maxSize=1 下 GetOrCreate 持有唯一对象后，探针 GetWithContext 必须挂起（配额已
// 耗尽）；若突破对象未计数，探针会立即创建第 2 个对象，assertSuspended 捕获并 Fatal
// TestPool_GetOrCreate_InFlightCounted covers D4 (acceptance criterion 8): an object created
// on-demand by GetOrCreate counts toward the in-flight quota (inFlight is still +1 under the A1
// break-through semantics) — this kills the "remove GetOrCreate's inFlight.Add(1)" mutation: with
// maxSize=1, once GetOrCreate holds the only object, a probe GetWithContext must suspend (the
// quota is exhausted); had the break-through object not been counted, the probe would immediately
// create a second object and assertSuspended would catch it and Fatal
func TestPool_GetOrCreate_InFlightCounted(t *testing.T) {
	var calls atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(1).
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", calls.Add(1)), nil
		})
	p := mustNewPool(t, conf)
	defer p.Stop()

	// GetOrCreate 空池创建 obj-1 并持有（inFlight 应=1=maxSize）
	// GetOrCreate creates obj-1 on the empty pool and holds it (inFlight should be 1=maxSize)
	v, err := p.GetOrCreate()
	if err != nil || v != "obj-1" {
		t.Fatalf("GetOrCreate() = (%v, %v), want (obj-1, nil)", v, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("newFunc calls = %d, want 1", got)
	}

	// 探针必须挂起：突破对象占用了唯一在途配额（mutation 删除 +1 时此处立即创建 obj-2 → Fatal）
	// The probe must suspend: the break-through object occupies the only in-flight quota (with the
	// +1 mutated away, this would immediately create obj-2 → Fatal)
	pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pcancel()
	probe := startAcquire(p, pctx)
	assertSuspended(t, probe)
	if got := calls.Load(); got != 1 {
		t.Fatalf("newFunc calls = %d, want 1 (a suspended probe must not create)", got)
	}

	// 归还 obj-1 → 唤醒探针 → 借到同一对象（借出/归还不改变在途计数）
	// Returning obj-1 wakes the probe, which borrows the same object (lending/returning never
	// changes the in-flight count)
	if err := p.Put(v); err != nil {
		t.Fatalf("Put(obj-1) error = %v", err)
	}
	res := awaitResult(t, probe, 5*time.Second)
	if res.err != nil || res.value != v {
		t.Fatalf("probe = (%v, %v), want (obj-1, nil)", res.value, res.err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("newFunc calls = %d, want 1 (the probe must borrow, never create)", got)
	}

	// 归还义务收尾 / Return-obligation cleanup
	if err := p.Put(res.value); err != nil {
		t.Fatalf("Put(cleanup) error = %v", err)
	}
}

// ----------------------------------------------------------------------------
// 水位管理（MinIdle 异步补齐 / MaxIdleTime 空闲回收）与 Stats 可观测性（Batch3b）
// Water-level management (MinIdle async refill / MaxIdleTime idle reclaim) and Stats observability (Batch3b)
//
// 本节用例复用既有辅助设施（waitUntil/mustNewPool/gatedCreator/startAcquire/assertSuspended/
// awaitResult/closedRecorder/countingCallback），时间窗口沿用 B1 裁决 D5：短寿命(≈50-400ms) +
// MinScanInterval + Eventually 断言，不引入 clock 抽象。Stats() 暴露后，B1 A4 裁决延迟的
// 恒等式直接断言（destroyedBy* 分类数值）在本节落地，不再仅靠行为代理。
// The cases in this section reuse the existing helpers (waitUntil/mustNewPool/gatedCreator/
// startAcquire/assertSuspended/awaitResult/closedRecorder/countingCallback), and the timing windows
// follow B1's D5 ruling: short durations (≈50-400ms) + MinScanInterval + Eventually assertions,
// with no clock abstraction. With Stats() exposed, the direct identity assertions deferred by B1's
// A4 ruling (the destroyedBy* category values) land in this section instead of behavioral proxies.
// ----------------------------------------------------------------------------

// assertDestroyIdentity 直接数值断言五分类恒等式（B3a deferred 义务 → B3b 经 Stats() 落地）：
// TotalDestroyed == DestroyedByPing + DestroyedByLifetime + DestroyedByIdle + DestroyedByBorrow +
// DestroyedByStop。调用时池须处于静止态（无并发销毁在途），快照才精确满足恒等式
// assertDestroyIdentity directly asserts the five-category identity numerically (B3a's deferred
// obligation → landed in B3b via Stats()): TotalDestroyed == DestroyedByPing + DestroyedByLifetime
// + DestroyedByIdle + DestroyedByBorrow + DestroyedByStop. The pool must be quiescent at the call
// site (no concurrent destruction in flight) for the snapshot to satisfy the identity exactly
func assertDestroyIdentity(t *testing.T, s conecta.Stats, stage string) {
	t.Helper()
	if sum := s.DestroyedByPing + s.DestroyedByLifetime + s.DestroyedByIdle +
		s.DestroyedByBorrow + s.DestroyedByStop; s.TotalDestroyed != sum {
		t.Errorf("%s: TotalDestroyed=%d != sum of categories=%d (ping=%d lifetime=%d idle=%d borrow=%d stop=%d)",
			stage, s.TotalDestroyed, sum, s.DestroyedByPing, s.DestroyedByLifetime,
			s.DestroyedByIdle, s.DestroyedByBorrow, s.DestroyedByStop)
	}
}

// TestPool_MinIdle_RefillRestoresWaterLevel 验收准则 1（补齐主臂）：有效空闲低于 minIdle 时，
// supervise 末尾的异步补齐把水位恢复到 minIdle——启动空池（水位 0）与借出降水位两个形态；
// 补齐对象计入 TotalCreated 且 ActiveCount/IdleCount 水位随之正确
// TestPool_MinIdle_RefillRestoresWaterLevel covers acceptance criterion 1 (the main refill arm):
// when the effective idle count drops below minIdle, the async refill triggered at the end of
// supervise restores the water level to minIdle — both from an empty start (level 0) and after a
// borrow lowers the level; refill objects count into TotalCreated and the ActiveCount/IdleCount
// water levels follow correctly
func TestPool_MinIdle_RefillRestoresWaterLevel(t *testing.T) {
	var created atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(4).
		WithMinIdle(2).
		WithScanInterval(conecta.MinScanInterval).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", created.Add(1)), nil
		}).
		WithPingFunc(func(any, int) bool { return true })
	p := mustNewPool(t, conf)
	defer p.Stop()

	// 启动水位 0：首轮扫描 tick（300ms）触发 deficit=2 的补齐
	// Start at level 0: the first scan tick (300ms) triggers a deficit=2 refill
	waitUntil(t, 5*time.Second, 20*time.Millisecond,
		func() bool { return p.Stats().IdleCount == 2 },
		"refill did not restore the idle water level to minIdle=2")

	s := p.Stats()
	if s.TotalCreated != 2 {
		t.Errorf("TotalCreated = %d, want 2 (refill objects must be counted)", s.TotalCreated)
	}
	if s.MinIdle != 2 || s.MaxSize != 4 {
		t.Errorf("(MaxSize, MinIdle) = (%d, %d), want (4, 2)", s.MaxSize, s.MinIdle)
	}
	if s.ActiveCount != 0 || s.TombstoneCount != 0 {
		t.Errorf("(ActiveCount, TombstoneCount) = (%d, %d), want (0, 0)", s.ActiveCount, s.TombstoneCount)
	}
	assertDestroyIdentity(t, s, "after the startup refill")

	// 借出一个 → 有效空闲 1 < minIdle=2 → 下轮扫描补齐回 2（借出不改变在途计数）
	// Borrow one → effective idle 1 < minIdle=2 → the next scan refills back to 2 (a borrow never
	// changes the in-flight count)
	v, err := p.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	waitUntil(t, 5*time.Second, 20*time.Millisecond,
		func() bool { return p.Stats().IdleCount == 2 },
		"refill did not restore the water level after a borrow")

	s = p.Stats()
	if s.TotalCreated != 3 {
		t.Errorf("TotalCreated = %d, want 3 (one refill after the borrow)", s.TotalCreated)
	}
	if s.ActiveCount != 1 {
		t.Errorf("ActiveCount = %d, want 1 (the borrowed object is still out)", s.ActiveCount)
	}
	if s.TotalBorrowed != 1 || s.TotalReturned != 0 {
		t.Errorf("(TotalBorrowed, TotalReturned) = (%d, %d), want (1, 0)", s.TotalBorrowed, s.TotalReturned)
	}

	// 归还后水位 3 > minIdle：minIdle 是下限不是上限，不得触发任何销毁或过冲创建
	//（削水位是 MaxIdleTime 的职责）
	// After the return the level is 3 > minIdle: minIdle is a floor, not a ceiling — no destruction
	// or overshoot creation may happen (trimming the level down is MaxIdleTime's job)
	if err := p.Put(v); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	time.Sleep(700 * time.Millisecond) // 覆盖两轮以上扫描 / covers more than two scan passes
	s = p.Stats()
	if s.TotalCreated != 3 || s.TotalDestroyed != 0 {
		t.Errorf("(TotalCreated, TotalDestroyed) = (%d, %d), want (3, 0): above the floor nothing happens",
			s.TotalCreated, s.TotalDestroyed)
	}
	if s.IdleCount != 3 || s.ActiveCount != 0 {
		t.Errorf("(IdleCount, ActiveCount) = (%d, %d), want (3, 0)", s.IdleCount, s.ActiveCount)
	}
}

// TestPool_MinIdle_RefillRespectsInFlightCap 验收准则 1（在途封顶臂）：补齐必经 reserveSlot——
// deficit>0 但在途配额被借出对象占满时，补齐绝不创建（否则突破 maxSize 在途封顶）；水位经
// 归还自然恢复后补齐正确停手（deficit=0）
// TestPool_MinIdle_RefillRespectsInFlightCap covers acceptance criterion 1 (the in-flight cap arm):
// a refill must go through reserveSlot — while deficit>0 but the in-flight quota is fully held by
// borrowed objects the refill creates nothing (otherwise it would break the maxSize in-flight
// cap); once the level recovers through returns the refill correctly stands down (deficit=0)
func TestPool_MinIdle_RefillRespectsInFlightCap(t *testing.T) {
	var calls atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(2).
		WithMinIdle(2).
		WithScanInterval(conecta.MinScanInterval).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", calls.Add(1)), nil
		}).
		WithPingFunc(func(any, int) bool { return true })
	p := mustNewPool(t, conf)
	defer p.Stop()

	ctx := context.Background()
	v1, err := p.GetWithContext(ctx)
	if err != nil {
		t.Fatalf("GetWithContext #1 error = %v", err)
	}
	v2, err := p.GetWithContext(ctx)
	if err != nil {
		t.Fatalf("GetWithContext #2 error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("newFunc calls = %d, want 2 (both objects created and held)", got)
	}

	// 两对象全部借出：idle=0，deficit=2，但 inFlight=2=maxSize——跨越多轮扫描补齐必须
	// 始终被 reserveSlot 拒绝（mutation：补齐绕过 reserveSlot 则 calls 增长 → FAIL）
	// Both objects are borrowed out: idle=0, deficit=2, but inFlight=2=maxSize — across several
	// scan passes the refill must stay rejected by reserveSlot (mutation: a refill bypassing
	// reserveSlot would grow calls → FAIL)
	time.Sleep(time.Second)
	if got := calls.Load(); got != 2 {
		t.Fatalf("newFunc calls = %d, want 2: the refill must never break the in-flight cap", got)
	}

	// 归还一个：idle=1，deficit=1，inFlight 仍=2（归还不改变在途）——补齐仍被封顶拒绝
	// Return one: idle=1, deficit=1, inFlight is still 2 (a return never changes it) — the refill
	// stays rejected by the cap
	if err := p.Put(v1); err != nil {
		t.Fatalf("Put(obj-1) error = %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if got := calls.Load(); got != 2 {
		t.Fatalf("newFunc calls = %d, want 2: a returned object already lifts the water level", got)
	}

	// 全部归还：idle=2=minIdle，deficit=0——补齐停手
	// Return everything: idle=2=minIdle, deficit=0 — the refill stands down
	if err := p.Put(v2); err != nil {
		t.Fatalf("Put(obj-2) error = %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if got := calls.Load(); got != 2 {
		t.Fatalf("newFunc calls = %d, want 2 (no creation once the water level is met)", got)
	}
	s := p.Stats()
	if s.IdleCount != 2 || s.TotalCreated != 2 || s.ActiveCount != 0 {
		t.Errorf("(IdleCount, TotalCreated, ActiveCount) = (%d, %d, %d), want (2, 2, 0)",
			s.IdleCount, s.TotalCreated, s.ActiveCount)
	}
	if s.TotalBorrowed != 2 || s.TotalReturned != 2 {
		t.Errorf("(TotalBorrowed, TotalReturned) = (%d, %d), want (2, 2): both creation hand-overs book as borrows",
			s.TotalBorrowed, s.TotalReturned)
	}
	assertDestroyIdentity(t, s, "cap-protected refill")
}

// TestPool_MinIdle_AllBorrowedPartialReturnWakesWaiter 验收准则 1（waiter 臂，dispatch 构造
// “全部借出→归还部分→waiter 唤醒”）：在途全部借出后 GetWithContext 挂起；部分归还经
// wake-after-enqueue 唤醒 waiter 借到归还对象；水位经归还恢复到 minIdle 后补齐正确停手。
// 注（N4 修正，B3 审计裁决 2 强化结论）：补齐入队点的 wakeWaiter 是竞态可达的承重信号，
// 绝非防御冗余——挂起 waiter 被前序 releaseSlot/Put 唤醒后与补齐 goroutine 竞争同一配额，
// 若补齐先赢（waiter 的有界创建得 errNoCapacity、复查见 inFlight==maxSize 且对象尚未入队
// 而重新挂起），补齐入队的这次唤醒是 waiter 在“对象已可借出”状态下唯一可感知的信号——
// 删除即真实丢失唤醒、无限期挂起（-count=100 概率性捕获，无确定性杀手测试）。它与
// tryAcquire tombstone 回收唤醒（被证明打不到挂起 waiter，属协议统一性保留）认识论地位
// 不同，机械纪律仍逐点配对
// TestPool_MinIdle_AllBorrowedPartialReturnWakesWaiter covers acceptance criterion 1 (the waiter
// arm, the dispatch's "all borrowed → partial return → waiter woken" construction): with every
// in-flight object borrowed a GetWithContext suspends; a partial return wakes the waiter via
// wake-after-enqueue, which borrows the returned object; once returns restore the level to minIdle
// the refill correctly stands down. Note (N4 fix, the strengthened conclusion of the B3 audit's
// ruling 2): the wakeWaiter at the refill enqueue point is a race-reachable LOAD-BEARING signal,
// never defensive redundancy — a suspended waiter woken by the preceding releaseSlot/Put races
// the refill goroutine for the same quota, and when the refill wins (the waiter's bounded
// creation gets errNoCapacity and its recheck sees inFlight==maxSize with the object not yet
// enqueued, so it re-suspends), the refill-enqueue wake is the only signal the waiter can ever
// observe while the object is borrowable — deleting it is a genuine lost wakeup and an indefinite
// suspension (caught probabilistically under -count=100; no deterministic killer test exists). It
// therefore holds a different epistemic status from the tryAcquire tombstone-reclaim wake (proven
// unable to hit a suspended waiter — kept for protocol uniformity), though the mechanical
// discipline still pairs every site)
func TestPool_MinIdle_AllBorrowedPartialReturnWakesWaiter(t *testing.T) {
	var calls atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(2).
		WithMinIdle(2).
		WithScanInterval(conecta.MinScanInterval).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", calls.Add(1)), nil
		}).
		WithPingFunc(func(any, int) bool { return true })
	p := mustNewPool(t, conf)
	defer p.Stop()

	ctx := context.Background()
	v1, err := p.GetWithContext(ctx)
	if err != nil {
		t.Fatalf("GetWithContext #1 error = %v", err)
	}
	v2, err := p.GetWithContext(ctx)
	if err != nil {
		t.Fatalf("GetWithContext #2 error = %v", err)
	}

	// 全部借出（inFlight=2=maxSize，队列空）→ 第三个获取挂起为 waiter
	// Everything borrowed (inFlight=2=maxSize, empty queue) → the third acquisition suspends
	wctx, wcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer wcancel()
	probe := startAcquire(p, wctx)
	assertSuspended(t, probe)
	if got := calls.Load(); got != 2 {
		t.Fatalf("newFunc calls = %d, want 2 (a suspended waiter must not create)", got)
	}

	// 归还部分 → wake-after-enqueue 唤醒 waiter → 借到归还对象（不新建）
	// Partial return → wake-after-enqueue wakes the waiter → it borrows the returned object (never
	// creates)
	if err := p.Put(v1); err != nil {
		t.Fatalf("Put(obj-1) error = %v", err)
	}
	res := awaitResult(t, probe, 5*time.Second)
	if res.err != nil || res.value != v1 {
		t.Fatalf("woken waiter = (%v, %v), want (obj-1, nil)", res.value, res.err)
	}

	// waiter 与另一借出者相继归还：水位经归还恢复到 minIdle=2，补齐无需介入（calls 不变）
	// The waiter and the other borrower return in turn: the returns restore the level to minIdle=2,
	// so the refill never intervenes (calls unchanged)
	if err := p.Put(res.value); err != nil {
		t.Fatalf("Put(woken value) error = %v", err)
	}
	if err := p.Put(v2); err != nil {
		t.Fatalf("Put(obj-2) error = %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if got := calls.Load(); got != 2 {
		t.Fatalf("newFunc calls = %d, want 2", got)
	}
	s := p.Stats()
	if s.IdleCount != 2 || s.ActiveCount != 0 || s.WaitCount != 1 {
		t.Errorf("(IdleCount, ActiveCount, WaitCount) = (%d, %d, %d), want (2, 0, 1)",
			s.IdleCount, s.ActiveCount, s.WaitCount)
	}
	assertDestroyIdentity(t, s, "all-borrowed partial-return flow")
}

// TestPool_MinIdle_StopReclaimsRefillGoroutine 验收准则 2（goroutine 零泄漏）：补齐 goroutine
// 必经 wg 跟踪 + ctx.Done 退出——① 补齐被门控 newFunc 钉住时 Stop 不得返回（wg.Wait 在等它，
// mutation：漏 wg.Add 则 Stop 提前完成 → Fatal）；② 放行后补齐完成当前轮、在下一轮的 ctx.Done
// 检查中途退出（deficit=4 也只创建 1 个，mutation：删除 ctx 检查则 calls 增至 4）；③ Stop 随后
// 正常完成，已入队对象由 Cleanup 关闭（destroyedByStop），Stats 恒等式闭合，Stop 后调用安全
// TestPool_MinIdle_StopReclaimsRefillGoroutine covers acceptance criterion 2 (zero goroutine
// leaks): the refill goroutine must be wg-tracked and exit via ctx.Done — ① while the refill is
// pinned inside a gated newFunc, Stop must not return (wg.Wait is waiting for it; mutation: a
// missing wg.Add lets Stop complete early → Fatal); ② once released, the refill finishes its
// current round and exits mid-flight at the next round's ctx.Done check (with deficit=4 only one
// object is created; mutation: removing the ctx check grows calls to 4); ③ Stop then completes
// normally, the enqueued object is closed by Cleanup (destroyedByStop), the Stats identity closes,
// and calling Stats after Stop is safe
func TestPool_MinIdle_StopReclaimsRefillGoroutine(t *testing.T) {
	gate := newGatedCreator()
	closed := &closedRecorder{}
	conf := conecta.NewConfig().
		WithMaxSize(4).
		WithMinIdle(4).
		WithScanInterval(conecta.MinScanInterval).
		WithNewFunc(gate.newFunc).
		WithCloseFunc(func(v any) error {
			closed.add(v)
			return nil
		}).
		WithPingFunc(func(any, int) bool { return true })
	gate.arm() // 首轮补齐的第一个创建即被门控钉住 / the first refill creation is pinned by the gate
	p := mustNewPool(t, conf)

	// 等待补齐 goroutine 进入门控 newFunc（此刻在途预留生效，wg 计数含 monitor+refill）
	// Wait until the refill goroutine enters the gated newFunc (the in-flight reservation is
	// active; the wg counter holds monitor+refill)
	gate.awaitPinned()
	if got := gate.calls.Load(); got != 1 {
		t.Fatalf("newFunc calls = %d, want 1 (the refill is pinned in the first creation)", got)
	}

	stopDone := make(chan struct{})
	go func() {
		p.Stop()
		close(stopDone)
	}()
	// ① 补齐钉住期间 Stop 不得完成（wg 跟踪的牙齿）
	// ① Stop must not complete while the refill is pinned (the teeth of the wg tracking)
	select {
	case <-stopDone:
		t.Fatal("Stop returned while the refill goroutine was pinned — it must be wg-tracked")
	case <-time.After(100 * time.Millisecond):
	}

	// ② 放行：当前创建成功入队；下一轮循环头的 ctx.Done 检查（Stop 已 cancel）使补齐中途退出
	// ② Release: the current creation succeeds and is enqueued; the next round's loop-head ctx.Done
	// check (Stop already cancelled) makes the refill exit mid-flight
	gate.open()
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not complete after the refill goroutine was released")
	}

	// ③ 中途退出证据：deficit=4 但只创建了 1 个；入队对象被 Cleanup 恰好关闭一次；账本闭合
	// ③ Mid-flight exit evidence: deficit=4 but only one object was created; the enqueued object
	// was closed exactly once by Cleanup; the ledger closes
	if got := gate.calls.Load(); got != 1 {
		t.Errorf("newFunc calls = %d, want 1: the refill must exit at the ctx.Done check after Stop", got)
	}
	if got := closed.count("created"); got != 1 {
		t.Errorf("closeFunc calls for the refilled object = %d, want exactly 1 (closed by Cleanup)", got)
	}
	s := p.Stats() // Stop 后调用安全 / safe to call after Stop
	if s.TotalCreated != 1 || s.DestroyedByStop != 1 || s.TotalDestroyed != 1 {
		t.Errorf("(TotalCreated, DestroyedByStop, TotalDestroyed) = (%d, %d, %d), want (1, 1, 1)",
			s.TotalCreated, s.DestroyedByStop, s.TotalDestroyed)
	}
	if s.IdleCount != 0 || s.TombstoneCount != 0 || s.ActiveCount != 0 {
		t.Errorf("(IdleCount, TombstoneCount, ActiveCount) = (%d, %d, %d), want (0, 0, 0)",
			s.IdleCount, s.TombstoneCount, s.ActiveCount)
	}
	assertDestroyIdentity(t, s, "after Stop reclaimed the refill goroutine")
}

// TestPool_MinIdle_RefillThrottleAndFailureBackoff 验收准则 3（节流 + 失败退避 + 预留不泄漏）：
// ① refilling CAS 节流——补齐被门控钉住跨越多个扫描 tick 时，绝不叠加第二个补齐 goroutine
// （mutation：删除 refilling 节流则钉住期间 calls 增长 → FAIL）；② 失败退避——newFunc 失败即
// 停止本轮（partial value 由池关闭恰好一次；失败创建不计 TotalCreated/TotalDestroyed），下个
// 扫描周期自然重试并最终收敛到水位；③ 失败对象的在途预留经 defer 恰好释放一次（mutation：
// 删除 releaseSlot 则 maxSize=1 探针挂起至 ctx 超时 → Fatal）
// TestPool_MinIdle_RefillThrottleAndFailureBackoff covers acceptance criterion 3 (throttle +
// failure backoff + no reservation leak): ① the refilling CAS throttle — while the refill is
// pinned across several scan ticks, a second refill goroutine never stacks up (mutation: removing
// the throttle grows calls during the pin → FAIL); ② failure backoff — a newFunc failure stops
// the round (the partial value is closed by the pool exactly once; a failed creation counts
// neither TotalCreated nor TotalDestroyed) and the next scan interval retries naturally until the
// level converges; ③ the failed object's in-flight reservation is released exactly once by the
// defer (mutation: removing releaseSlot suspends the maxSize=1 probe until the ctx timeout → Fatal)
func TestPool_MinIdle_RefillThrottleAndFailureBackoff(t *testing.T) {
	t.Run("throttle pins one refill and failure stops the round", func(t *testing.T) {
		gate := newGatedCreator()
		closed := &closedRecorder{}
		conf := conecta.NewConfig().
			WithMaxSize(4).
			WithMinIdle(2).
			WithScanInterval(conecta.MinScanInterval).
			WithNewFunc(gate.newFunc).
			WithCloseFunc(func(v any) error {
				closed.add(v)
				return nil
			}).
			WithPingFunc(func(any, int) bool { return true })
		gate.armFail() // 首轮补齐的第一个创解放行后失败 / the first refill creation fails once released
		p := mustNewPool(t, conf)
		defer func() {
			gate.tryOpen() // 清理兜底 / cleanup safety net
			p.Stop()
		}()

		gate.awaitPinned()
		// ① 节流：补齐钉住期间跨越多个扫描 tick（300ms 周期，睡 700ms 覆盖至少两轮），
		// refilling=true 使后续 tick 全部跳过启动——绝不叠加第二个补齐 goroutine
		// ① Throttle: while the refill is pinned, several scan ticks pass (a 300ms period; the
		// 700ms sleep covers at least two) — refilling=true makes every later tick skip the
		// launch, so a second refill goroutine never stacks up
		time.Sleep(700 * time.Millisecond)
		if got := gate.calls.Load(); got != 1 {
			t.Fatalf("newFunc calls during the pin = %d, want 1: refill goroutines must never stack", got)
		}

		// ② 放行 → newFunc 失败：partial value 由池关闭恰好一次（补齐路径无调用方移交所有权），
		// 本轮补齐即停（不在本轮重试到成功）；预留经 defer 释放
		// ② Release → newFunc fails: the pool closes the partial value exactly once (a refill has no
		// caller to hand ownership to), the round stops immediately (no retry-until-success within
		// the round), and the defer releases the reservation
		gate.open()
		waitUntil(t, 5*time.Second, 20*time.Millisecond,
			func() bool { return closed.count(gatedPartialValue) == 1 },
			"the failed creation's partial value was not closed exactly once")

		// 失败退避后的自然重试：下个扫描周期重算 deficit，后续创建（门控已消费，立即成功）
		// 把水位补齐到 minIdle=2；失败的那次不计数
		// Natural retry after the backoff: the next scan recomputes the deficit and the following
		// creations (the gate is consumed — they succeed immediately) lift the level to minIdle=2;
		// the failed attempt is never counted
		waitUntil(t, 5*time.Second, 20*time.Millisecond,
			func() bool {
				s := p.Stats()
				return s.IdleCount == 2 && s.TotalCreated == 2
			},
			"the refill did not converge to minIdle=2 after the failure backoff")

		if got := gate.calls.Load(); got != 3 {
			t.Errorf("newFunc calls = %d, want 3 (1 failed + 2 succeeded, one round per scan)", got)
		}
		if got := closed.count("created"); got != 0 {
			t.Errorf("closeFunc calls for successful refills = %d, want 0", got)
		}
		time.Sleep(400 * time.Millisecond) // 再跨一轮扫描确认水位稳定 / one more scan to confirm stability
		s := p.Stats()
		if s.TotalCreated != 2 || s.TotalDestroyed != 0 || s.IdleCount != 2 {
			t.Errorf("(TotalCreated, TotalDestroyed, IdleCount) = (%d, %d, %d), want (2, 0, 2): a failed creation counts as neither created nor destroyed",
				s.TotalCreated, s.TotalDestroyed, s.IdleCount)
		}
		assertDestroyIdentity(t, s, "after the failure backoff")
	})

	t.Run("failed refill releases the in-flight reservation", func(t *testing.T) {
		var calls atomic.Int64
		failFirst := atomic.Bool{}
		failFirst.Store(true)
		conf := conecta.NewConfig().
			WithMaxSize(1).
			WithMinIdle(1).
			WithScanInterval(conecta.MinScanInterval).
			WithNewFunc(func() (any, error) {
				if calls.Add(1) == 1 {
					if failFirst.CompareAndSwap(true, false) {
						return nil, errors.New("transient downstream failure")
					}
				}
				return fmt.Sprintf("obj-%d", calls.Load()), nil
			}).
			WithPingFunc(func(any, int) bool { return true })
		p := mustNewPool(t, conf)
		defer p.Stop()

		// 首轮补齐：reserveSlot（inFlight=1=maxSize）→ newFunc 失败 → defer releaseSlot → 退避
		// The first refill round: reserveSlot (inFlight=1=maxSize) → newFunc fails → the defer
		// releases → backoff
		waitUntil(t, 5*time.Second, 20*time.Millisecond,
			func() bool { return calls.Load() >= 1 },
			"the failing refill round never ran")

		// ③ 泄漏探针：失败预留若未释放，inFlight 永久滞留 1=maxSize——本 GWC 将挂起至 ctx
		// 超时（Fatal）。健康形态：配额完好，GWC 立即成功（借到补齐对象或创建新的，两种
		// 结果都证明 reserveSlot 可再次成功）
		// ③ Leak probe: had the failed reservation not been released, inFlight would stick at
		// 1=maxSize forever and this GWC would suspend until the ctx timeout (Fatal). In the
		// healthy shape the quota is intact and the GWC succeeds promptly (borrowing a refilled
		// object or creating a new one — either outcome proves reserveSlot succeeds again)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		v, err := p.GetWithContext(ctx)
		if err != nil {
			t.Fatalf("GetWithContext() error = %v — the failed refill must have released its reservation", err)
		}
		if v == nil {
			t.Fatal("GetWithContext() returned a nil value")
		}
		if err := p.Put(v); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
	})
}

// TestPool_MinIdle_LifetimeRotation_TombstoneGCBoundsSize N1 墓碑泵确定性回归（试金石，B3
// 审计 §3-N1 的参数化构造）：maxSize=minIdle=10 + maxLifetime=100ms + 零流量（无 Get）——
// 泵的完整触发条件（minIdle 保温 × maxLifetime 轮换恰是 README 推荐的生产组合 × 空闲池
// 无回收流量）。修复前行为（确定性，无需竞态配合）：每轮 supervise 销毁 10 个活对象 →
// 10 个 tombstone 占住 size（A3：零流量下无 Get 回收）→ idleCount=0 → deficit=10 →
// 补齐入队 10 个活对象（refillOne 免 size 门禁）→ 每周期净增 ~minIdle，第 k 轮
// size=10(k+1) 线性无界（内存无界 + supervise/Stats O(N) 恶化 + size 虚高拒绝归还）。
// 修复后行为：下一轮扫描末尾的 tombstone GC 回收全部既有墓碑，size 回落真实活对象数，
// 稳态振荡峰值 = maxSize+minIdle（销毁轮墓碑 + 补齐活对象的瞬时共存），跨周期零增长。
// 牙齿（mutation）：删除 supervise 末尾的 GC 调用（或 gcTombstones 体）→ 第 2 轮起
// Len() 线性增长击穿上界断言 → FAIL。
// 同时钉住验收准则 2：GC 不破坏 MinIdle 补齐语义——轮换持续进行、水位每周期补回
// minIdle（IdleCount 恢复到 10）；Stop 后五分类恒等式与 created-destroyed 配对闭合
// （每个池自建对象恰好销毁一次，无静默配额泄漏）。
// TestPool_MinIdle_LifetimeRotation_TombstoneGCBoundsSize is the deterministic regression of the
// N1 tombstone pump (the touchstone; the parameterized construction of the B3 audit §3-N1):
// maxSize=minIdle=10 + maxLifetime=100ms + zero traffic (no Get) — the pump's full trigger shape
// (the minIdle warm-keeping × maxLifetime rotation combination the README recommends for
// production × an idle pool with no reclaim traffic). Pre-fix behavior (deterministic, needs no
// racing): every supervise pass destroys 10 live objects → 10 tombstones keep occupying size
// (A3: with zero traffic no Get reclaims them) → idleCount=0 → deficit=10 → the refill enqueues
// 10 live objects (refillOne is exempt from the size gate) → a net gain of ~minIdle per cycle,
// size=10(k+1) at cycle k, linearly unbounded (unbounded memory + O(N) degradation of
// supervise/Stats + an inflated size rejecting returns). Post-fix behavior: the tombstone GC at
// the end of the next pass reclaims every pre-existing tombstone, size drops back to the true
// live-object count, the steady oscillation peaks at maxSize+minIdle (the transient coexistence
// of a destruction round's tombstones and the refilled live objects) with zero growth across
// cycles. Teeth (mutation): removing the GC call at the end of supervise (or the gcTombstones
// body) → from the second round on, Len() grows linearly through the bound assertion → FAIL.
// It equally pins acceptance criterion 2: the GC never breaks the MinIdle refill semantics —
// the rotation keeps running and the water level is restored to minIdle every cycle (IdleCount
// recovers to 10); after Stop the five-category identity and the created-destroyed pairing close
// (every pool-created object is destroyed exactly once, with no silent quota leak).
func TestPool_MinIdle_LifetimeRotation_TombstoneGCBoundsSize(t *testing.T) {
	const (
		maxSize  = 10
		minIdle  = 10
		lifetime = 100 * time.Millisecond
		cycles   = 6 // ≥5 个 supervise 周期：6 × 300ms = 1.8s / ≥5 supervise cycles
	)
	var created, closed atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(maxSize).
		WithMinIdle(minIdle).
		WithInitialize(maxSize).
		WithMaxLifetime(lifetime).
		WithScanInterval(conecta.MinScanInterval).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("conn-%d", created.Add(1)), nil
		}).
		WithPingFunc(func(any, int) bool { return true }).
		WithCloseFunc(func(any) error {
			closed.Add(1)
			return nil
		})
	p := mustNewPool(t, conf)

	// 零流量采样：Len() 每 50ms 一次、Stats().TombstoneCount 每 200ms 一次（Stats 走 O(N)
	// Range，N 有界故成本可忽略）。上界 = 健康轨道峰值 maxSize+minIdle + 采样与补齐/GC
	// 交错的容差；修复前轨道每轮 +minIdle，第 2-3 轮即击穿
	// Zero-traffic sampling: Len() every 50ms and Stats().TombstoneCount every 200ms (Stats runs
	// an O(N) Range; with N bounded the cost is negligible). The bound = the healthy orbit's peak
	// maxSize+minIdle plus slack for sampling racing the refill/GC; the pre-fix orbit gains
	// +minIdle per round and breaks through it by round 2-3
	limit := int64(maxSize + minIdle + 5)
	var maxLen, maxTombstone int64
	sampleN := 0
	deadline := time.Now().Add(time.Duration(cycles) * conecta.MinScanInterval * time.Millisecond)
	for time.Now().Before(deadline) {
		if n := int64(p.Len()); n > maxLen {
			maxLen = n
		}
		if sampleN%4 == 0 {
			if n := int64(p.Stats().TombstoneCount); n > maxTombstone {
				maxTombstone = n
			}
		}
		sampleN++
		time.Sleep(50 * time.Millisecond)
	}
	if maxLen > limit {
		t.Errorf("max Len() across %d cycles = %d, want <= %d — size must stay bounded (the tombstone GC reclaims the pre-existing tombstones every pass); linear growth across cycles means the N1 tombstone pump is back", cycles, maxLen, limit)
	}
	if maxTombstone > limit {
		t.Errorf("max TombstoneCount across %d cycles = %d, want <= %d — tombstones must never accumulate", cycles, maxTombstone, limit)
	}
	// 防真空臂：轮换真实发生（至少一整轮销毁），否则上界断言无意义
	// Anti-vacuity arm: the rotation really ran (at least one full destruction round), otherwise
	// the bound assertion proves nothing
	if got := closed.Load(); got < int64(maxSize) {
		t.Fatalf("closeFunc calls = %d, want >= %d: the lifetime rotation never ran", got, maxSize)
	}

	// 验收准则 2：GC 后补齐语义完好——水位在轮换周期中恢复到 minIdle
	// Acceptance criterion 2: the refill semantics survive the GC — the water level recovers to
	// minIdle across the rotation cycles
	waitUntil(t, 3*time.Second, 20*time.Millisecond,
		func() bool { return p.Stats().IdleCount == minIdle },
		"the refill never restored the water level to minIdle across the GC cycles")

	p.Stop()
	s := p.Stats()
	assertDestroyIdentity(t, s, "after the rotation stress")
	// 零流量 + 纯池自建：每个创建对象恰好被销毁一次（轮换或 Stop 清理），created-destroyed
	// 配对闭合 = 在途配额无静默泄漏
	// Zero traffic + purely pool-created: every created object is destroyed exactly once (by the
	// rotation or the Stop cleanup); the created-destroyed pairing closes = no silent quota leak
	if s.TotalCreated != s.TotalDestroyed {
		t.Errorf("(TotalCreated, TotalDestroyed) = (%d, %d), want equal: every pool-created object was destroyed exactly once", s.TotalCreated, s.TotalDestroyed)
	}
	if s.IdleCount != 0 || s.TombstoneCount != 0 || s.ActiveCount != 0 {
		t.Errorf("(IdleCount, TombstoneCount, ActiveCount) = (%d, %d, %d), want (0, 0, 0) after Stop",
			s.IdleCount, s.TombstoneCount, s.ActiveCount)
	}
}

// TestPool_MinIdle_LifetimeRotation_ReturnPutSucceedsAfterIdle N1 配套义务（B3 审计 §3-N1
// 影响面 4 与修复方向的配套义务）：借出对象在空闲期经历完整寿命轮换后，归还 Put 必须成功。
// 契约证伪前的构造：借出 L1 → 全池轮换 + 补齐过冲 → size 虚高（修复前每轮无界增长）→
// Put 被 ErrPoolFull 拒绝且重试永不成功（无 Get 则无回收）→ 调用方被迫丢弃 → 在途配额
// 永久搁浅（D1 升级为纯负载可达）。修复后：tombstone GC 每轮回落 size，且在途封顶保证
// 借出者持有 1 格时补齐至多补到 maxSize-1 个空闲 → size < maxSize 的稳定窗口每轮存在，
// Put 重试至多一个扫描周期内成功——拒绝从“永久搁浅”降级为“瞬时可自愈”。
// 牙齿（mutation）：删除 GC → size 从 maxSize+minIdle 起每轮单调 +minIdle-1，Put 永远
// 被拒 → 3s 超时 Fatal。
// TestPool_MinIdle_LifetimeRotation_ReturnPutSucceedsAfterIdle is N1's companion obligation (the
// B3 audit §3-N1 impact 4 and the fix direction's companion duty): a borrowed object's return Put must
// succeed after the pool rotated through full lifetimes while idle. The construction that
// falsified the old contract: borrow L1 → the whole pool rotates + the refill overshoots → size
// inflates (pre-fix it grew unboundedly every round) → the Put is rejected with ErrPoolFull and
// retrying never succeeds (no Get, no reclaim) → the caller is forced to drop the value → the
// in-flight quota is stranded permanently (D1 upgraded to reachable on a pure workload).
// Post-fix: the tombstone GC drops size every pass, and the in-flight cap guarantees that with
// one slot held by the borrower the refill tops up to at most maxSize-1 idle objects → a stable
// size < maxSize window exists every pass, so a retried Put succeeds within at most one scan
// interval — the rejection degrades from "permanent stranding" to "transient and self-healing".
// Teeth (mutation): removing the GC → size grows monotonically from maxSize+minIdle by
// +minIdle-1 per round, the Put is rejected forever → the 3s timeout Fatals.
func TestPool_MinIdle_LifetimeRotation_ReturnPutSucceedsAfterIdle(t *testing.T) {
	const (
		maxSize  = 4
		minIdle  = 4
		lifetime = 100 * time.Millisecond
	)
	var created atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(maxSize).
		WithMinIdle(minIdle).
		WithInitialize(maxSize).
		WithMaxLifetime(lifetime).
		WithScanInterval(conecta.MinScanInterval).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("conn-%d", created.Add(1)), nil
		}).
		WithPingFunc(func(any, int) bool { return true }).
		WithCloseFunc(func(any) error { return nil })
	p := mustNewPool(t, conf)

	// 借出 L1：initialize 刚建完对象必新鲜（年龄≈0 < 100ms 寿命），首次 Get 直接命中；
	// 若测试 goroutine 被调度拖过寿命，Get 会就地销毁到期对象再试（maxRetries 预算），
	// 重试直至借到某轮补齐的新鲜对象
	// Borrow L1: right after initialize the objects are necessarily fresh (age≈0 < the 100ms
	// lifetime), so the first Get hits directly; should the test goroutine be scheduled past the
	// lifetime, Get destroys the expired objects in place and keeps trying (the maxRetries
	// budget) — retry until borrowing a fresh object from some refill round
	var v any
	borrowDeadline := time.Now().Add(5 * time.Second)
	for {
		got, err := p.Get()
		if err == nil {
			v = got
			break
		}
		if !errors.Is(err, conecta.ErrPoolEmpty) {
			t.Fatalf("Get() error = %v, want nil or ErrPoolEmpty", err)
		}
		if time.Now().After(borrowDeadline) {
			t.Fatal("never managed to borrow a live object within 5s")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 空闲期：持有 L1 跨 ≥2 个完整轮换周期（期间销毁→墓碑→GC→补齐反复进行）
	// The idle period: hold L1 across ≥2 full rotation cycles (destruction → tombstones → GC →
	// refill repeating throughout)
	time.Sleep(700 * time.Millisecond)

	// 归还：修复后 Put 至多一个扫描周期内重试成功；修复前 size 单调增长 → 永远
	// ErrPoolFull → 超时 Fatal（牙齿）
	// The return: post-fix the retried Put succeeds within at most one scan interval; pre-fix
	// size grows monotonically → ErrPoolFull forever → the timeout Fatals (the teeth)
	putDeadline := time.Now().Add(3 * time.Second)
	for {
		err := p.Put(v)
		if err == nil {
			break
		}
		if !errors.Is(err, conecta.ErrPoolFull) {
			t.Fatalf("Put() error = %v, want nil or a transient ErrPoolFull", err)
		}
		if time.Now().After(putDeadline) {
			s := p.Stats()
			t.Fatalf("Put never succeeded within 3s (Len=%d Idle=%d Tombstone=%d FullPut=%d) — the return is permanently stranded, the N1/D1 shape is back",
				p.Len(), s.IdleCount, s.TombstoneCount, s.FullPutCount)
		}
		time.Sleep(10 * time.Millisecond)
	}

	p.Stop()
	s := p.Stats()
	assertDestroyIdentity(t, s, "after the stranded-return regression")
	if s.TotalCreated != s.TotalDestroyed {
		t.Errorf("(TotalCreated, TotalDestroyed) = (%d, %d), want equal: every pool-created object was destroyed exactly once", s.TotalCreated, s.TotalDestroyed)
	}
	if s.ActiveCount != 0 {
		t.Errorf("ActiveCount = %d, want 0: the borrowed object was returned", s.ActiveCount)
	}
}

// TestPool_MinIdle_LifetimeMixedRoundPreservesIdleFloor N3-①（B3 审计 §1 复审点 3 / §3-N3）：
// idleCount 守恒律的承重递减点——lifetime 销毁点的 idleCount--——的测试覆盖（此前 mutation
// 存活）。混合轮次构造（审计原文构造：minIdle=2，e1 lifetime 销毁、e2-e4 idle 超时）：
// obj-1 比 obj-2..4 早 400ms 入池 → R2@1000ms 时 obj-1 寿命到期（990≥850）而 obj-2..4 仅
// 空闲超时（600>300）未到寿命（600<850）——同一轮 lifetime 销毁 ∧ idle 淘汰并存。
// 正确实现：obj-1(lifetime) 销毁后 idleCount 即刻递减 → idle 下限护栏按真实水位放行——
// obj-2/obj-3 受护栏保护（ic≤2）、恰淘汰 obj-4（ic=3>2），存活 2 = minIdle 下限不破，
// deficit=0 不误补。mutation（删 lifetime 销毁点 idleCount--）：ic 虚高 → obj-3 的护栏
// 判定提前为真被误淘汰（存活仅 1，穿透下限）且 deficit 虚低 → 补齐延迟。
// 第二臂（lifetime 销毁 × minIdle 补齐混合轮）：R3 幸存者 obj-2/3 寿命销毁 → ic=0 →
// deficit=2 → 补齐把水位补回 2（TotalCreated=6 且 TotalDestroyed=4 精确匹配）；mutation
// 下 ic 虚高 → deficit=0 → 不补齐，精确匹配条件永不成立 → 超时 Fatal（牙齿）。
// TestPool_MinIdle_LifetimeMixedRoundPreservesIdleFloor covers N3-① (B3 audit §1 review point 3
// / §3-N3): test coverage of the load-bearing decrement of the idleCount conservation law — the
// lifetime destruction site's idleCount-- (previously a surviving mutant). The mixed-round
// construction (the audit's own: minIdle=2, e1 destroyed by lifetime, e2-e4 idle-timed-out):
// obj-1 enters the pool 400ms before obj-2..4 → at R2@1000ms obj-1's lifetime has elapsed
// (990≥850) while obj-2..4 are only idle-timed-out (600>300) and not yet expired (600<850) —
// lifetime destruction ∧ idle eviction coexist in one pass. The correct implementation: the
// idleCount decrement right after obj-1's lifetime destruction keeps the idle floor guard on the
// true water level — obj-2/obj-3 stay guarded (ic≤2) and exactly obj-4 is evicted (ic=3>2),
// leaving 2 survivors = the minIdle floor unbroken and deficit=0 (no phantom refill). The
// mutation (removing the lifetime site's idleCount--): the inflated ic makes the guard turn true
// early → obj-3 is wrongly evicted as well (only 1 survivor — the floor is pierced) and the
// deficit is under-counted → the refill is delayed. Second arm (the lifetime-destruction ×
// minIdle-refill mixed round): at R3 the survivors obj-2/3 are destroyed by lifetime → ic=0 →
// deficit=2 → the refill restores the level to 2 (TotalCreated=6 with TotalDestroyed=4 matched
// exactly); under the mutation the inflated ic → deficit=0 → no refill, the exact-match condition
// never holds → the timeout Fatals (the teeth).
func TestPool_MinIdle_LifetimeMixedRoundPreservesIdleFloor(t *testing.T) {
	const (
		maxSize  = 6
		minIdle  = 2
		lifetime = 850 * time.Millisecond
		idleTime = 300 * time.Millisecond
		scan     = 500 // > MinScanInterval：为混合轮次留足时序裕量 / leaves ample timing margin
	)
	var created atomic.Int64
	closed := &closedRecorder{}
	conf := conecta.NewConfig().
		WithMaxSize(maxSize).
		WithMinIdle(minIdle).
		WithMaxLifetime(lifetime).
		WithMaxIdleTime(idleTime).
		WithScanInterval(scan).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", created.Add(1)), nil
		}).
		WithPingFunc(func(any, int) bool { return true }).
		WithCloseFunc(func(v any) error {
			closed.add(v)
			return nil
		})
	p := mustNewPool(t, conf)
	defer p.Stop()

	// 4 个对象全部池自建（GWC 创建直交，账本干净）→ 分两批归还制造年龄差：obj-1 的
	// expireAt 比 obj-2..4 早 400ms。时序（scan=500ms）：R1@500 无销毁（obj-2..4 空闲
	// 100<300 未超时；obj-1 虽空闲超时但 ic=1≤minIdle 受护栏保护）；R2@1000 混合轮；
	// R3@1500 幸存者寿命销毁 + 补齐
	// All 4 objects are pool-created (GWC creation hand-overs, a clean ledger) → returned in two
	// batches to create the age gap: obj-1's expireAt is 400ms earlier than obj-2..4's. Timeline
	// (scan=500ms): R1@500 destroys nothing (obj-2..4 idled 100<300, not timed out; obj-1 is
	// idle-timed-out but guarded by ic=1≤minIdle); R2@1000 is the mixed round; R3@1500 destroys
	// the survivors by lifetime and refills
	ctx := context.Background()
	values := make([]any, 0, 4)
	for i := 1; i <= 4; i++ {
		v, err := p.GetWithContext(ctx)
		if err != nil {
			t.Fatalf("GetWithContext #%d error = %v", i, err)
		}
		values = append(values, v)
	}
	if err := p.Put(values[0]); err != nil { // obj-1 先入池（年龄最长）/ obj-1 enters first (the oldest)
		t.Fatalf("Put(obj-1) error = %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	for i, v := range values[1:] {
		if err := p.Put(v); err != nil {
			t.Fatalf("Put(obj-%d) error = %v", i+2, err)
		}
	}
	if got := created.Load(); got != 4 {
		t.Fatalf("newFunc calls = %d, want 4", got)
	}

	// R2 混合轮：正确实现恰关闭 2 个（obj-1 lifetime + obj-4 idle）；mutation → obj-3 被
	// 误淘汰 → closes=3 且 ByIdle=2、存活仅 1（穿透下限）
	// The R2 mixed round: the correct implementation closes exactly 2 (obj-1 by lifetime + obj-4
	// by idle); the mutation → obj-3 is wrongly evicted → closes=3 with ByIdle=2 and only 1
	// survivor (the floor pierced)
	waitUntil(t, 5*time.Second, 20*time.Millisecond,
		func() bool { return closed.total() >= 2 },
		"the mixed round never destroyed 2 objects: closeFunc=%d", closed.total())
	s := p.Stats()
	if got := closed.total(); got != 2 {
		t.Errorf("closeFunc calls after the mixed round = %d, want exactly 2 (obj-1 by lifetime + obj-4 by idle): an extra eviction means the lifetime destruction's idleCount-- is missing and the minIdle floor was pierced", got)
	}
	if s.DestroyedByLifetime != 1 || s.DestroyedByIdle != 1 {
		t.Errorf("(DestroyedByLifetime, DestroyedByIdle) = (%d, %d), want (1, 1)",
			s.DestroyedByLifetime, s.DestroyedByIdle)
	}
	if s.IdleCount != minIdle {
		t.Errorf("IdleCount after the mixed round = %d, want %d: the floor must never be pierced within the pass", s.IdleCount, minIdle)
	}
	if s.TotalCreated != 4 {
		t.Errorf("TotalCreated = %d, want 4: at the floor the deficit is 0 (no phantom refill)", s.TotalCreated)
	}
	assertDestroyIdentity(t, s, "after the lifetime×idle mixed round")

	// 第二臂：R3 幸存者寿命销毁 → deficit=2 → 补齐（TotalCreated=6 且 TotalDestroyed=4
	// 的精确匹配窗口 ≈1s，稳定可检测）；mutation 下补齐延迟一轮且销毁计数错位，精确匹配
	// 永不成立 → 超时 Fatal
	// The second arm: R3 destroys the survivors by lifetime → deficit=2 → the refill
	// (the exact-match window TotalCreated=6 with TotalDestroyed=4 lasts ≈1s, stably
	// detectable); under the mutation the refill is delayed a round and the destruction counts
	// shift, the exact match never holds → the timeout Fatals
	waitUntil(t, 5*time.Second, 20*time.Millisecond,
		func() bool {
			s := p.Stats()
			return s.TotalCreated == 6 && s.TotalDestroyed == 4
		},
		"the refill never restored the water level after the survivors rotated out: created=%d", created.Load())
	s = p.Stats()
	if s.DestroyedByLifetime != 3 || s.DestroyedByIdle != 1 || s.TotalDestroyed != 4 {
		t.Errorf("(ByLifetime, ByIdle, TotalDestroyed) = (%d, %d, %d), want (3, 1, 4)",
			s.DestroyedByLifetime, s.DestroyedByIdle, s.TotalDestroyed)
	}
	if s.IdleCount != minIdle || s.TotalCreated != 6 {
		t.Errorf("(IdleCount, TotalCreated) = (%d, %d), want (2, 6): the refill restored the floor after the lifetime rotation", s.IdleCount, s.TotalCreated)
	}
	if s.TotalBorrowed != 4 || s.TotalReturned != 4 || s.ActiveCount != 0 {
		t.Errorf("(Borrowed, Returned, Active) = (%d, %d, %d), want (4, 4, 0)",
			s.TotalBorrowed, s.TotalReturned, s.ActiveCount)
	}
	assertDestroyIdentity(t, s, "after the lifetime×refill mixed round")
}

// TestPool_MinIdle_PingDestroyMixedRoundPreservesIdleFloor N3-②（B3 审计 §3-N3）：
// idleCount 守恒律的另一承重递减点——ping 销毁点的 idleCount--——的测试覆盖。构造与
// lifetime 臂同型（年龄差分批归还），销毁源换为 ping 连续失败（maxRetries=2：obj-1 自
// 首轮恒失败 → R1 累计 retries=1、R2 达上限销毁）：R2@1000ms 混合轮 = obj-1 ping 销毁
// ∧ obj-4 idle 淘汰（ic=3>2 + 空闲 600>300）。正确实现存活 obj-2/obj-3 恰为下限；
// mutation（删 ping 销毁点 idleCount--）→ ic 虚高 → obj-3 被误淘汰（存活 1，穿透下限）。
// 第二臂（ping 销毁 × minIdle 补齐混合轮）：第一臂断言后武装 condemn 标志 → obj-2/3 自
// R3 起 ping 失败 → R4 双双达上限销毁 → ic=0 → deficit=2 → 补齐 2 个健康对象
// （TotalCreated=6 且 TotalDestroyed=4，此后 obj-5/6 健康且受护栏保护、状态永久稳定）；
// mutation → deficit=0 → 不补齐，精确匹配永不成立 → 超时 Fatal（牙齿）。
// TestPool_MinIdle_PingDestroyMixedRoundPreservesIdleFloor covers N3-② (B3 audit §3-N3): test
// coverage of the conservation law's other load-bearing decrement — the ping destruction site's
// idleCount--. The construction mirrors the lifetime arm (the age gap via batched returns) with
// repeated ping failures as the destruction source (maxRetries=2: obj-1 fails from the first
// pass → retries=1 at R1, destroyed at the cap in R2): the R2@1000ms mixed round = obj-1's ping
// destruction ∧ obj-4's idle eviction (ic=3>2 + idle 600>300). The correct implementation leaves
// exactly obj-2/obj-3 at the floor; the mutation (removing the ping site's idleCount--) → the
// inflated ic → obj-3 is wrongly evicted (1 survivor — the floor pierced). Second arm (the
// ping-destruction × minIdle-refill mixed round): after the first arm's assertions the condemn
// flag is armed → obj-2/3 fail their pings from R3 → both hit the cap and are destroyed in R4 →
// ic=0 → deficit=2 → the refill creates 2 healthy objects (TotalCreated=6 with TotalDestroyed=4;
// afterwards obj-5/6 are healthy and floor-guarded, the state stays stable forever); the mutation
// → deficit=0 → no refill, the exact match never holds → the timeout Fatals (the teeth).
func TestPool_MinIdle_PingDestroyMixedRoundPreservesIdleFloor(t *testing.T) {
	const (
		maxSize  = 6
		minIdle  = 2
		idleTime = 300 * time.Millisecond
		scan     = 500 // > MinScanInterval：为混合轮次留足时序裕量 / leaves ample timing margin
	)
	var created atomic.Int64
	// condemn 在第一臂断言完成后武装：obj-2/obj-3 自 R3 起 ping 失败（R4 达 maxRetries=2
	// 上限销毁）——武装时刻由断言驱动而非裸时钟，确定性构造
	// condemn is armed after the first arm's assertions complete: obj-2/obj-3 fail their pings
	// from R3 (destroyed at the maxRetries=2 cap in R4) — the arming moment is assertion-driven
	// instead of a bare clock, a deterministic construction
	var condemn atomic.Bool
	closed := &closedRecorder{}
	conf := conecta.NewConfig().
		WithMaxSize(maxSize).
		WithMinIdle(minIdle).
		WithMaxIdleTime(idleTime).
		WithScanInterval(scan).
		WithPingMaxRetries(2).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", created.Add(1)), nil
		}).
		WithPingFunc(func(v any, _ int) bool {
			switch v {
			case "obj-1":
				return false // 恒失败：R1 retries=1，R2 达上限销毁 / fails always: retries=1 at R1, destroyed at the cap in R2
			case "obj-2", "obj-3":
				return !condemn.Load() // 武装后失败：R3 retries=1，R4 达上限销毁 / fails once armed: retries=1 at R3, destroyed in R4
			default:
				return true
			}
		}).
		WithCloseFunc(func(v any) error {
			closed.add(v)
			return nil
		})
	p := mustNewPool(t, conf)
	defer p.Stop()

	// 与 lifetime 臂同型的年龄差构造：obj-1 先归还（R1 时其空闲已超时但 ic=1≤minIdle 受
	// 护栏保护、ping 失败仅累计 retries），obj-2..4 于 400ms 后归还（R1 空闲 100<300 未
	// 超时——保证混合轮发生在 R2 而非 R1）
	// The same age-gap construction as the lifetime arm: obj-1 is returned first (at R1 it is
	// already idle-timed-out but guarded by ic=1≤minIdle, and its ping failure only accumulates
	// retries), obj-2..4 are returned after 400ms (idle 100<300 at R1, not timed out — ensuring
	// the mixed round happens in R2, not R1)
	ctx := context.Background()
	values := make([]any, 0, 4)
	for i := 1; i <= 4; i++ {
		v, err := p.GetWithContext(ctx)
		if err != nil {
			t.Fatalf("GetWithContext #%d error = %v", i, err)
		}
		values = append(values, v)
	}
	if err := p.Put(values[0]); err != nil {
		t.Fatalf("Put(obj-1) error = %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	for i, v := range values[1:] {
		if err := p.Put(v); err != nil {
			t.Fatalf("Put(obj-%d) error = %v", i+2, err)
		}
	}
	if got := created.Load(); got != 4 {
		t.Fatalf("newFunc calls = %d, want 4", got)
	}

	// R2 混合轮：正确实现恰关闭 2 个（obj-1 ping + obj-4 idle）；mutation → obj-3 被误淘汰
	// → closes=3 且 ByIdle=2、存活仅 1（穿透下限）
	// The R2 mixed round: the correct implementation closes exactly 2 (obj-1 by ping + obj-4 by
	// idle); the mutation → obj-3 is wrongly evicted → closes=3 with ByIdle=2 and only 1
	// survivor (the floor pierced)
	waitUntil(t, 5*time.Second, 20*time.Millisecond,
		func() bool { return closed.total() >= 2 },
		"the mixed round never destroyed 2 objects: closeFunc=%d", closed.total())
	s := p.Stats()
	if got := closed.total(); got != 2 {
		t.Errorf("closeFunc calls after the mixed round = %d, want exactly 2 (obj-1 by ping + obj-4 by idle): an extra eviction means the ping destruction's idleCount-- is missing and the minIdle floor was pierced", got)
	}
	if s.DestroyedByPing != 1 || s.DestroyedByIdle != 1 {
		t.Errorf("(DestroyedByPing, DestroyedByIdle) = (%d, %d), want (1, 1)",
			s.DestroyedByPing, s.DestroyedByIdle)
	}
	if s.IdleCount != minIdle {
		t.Errorf("IdleCount after the mixed round = %d, want %d: the floor must never be pierced within the pass", s.IdleCount, minIdle)
	}
	if s.TotalCreated != 4 {
		t.Errorf("TotalCreated = %d, want 4: at the floor the deficit is 0 (no phantom refill)", s.TotalCreated)
	}
	assertDestroyIdentity(t, s, "after the ping×idle mixed round")

	// 武装：obj-2/3 自 R3 起 ping 失败 → R4 达上限销毁（此刻 ~R2+20ms，距 R3 有 ~480ms 裕量）
	// Arm: obj-2/3 fail their pings from R3 → destroyed at the cap in R4 (this runs ~R2+20ms,
	// ~480ms ahead of R3)
	condemn.Store(true)

	// 第二臂：R4 ping 销毁 × 补齐混合轮——正确实现 TotalCreated=6 且 TotalDestroyed=4
	//（此后 obj-5/6 健康 + 护栏保护，状态永久稳定，检测窗口无界）；mutation 下 deficit=0
	// 不补齐（created 停在 4，晚一轮才补且 destroyed=5 错位）→ 精确匹配永不成立 → 超时 Fatal
	// The second arm: R4's ping-destruction × refill mixed round — the correct implementation
	// reaches TotalCreated=6 with TotalDestroyed=4 (afterwards obj-5/6 are healthy and
	// floor-guarded, the state stays stable forever, the detection window is unbounded); under
	// the mutation deficit=0 skips the refill (created stalls at 4, the late refill lands with
	// destroyed=5 shifted) → the exact match never holds → the timeout Fatals
	waitUntil(t, 5*time.Second, 20*time.Millisecond,
		func() bool {
			s := p.Stats()
			return s.TotalCreated == 6 && s.TotalDestroyed == 4
		},
		"the refill never restored the water level after the ping destructions: created=%d", created.Load())
	s = p.Stats()
	if s.DestroyedByPing != 3 || s.DestroyedByIdle != 1 || s.TotalDestroyed != 4 {
		t.Errorf("(ByPing, ByIdle, TotalDestroyed) = (%d, %d, %d), want (3, 1, 4)",
			s.DestroyedByPing, s.DestroyedByIdle, s.TotalDestroyed)
	}
	if s.IdleCount != minIdle || s.TotalCreated != 6 {
		t.Errorf("(IdleCount, TotalCreated) = (%d, %d), want (2, 6): the refill restored the floor after the ping rotation", s.IdleCount, s.TotalCreated)
	}
	if s.TotalBorrowed != 4 || s.TotalReturned != 4 || s.ActiveCount != 0 {
		t.Errorf("(Borrowed, Returned, Active) = (%d, %d, %d), want (4, 4, 0)",
			s.TotalBorrowed, s.TotalReturned, s.ActiveCount)
	}
	if got := closed.total(); got != 4 {
		t.Errorf("closeFunc calls = %d, want exactly 4 (every object closed exactly once)", got)
	}
	assertDestroyIdentity(t, s, "after the ping×refill mixed round")
}

// TestPool_MaxIdleTime_EvictsIdleBeyondDuration 验收准则 4：持续空闲超过 maxIdleTime 的对象被
// supervise 回收——判定位于 ping 之前（pingCount==0 的牙齿：若 idle 判定排在 ping 之后，健康
// 对象会因 ping 成功 continue 而永不被回收）；销毁三件套完整（closeFunc/OnClose 各一次 +
// recordDestroyed(destroyedByIdle) 直断言 + releaseSlot 释放在途配额）；tombstone 留队列占
// size（A3），由后续 Get 回收
// TestPool_MaxIdleTime_EvictsIdleBeyondDuration covers acceptance criterion 4: objects idling
// continuously beyond maxIdleTime are reclaimed by supervise — the check runs before the ping
// (the teeth of pingCount==0: with the idle check placed after the ping, a healthy object would
// never be reclaimed because a successful ping continues the loop); the destruction triple is
// complete (closeFunc/OnClose exactly once each + a direct recordDestroyed(destroyedByIdle)
// assertion + releaseSlot freeing the in-flight quota); the tombstone stays in the queue occupying
// size (A3) until a later Get reclaims it
func TestPool_MaxIdleTime_EvictsIdleBeyondDuration(t *testing.T) {
	var created, pingCount atomic.Int64
	closed := &closedRecorder{}
	cb := &countingCallback{}
	conf := conecta.NewConfig().
		WithMaxSize(4).
		WithMaxIdleTime(150 * time.Millisecond).
		WithScanInterval(conecta.MinScanInterval).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", created.Add(1)), nil
		}).
		WithPingFunc(func(any, int) bool {
			pingCount.Add(1)
			return true
		}).
		WithCloseFunc(func(v any) error {
			closed.add(v)
			return nil
		}).
		WithCallback(cb)
	p := mustNewPool(t, conf)
	defer p.Stop()

	// 用池自建对象（经 GWC 创建 → Put 归还入池打 idleSince 戳），保证在途账本干净
	// Pool-created objects (created via GWC → returned via Put, which stamps idleSince), keeping
	// the in-flight ledger clean
	ctx := context.Background()
	v1, err := p.GetWithContext(ctx)
	if err != nil {
		t.Fatalf("GetWithContext #1 error = %v", err)
	}
	v2, err := p.GetWithContext(ctx)
	if err != nil {
		t.Fatalf("GetWithContext #2 error = %v", err)
	}
	if err := p.Put(v1); err != nil {
		t.Fatalf("Put #1 error = %v", err)
	}
	if err := p.Put(v2); err != nil {
		t.Fatalf("Put #2 error = %v", err)
	}

	// 首轮扫描（300ms）时对象已空闲 ~300ms > 150ms → 两个全部回收（minIdle=0 无下限护栏）
	// At the first scan (300ms) the objects have idled ~300ms > 150ms → both are reclaimed
	// (minIdle=0 means no floor guard)
	waitUntil(t, 5*time.Second, 20*time.Millisecond,
		func() bool { return closed.total() == 2 && cb.close.Load() == 2 },
		"idle objects were not reclaimed: closeFunc=%d OnClose=%d", closed.total(), cb.close.Load())

	if got := pingCount.Load(); got != 0 {
		t.Errorf("pingFunc calls = %d, want 0: the idle check must run before the ping", got)
	}
	// tombstone 留队列占 size（A3）/ the tombstones stay in the queue occupying size (A3)
	if got := p.Len(); got != 2 {
		t.Errorf("Len() after the reclaim = %d, want 2 (tombstones occupy capacity)", got)
	}
	s := p.Stats()
	if s.DestroyedByIdle != 2 || s.TotalDestroyed != 2 {
		t.Errorf("(DestroyedByIdle, TotalDestroyed) = (%d, %d), want (2, 2)", s.DestroyedByIdle, s.TotalDestroyed)
	}
	if s.IdleCount != 0 || s.TombstoneCount != 2 || s.ActiveCount != 0 {
		t.Errorf("(IdleCount, TombstoneCount, ActiveCount) = (%d, %d, %d), want (0, 2, 0)",
			s.IdleCount, s.TombstoneCount, s.ActiveCount)
	}
	if s.TotalCreated != 2 || s.TotalBorrowed != 2 || s.TotalReturned != 2 {
		t.Errorf("(TotalCreated, TotalBorrowed, TotalReturned) = (%d, %d, %d), want (2, 2, 2)",
			s.TotalCreated, s.TotalBorrowed, s.TotalReturned)
	}
	assertDestroyIdentity(t, s, "after the idle reclaim")

	// releaseSlot 行为探针：在途配额已随销毁释放（inFlight=0）——新 GWC 立即创建成功；
	// 若三件套缺 releaseSlot，inFlight 滞留 2，maxSize=4 下第 3 次 GWC 仍能创建、第 5 次
	// 才挂起，探针弱化为恒等式+后续创建（此处直接验证创建成功且计数正确）
	// releaseSlot behavioral probe: the in-flight quota was released with the destruction
	// (inFlight=0) — a new GWC creates immediately; had the triple missed releaseSlot, inFlight
	// would stick at 2
	v3, err := p.GetWithContext(ctx)
	if err != nil {
		t.Fatalf("GetWithContext #3 error = %v (the idle reclaim must release in-flight quota)", err)
	}
	if v3 == nil {
		t.Fatal("GetWithContext #3 returned a nil value")
	}
	// GWC 借出路径先回收了两个 tombstone（size 2→0），随后有界创建 obj-3
	// The GWC borrow path reclaimed both tombstones first (size 2→0), then bounded-created obj-3
	if got := p.Len(); got != 0 {
		t.Errorf("Len() after the tombstone reclaims = %d, want 0", got)
	}
	if got := p.Stats().TombstoneCount; got != 0 {
		t.Errorf("TombstoneCount after the reclaims = %d, want 0", got)
	}
	if err := p.Put(v3); err != nil {
		t.Fatalf("Put(obj-3) error = %v", err)
	}
}

// TestPool_MaxIdleTime_RespectsMinIdleFloor 验收准则 5：空闲回收绝不打穿 minIdle 下限——
// 4 个超时空闲对象只回收超出 minIdle=2 的 2 个，且后续扫描轮次水位稳定在下限（mutation：
// 删除 minIdle 护栏则 4 个全被回收 → FAIL）；水位恰在下限时 deficit=0，补齐正确停手
// TestPool_MaxIdleTime_RespectsMinIdleFloor covers acceptance criterion 5: the idle reclaim never
// breaks through the minIdle floor — of 4 timed-out idle objects only the 2 above minIdle=2 are
// reclaimed, and later scan passes keep the level stable at the floor (mutation: removing the
// minIdle guard reclaims all 4 → FAIL); with the level exactly at the floor deficit=0 and the
// refill correctly stands down
func TestPool_MaxIdleTime_RespectsMinIdleFloor(t *testing.T) {
	var created atomic.Int64
	closed := &closedRecorder{}
	conf := conecta.NewConfig().
		WithMaxSize(6).
		WithMinIdle(2).
		WithMaxIdleTime(200 * time.Millisecond).
		WithScanInterval(conecta.MinScanInterval).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", created.Add(1)), nil
		}).
		WithPingFunc(func(any, int) bool { return true }).
		WithCloseFunc(func(v any) error {
			closed.add(v)
			return nil
		})
	p := mustNewPool(t, conf)
	defer p.Stop()

	// 先全部借出再统一归还：队列恒空使每次 GWC 都走有界创建（否则 GWC 会直接借出
	// 已归还对象而不创建）——4 个对象全部池自建，在途账本干净
	// Borrow everything first, then return it all: keeping the queue empty makes every GWC run a
	// bounded creation (otherwise a GWC would simply borrow an already-returned object) — all 4
	// objects are pool-created, keeping the in-flight ledger clean
	ctx := context.Background()
	values := make([]any, 0, 4)
	for i := 1; i <= 4; i++ {
		v, err := p.GetWithContext(ctx)
		if err != nil {
			t.Fatalf("GetWithContext #%d error = %v", i, err)
		}
		values = append(values, v)
	}
	for i, v := range values {
		if err := p.Put(v); err != nil {
			t.Fatalf("Put #%d error = %v", i+1, err)
		}
	}
	if got := created.Load(); got != 4 {
		t.Fatalf("newFunc calls = %d, want 4", got)
	}

	// 4 个对象全部空闲超时：单轮扫描按快照序回收超出下限的 2 个（同轮不穿透），水位停在 minIdle=2
	// All 4 objects time out idle: one scan pass reclaims the 2 above the floor in snapshot order
	// (never breaking through within the pass), leaving the level at minIdle=2
	waitUntil(t, 5*time.Second, 20*time.Millisecond,
		func() bool { return closed.total() == 2 },
		"the idle reclaim did not stop at the minIdle floor: closeFunc=%d", closed.total())

	s := p.Stats()
	if s.IdleCount != 2 || s.DestroyedByIdle != 2 {
		t.Fatalf("(IdleCount, DestroyedByIdle) = (%d, %d), want (2, 2)", s.IdleCount, s.DestroyedByIdle)
	}

	// 水位稳定：再跨两轮扫描，剩余 2 个（=minIdle）绝不被回收，deficit=0 补齐不创建
	// Level stability: across two more scan passes the remaining 2 (=minIdle) are never reclaimed
	// and the refill creates nothing (deficit=0)
	time.Sleep(700 * time.Millisecond)
	if got := closed.total(); got != 2 {
		t.Errorf("closeFunc calls = %d, want 2: the floor objects must never be reclaimed", got)
	}
	s = p.Stats()
	if s.IdleCount != 2 || s.TotalCreated != 4 {
		t.Errorf("(IdleCount, TotalCreated) = (%d, %d), want (2, 4): no refill below-zero deficit, no over-reclaim",
			s.IdleCount, s.TotalCreated)
	}
	if s.ActiveCount != 0 || s.TotalBorrowed != 4 || s.TotalReturned != 4 {
		t.Errorf("(ActiveCount, TotalBorrowed, TotalReturned) = (%d, %d, %d), want (0, 4, 4)",
			s.ActiveCount, s.TotalBorrowed, s.TotalReturned)
	}
	assertDestroyIdentity(t, s, "idle reclaim at the minIdle floor")
}

// TestPool_MaxIdleTime_StampGateWithoutLifetime 验收准则 6（F4 门禁扩展）：仅启用 maxIdleTime
// （maxLifetime=0）时 idleSince 必须被正确打戳——门禁若未扩展（仍为 maxLifetime>0），idleSince
// 恒 0，fail-closed 判定会把刚入池的健康对象当“立即超时”在第一轮扫描误销毁。双向断言：
// 早期扫描轮对象存活且被正常 ping（pingCount>=1 && closed==0 的牙齿），寿命到期后被回收
// TestPool_MaxIdleTime_StampGateWithoutLifetime covers acceptance criterion 6 (the F4 gate
// extension): with only maxIdleTime enabled (maxLifetime=0) idleSince must be stamped correctly —
// without the gate extension (still maxLifetime>0) idleSince stays 0 and the fail-closed check
// misjudges a freshly enqueued healthy object as "timed out immediately", wrongly destroying it in
// the first scan. Asserted in both directions: the object survives the early scans and is pinged
// as usual (the teeth of pingCount>=1 && closed==0), and is reclaimed once its idle time elapses
func TestPool_MaxIdleTime_StampGateWithoutLifetime(t *testing.T) {
	var created atomic.Int64
	var pingCount atomic.Int64
	closed := &closedRecorder{}
	conf := conecta.NewConfig().
		WithMaxSize(2).
		WithMaxIdleTime(time.Second). // maxLifetime 保持默认 0 / maxLifetime stays at its default 0
		WithScanInterval(conecta.MinScanInterval).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", created.Add(1)), nil
		}).
		WithPingFunc(func(any, int) bool {
			pingCount.Add(1)
			return true
		}).
		WithCloseFunc(func(v any) error {
			closed.add(v)
			return nil
		})
	p := mustNewPool(t, conf)
	defer p.Stop()

	v, err := p.GetWithContext(context.Background())
	if err != nil {
		t.Fatalf("GetWithContext() error = %v", err)
	}
	if err := p.Put(v); err != nil { // 归还入池打 idleSince 戳 / the return stamps idleSince
		t.Fatalf("Put() error = %v", err)
	}

	// 前两轮扫描（~300/600ms）：空闲 <1s，对象必须存活并被正常 ping。
	// 门禁缺失形态：idleSince==0 → now-0 > 1s → 第一轮扫描即误销毁（closed==1 && pingCount==0）
	// The first two scans (~300/600ms): idle <1s, so the object must survive and be pinged as
	// usual. Without the gate: idleSince==0 → now-0 > 1s → wrongly destroyed in the first scan
	// (closed==1 && pingCount==0)
	time.Sleep(700 * time.Millisecond)
	if got := closed.total(); got != 0 {
		t.Fatalf("closeFunc calls at 700ms = %d, want 0: idleSince must be stamped with only maxIdleTime enabled (the F4 gate)", got)
	}
	if got := pingCount.Load(); got < 1 {
		t.Errorf("pingFunc calls at 700ms = %d, want >= 1 (the object must survive the early scans)", got)
	}

	// 空闲满 1s 后被回收（扫描 ~1.2s 轮）/ reclaimed once it has idled for 1s (the ~1.2s scan)
	waitUntil(t, 5*time.Second, 20*time.Millisecond,
		func() bool { return closed.total() == 1 },
		"the idle object was not reclaimed after maxIdleTime elapsed: closeFunc=%d", closed.total())

	s := p.Stats()
	if s.DestroyedByIdle != 1 || s.DestroyedByLifetime != 0 {
		t.Errorf("(DestroyedByIdle, DestroyedByLifetime) = (%d, %d), want (1, 0): only the idle category fires",
			s.DestroyedByIdle, s.DestroyedByLifetime)
	}
	assertDestroyIdentity(t, s, "idle-only stamp gate")
}

// TestPool_Stats_WaterLevelsAndCounters 验收准则 7（水位与累计计数臂）：混合操作序列后逐字段
// 断言 Stats 快照——配置回显、ActiveCount 派生恒等式（==TotalBorrowed-TotalReturned，含创建
// 直交入账）、IdleCount/TombstoneCount 的 Range 遍历统计、EmptyCount/FullPutCount 拒绝计数
// TestPool_Stats_WaterLevelsAndCounters covers acceptance criterion 7 (the water-level and
// cumulative-counter arm): after a mixed operation sequence every Stats field is asserted —
// the config echo, the ActiveCount derivation identity (==TotalBorrowed-TotalReturned, creation
// hand-overs booked as borrows included), the Range-traversal IdleCount/TombstoneCount tallies,
// and the EmptyCount/FullPutCount rejection counters
func TestPool_Stats_WaterLevelsAndCounters(t *testing.T) {
	t.Run("water levels and cumulative counters follow the operation sequence", func(t *testing.T) {
		var created atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(3).
			WithScanInterval(10000). // 抑制后台扫描，纯同步序列 / suppress the scan for a purely synchronous sequence
			WithNewFunc(func() (any, error) {
				return fmt.Sprintf("obj-%d", created.Add(1)), nil
			}).
			WithPingFunc(func(any, int) bool { return true })
		p := mustNewPool(t, conf)
		defer p.Stop()

		// 初始快照：全零 + 配置回显 / the initial snapshot: all zeros + the config echo
		s := p.Stats()
		if s.MaxSize != 3 || s.MinIdle != 0 {
			t.Errorf("(MaxSize, MinIdle) = (%d, %d), want (3, 0)", s.MaxSize, s.MinIdle)
		}
		if s != (conecta.Stats{MaxSize: 3}) {
			t.Errorf("initial snapshot = %+v, want every counter zero", s)
		}

		// GWC 有界创建并直交调用方：created+1 且 borrowed+1（ActiveCount 账本，B3b 修正）
		// A GWC bounded creation handed straight to the caller: created+1 and borrowed+1 (the
		// ActiveCount ledger, fixed in B3b)
		v, err := p.GetWithContext(context.Background())
		if err != nil {
			t.Fatalf("GetWithContext() error = %v", err)
		}
		s = p.Stats()
		if s.TotalCreated != 1 || s.TotalBorrowed != 1 || s.ActiveCount != 1 || s.IdleCount != 0 {
			t.Errorf("after the creation hand-over: (Created, Borrowed, Active, Idle) = (%d, %d, %d, %d), want (1, 1, 1, 0)",
				s.TotalCreated, s.TotalBorrowed, s.ActiveCount, s.IdleCount)
		}

		// 归还：returned+1，ActiveCount 归零，IdleCount=1
		// The return: returned+1, ActiveCount back to zero, IdleCount=1
		if err := p.Put(v); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		s = p.Stats()
		if s.TotalReturned != 1 || s.ActiveCount != 0 || s.IdleCount != 1 {
			t.Errorf("after the return: (Returned, Active, Idle) = (%d, %d, %d), want (1, 0, 1)",
				s.TotalReturned, s.ActiveCount, s.IdleCount)
		}

		// 队列借出：borrowed+1（与创建直交同账本）/ a queue borrow: borrowed+1 (the same ledger)
		v2, err := p.Get()
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		s = p.Stats()
		if s.TotalBorrowed != 2 || s.ActiveCount != 1 || s.IdleCount != 0 {
			t.Errorf("after the borrow: (Borrowed, Active, Idle) = (%d, %d, %d), want (2, 1, 0)",
				s.TotalBorrowed, s.ActiveCount, s.IdleCount)
		}

		// 空池 Get：EmptyCount+1 / a Get on the empty pool: EmptyCount+1
		if _, err := p.Get(); !errors.Is(err, conecta.ErrPoolEmpty) {
			t.Fatalf("Get() on the empty pool error = %v, want ErrPoolEmpty", err)
		}
		if s = p.Stats(); s.EmptyCount != 1 {
			t.Errorf("EmptyCount = %d, want 1", s.EmptyCount)
		}

		// 归还后 ActiveCount 派生恒等式全程成立 / after the return the ActiveCount identity holds
		if err := p.Put(v2); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		s = p.Stats()
		if s.ActiveCount != int(s.TotalBorrowed-s.TotalReturned) {
			t.Errorf("ActiveCount = %d, want TotalBorrowed-TotalReturned = %d",
				s.ActiveCount, s.TotalBorrowed-s.TotalReturned)
		}
		if s.ActiveCount != 0 || s.IdleCount != 1 || s.TotalDestroyed != 0 {
			t.Errorf("(Active, Idle, Destroyed) = (%d, %d, %d), want (0, 1, 0)",
				s.ActiveCount, s.IdleCount, s.TotalDestroyed)
		}
		if s.WaitCount != 0 || s.WaitTimeoutCount != 0 || s.TotalWaitDuration != 0 || s.FullPutCount != 0 {
			t.Errorf("wait/full counters = (%d, %d, %v, %d), want all zero",
				s.WaitCount, s.WaitTimeoutCount, s.TotalWaitDuration, s.FullPutCount)
		}
		assertDestroyIdentity(t, s, "synchronous mixed sequence")
	})

	t.Run("full put rejection is counted without polluting the ledger", func(t *testing.T) {
		var created atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(1).
			WithScanInterval(10000).
			WithNewFunc(func() (any, error) {
				return fmt.Sprintf("obj-%d", created.Add(1)), nil
			}).
			WithPingFunc(func(any, int) bool { return true })
		p := mustNewPool(t, conf)
		defer p.Stop()

		v, err := p.GetWithContext(context.Background())
		if err != nil {
			t.Fatalf("GetWithContext() error = %v", err)
		}
		if err := p.Put(v); err != nil { // size=1=maxSize / size=1=maxSize
			t.Fatalf("Put() error = %v", err)
		}
		// 池满拒绝：所有权不转移、不入账（D1 边界：被拒对象的在途配额由调用方处置——
		// 此处外部对象从未经池创建，无配额可搁浅）
		// The full rejection: ownership never transfers and nothing is booked (the D1 boundary: a
		// rejected object's in-flight slot stays the caller's — here the foreign object was never
		// pool-created, so there is no quota to strand)
		if err := p.Put("foreign"); !errors.Is(err, conecta.ErrPoolFull) {
			t.Fatalf("Put(foreign) error = %v, want ErrPoolFull", err)
		}
		s := p.Stats()
		if s.FullPutCount != 1 {
			t.Errorf("FullPutCount = %d, want 1", s.FullPutCount)
		}
		if s.TotalReturned != 1 || s.ActiveCount != 0 || s.IdleCount != 1 {
			t.Errorf("(Returned, Active, Idle) = (%d, %d, %d), want (1, 0, 1): the rejected put books nothing",
				s.TotalReturned, s.ActiveCount, s.IdleCount)
		}
	})

	t.Run("idle and tombstone counts follow destruction and reclaim", func(t *testing.T) {
		var created atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(2).
			WithMaxIdleTime(150 * time.Millisecond).
			WithScanInterval(conecta.MinScanInterval).
			WithNewFunc(func() (any, error) {
				return fmt.Sprintf("obj-%d", created.Add(1)), nil
			}).
			WithPingFunc(func(any, int) bool { return true }).
			WithCloseFunc(func(any) error { return nil })
		p := mustNewPool(t, conf)
		defer p.Stop()

		v, err := p.GetWithContext(context.Background())
		if err != nil {
			t.Fatalf("GetWithContext() error = %v", err)
		}
		if err := p.Put(v); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		if s := p.Stats(); s.IdleCount != 1 || s.TombstoneCount != 0 {
			t.Errorf("(Idle, Tombstone) = (%d, %d), want (1, 0)", s.IdleCount, s.TombstoneCount)
		}

		// 空闲回收 → tombstone 留队列：Len 含墓碑，Stats 分类精确区分
		// The idle reclaim → the tombstone stays queued: Len counts it while Stats classifies it
		waitUntil(t, 5*time.Second, 20*time.Millisecond,
			func() bool { return p.Stats().TombstoneCount == 1 },
			"the reclaimed object never showed up as a tombstone")
		s := p.Stats()
		if s.IdleCount != 0 || s.TombstoneCount != 1 || p.Len() != 1 {
			t.Errorf("(Idle, Tombstone, Len) = (%d, %d, %d), want (0, 1, 1)", s.IdleCount, s.TombstoneCount, p.Len())
		}

		// Get 回收 tombstone → 队列清空 / the Get reclaim clears the queue
		if _, err := p.Get(); !errors.Is(err, conecta.ErrPoolEmpty) {
			t.Fatalf("Get() error = %v, want ErrPoolEmpty (only a tombstone queued)", err)
		}
		s = p.Stats()
		if s.TombstoneCount != 0 || s.IdleCount != 0 || p.Len() != 0 {
			t.Errorf("(Tombstone, Idle, Len) = (%d, %d, %d), want (0, 0, 0) after the reclaim",
				s.TombstoneCount, s.IdleCount, p.Len())
		}
		assertDestroyIdentity(t, s, "tombstone lifecycle")
	})
}

// TestPool_Stats_WaitMetrics 验收准则 7（等待指标臂）：WaitCount（实际挂起才计数）、
// WaitTimeoutCount（挂起后超时；入口 fast-fail 不计）、TotalWaitDuration（注册到醒来的累计
// 时长，下界断言）——B2 以行为代理钉住的三个字段经 Stats() 直接数值断言
// TestPool_Stats_WaitMetrics covers acceptance criterion 7 (the wait-metrics arm): WaitCount
// (only an actual suspension counts), WaitTimeoutCount (a timeout after suspension; the entry
// fast-fail does not count) and TotalWaitDuration (the cumulative registration-to-wake duration,
// asserted with a lower bound) — the three fields B2 pinned via behavioral proxies are now
// asserted numerically through Stats()
func TestPool_Stats_WaitMetrics(t *testing.T) {
	gate := newGatedCreator()
	conf := conecta.NewConfig().
		WithMaxSize(1).
		WithScanInterval(10000).
		WithNewFunc(gate.newFunc).
		WithPingFunc(func(any, int) bool { return true })
	gate.arm()
	p := mustNewPool(t, conf)
	defer func() {
		gate.tryOpen()
		p.Stop()
	}()

	// C1 钉住唯一在途槽位（门控创建中）/ C1 pins the only in-flight slot (inside the gated creation)
	c1 := startAcquire(p, context.Background())
	gate.awaitPinned()

	// W 挂起（300ms 超时）：注册 → 复查失败 → park → WaitCount+1
	// W suspends (a 300ms timeout): register → the recheck fails → park → WaitCount+1
	wctx, wcancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer wcancel()
	w := startAcquire(p, wctx)
	assertSuspended(t, w)
	if s := p.Stats(); s.WaitCount != 1 || s.WaitTimeoutCount != 0 {
		t.Errorf("(WaitCount, WaitTimeoutCount) = (%d, %d), want (1, 0) while suspended",
			s.WaitCount, s.WaitTimeoutCount)
	}

	// W 超时返回：WaitTimeoutCount+1，TotalWaitDuration 记录完整挂起时长（>=200ms 下界，
	// 挂起窗口由门控钉死确定性构造，仅留调度余量）
	// W times out: WaitTimeoutCount+1 and TotalWaitDuration records the full suspension (a >=200ms
	// lower bound; the suspension window is pinned deterministically by the gate, leaving only
	// scheduling slack)
	res := awaitResult(t, w, 5*time.Second)
	if !errors.Is(res.err, context.DeadlineExceeded) {
		t.Fatalf("waiter error = %v, want context.DeadlineExceeded", res.err)
	}
	s := p.Stats()
	if s.WaitTimeoutCount != 1 {
		t.Errorf("WaitTimeoutCount = %d, want 1", s.WaitTimeoutCount)
	}
	if s.TotalWaitDuration < 200*time.Millisecond {
		t.Errorf("TotalWaitDuration = %v, want >= 200ms (the waiter was suspended for ~300ms)", s.TotalWaitDuration)
	}
	if s.WaitCount != 1 {
		t.Errorf("WaitCount = %d, want 1 (a single suspension)", s.WaitCount)
	}

	// 放行 C1：创建直交账本 / release C1: the creation hand-over ledger
	gate.open()
	c1res := awaitResult(t, c1, 5*time.Second)
	if c1res.err != nil || c1res.value != "created" {
		t.Fatalf("C1 = (%v, %v), want (created, nil)", c1res.value, c1res.err)
	}
	if err := p.Put(c1res.value); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	s = p.Stats()
	if s.TotalCreated != 1 || s.TotalBorrowed != 1 || s.TotalReturned != 1 || s.ActiveCount != 0 {
		t.Errorf("(Created, Borrowed, Returned, Active) = (%d, %d, %d, %d), want (1, 1, 1, 0)",
			s.TotalCreated, s.TotalBorrowed, s.TotalReturned, s.ActiveCount)
	}
}

// TestPool_Stats_DestroyCategories 验收准则 7/10（五分类直断言臂，B3a deferred 义务落地）：
// ping/lifetime/idle/borrow/stop 五个销毁分类各自独立触发并直接数值断言，每臂都验证五分类
// 恒等式 TotalDestroyed == Ping+Lifetime+Idle+Borrow+Stop
// TestPool_Stats_DestroyCategories covers acceptance criteria 7/10 (the direct five-category arm,
// landing B3a's deferred obligation): each of the ping/lifetime/idle/borrow/stop destruction
// categories is triggered independently and asserted numerically, and every arm verifies the
// five-category identity TotalDestroyed == Ping+Lifetime+Idle+Borrow+Stop
func TestPool_Stats_DestroyCategories(t *testing.T) {
	t.Run("destroyedByPing", func(t *testing.T) {
		var created atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(2).
			WithPingMaxRetries(1).
			WithScanInterval(conecta.MinScanInterval).
			WithNewFunc(func() (any, error) {
				return fmt.Sprintf("obj-%d", created.Add(1)), nil
			}).
			WithPingFunc(func(any, int) bool { return false }). // 恒不健康 / never healthy
			WithCloseFunc(func(any) error { return nil })
		p := mustNewPool(t, conf)
		defer p.Stop()

		v, err := p.GetWithContext(context.Background())
		if err != nil {
			t.Fatalf("GetWithContext() error = %v", err)
		}
		if err := p.Put(v); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		waitUntil(t, 5*time.Second, 20*time.Millisecond,
			func() bool { return p.Stats().DestroyedByPing == 1 },
			"the ping-failure destroy was never counted")
		s := p.Stats()
		if s.TotalDestroyed != 1 || s.DestroyedByIdle != 0 || s.DestroyedByLifetime != 0 {
			t.Errorf("(Destroyed, Idle, Lifetime) = (%d, %d, %d), want (1, 0, 0)",
				s.TotalDestroyed, s.DestroyedByIdle, s.DestroyedByLifetime)
		}
		assertDestroyIdentity(t, s, "ping category")
	})

	t.Run("destroyedByLifetime on the borrow path", func(t *testing.T) {
		var created atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(2).
			WithMaxLifetime(30 * time.Millisecond).
			WithScanInterval(10000). // 抑制后台扫描，单独考察借出判定 / suppress the scan to isolate the borrow check
			WithNewFunc(func() (any, error) {
				return fmt.Sprintf("obj-%d", created.Add(1)), nil
			}).
			WithPingFunc(func(any, int) bool { return true }).
			WithCloseFunc(func(any) error { return nil })
		p := mustNewPool(t, conf)
		defer p.Stop()

		v, err := p.GetWithContext(context.Background())
		if err != nil {
			t.Fatalf("GetWithContext() error = %v", err)
		}
		if err := p.Put(v); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		time.Sleep(60 * time.Millisecond) // 超过 30ms 寿命 / beyond the 30ms lifetime
		if _, err := p.Get(); !errors.Is(err, conecta.ErrPoolEmpty) {
			t.Fatalf("Get() error = %v, want ErrPoolEmpty (the expired object is destroyed before lending)", err)
		}
		s := p.Stats()
		if s.DestroyedByLifetime != 1 || s.TotalDestroyed != 1 {
			t.Errorf("(DestroyedByLifetime, TotalDestroyed) = (%d, %d), want (1, 1)",
				s.DestroyedByLifetime, s.TotalDestroyed)
		}
		if s.EmptyCount != 1 {
			t.Errorf("EmptyCount = %d, want 1 (the destroy budget is spent on the single expired object)", s.EmptyCount)
		}
		assertDestroyIdentity(t, s, "lifetime category")
	})

	t.Run("destroyedByIdle", func(t *testing.T) {
		var created atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(2).
			WithMaxIdleTime(150 * time.Millisecond).
			WithScanInterval(conecta.MinScanInterval).
			WithNewFunc(func() (any, error) {
				return fmt.Sprintf("obj-%d", created.Add(1)), nil
			}).
			WithPingFunc(func(any, int) bool { return true }).
			WithCloseFunc(func(any) error { return nil })
		p := mustNewPool(t, conf)
		defer p.Stop()

		v, err := p.GetWithContext(context.Background())
		if err != nil {
			t.Fatalf("GetWithContext() error = %v", err)
		}
		if err := p.Put(v); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		waitUntil(t, 5*time.Second, 20*time.Millisecond,
			func() bool { return p.Stats().DestroyedByIdle == 1 },
			"the idle reclaim was never counted")
		s := p.Stats()
		if s.TotalDestroyed != 1 || s.DestroyedByPing != 0 {
			t.Errorf("(TotalDestroyed, DestroyedByPing) = (%d, %d), want (1, 0)", s.TotalDestroyed, s.DestroyedByPing)
		}
		assertDestroyIdentity(t, s, "idle category")
	})

	t.Run("destroyedByBorrow via a BeforeGet rejection", func(t *testing.T) {
		var created atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(2).
			WithScanInterval(10000).
			WithNewFunc(func() (any, error) {
				if created.Add(1) == 1 {
					return "bad", nil
				}
				return "good", nil
			}).
			WithPingFunc(func(any, int) bool { return true }).
			WithBeforeGet(func(v any) error {
				if v == "bad" {
					return errors.New("rejected on borrow")
				}
				return nil
			}).
			WithCloseFunc(func(any) error { return nil })
		p := mustNewPool(t, conf)
		defer p.Stop()

		// 池自建对象（非外部收编）：GWC 创建直交 "bad" → 归还入队 → 再 GWC 借出时被
		// BeforeGet 拒绝销毁 → 队列已空 → 有界创建 "good" 直交（borrowed 再 +1）
		// Pool-created objects (not foreign adoptions): a GWC creation hands over "bad" → it is
		// returned to the queue → the next GWC borrow is rejected by BeforeGet and destroyed →
		// the queue is now empty → a bounded creation hands over "good" (borrowed +1 again)
		bad, err := p.GetWithContext(context.Background())
		if err != nil || bad != "bad" {
			t.Fatalf("GetWithContext #1 = (%v, %v), want (bad, nil)", bad, err)
		}
		if err := p.Put(bad); err != nil {
			t.Fatalf("Put(bad) error = %v", err)
		}
		v, err := p.GetWithContext(context.Background())
		if err != nil || v != "good" {
			t.Fatalf("GetWithContext #2 = (%v, %v), want (good, nil)", v, err)
		}
		s := p.Stats()
		if s.DestroyedByBorrow != 1 || s.TotalDestroyed != 1 {
			t.Errorf("(DestroyedByBorrow, TotalDestroyed) = (%d, %d), want (1, 1)",
				s.DestroyedByBorrow, s.TotalDestroyed)
		}
		// 账本：两次创建直交入账 borrowed（bad+good），一次归还（bad），good 仍在调用方手中
		// The ledger: both creation hand-overs book borrowed (bad+good), one return (bad), and
		// good is still in the caller's hands
		if s.TotalBorrowed != 2 || s.TotalReturned != 1 || s.ActiveCount != 1 {
			t.Errorf("(TotalBorrowed, TotalReturned, ActiveCount) = (%d, %d, %d), want (2, 1, 1)",
				s.TotalBorrowed, s.TotalReturned, s.ActiveCount)
		}
		assertDestroyIdentity(t, s, "borrow category")
		if err := p.Put(v); err != nil {
			t.Fatalf("Put(good) error = %v", err)
		}
	})

	t.Run("destroyedByStop via Stop", func(t *testing.T) {
		conf := conecta.NewConfig().
			WithMaxSize(4).
			WithScanInterval(10000).
			WithCloseFunc(func(any) error { return nil })
		p := mustNewPool(t, conf)
		for _, v := range []string{"x", "y"} {
			if err := p.Put(v); err != nil {
				t.Fatalf("Put(%q) error = %v", v, err)
			}
		}
		p.Stop()
		s := p.Stats() // Stop 后快照安全 / the post-Stop snapshot is safe
		if s.DestroyedByStop != 2 || s.TotalDestroyed != 2 {
			t.Errorf("(DestroyedByStop, TotalDestroyed) = (%d, %d), want (2, 2)",
				s.DestroyedByStop, s.TotalDestroyed)
		}
		if s.IdleCount != 0 || s.TombstoneCount != 0 {
			t.Errorf("(IdleCount, TombstoneCount) = (%d, %d), want (0, 0): the queue was drained",
				s.IdleCount, s.TombstoneCount)
		}
		assertDestroyIdentity(t, s, "stop category")
	})
}

// TestPool_Stats_DefaultsAndNormalization 验收准则 8 + 归一化：默认配置 Stats 返回合理零值与
// 配置回显；minIdle 负值归零、超过 maxSize 钳制（经 Stats().MinIdle 回显直断言，且钳制后补齐
// 精确收敛到 maxSize 不过冲）；maxIdleTime 负值归零（行为臂：对象跨多轮扫描存活）；导出的
// 默认常量必须为 0（关闭态）且可直接传入 Builder
// TestPool_Stats_DefaultsAndNormalization covers acceptance criterion 8 + normalization: the
// default config's Stats returns sensible zeros with the config echo; a negative minIdle normalizes
// to zero and a minIdle above maxSize is clamped (asserted directly through the Stats().MinIdle
// echo, and the clamped refill converges exactly to maxSize without overshoot); a negative
// maxIdleTime normalizes to zero (the behavioral arm: the object survives several scans); the
// exported default constants must be 0 (disabled) and directly usable in the builders
func TestPool_Stats_DefaultsAndNormalization(t *testing.T) {
	// 导出的默认常量必须为 0（关闭态），且可直接作为对应类型传入 Builder（B1 同款模式）
	// The exported defaults must be 0 (disabled) and directly usable as their types in the builders
	// (the same pattern as B1)
	var (
		defaultMinIdle     int           = conecta.DefaultMinIdle
		defaultMaxIdleTime time.Duration = conecta.DefaultMaxIdleTime
	)
	if defaultMinIdle != 0 || defaultMaxIdleTime != 0 {
		t.Errorf("defaults = (minIdle %d, maxIdleTime %v), want (0, 0s)", defaultMinIdle, defaultMaxIdleTime)
	}

	t.Run("default config snapshot is all zeros with the config echo", func(t *testing.T) {
		var created atomic.Int64
		var closeCount atomic.Int64
		conf := conecta.NewConfig().
			WithScanInterval(conecta.MinScanInterval).
			WithNewFunc(func() (any, error) {
				return fmt.Sprintf("obj-%d", created.Add(1)), nil
			}).
			WithPingFunc(func(any, int) bool { return true }).
			WithCloseFunc(func(any) error {
				closeCount.Add(1)
				return nil
			})
		p := mustNewPool(t, conf)
		defer p.Stop()

		if err := p.Put("conn"); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		// 跨两轮以上扫描：minIdle=0 不补齐（newFunc 从未被调用）、maxIdleTime=0 不回收
		// Across more than two scans: minIdle=0 never refills (newFunc is never called) and
		// maxIdleTime=0 never reclaims
		time.Sleep(700 * time.Millisecond)
		if got := created.Load(); got != 0 {
			t.Errorf("newFunc calls = %d, want 0 (no refill by default)", got)
		}
		if got := closeCount.Load(); got != 0 {
			t.Errorf("closeFunc calls = %d, want 0 (no idle reclaim by default)", got)
		}
		s := p.Stats()
		if s.MinIdle != 0 || s.MaxSize != conecta.DefaultMaxSize {
			t.Errorf("(MaxSize, MinIdle) = (%d, %d), want (%d, 0)", s.MaxSize, s.MinIdle, conecta.DefaultMaxSize)
		}
		if s.TotalCreated != 0 || s.TotalDestroyed != 0 || s.IdleCount != 1 {
			t.Errorf("(Created, Destroyed, Idle) = (%d, %d, %d), want (0, 0, 1)",
				s.TotalCreated, s.TotalDestroyed, s.IdleCount)
		}
		if v, err := p.Get(); err != nil || v != "conn" {
			t.Errorf("Get() = (%v, %v), want (conn, nil)", v, err)
		}
	})

	t.Run("negative minIdle normalizes to zero", func(t *testing.T) {
		p := mustNewPool(t, conecta.NewConfig().WithMinIdle(-1).WithScanInterval(10000))
		defer p.Stop()
		if got := p.Stats().MinIdle; got != 0 {
			t.Errorf("MinIdle = %d, want 0 (a negative minIdle must normalize to zero)", got)
		}
	})

	t.Run("minIdle above maxSize is clamped and the refill converges exactly", func(t *testing.T) {
		var created atomic.Int64
		conf := conecta.NewConfig().
			WithMaxSize(4).
			WithMinIdle(99).
			WithScanInterval(conecta.MinScanInterval).
			WithNewFunc(func() (any, error) {
				return fmt.Sprintf("obj-%d", created.Add(1)), nil
			}).
			WithPingFunc(func(any, int) bool { return true })
		p := mustNewPool(t, conf)
		defer p.Stop()

		if got := p.Stats().MinIdle; got != 4 {
			t.Fatalf("MinIdle = %d, want 4 (clamped to maxSize)", got)
		}
		waitUntil(t, 5*time.Second, 20*time.Millisecond,
			func() bool { return p.Stats().IdleCount == 4 },
			"the clamped refill did not converge to maxSize")
		time.Sleep(400 * time.Millisecond) // 跨一轮确认不过冲 / one more pass to confirm no overshoot
		s := p.Stats()
		if s.TotalCreated != 4 || s.IdleCount != 4 {
			t.Errorf("(TotalCreated, IdleCount) = (%d, %d), want (4, 4): the refill never overshoots the clamp",
				s.TotalCreated, s.IdleCount)
		}
	})

	t.Run("negative maxIdleTime disables the idle reclaim", func(t *testing.T) {
		var pingCount, closeCount atomic.Int64
		conf := conecta.NewConfig().
			WithMaxIdleTime(-5 * time.Second).
			WithScanInterval(conecta.MinScanInterval).
			WithPingFunc(func(any, int) bool {
				pingCount.Add(1)
				return true
			}).
			WithCloseFunc(func(any) error {
				closeCount.Add(1)
				return nil
			})
		p := mustNewPool(t, conf)
		defer p.Stop()

		if err := p.Put("conn"); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		time.Sleep(700 * time.Millisecond) // 跨两轮以上扫描 / more than two scan passes
		if got := closeCount.Load(); got != 0 {
			t.Errorf("closeFunc calls = %d, want 0 (a negative maxIdleTime must normalize to disabled)", got)
		}
		if got := pingCount.Load(); got < 2 {
			t.Errorf("pingFunc calls = %d, want >= 2 (health supervision keeps running)", got)
		}
		if s := p.Stats(); s.DestroyedByIdle != 0 {
			t.Errorf("DestroyedByIdle = %d, want 0", s.DestroyedByIdle)
		}
	})
}

// TestPool_GetWithContext_WakeConsumedByDoneCtxIsRedelivered 钉住 P0-1 唤醒信号蒸发回归：
// maxSize=1、V 借出（在途配额占满）；W1 先注册挂起（可取消 ctx，FIFO 头）、W2 后注册（200ms
// deadline）。时序控制 Put(V) 先于 cancel1：close(w1) 是 W1 parked select 收到的第一个 close，
// 由 gp.selectDone CAS 裁决确定性获胜（runtime/select.go closechan——非 both-ready 50% 随机，
// 任意 GOMAXPROCS 命中）；随后 cancel1 关闭 ctx1.Done。W1 被调度后 select 必走 <-w 分支
// （信号已消费），waitForSlot 返回 (nil,false,nil)，外层 P1-A 循环头见 ctx1 已 done 即退出——
// 消费端配对义务要求该信号在退出前重投递给下一 waiter；缺失重投递时信号蒸发，W2 在
// “对象就在队列中可借”（Len>0）的状态下伪超时（DeadlineExceeded ∧ WaitTimeoutCount+1）。
// 修复后断言：W2 在自身 deadline 内成功借出 V（非超时），且借到的正是归还的 V（同一性）。
// W1 存在一条良性对照臂（D9 分支：select 选中 ctx.Done 且复查发现已被唤醒 → acquireOnce
// 重试拿到 V，(value, nil) 交付、持有即负归还义务）——两臂下 W2 都必须成功。
// TestPool_GetWithContext_WakeConsumedByDoneCtxIsRedelivered pins the P0-1 wake-evaporation
// regression: maxSize=1 with V borrowed out (the in-flight quota is full); W1 registers first
// (a cancelable ctx, the FIFO head) and W2 second (a 200ms deadline). Put(V) is ordered BEFORE
// cancel1: close(w1) is the first close reaching W1's parked select and deterministically wins the
// gp.selectDone CAS (runtime/select.go closechan — not the 50% randomness of a both-ready poll;
// the hit is deterministic at any GOMAXPROCS); cancel1 then closes ctx1.Done. Once scheduled, W1's
// select necessarily takes the <-w branch (the signal is consumed), waitForSlot returns
// (nil,false,nil), and the outer P1-A loop head exits on the already-done ctx1 — the consumer-side
// pairing obligation requires redelivering the signal to the next waiter before that exit; without
// the redelivery the signal evaporates and W2 spuriously times out (DeadlineExceeded with
// WaitTimeoutCount+1) while the object sits borrowable in the queue (Len>0). After the fix W2 must
// borrow V successfully within its own deadline (never a timeout), and the value it borrows must be
// exactly the returned V (identity). W1 keeps one benign counterpart arm (D9: select picks ctx.Done,
// the recheck finds it already woken → the acquireOnce retry takes V and delivers (value, nil) —
// holding the value means owning its return); W2 must succeed under both arms.
func TestPool_GetWithContext_WakeConsumedByDoneCtxIsRedelivered(t *testing.T) {
	conf := conecta.NewConfig().
		WithMaxSize(1).
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) { return new(int), nil })
	p := mustNewPool(t, conf)
	defer p.Stop()

	// V 借出唯一在途配额：inFlight=1=maxSize，队列空
	// V borrows the only in-flight slot: inFlight=1=maxSize with an empty queue
	v, err := p.GetWithContext(context.Background())
	if err != nil {
		t.Fatalf("GetWithContext() error = %v", err)
	}

	// W1 先注册（FIFO 头，可取消）；W2 后注册（200ms deadline）。WaitCount==2 确保两者
	// 都已完成注册并进入挂起（waitCount 在 select 之前递增），Put 的定向唤醒必打 FIFO 头 W1
	// W1 registers first (the FIFO head, cancelable); W2 second (a 200ms deadline). WaitCount==2
	// guarantees both completed registration and suspension (waitCount increments before the
	// select), so Put's targeted wake must hit the FIFO head W1
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	w1 := startAcquire(p, ctx1)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	w2 := startAcquire(p, ctx2)
	waitUntil(t, 5*time.Second, time.Millisecond,
		func() bool { return p.Stats().WaitCount == 2 },
		"both waiters must be suspended first (WaitCount=2)")

	// 时序核心：Put 先于 cancel——close(w1) 先到，parked select 第一 close 确定性获胜
	// The timing core: Put precedes cancel — close(w1) arrives first and wins the parked select's
	// first-close CAS deterministically
	if err := p.Put(v); err != nil {
		t.Fatalf("Put(v) error = %v", err)
	}
	cancel1()

	// W1 两条合法臂：a) <-w 分支消费信号 + 循环头 ctx-done 退出（Canceled，信号必须已重投递）；
	// b) D9 重试交付 (v, nil)——归还后由 Put 的唤醒放行 W2
	// W1's two legal arms: a) the <-w branch consumes the signal and the loop head exits on the
	// done ctx (Canceled — the signal must have been redelivered); b) the D9 retry delivers
	// (v, nil) — returning it releases W2 through the Put's wake
	resW1 := awaitResult(t, w1, 5*time.Second)
	switch {
	case resW1.err == nil:
		t.Logf("W1 took the benign D9 arm: acquired %v with a done ctx", resW1.value)
		if err := p.Put(resW1.value); err != nil {
			t.Fatalf("Put(W1 value) error = %v", err)
		}
	case resW1.err == context.Canceled:
		// 蒸发臂（修复前信号在此丢失；修复后已重投递 W2）
		// The evaporation arm (before the fix the signal was lost here; after the fix it was redelivered to W2)
	default:
		t.Fatalf("W1 error = %v, want context.Canceled or a benign D9 delivery (value, nil)", resW1.err)
	}

	// 核心断言：W2 在 deadline 内成功借出，且借到的正是归还的 V。修复前的蒸发指纹：
	// W2 挂满 200ms → DeadlineExceeded ∧ Len()>0 ∧ WaitTimeoutCount=1
	// The core assertion: W2 borrows successfully within its deadline, and the value is exactly
	// the returned V. The pre-fix evaporation fingerprint: W2 sleeps the full 200ms →
	// DeadlineExceeded with Len()>0 and WaitTimeoutCount=1
	resW2 := awaitResult(t, w2, 5*time.Second)
	if resW2.err != nil || resW2.value == nil {
		s := p.Stats()
		t.Fatalf("W2 result = (%v, %v), want a successful borrow; Len()=%d IdleCount=%d WaitTimeoutCount=%d"+
			" (evaporated-wake fingerprint: DeadlineExceeded while the object sits borrowable in the queue)",
			resW2.value, resW2.err, p.Len(), s.IdleCount, s.WaitTimeoutCount)
	}
	if resW2.value != v {
		t.Errorf("W2 got %v, want the returned object %v (the redelivered wake must lead to borrowing V)", resW2.value, v)
	}
}

// TestConfig_Reuse_TestOnBorrowToggleNotLeaked 钉住 P1-1 Config 原地突变回归（单向棘轮臂）：
// normalizeConfig 曾把 TestOnBorrow 预设闭包直接注入调用方的 Config 本体——第一次 New 之后
// beforeGet != nil 永久成立，复用同一 Config 并显式 WithTestOnBorrow(false) 无法撤销注入
// （注入条件 beforeGet==nil 短路跳过，但旧预设仍在），第二个池的 Get 静默执行陈旧 ping 预设。
// 修复（浅拷贝隔离）后：臂 1 证明 TestOnBorrow=true 时预设确实触发（防真空断言——pingCalls=1，
// 否则臂 2 的 0 断言无意义）；臂 2 证明显式关闭后归一化派生态不再泄漏进复用的 Config（pingCalls=0）。
// TestConfig_Reuse_TestOnBorrowToggleNotLeaked pins the P1-1 in-place Config mutation regression
// (the one-way-ratchet arm): normalizeConfig used to inject the TestOnBorrow preset closure straight
// into the caller's Config — after the first New, beforeGet != nil holds forever, so reusing the same
// Config with an explicit WithTestOnBorrow(false) cannot undo the injection (the beforeGet==nil
// injection condition short-circuits while the stale preset remains), and the second pool's Get
// silently runs the stale ping preset. After the fix (shallow-copy isolation): arm 1 proves the
// preset really fires with TestOnBorrow=true (the anti-vacuity assertion — pingCalls=1, without
// which arm 2's zero assertion is meaningless); arm 2 proves the normalized derived state no longer
// leaks into a reused Config once explicitly disabled (pingCalls=0).
func TestConfig_Reuse_TestOnBorrowToggleNotLeaked(t *testing.T) {
	var pingCalls atomic.Int64
	conf := conecta.NewConfig().
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) { return new(int), nil }).
		WithPingFunc(func(any, int) bool { pingCalls.Add(1); return true }).
		WithTestOnBorrow(true)

	// 臂 1（防真空）：TestOnBorrow=true 时借出必触发预设 ping
	// Arm 1 (anti-vacuity): with TestOnBorrow=true a borrow must fire the preset ping
	p1 := mustNewPool(t, conf)
	seed, err := p1.GetOrCreate()
	require.NoError(t, err)
	require.NoError(t, p1.Put(seed))
	pingCalls.Store(0)
	v1, err := p1.Get()
	require.NoError(t, err)
	if got := pingCalls.Load(); got != 1 {
		t.Fatalf("pingCalls on pool1 (TestOnBorrow=true) = %d, want 1 (the preset must fire — otherwise arm 2 is vacuous)", got)
	}
	require.NoError(t, p1.Put(v1))
	p1.Stop()

	// 臂 2（棘轮回归）：复用同一 Config、显式关闭 TestOnBorrow —— 第二个池的借出不得触发任何 ping
	// Arm 2 (the ratchet regression): reuse the same Config with TestOnBorrow explicitly disabled —
	// the second pool's borrow must not fire any ping
	conf.WithTestOnBorrow(false)
	p2 := mustNewPool(t, conf)
	defer p2.Stop()
	seed2, err := p2.GetOrCreate()
	require.NoError(t, err)
	require.NoError(t, p2.Put(seed2))
	pingCalls.Store(0)
	v2, err := p2.Get()
	require.NoError(t, err)
	defer func() { _ = p2.Put(v2) }()
	if got := pingCalls.Load(); got != 0 {
		t.Errorf("pingCalls on pool2 (TestOnBorrow=false) = %d, want 0 — the BeforeGet preset injected by pool1's New leaked into the reused Config (the one-way ratchet)", got)
	}
}

// TestConfig_ConcurrentNew_SharedTemplateNoRace 钉住 P1-1 的并发臂：模板 Config 并发建多池是
// 常见工厂模式，原地突变的 normalizeConfig 使两个 goroutine 对同一 Config 的派生字段
// （beforeGet/closeFunc/callback）产生无同步写-写竞争（-race 可报的用户侧崩溃面）。浅拷贝
// 隔离后并发 New 只读用户对象、各写私有副本，多轮配对并发建池全程零竞态。
// TestConfig_ConcurrentNew_SharedTemplateNoRace pins the concurrency arm of P1-1: building several
// pools from one template Config concurrently is a common factory pattern, and the mutating
// normalizeConfig turned the derived fields (beforeGet/closeFunc/callback) of the shared Config into
// an unsynchronized write-write race between the two goroutines (a user-visible -race crash
// surface). After the shallow-copy isolation a concurrent New only reads the user's object and
// writes its private copy, so repeated paired concurrent builds stay race-free.
func TestConfig_ConcurrentNew_SharedTemplateNoRace(t *testing.T) {
	conf := conecta.NewConfig().
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) { return new(int), nil }).
		WithPingFunc(func(any, int) bool { return true }).
		WithTestOnBorrow(true)

	// 每轮两 goroutine 各以同一 Config New 一池；多轮放大竞争窗口的观测概率（修复前 -race 报警，
	// 修复后每轮各自命中私有副本）
	// Each round has two goroutines building one pool each from the same Config; the rounds amplify
	// the observation probability of the race window (before the fix -race reports it, after the fix
	// every round lands on its own private copy)
	for round := range 8 {
		pools := make([]*conecta.Pool, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range 2 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				pools[i], errs[i] = conecta.New(wkq.NewQueue(nil), conf)
			}(i)
		}
		wg.Wait()
		for i := range 2 {
			if errs[i] != nil {
				t.Fatalf("round %d: New() error = %v", round, errs[i])
			}
			pools[i].Stop()
		}
	}
}

// TestPool_MaxLifetime_MaxInt64NoOverflow 钉住 P2-1 expireAt 加法溢出回归：
// time.Duration(math.MaxInt64)（≈292 年，“永不过期”的惯用写法）曾使 stampEnqueue 的
// now+effectiveLifetime 在 int64 纳秒域回绕为负，fail-closed 判定（now>=expireAt）把每个
// 新生对象误判为已到期——池退化为“创建即销毁”的 churn 机器（Get 收到 ErrPoolEmpty，
// DestroyedByLifetime 吞掉全部预建对象，配置意图与实际行为完全相反）。溢出卫把回绕钳到
// MaxInt64（等价“永不过期”）后：Get 正常借出、lifetime 销毁为零；门禁与 fail-closed 语义
// 不变（非溢出配置逐字同前，见 TestPool_MaxLifetime_SuperviseEvictsExpired 仍在套件内钉住淘汰臂）。
// TestPool_MaxLifetime_MaxInt64NoOverflow pins the P2-1 expireAt addition-overflow regression:
// time.Duration(math.MaxInt64) (≈292 years — the idiomatic "never expires") used to wrap
// stampEnqueue's now+effectiveLifetime negative in the int64 nanosecond domain, and the fail-closed
// check (now>=expireAt) judged every newborn object already expired — the pool degenerated into a
// create-then-destroy churn machine (Get got ErrPoolEmpty while DestroyedByLifetime swallowed every
// pre-created object, the exact opposite of the configured intent). With the overflow guard clamping
// a wraparound to MaxInt64 (equivalent to "never expires"): Get borrows normally and lifetime
// destructions stay zero; the stamp gate and the fail-closed semantics are untouched (non-overflow
// configurations behave verbatim as before — the eviction arm stays pinned by
// TestPool_MaxLifetime_SuperviseEvictsExpired in the suite).
func TestPool_MaxLifetime_MaxInt64NoOverflow(t *testing.T) {
	conf := conecta.NewConfig().
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) { return new(int), nil }).
		WithInitialize(2).
		WithMaxLifetime(time.Duration(math.MaxInt64))
	p := mustNewPool(t, conf)
	defer p.Stop()

	v, err := p.Get()
	if err != nil {
		s := p.Stats()
		t.Fatalf("Get() error = %v (Len=%d TotalCreated=%d DestroyedByLifetime=%d), want a successful borrow — an expireAt overflow made every object instantly expired",
			err, p.Len(), s.TotalCreated, s.DestroyedByLifetime)
	}
	if v == nil {
		t.Fatalf("Get() = (nil, nil), want an object")
	}
	require.NoError(t, p.Put(v))

	s := p.Stats()
	if s.DestroyedByLifetime != 0 {
		t.Errorf("DestroyedByLifetime = %d, want 0 (a MaxInt64 lifetime means never expires)", s.DestroyedByLifetime)
	}
	if s.TotalCreated != 2 || s.TotalDestroyed != 0 {
		t.Errorf("(TotalCreated, TotalDestroyed) = (%d, %d), want (2, 0): no churn under the overflow guard",
			s.TotalCreated, s.TotalDestroyed)
	}
}

// errQueuePutRejected 是 failPutQueue 的固定拒绝错误（透传断言用哨兵）
// errQueuePutRejected is failPutQueue's fixed rejection error (a sentinel for the passthrough assertion)
var errQueuePutRejected = errors.New("queue put rejected (test)")

// failPutQueue 装饰真实队列：第 failAfter+1 次起的 Put 返回固定错误，确定性构造
// “initialize 中途入队失败”场景（New 接收导出的 Queue 接口，外部测试包可构造——
// 无需白盒钩子）；其余方法全部直通底层队列
// failPutQueue decorates a real queue: every Put from the failAfter+1-th call on returns a fixed
// error, deterministically constructing the "enqueue fails mid-initialize" scenario (New takes the
// exported Queue interface, so this is constructible from the external test package — no
// white-box hooks needed); every other method passes straight through
type failPutQueue struct {
	conecta.Queue
	failAfter int
	puts      atomic.Int64
}

// Put 前 failAfter 次直通，其后固定拒绝
// Put passes through for the first failAfter calls and rejects with a fixed error afterwards
func (q *failPutQueue) Put(v any) error {
	if int(q.puts.Add(1)) > q.failAfter {
		return errQueuePutRejected
	}
	return q.Queue.Put(v)
}

// TestPool_New_InitializeEnqueueFailureFiresOnClose 钉住 P2-2 销毁观察者对称回归：
// initialize 的入队失败分支与 drainAndClose 曾把销毁计入 DestroyedByStop 却不触发 OnClose、
// 并以 `_ =` 吞掉 closeFunc 错误——“账本计为销毁、观察者事件缺席”违背 interface.go 的
// OnClose 契约（在销毁对象时调用），以 OnClose 做资源关闭审计（如连接泄漏核对）的用户在
// New 失败场景漏计。构造：WithInitialize(2) + failAfter=1 的装饰队列——第 1 个对象入队成功，
// 第 2 个触发入队失败分支（销毁 obj-2），随后 drainAndClose 排空销毁 obj-1。
// 修复后两个销毁点都镜像 refillOne 的既有配对 closeFunc→OnClose(value, closeErr)：
// OnClose 恰好 2 次、两个 value 各关闭一次、队列错误经 New 原样透传、池不交付。
// TestPool_New_InitializeEnqueueFailureFiresOnClose pins the P2-2 destruction-observer symmetry
// regression: initialize's enqueue-failure branch and drainAndClose used to book destructions into
// DestroyedByStop without firing OnClose, swallowing the closeFunc error with `_ =` — "booked as
// destroyed while the observer event is absent" violates the OnClose contract in interface.go
// (called when an object is destroyed), so users auditing resource closes through OnClose (e.g.
// connection-leak reconciliation) undercounted on the New-failure path. Construction:
// WithInitialize(2) plus a failAfter=1 decorated queue — the first object enqueues, the second
// triggers the enqueue-failure branch (destroying obj-2), then drainAndClose empties and destroys
// obj-1. After the fix both destruction points mirror refillOne's existing closeFunc→OnClose(value,
// closeErr) pairing: OnClose fires exactly twice, each value is closed exactly once, the queue
// error passes through New as-is, and no pool is delivered.
func TestPool_New_InitializeEnqueueFailureFiresOnClose(t *testing.T) {
	var cb countingCallback
	var closed closedRecorder
	var created atomic.Int64
	conf := conecta.NewConfig().
		WithInitialize(2).
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) {
			return fmt.Sprintf("obj-%d", created.Add(1)), nil
		}).
		WithCloseFunc(func(v any) error {
			closed.add(v)
			return nil
		}).
		WithCallback(&cb)

	q := &failPutQueue{Queue: wkq.NewQueue(nil), failAfter: 1}
	p, err := conecta.New(q, conf)
	if p != nil {
		p.Stop()
		t.Fatalf("New() pool = %v, want nil (initialize must fail)", p)
	}
	if err != errQueuePutRejected {
		t.Fatalf("New() error = %v, want the bare errQueuePutRejected (unwrapped passthrough)", err)
	}

	// 两个销毁点（入队失败分支的 obj-2 + drainAndClose 的 obj-1）都必须触发观察者事件
	// Both destruction points (obj-2 in the enqueue-failure branch + obj-1 in drainAndClose)
	// must fire the observer event
	if got := cb.close.Load(); got != 2 {
		t.Errorf("OnClose calls = %d, want 2 (one per destroyed object — a booked destruction without an observer event is the P2-2 defect)", got)
	}
	if closed.total() != 2 || closed.count("obj-1") != 1 || closed.count("obj-2") != 1 {
		t.Errorf("closeFunc records: total=%d obj-1=%d obj-2=%d, want each of obj-1/obj-2 closed exactly once",
			closed.total(), closed.count("obj-1"), closed.count("obj-2"))
	}
}

// TestPool_New_NilQueueRejected 补齐覆盖缺口 a：New 的 nil-queue 守卫与导出哨兵 ErrQueueIsNil
// （此前全文件零引用）。== 直判钉住裸哨兵契约；nil Config 同样先命中 queue 守卫（守卫先于归一化）
// TestPool_New_NilQueueRejected fills coverage gap a: New's nil-queue guard and the exported sentinel
// ErrQueueIsNil (zero references across the file until now). The == comparison pins the bare-sentinel
// contract; a nil Config hits the same queue guard first (the guard precedes normalization)
func TestPool_New_NilQueueRejected(t *testing.T) {
	p, err := conecta.New(nil, conecta.NewConfig())
	if err != conecta.ErrQueueIsNil {
		t.Errorf("New(nil, conf) error = %v, want the bare ErrQueueIsNil (== identity)", err)
	}
	if p != nil {
		t.Errorf("New(nil, conf) pool = %v, want nil", p)
	}
	p2, err2 := conecta.New(nil, nil)
	if err2 != conecta.ErrQueueIsNil || p2 != nil {
		t.Errorf("New(nil, nil) = (%v, %v), want (nil, ErrQueueIsNil) — the queue guard precedes config normalization", p2, err2)
	}
}

// TestPool_AfterPut_NotRunAfterStop 补齐覆盖缺口 b：AfterPut 的“Stop 后不执行”文档化契约
// （config.go/interface.go 均声明；Put 的免锁停止快检先于钩子执行）。防真空臂：Stop 前钩子
// 确实被接线执行（计数 1）；Stop 后 Put 返回 ErrQueueClosed 且钩子计数不再增长（增量 0）
// TestPool_AfterPut_NotRunAfterStop fills coverage gap b: AfterPut's documented "never runs after
// Stop" contract (declared in both config.go and interface.go; Put's lock-free stop fast check runs
// before the hook). Anti-vacuity arm: the hook is demonstrably wired before Stop (count 1); after
// Stop, Put returns ErrQueueClosed and the hook count never grows (zero increment)
func TestPool_AfterPut_NotRunAfterStop(t *testing.T) {
	var hookCalls atomic.Int64
	conf := conecta.NewConfig().
		WithScanInterval(10000).
		WithAfterPut(func(any) error { hookCalls.Add(1); return nil })
	p := mustNewPool(t, conf)

	if err := p.Put("before-stop"); err != nil {
		t.Fatalf("Put(before-stop) error = %v", err)
	}
	if got := hookCalls.Load(); got != 1 {
		t.Fatalf("AfterPut calls before Stop = %d, want 1 (the hook must be wired — otherwise the post-Stop zero assertion is vacuous)", got)
	}

	p.Stop()
	if err := p.Put("after-stop"); err != conecta.ErrQueueClosed {
		t.Errorf("Put after Stop = %v, want ErrQueueClosed", err)
	}
	if got := hookCalls.Load(); got != 1 {
		t.Errorf("AfterPut calls after Stop = %d, want 1 (the hook must never run after Stop)", got)
	}
}

// TestPool_Cleanup_SuspendedWaiterProgresses 补齐覆盖缺口 c：standalone Cleanup 与挂起 waiter
// 并存时不得扰动 waiter 协议（不注销、不丢失、不误广播）——Cleanup 之后 waiter 必须仍能经
// 后续池事件正常推进。构造：maxSize=1 + 门控创建占位（在途配额钉死）→ W 挂起 → Cleanup
// （队列空、配额被在途创建占用，无可销毁对象——单 waiter 下“Cleanup 销毁并唤醒”的稳态
// 共存被双序协议排除：任何入队/释放必先唤醒 W，故本测试钉住的是协议完整性臂）→ 放行门控、
// 归还对象 → Put 的定向唤醒必须仍能找到 W。断言：W 重新获取成功（value 同一性），或按契约
// 允许的 ErrQueueClosed（standalone Cleanup 不关队列，实际恒为成功臂——任何其他错误或永久
// 挂起都是 waiter 被 Cleanup 破坏的证据）
// TestPool_Cleanup_SuspendedWaiterProgresses fills coverage gap c: a standalone Cleanup coexisting
// with a suspended waiter must not disturb the waiter protocol (no deregistration, no loss, no
// spurious broadcast) — after Cleanup the waiter must still progress normally through later pool
// events. Construction: maxSize=1 + a gated creation pinning the in-flight quota → W suspends →
// Cleanup (the queue is empty and the quota is held by the in-flight creation, so there is nothing
// to destroy — with a single waiter the "Cleanup destroys and wakes" steady-state coexistence is
// ruled out by the dual-order protocol: any enqueue/release wakes W first, so this test pins the
// protocol-integrity arm) → open the gate and return the object → Put's targeted wake must still
// find W. Assertion: W re-acquires successfully (value identity), or the contractually allowed
// ErrQueueClosed (a standalone Cleanup never closes the queue, so in practice this is always the
// success arm — any other error or a permanent suspension proves Cleanup corrupted the waiter)
func TestPool_Cleanup_SuspendedWaiterProgresses(t *testing.T) {
	g := newGatedCreator()
	conf := conecta.NewConfig().WithMaxSize(1).WithScanInterval(10000).WithNewFunc(g.newFunc)
	p := mustNewPool(t, conf)
	defer p.Stop()
	defer g.tryOpen()

	g.arm()
	g1 := startAcquire(p, context.Background())
	g.awaitPinned()
	w := startAcquire(p, context.Background())
	assertSuspended(t, w)

	p.Cleanup()
	assertSuspended(t, w) // Cleanup 无可销毁对象：W 必须原样保持挂起 / nothing to destroy: W must stay suspended as-is

	g.open()
	resG1 := awaitResult(t, g1, 5*time.Second)
	if resG1.err != nil || resG1.value == nil {
		t.Fatalf("G1 result = (%v, %v), want an acquired object", resG1.value, resG1.err)
	}
	if err := p.Put(resG1.value); err != nil {
		t.Fatalf("Put(G1 value) error = %v", err)
	}
	resW := awaitResult(t, w, 5*time.Second)
	if resW.err == conecta.ErrQueueClosed {
		return // 契约允许臂 / the contractually allowed arm
	}
	if resW.err != nil || resW.value != resG1.value {
		t.Fatalf("W result = (%v, %v), want the successful re-acquisition of %v (the waiter must survive a standalone Cleanup)",
			resW.value, resW.err, resG1.value)
	}
	if got := p.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0 (W borrowed the returned object)", got)
	}
}

// TestPool_Cleanup_MinIdleRefillRestoresLevel 补齐覆盖缺口 d：standalone Cleanup 后池仍存活，
// MinIdle 自动补齐把空闲水位恢复回 minIdle——pool.go 将其文档化为“语义合理但可能出乎意料”
// 的行为，正是最该被测试钉住的意外语义。防真空臂：created==4 证明恢复的水位是补齐新建的
// 对象（2 启动 + 2 补齐），而非 Cleanup 的幸存者
// TestPool_Cleanup_MinIdleRefillRestoresLevel fills coverage gap d: after a standalone Cleanup the
// pool stays alive and the MinIdle auto-refill restores the idle water level back to minIdle —
// documented in pool.go as "semantically sound but possibly surprising", exactly the kind of
// surprising semantics a test should pin. Anti-vacuity arm: created==4 proves the restored level
// consists of freshly refilled objects (2 startup + 2 refill), not Cleanup survivors
func TestPool_Cleanup_MinIdleRefillRestoresLevel(t *testing.T) {
	var created atomic.Int64
	conf := conecta.NewConfig().
		WithMaxSize(4).
		WithMinIdle(2).
		WithScanInterval(conecta.MinScanInterval).
		WithNewFunc(func() (any, error) { return fmt.Sprintf("obj-%d", created.Add(1)), nil }).
		WithPingFunc(func(any, int) bool { return true })
	p := mustNewPool(t, conf)
	defer p.Stop()

	waitUntil(t, 5*time.Second, 20*time.Millisecond,
		func() bool { return p.Stats().IdleCount == 2 },
		"the startup refill must first reach minIdle=2 (anti-vacuity)")
	p.Cleanup()
	if got := p.Len(); got != 0 {
		t.Fatalf("Len() after Cleanup = %d, want 0", got)
	}
	waitUntil(t, time.Duration(2*conecta.MinScanInterval)*time.Millisecond+time.Second, 20*time.Millisecond,
		func() bool { return p.Stats().IdleCount == 2 },
		"the refill did not restore IdleCount to minIdle=2 within 2 scan intervals after Cleanup")
	if got := created.Load(); got != 4 {
		t.Errorf("newFunc calls = %d, want 4 (2 startup + 2 refill — the restored level must be freshly created objects)", got)
	}
}
