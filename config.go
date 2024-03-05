package conecta

import "math"

// 定义一些默认的常量
// Define some default constants
const (
	DefaultInitialize       = 0     // 默认初始化元素的数量 Default number of elements to initialize
	DefaultMaxPingRetry     = 3     // 默认最大重试次数 Default maximum number of retries
	DefaultScanInterval     = 10000 // 默认扫描全部对象实例间隔 (ms) Default interval to scan all object instances (ms)
	DefautMiniItemsInterval = 200   // 默认最小元素间隔 (ms) Default minimum element interval (ms)
)

// 定义一些默认的函数
// Define some default functions
var (
	DefaultNewFunc   = func() (any, error) { return nil, nil } // 默认的创建新元素的函数 Default function to create a new element
	DefaultPingFunc  = func(any, int) bool { return true }     // 默认的验证函数 Default validation function
	DefaultCloseFunc = func(any) error { return nil }          // 默认的关闭函数 Default close function
)

// Config 是配置的结构体
// Config is the struct of configuration
type Config struct {
	maxRetries   int       // 最大重试次数 Maximum number of retries
	initialize   int       // 初始化元素的数量 Number of elements to initialize
	scanInterval int       // 扫描全部对象实例间隔 Interval to scan all object instances
	newFunc      NewFunc   // 创建新元素的函数 Function to create a new element
	pingFunc     PingFunc  // 验证函数 Validation function
	closeFunc    CloseFunc // 关闭函数 Close function
	callback     Callback  // 回调函数 Callback function
}

// NewConfig 是创建新的配置的函数
// NewConfig is the function to create a new configuration
func NewConfig() *Config {
	return &Config{
		initialize:   DefaultInitialize,
		maxRetries:   DefaultMaxPingRetry,
		scanInterval: DefaultScanInterval,
		newFunc:      DefaultNewFunc,
		pingFunc:     DefaultPingFunc,
		closeFunc:    DefaultCloseFunc,
		callback:     newEmptyCallback(),
	}
}

// DefaultConfig 是获取默认配置的函数
// DefaultConfig is the function to get the default configuration
func DefaultConfig() *Config {
	return NewConfig()
}

// WithCallback 是设置回调函数的方法
// WithCallback is the method to set the callback function
func (c *Config) WithCallback(callback Callback) *Config {
	c.callback = callback
	return c
}

// WithInitialize 是设置初始化元素的数量的方法
// WithInitialize is the method to set the number of elements to initialize
func (c *Config) WithInitialize(init int) *Config {
	c.initialize = init
	return c
}

// WithScanInterval 是设置扫描全部对象实例间隔的方法
// WithScanInterval is the method to set the interval to scan all object instances
func (c *Config) WithScanInterval(scanInterval int) *Config {
	c.scanInterval = scanInterval
	return c
}

// WithNewFunc 是设置创建新元素的函数的方法
// WithNewFunc is the method to set the function to create a new element
func (c *Config) WithNewFunc(newFunc NewFunc) *Config {
	c.newFunc = newFunc
	return c
}

// WithCloseFunc 是设置关闭函数的方法
// WithCloseFunc is the method to set the close function
func (c *Config) WithCloseFunc(closeFunc CloseFunc) *Config {
	c.closeFunc = closeFunc
	return c
}

// WithPingFunc 是设置验证函数的方法
// WithPingFunc is the method to set the validation function
func (c *Config) WithPingFunc(pingFunc PingFunc) *Config {
	c.pingFunc = pingFunc
	return c
}

// WithPingMaxRetries 是设置最大重试次数的方法
// WithPingMaxRetries is the method to set the maximum number of retries
func (c *Config) WithPingMaxRetries(maxRetries int) *Config {
	c.maxRetries = maxRetries
	return c
}

// isConfigValid 是验证配置是否有效的函数
// isConfigValid is the function to validate whether the configuration is valid
func isConfigValid(conf *Config) *Config {
	if conf != nil {
		if conf.initialize < 0 {
			conf.initialize = DefaultInitialize
		}
		if conf.maxRetries <= 0 || conf.maxRetries >= math.MaxUint16 {
			conf.maxRetries = DefaultMaxPingRetry
		}
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
			conf.callback = newEmptyCallback()
		}
	} else {
		conf = NewConfig()
	}

	return conf
}