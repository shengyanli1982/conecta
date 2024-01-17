package conecta

import "math"

const (
	// 默认初始化元素的数量
	// DefaultInitialize is the default number of elements to initialize.
	DefaultInitialize = 1
	// 默认最大重试次数
	// DefaultMaxValidateRetry is the default number of times to retry a validation.
	DefaultMaxValidateRetry = 3
)

var (
	// DefaultNewFunc is the default function to create a new element.
	DefaultNewFunc = func() (any, error) { return nil, nil }
	// DefaultValidateFunc is the default function to validate an element.
	DefaultValidateFunc = func(any, int) bool { return true }
	// DefaultCloseFunc is the default function to close an element.
	DefaultCloseFunc = func(any) error { return nil }
)

type Config struct {
	// These variables define the configuration options for the `Config` struct.
	// maxRetries specifies the maximum number of times to retry a validation.
	// initialize specifies the number of elements to initialize.
	// newFunc is a function that creates a new element.
	// validateFunc is a function that validates an element.
	// closeFunc is a function that closes an element.
	// callback is a callback interface for handling events.
	maxRetries   int
	initialize   int
	newFunc      func() (any, error)
	validateFunc func(any, int) bool
	closeFunc    func(any) error
	callback     Callback
}

// 返回一个新的配置
// NewConfig returns a new configuration.
func NewConfig() *Config {
	conf := Config{
		initialize:   DefaultInitialize,
		maxRetries:   DefaultMaxValidateRetry,
		newFunc:      DefaultNewFunc,
		validateFunc: DefaultValidateFunc,
		closeFunc:    DefaultCloseFunc,
		callback:     &emptyCallback{},
	}

	return &conf
}

// 返回一个默认的配置
// DefaultConfig returns a default configuration.
func DefaultConfig() *Config {
	return NewConfig()
}

// WithCallback 设置回调接口
// WithCallback sets the callback interface.
func (c *Config) WithCallback(callback Callback) *Config {
	c.callback = callback
	return c
}

// WithInitialize 设置初始化元素的数量
// WithInitialize sets the number of elements to initialize.
func (c *Config) WithInitialize(init int) *Config {
	c.initialize = init
	return c
}

// WithNewFunc 设置创建元素的函数
// WithNewFunc sets the function to create an element.
func (c *Config) WithNewFunc(newFunc func() (any, error)) *Config {
	c.newFunc = newFunc
	return c
}

// WithValidateFunc 设置验证元素的函数
// WithValidateFunc sets the function to validate an element.
func (c *Config) WithValidateFunc(pingFunc func(any, int) bool) *Config {
	c.validateFunc = pingFunc
	return c
}

// WithValidateMaxRetries 设置最大重试次数
// WithValidateMaxRetries sets the maximum number of retries.
func (c *Config) WithValidateMaxRetries(maxRetries int) *Config {
	c.maxRetries = maxRetries
	return c
}

// WithCloseFunc 设置关闭元素的函数
// WithCloseFunc sets the function to close an element.
func (c *Config) WithCloseFunc(closeFunc func(any) error) *Config {
	c.closeFunc = closeFunc
	return c
}

// 返回一个有效的配置
// isConfigValid returns a valid configuration.
func isConfigValid(conf *Config) *Config {
	if conf != nil {
		if conf.initialize <= 0 {
			conf.initialize = DefaultInitialize
		}
		if conf.maxRetries <= 0 || conf.maxRetries >= math.MaxUint16 {
			conf.maxRetries = DefaultMaxValidateRetry
		}
		if conf.newFunc == nil {
			conf.newFunc = DefaultNewFunc
		}
		if conf.validateFunc == nil {
			conf.validateFunc = DefaultValidateFunc
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
