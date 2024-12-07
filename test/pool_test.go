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
	assert.Equal(t, wkq.ErrQueueIsEmpty, err)
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
	assert.Equal(t, conecta.ErrorQueueClosed, err)
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
	scanInterval := 5000

	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().WithCallback(&testCallback{t: t}).WithPingFunc(testCallbackPingFunc).WithCloseFunc(testCallbackCloseFunc).WithPingMaxRetries(1).WithScanInterval(scanInterval)
	assert.NotNil(t, conf)

	p, err := conecta.New(queue, conf)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	_ = p.Put("success")
	_ = p.Put("fail")

	fmt.Println("Please wait for the callback to be executed... (11 seconds)")

	time.Sleep(time.Millisecond * time.Duration(scanInterval*2+1000))
}

func TestPool_Initialize(t *testing.T) {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().WithInitialize(2)
	assert.NotNil(t, conf)

	p, err := conecta.New(queue, conf)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	assert.Equal(t, 2, p.Len())
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

	for i := 0; i < 100; i++ {
		go func() {
			_ = p.Put("item1")
		}()
	}

	time.Sleep(time.Second)

	assert.Equal(t, 100, p.Len())
}

func TestPool_GetWithParallel(t *testing.T) {
	queue := wkq.NewQueue(nil)
	p, err := conecta.New(queue, nil)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	for i := 0; i < 100; i++ {
		go func() {
			_ = p.Put("item1")
		}()
	}

	time.Sleep(time.Second)

	for i := 0; i < 100; i++ {
		go func() {
			_, _ = p.Get()
		}()
	}

	time.Sleep(time.Second)

	assert.Equal(t, 0, p.Len())
}

func TestPool_GetOrCreateWithParallel(t *testing.T) {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().WithNewFunc(testNewFunc)
	assert.NotNil(t, conf)

	p, err := conecta.New(queue, conf)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	for i := 0; i < 20; i++ {
		go func() {
			_, _ = p.GetOrCreate()
		}()
	}

	time.Sleep(time.Second)

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
	assert.NoError(t, err)
	assert.Equal(t, 1, p.Len())
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
	assert.Equal(t, wkq.ErrQueueIsEmpty, err)
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
	assert.Equal(t, conecta.ErrorQueueClosed, err)
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
		WithScanInterval(100)

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
	var wg sync.WaitGroup
	wg.Add(numProducers + numConsumers)

	// 启动生产者
	for i := 0; i < numProducers; i++ {
		go func(producerID int) {
			defer wg.Done()
			for j := 0; j < itemsPerProducer; j++ {
				item := fmt.Sprintf("producer_%d_item_%d", producerID, j)
				_ = p.Put(item)
			}
		}(i)
	}

	// 用于记录消费的元素数量
	var consumedCount int32

	// 启动消费者
	for i := 0; i < numConsumers; i++ {
		go func() {
			defer wg.Done()
			for {
				_, err := p.Get()
				if err != nil {
					// 如果获取失败，短暂等待后重试
					time.Sleep(time.Millisecond)
					continue
				}
				atomic.AddInt32(&consumedCount, 1)
			}
		}()
	}

	// 等待生产者完成
	time.Sleep(time.Second)

	// 确保总的生产数量正确
	totalProduced := numProducers * itemsPerProducer
	currentLen := p.Len()
	consumed := int(atomic.LoadInt32(&consumedCount))

	// 验证：已消费数量 + 当前队列中的数量 = 总生产数量
	assert.Equal(t, totalProduced, consumed+currentLen,
		"Total items (%d) should equal consumed items (%d) plus items in queue (%d)",
		totalProduced, consumed, currentLen)

	// 停止池，确保清理
	p.Stop()
	assert.Equal(t, 0, p.Len())
}
