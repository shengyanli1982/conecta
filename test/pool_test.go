package conecta

import (
	"fmt"
	"testing"
	"time"

	"github.com/shengyanli1982/conecta"
	"github.com/shengyanli1982/workqueue"
	"github.com/stretchr/testify/assert"
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
	queue := workqueue.NewSimpleQueue(nil)
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
	queue := workqueue.NewSimpleQueue(nil)
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
	assert.Equal(t, workqueue.ErrorQueueEmpty, err)
}

func TestPool_GetOrCreate(t *testing.T) {
	queue := workqueue.NewSimpleQueue(nil)

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
	queue := workqueue.NewSimpleQueue(nil)
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
	queue := workqueue.NewSimpleQueue(nil)
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

	queue := workqueue.NewSimpleQueue(nil)
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
	queue := workqueue.NewSimpleQueue(nil)
	conf := conecta.NewConfig().WithInitialize(2).WithNewFunc(testNewFunc)
	assert.NotNil(t, conf)

	p, err := conecta.New(queue, conf)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	defer p.Stop()

	assert.Equal(t, 2, p.Len())
}

func TestPool_PutWithParallel(t *testing.T) {
	queue := workqueue.NewSimpleQueue(nil)
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
	queue := workqueue.NewSimpleQueue(nil)
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
	queue := workqueue.NewSimpleQueue(nil)
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
