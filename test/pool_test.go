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
	q0 := workqueue.NewSimpleQueue(nil)
	p := conecta.NewPool(q0, nil)
	assert.NotNil(t, p)

	defer p.Stop()

	_ = p.Put("item1")
	_ = p.Put("item2")
	_ = p.Put("item3")

	assert.Equal(t, 3, p.Len())
}

func TestPool_Get(t *testing.T) {
	q0 := workqueue.NewSimpleQueue(nil)
	p := conecta.NewPool(q0, nil)
	assert.NotNil(t, p)

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
	q0 := workqueue.NewSimpleQueue(nil)

	conf := conecta.NewConfig().WithNewFunc(testNewFunc)
	assert.NotNil(t, conf)

	p := conecta.NewPool(q0, conf)
	assert.NotNil(t, p)

	defer p.Stop()

	data, err := p.GetOrCreate()
	assert.NotNil(t, data)
	assert.Nil(t, err)
	assert.Equal(t, "success", data.(string))
}

func TestPool_Stop(t *testing.T) {
	q0 := workqueue.NewSimpleQueue(nil)
	p := conecta.NewPool(q0, nil)
	assert.NotNil(t, p)

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
	q0 := workqueue.NewSimpleQueue(nil)
	p := conecta.NewPool(q0, nil)
	assert.NotNil(t, p)

	defer p.Stop()

	_ = p.Put("item1")
	_ = p.Put("item2")
	_ = p.Put("item3")

	assert.Equal(t, 3, p.Len())
}

func TestPool_Callback(t *testing.T) {
	q0 := workqueue.NewSimpleQueue(nil)
	conf := conecta.NewConfig().WithCallback(&testCallback{t: t}).WithPingFunc(testCallbackPingFunc).WithCloseFunc(testCallbackCloseFunc).WithPingMaxRetries(1)
	assert.NotNil(t, conf)

	p := conecta.NewPool(q0, conf)
	assert.NotNil(t, p)

	defer p.Stop()

	_ = p.Put("success")
	_ = p.Put("fail")

	fmt.Println("Please wait for the callback to be executed... (11 seconds)")

	time.Sleep(time.Second * 11)
}
