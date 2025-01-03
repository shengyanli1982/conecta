package conecta

import "math"

// 定义一些默认的常量
// Define some default constants
const (
	// 默认初始化元素的数量
	// Default number of elements to initialize
	DefaultInitialize = 0

	// 默认最大重试次数
	// Default maximum number of retries
	DefaultMaxPingRetry = 3

	// 默认扫描全部对象实例间隔 (ms)
	// Default interval to scan all object instances (ms)
	DefaultScanInterval = 10000

	// 默认最小元素扫描间隔 (ms)
	// Default minimum element scan interval (ms)
	DefautMiniScanItemsInterval = 300
)

// 定义一些默认的函数
// Define some default functions
var (
	// 默认的创建新元素的函数
	// Default function to create a new element
	DefaultNewFunc = func() (any, error) { return nil, nil }

	// 默认的验证函数
	// Default validation function
	DefaultPingFunc = func(any, int) bool { return true }

	// 默认的关闭函数
	// Default close function
	DefaultCloseFunc = func(any) error { return nil }
)

// Config 是配置的结构体
// Config is the struct of configuration
type Config struct {
	// 最大重试次数
	// Maximum number of retries
	maxRetries int

	// 初始化元素的数量
	// Number of elements to initialize
	initialize int

	// 扫描全部对象实例间隔
	// Interval to scan all object instances
	scanInterval int

	// 创建新元素的函数
	// Function to create a new element
	newFunc NewFunc

	// 验证函数
	// Validation function
	pingFunc PingFunc

	// 关闭函数
	// Close function
	closeFunc CloseFunc

	// 回调函数
	// Callback function
	callback Callback
}

// NewConfig 是创建新的配置的函数
// NewConfig is the function to create a new configuration
func NewConfig() *Config {
	// 返回一个新的配置对象，其中包含了默认的初始化元素数量、最大重试次数、扫描间隔、创建新元素的函数、验证函数、关闭函数和回调函数
	// Returns a new configuration object, which includes the default number of elements to initialize, maximum number of retries, scan interval, function to create a new element, validation function, close function, and callback function
	return &Config{
		// 默认的初始化元素数量
		// Default number of elements to initialize
		initialize: DefaultInitialize,

		// 默认的最大重试次数
		// Default maximum number of retries
		maxRetries: DefaultMaxPingRetry,

		// 默认的扫描间隔
		// Default scan interval
		scanInterval: DefaultScanInterval,

		// 默认的创建新元素的函数
		// Default function to create a new element
		newFunc: DefaultNewFunc,

		// 默认的验证函数
		// Default validation function
		pingFunc: DefaultPingFunc,

		// 默认的关闭函数
		// Default close function
		closeFunc: DefaultCloseFunc,

		// 默认的回调函数
		// Default callback function
		callback: newEmptyCallback(),
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
	// 如果配置不为空
	// If the configuration is not null
	if conf != nil {
		// 如果初始化值小于0，设置为默认初始化值
		// If the initialization value is less than 0, set it to the default initialization value
		if conf.initialize < 0 {
			conf.initialize = DefaultInitialize
		}

		// 如果最大重试次数小于等于0或大于等于最大无符号16位整数，设置为默认最大Ping重试次数
		// If the maximum number of retries is less than or equal to 0 or greater than or equal to the maximum unsigned 16-bit integer, set it to the default maximum Ping retry count
		if conf.maxRetries <= 0 || conf.maxRetries >= math.MaxUint16 {
			conf.maxRetries = DefaultMaxPingRetry
		}

		// 如果扫描间隔小于等于初始化值乘以默认最小项间隔，设置为默认扫描间隔
		// If the scan interval is less than or equal to the initialization value times the default minimum item interval, set it to the default scan interval
		if conf.scanInterval < DefautMiniScanItemsInterval {
			conf.scanInterval = DefaultScanInterval
		}

		// 如果新建函数为空，设置为默认新建函数
		// If the new function is null, set it to the default new function
		if conf.newFunc == nil {
			conf.newFunc = DefaultNewFunc
		}

		// 如果Ping函数为空，设置为默认Ping函数
		// If the Ping function is null, set it to the default Ping function
		if conf.pingFunc == nil {
			conf.pingFunc = DefaultPingFunc
		}

		// 如果关闭函数为空，设置为默认关闭函数
		// If the close function is null, set it to the default close function
		if conf.closeFunc == nil {
			conf.closeFunc = DefaultCloseFunc
		}

		// 如果回调为空，设置为新的空回调
		// If the callback is null, set it to a new empty callback
		if conf.callback == nil {
			conf.callback = newEmptyCallback()
		}
	} else {
		// 如果配置为空，新建一个配置
		// If the configuration is null, create a new configuration
		conf = NewConfig()
	}

	// 返回配置
	// Return the configuration
	return conf
}
