package conecta

import "math"

const (
	// 默认初始化元素的数量
	// DefaultInitialize is the default number of elements to initialize.
	DefaultInitialize = 0

	// 默认最大重试次数
	// DefaultMaxPingRetry is the default number of times to retry a validation.
	DefaultMaxPingRetry = 3

	// 默认扫描全部对象实例间隔 (ms)
	// DefaultScanInterval is the default interval to scan all object instances. (ms)
	DefaultScanInterval = 10000

	// 默认最小元素间隔 (ms)
	// DefaultMiniItemsInterval is the default minimum interval between elements. (ms)
	DefautMiniItemsInterval = 200
)

var (
	// DefaultNewFunc is the default function to create a new element.
	DefaultNewFunc = func() (any, error) { return nil, nil }

	// DefaultPingFunc is the default function to validate an element.
	DefaultPingFunc = func(any, int) bool { return true }

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
	scanInterval int
	newFunc      func() (any, error)
	pingFunc     func(any, int) bool
	closeFunc    func(any) error
	callback     Callback
}

// 返回一个新的配置
// NewConfig returns a new configuration.
func NewConfig() *Config {
	conf := Config{
		initialize:   DefaultInitialize,
		maxRetries:   DefaultMaxPingRetry,
		scanInterval: DefaultScanInterval,
		newFunc:      DefaultNewFunc,
		pingFunc:     DefaultPingFunc,
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

// WithScanInterval 设置扫描全部对象实例间隔
// WithScanInterval sets the interval to scan all object instances.
func (c *Config) WithScanInterval(scanInterval int) *Config {
	c.scanInterval = scanInterval
	return c
}

// WithNewFunc 设置创建元素的函数
// WithNewFunc sets the function to create an element.
func (c *Config) WithNewFunc(newFunc func() (any, error)) *Config {
	c.newFunc = newFunc
	return c
}

// WithPingFunc 设置验证元素的函数
// WithPingFunc sets the function to validate an element.
func (c *Config) WithPingFunc(pingFunc func(any, int) bool) *Config {
	c.pingFunc = pingFunc
	return c
}

// WithPingMaxRetries 设置最大重试次数
// WithPingMaxRetries sets the maximum number of retries.
func (c *Config) WithPingMaxRetries(maxRetries int) *Config {
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
		if conf.initialize < 0 {
			conf.initialize = DefaultInitialize
		}
		if conf.maxRetries <= 0 || conf.maxRetries >= math.MaxUint16 {
			conf.maxRetries = DefaultMaxPingRetry
		}
		// 扫描间隔不能小于初始化元素间隔, 否则会导致初始化元素无法被扫描到。
		// 如果 scanInterval <= initialize*DefautMiniItemsInterval, 则 scanInterval = DefaultScanInterval
		// The scan interval cannot be less than the initialization element interval,
		// otherwise the initialization element cannot be scanned.
		// If scanInterval <= initialize*DefautMiniItemsInterval, then scanInterval = DefaultScanInterval
		if conf.scanInterval <= conf.initialize*DefautMiniItemsInterval {
			conf.scanInterval = DefaultScanInterval
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
