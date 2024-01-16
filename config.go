package conecta

const (
	DefaultInitialize   = 1
	DefaultMaxPingRetry = 3
)

var (
	DefaultNewFunc   = func() (any, error) { return nil, nil }
	DefaultPingFunc  = func(any) bool { return true }
	DefaultCloseFunc = func(any) error { return nil }
)

type Config struct {
	maxRetries int
	initialize int
	newFunc    func() (any, error)
	pingFunc   func(any) bool
	closeFunc  func(any) error
	callback   Callback
}

func NewConfig() *Config {
	conf := Config{
		initialize: DefaultInitialize,
		newFunc:    DefaultNewFunc,
		pingFunc:   DefaultPingFunc,
		closeFunc:  DefaultCloseFunc,
		callback:   &emptyCallback{},
	}

	return &conf
}

func DefaultConfig() *Config {
	return NewConfig()
}

func (c *Config) WithPingMaxRetries(maxRetries int) *Config {
	c.maxRetries = maxRetries
	return c
}

func (c *Config) WithCallback(callback Callback) *Config {
	c.callback = callback
	return c
}

func (c *Config) WithInitialize(init int) *Config {
	c.initialize = init
	return c
}

func (c *Config) WithNewFunc(newFunc func() (any, error)) *Config {
	c.newFunc = newFunc
	return c
}

func (c *Config) WithPingFunc(pingFunc func(any) bool) *Config {
	c.pingFunc = pingFunc
	return c
}

func (c *Config) WithCloseFunc(closeFunc func(any) error) *Config {
	c.closeFunc = closeFunc
	return c
}

func isConfigValid(conf *Config) *Config {
	if conf != nil {
		if conf.initialize <= 0 {
			conf.initialize = DefaultInitialize
		}
		if conf.newFunc == nil {
			conf.newFunc = DefaultNewFunc
		}
		if conf.pingFunc == nil {
			conf.pingFunc = DefaultPingFunc
		}
		if conf.closeFunc == nil {
			conf.closeFunc = DefaultCloseFunc
		}
		if conf.callback == nil {
			conf.callback = &emptyCallback{}
		}
	} else {
		return NewConfig()
	}

	return conf
}
