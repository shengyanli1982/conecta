package conecta

import (
	"errors"
	"math"
)

// 定义一些默认的常量
// Define some default constants
const (
	// DefaultInitialize 是默认初始化元素的数量
	// DefaultInitialize is the default number of elements to initialize
	DefaultInitialize = 0

	// DefaultMaxPingRetry 是默认的最大 ping 重试次数
	// DefaultMaxPingRetry is the default maximum number of ping retries
	DefaultMaxPingRetry = 3

	// DefaultScanInterval 是默认扫描全部对象实例的间隔 (ms)
	// DefaultScanInterval is the default interval to scan all object instances (ms)
	DefaultScanInterval = 10000

	// MinScanInterval 是扫描间隔的下限 (ms)
	// MinScanInterval is the minimum allowed scan interval (ms)
	MinScanInterval = 300
)

// 定义一些默认的函数
// Define some default functions
var (
	// DefaultNewFunc 是默认的创建新元素的函数（未配置时返回错误）
	// DefaultNewFunc is the default function to create a new element (returns an error when not configured)
	DefaultNewFunc = func() (any, error) { return nil, errors.New("newFunc not configured") }

	// DefaultPingFunc 是默认的验证函数（始终返回健康）
	// DefaultPingFunc is the default validation function (always reports healthy)
	DefaultPingFunc = func(any, int) bool { return true }

	// DefaultCloseFunc 是默认的关闭函数（空操作）
	// DefaultCloseFunc is the default close function (no-op)
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

// normalizeConfig 验证并规范化配置
// normalizeConfig validates and normalizes the configuration
func normalizeConfig(conf *Config) *Config {
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

		// 如果扫描间隔小于扫描间隔下限，钳制到下限
		// If the scan interval is less than the minimum allowed scan interval, clamp to the minimum
		if conf.scanInterval < MinScanInterval {
			conf.scanInterval = MinScanInterval
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
