package conecta

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/shengyanli1982/conecta"
	wkq "github.com/shengyanli1982/workqueue/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testCallback struct {
	t *testing.T
	sync.Mutex
	pingSuccessCount int
	pingFailureCount int
	closeCount       int
}

func (c *testCallback) OnPingSuccess(data any) {
	c.Lock()
	defer c.Unlock()
	c.pingSuccessCount++
	assert.Equal(c.t, "success", data.(string))
}

func (c *testCallback) OnPingFailure(data any) {
	c.Lock()
	defer c.Unlock()
	c.pingFailureCount++
	assert.Equal(c.t, "fail", data.(string))
}

func (c *testCallback) OnClose(data any, err error) {
	c.Lock()
	defer c.Unlock()
	c.closeCount++
}

func testCallbackPingFunc(data any, c int) bool {
	return data.(string) == "success"
}

func testCallbackCloseFunc(data any) error {
	return nil
}

func testErrorNewFunc() (any, error) {
	return nil, errors.New("new func error")
}

// 基本功能测试
func TestPool_New_Success(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()
}

func TestPool_New_NilQueue(t *testing.T) {
	p, err := conecta.New(nil, nil)
	require.Error(t, err)
	require.Nil(t, p)
	assert.Equal(t, conecta.ErrorQueueInterfaceIsNil, err)
}

func TestPool_Put_Basic(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	require.NoError(t, err)
	defer p.Stop()

	err = p.Put("item1")
	require.NoError(t, err)
	assert.Equal(t, 1, p.Len())
}

func TestPool_Put_AfterStop(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	require.NoError(t, err)

	p.Stop()
	err = p.Put("item1")
	assert.Equal(t, conecta.ErrorQueueClosed, err)
}

func TestPool_Get_EmptyQueue(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	require.NoError(t, err)
	defer p.Stop()

	_, err = p.Get()
	assert.Equal(t, wkq.ErrQueueIsEmpty, err)
}

func TestPool_Get_AfterStop(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	require.NoError(t, err)

	p.Stop()
	_, err = p.Get()
	assert.Equal(t, conecta.ErrorQueueClosed, err)
}

// 并发测试
func TestPool_Put_ConcurrentAccess(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	require.NoError(t, err)
	defer p.Stop()

	var wg sync.WaitGroup
	itemCount := 1000

	wg.Add(itemCount)
	for i := 0; i < itemCount; i++ {
		go func(val int) {
			defer wg.Done()
			err := p.Put(fmt.Sprintf("item%d", val))
			require.NoError(t, err)
		}(i)
	}
	wg.Wait()

	assert.Equal(t, itemCount, p.Len())
}

func TestPool_Get_ConcurrentAccess(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	require.NoError(t, err)
	defer p.Stop()

	itemCount := 1000
	for i := 0; i < itemCount; i++ {
		err := p.Put(fmt.Sprintf("item%d", i))
		require.NoError(t, err)
	}

	var wg sync.WaitGroup
	wg.Add(itemCount)

	results := make(map[string]bool)
	var resultMutex sync.Mutex

	for i := 0; i < itemCount; i++ {
		go func() {
			defer wg.Done()
			val, err := p.Get()
			require.NoError(t, err)

			resultMutex.Lock()
			results[val.(string)] = true
			resultMutex.Unlock()
		}()
	}
	wg.Wait()

	assert.Equal(t, itemCount, len(results))
	assert.Equal(t, 0, p.Len())
}

// 边界测试
func TestPool_Initialize_ZeroSize(t *testing.T) {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().WithInitialize(0)
	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	defer p.Stop()

	assert.Equal(t, 0, p.Len())
}

func TestPool_Initialize_NegativeSize(t *testing.T) {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().WithInitialize(-1)
	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	defer p.Stop()

	assert.Equal(t, 0, p.Len())
}

func TestPool_Initialize_ErrorInNewFunc(t *testing.T) {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().
		WithInitialize(5).
		WithNewFunc(testErrorNewFunc)

	p, err := conecta.New(queue, conf)
	require.Error(t, err)
	assert.Nil(t, p)
}

// 回调测试
func TestPool_Callback_RetryAndClose(t *testing.T) {
	queue := wkq.NewQueue(nil)
	callback := &testCallback{t: t}

	conf := conecta.NewConfig().
		WithCallback(callback).
		WithPingFunc(testCallbackPingFunc).
		WithCloseFunc(testCallbackCloseFunc).
		WithPingMaxRetries(2).
		WithScanInterval(100)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	defer p.Stop()

	_ = p.Put("success")
	_ = p.Put("fail")

	// 等待足够的时间让回调执行
	time.Sleep(time.Millisecond * 500)

	assert.Equal(t, 4, callback.pingSuccessCount)
	assert.GreaterOrEqual(t, callback.pingFailureCount, 2)
	assert.Equal(t, 1, callback.closeCount)
}

// 资源清理测试
func TestPool_Cleanup_WithCallbacks(t *testing.T) {
	queue := wkq.NewQueue(nil)
	callback := &testCallback{t: t}

	conf := conecta.NewConfig().
		WithCallback(callback).
		WithCloseFunc(testCallbackCloseFunc)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	defer p.Stop()

	itemCount := 5
	for i := 0; i < itemCount; i++ {
		_ = p.Put(fmt.Sprintf("item%d", i))
	}

	p.Stop()
	assert.Equal(t, itemCount, callback.closeCount)
	assert.Equal(t, 0, p.Len())
}

func TestPool_Stop_MultipleStops(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	require.NoError(t, err)

	// 多次调用 Stop 应该是安全的
	p.Stop()
	p.Stop()
	p.Stop()

	assert.Equal(t, 0, p.Len())
}

// GetOrCreate 测试
func TestPool_GetOrCreate(t *testing.T) {
	queue := wkq.NewQueue(nil)

	// 创建配置并设置 newFunc
	conf := conecta.NewConfig().
		WithNewFunc(func() (any, error) {
			return "new_item", nil
		})

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	defer p.Stop()

	// 测试从空队列创建新元素
	val1, err := p.GetOrCreate()
	require.NoError(t, err)
	require.NotNil(t, val1)
	assert.Equal(t, "new_item", val1)

	// 放入一个元素后测试获取
	err = p.Put("test_item")
	require.NoError(t, err)

	val2, err := p.GetOrCreate()
	require.NoError(t, err)
	assert.Equal(t, "test_item", val2)

	// 测试队列���闭后的行为
	p.Stop()
	_, err = p.GetOrCreate()
	assert.Equal(t, conecta.ErrorQueueClosed, err)
}

// 添加错误情况的测试
func TestPool_GetOrCreate_NewFuncError(t *testing.T) {
	queue := wkq.NewQueue(nil)

	// 创建配置并设置返回错误的 newFunc
	conf := conecta.NewConfig().
		WithNewFunc(func() (any, error) {
			return nil, errors.New("new func error")
		})

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	defer p.Stop()

	// 测试 GetOrCreate 在 newFunc 返回错误时的行为
	val, err := p.GetOrCreate()
	require.Error(t, err)
	assert.Nil(t, val)
	assert.Equal(t, "new func error", err.Error())
}

// Cleanup 独立测试
func TestPool_Cleanup_Standalone(t *testing.T) {
	queue := wkq.NewQueue(nil)
	callback := &testCallback{t: t}

	conf := conecta.NewConfig().
		WithCallback(callback).
		WithCloseFunc(testCallbackCloseFunc)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	defer p.Stop()

	// 添加测试数据
	for i := 0; i < 5; i++ {
		err := p.Put(fmt.Sprintf("item%d", i))
		require.NoError(t, err)
	}

	// 执行清理
	p.Cleanup()

	// 验证结果
	assert.Equal(t, 5, callback.closeCount)
	assert.Equal(t, 0, p.Len())
}

// 工作池并发限制测试
func TestPool_WorkerPool_ConcurrencyLimit(t *testing.T) {
	queue := wkq.NewQueue(nil)
	callback := &testCallback{t: t}

	conf := conecta.NewConfig().
		WithCallback(callback).
		WithPingFunc(func(data any, c int) bool {
			time.Sleep(time.Millisecond * 100) // 模拟耗时操作
			return true
		}).
		WithScanInterval(50)

	p, err := conecta.New(queue, conf)
	require.NoError(t, err)
	defer p.Stop()

	// 添加测试数据
	itemCount := 50
	for i := 0; i < itemCount; i++ {
		err := p.Put("success")
		require.NoError(t, err)
	}

	// 修改：使用更精确的等待方式
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		callback.Lock()
		count := callback.pingSuccessCount
		callback.Unlock()

		if count >= itemCount {
			break
		}
		time.Sleep(time.Millisecond * 100)
	}

	// 验证处理数量
	callback.Lock()
	count := callback.pingSuccessCount
	callback.Unlock()

	assert.GreaterOrEqual(t, count, itemCount, "Should process all items")
}
