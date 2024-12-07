package conecta

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/shengyanli1982/conecta/internal/pool"
)

var (
	ErrorQueueClosed         = errors.New("queue is closed")
	ErrorQueueInterfaceIsNil = errors.New("queue interface is nil")
)

type Pool struct {
	queue       Queue
	config      *Config
	wg          sync.WaitGroup
	once        sync.Once
	ctx         context.Context
	cancel      context.CancelFunc
	elementpool *pool.ElementPool
}

func New(queue Queue, conf *Config) (*Pool, error) {
	if queue == nil {
		return nil, ErrorQueueInterfaceIsNil
	}

	conf = isConfigValid(conf)
	ctx, cancel := context.WithCancel(context.Background())

	p := &Pool{
		queue:       queue,
		config:      conf,
		elementpool: pool.NewElementPool(),
		ctx:         ctx,
		cancel:      cancel,
	}

	if err := p.initialize(); err != nil {
		cancel()
		return nil, err
	}

	p.wg.Add(1)
	go p.executor()

	return p, nil
}

func (p *Pool) Stop() {
	p.once.Do(func() {
		p.cancel()
		p.wg.Wait()
		p.cleanupElements()
		p.queue.Shutdown()
	})
}

func (p *Pool) initialize() error {
	elements := make([]any, 0, p.config.initialize)

	for i := 0; i < p.config.initialize; i++ {
		if value, err := p.config.newFunc(); err != nil {
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

	for _, value := range elements {
		element := p.elementpool.Get()
		element.SetData(value)

		if err := p.queue.Put(element); err != nil {
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

func (p *Pool) executor() {
	ticker := time.NewTicker(time.Millisecond * time.Duration(p.config.scanInterval))
	defer ticker.Stop()
	defer p.wg.Done()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.processElements()
		}
	}
}

func (p *Pool) processElements() {
	p.queue.Range(func(data any) bool {
		element := data.(*pool.Element)
		value := element.GetData()
		retryCount := int(element.GetValue())

		if retryCount >= p.config.maxRetries {
			if value != nil {
				err := p.config.closeFunc(value)
				p.config.callback.OnClose(value, err)
				element.SetData(nil)
			}
			p.queue.Done(element)
			p.elementpool.Put(element)
			return true
		}

		if ok := p.config.pingFunc(value, retryCount); ok {
			element.SetValue(0)
			p.config.callback.OnPingSuccess(value)
		} else {
			element.SetValue(int64(retryCount) + 1)
			p.config.callback.OnPingFailure(value)
		}

		return true
	})
}

func (p *Pool) cleanupElements() {
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

func (p *Pool) Get() (any, error) {
	if p.queue.IsClosed() {
		return nil, ErrorQueueClosed
	}

	ctx, cancel := context.WithTimeout(p.ctx, time.Duration(p.config.scanInterval)*time.Millisecond)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			element, err := p.queue.Get()
			if err != nil {
				return nil, err
			}

			p.queue.Done(element)
			data := element.(*pool.Element)
			value := data.GetData()
			p.elementpool.Put(data)

			if value != nil {
				return value, nil
			}
		}
	}
}

func (p *Pool) GetOrCreate() (any, error) {
	value, err := p.Get()
	if err == nil {
		return value, nil
	}

	if errors.Is(err, ErrorQueueClosed) {
		return nil, err
	}

	return p.config.newFunc()
}

func (p *Pool) Len() int {
	return p.queue.Len()
}

func (p *Pool) Cleanup() {
	p.cleanupElements()
}
