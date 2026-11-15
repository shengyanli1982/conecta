package conecta

import (
	"errors"
	"math"
	"time"
)

// 定义一些默认的常量
// Define some default constants
const (
	// DefaultInitialize 是默认初始化元素的数量
	// DefaultInitialize is the default number of elements to initialize
	DefaultInitialize = 0

	// DefaultMaxSize 是默认连接池最大容量
	// DefaultMaxSize is the default maximum capacity of the pool
	DefaultMaxSize = 1024

	// DefaultMaxPingRetry 是默认的最大 ping 重试次数
	// DefaultMaxPingRetry is the default maximum number of ping retries
	DefaultMaxPingRetry = 3

	// DefaultScanInterval 是默认扫描全部对象实例的间隔 (ms)
	// DefaultScanInterval is the default interval to scan all object instances (ms)
	DefaultScanInterval = 10000

	// MinScanInterval 是扫描间隔的下限 (ms)
	// MinScanInterval is the minimum allowed scan interval (ms)
	MinScanInterval = 300

	// DefaultMaxLifetime 是默认的对象最大存活时间，0 表示不限制（关闭年龄淘汰，保持向后兼容）
	// DefaultMaxLifetime is the default maximum lifetime of an object; 0 means unlimited (age eviction disabled, backward compatible)
	DefaultMaxLifetime = 0

	// DefaultMaxLifetimeJitter 是默认的最大存活时间抖动，0 表示不抖动
	// DefaultMaxLifetimeJitter is the default jitter applied to the maximum lifetime; 0 means no jitter
	DefaultMaxLifetimeJitter = 0

	// DefaultAcquireTimeout 是默认的阻塞获取超时，0 表示不设默认超时
	//（GetWithContext 在 ctx 无 deadline 时将无限期等待，直至被唤醒或池停止）
	// DefaultAcquireTimeout is the default blocking-acquire timeout; 0 means no default timeout is applied
	// (GetWithContext then waits indefinitely when ctx carries no deadline, until woken or the pool stops)
	DefaultAcquireTimeout = 0

	// DefaultMinIdle 是默认的最小空闲水位，0 表示关闭水位保持（不做后台异步补齐，向后兼容）
	// DefaultMinIdle is the default minimum idle water level; 0 disables water-level maintenance
	// (no background async refill, backward compatible)
	DefaultMinIdle = 0

	// DefaultMaxIdleTime 是默认的对象最大空闲时间，0 表示不限制（关闭空闲淘汰，向后兼容）
	// DefaultMaxIdleTime is the default maximum idle time of an object; 0 means unlimited
	// (idle eviction disabled, backward compatible)
	DefaultMaxIdleTime = 0
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

	// 连接池最大容量：封顶在途对象总数（已借出 + 池内空闲 + 在途创建预留，P0-B）——
	// GetWithContext 在途满时挂起等待归还；Put 另按队列占用（size）以 ErrPoolFull 拒绝归还；
	// GetOrCreate 的按需创建不受该上限约束（A1 向后兼容）
	// Maximum capacity of the pool: caps the total in-flight object count (borrowed + idle in the pool +
	// in-flight creation reservations, P0-B) — GetWithContext suspends until a return when the in-flight
	// quota is full; Put separately rejects returns with ErrPoolFull based on the queue occupancy (size);
	// GetOrCreate's on-demand creation is NOT constrained by this cap (A1 backward compatibility)
	maxSize int

	// 扫描全部对象实例间隔
	// Interval to scan all object instances
	scanInterval int

	// 对象最大存活时间，0 表示不限制；到期对象由后台扫描与借出判定淘汰
	// Maximum lifetime of an object, 0 means unlimited; expired objects are evicted by the background scan and by the borrow check
	maxLifetime time.Duration

	// 最大存活时间的随机抖动上界，用于打散同批对象的到期时刻，避免集体过期引发创建风暴
	// Upper bound of the random jitter applied to the maximum lifetime, spreading expiry times of a cohort to avoid a creation storm
	maxLifetimeJitter time.Duration

	// 阻塞获取的默认超时：GetWithContext 在传入 ctx 无 deadline 时以该时长派生超时上下文；
	// 0 表示不设默认超时（无限期等待）。ctx 自带 deadline 时该配置不生效
	// Default timeout for blocking acquisition: GetWithContext derives a timeout context of this duration
	// when the incoming ctx carries no deadline; 0 means no default timeout (wait indefinitely).
	// The setting has no effect when the ctx already carries a deadline
	acquireTimeout time.Duration

	// 最小空闲水位：后台扫描发现有效空闲对象数（队列中未销毁的对象，不含 tombstone）低于该值时，
	// 异步补齐至该水位（P1-4，对齐 pgxpool.MinConns / go-redis MinIdleConns）。补齐经 reserveSlot
	// 走在途封顶（minIdle ≤ maxSize 归一化保证不超发）；0（默认）表示关闭水位保持。
	// 建议与 initialize 配合使用（initialize == minIdle 消除冷启动窗口）
	// Minimum idle water level: when the background scan finds fewer effective idle objects (live
	// objects in the queue, tombstones excluded) than this value, it asynchronously refills the pool
	// back to this level (P1-4, aligning with pgxpool.MinConns / go-redis MinIdleConns). Refill
	// creations go through reserveSlot under the in-flight cap (the minIdle ≤ maxSize normalization
	// guarantees no over-issuing); 0 (the default) disables water-level maintenance. Combine it with
	// initialize (initialize == minIdle eliminates the cold-start window)
	minIdle int

	// 对象最大空闲时间：自最近一次入队（开始空闲）起持续空闲超过该时长的对象由后台扫描回收，
	// 但回收不得打穿 minIdle 下限（对齐 HikariCP idleTimeout / Commons Pool2 的 softMinEvictable
	// 语义）。0（默认）表示不限制；与 maxLifetime 语义无冲突可同时设置——maxLifetime 是对象年龄
	// 硬上限（无论是否被借出过），maxIdleTime 是空闲软回收（波峰波谷流量下把水位降回合理值）
	// Maximum idle time of an object: an object idling continuously for longer than this duration
	// since its most recent enqueue (idle start) is reclaimed by the background scan, never breaking
	// through the minIdle floor (aligning with HikariCP's idleTimeout / Commons Pool2's
	// softMinEvictableIdleTime semantics). 0 (the default) means unlimited; it composes with
	// maxLifetime without conflict — maxLifetime is the hard age cap (regardless of borrows) while
	// maxIdleTime is the soft idle reclaim (bringing the water level back down under tidal traffic)
	maxIdleTime time.Duration

	// 创建新元素的函数
	// Function to create a new element
	newFunc NewFunc

	// 验证函数
	// Validation function
	pingFunc PingFunc

	// 关闭函数
	// Close function
	closeFunc CloseFunc

	// 借出前决策钩子：出队后、返回调用方前在池外（不持任何池内锁）调用；返回 error/panic
	// 则销毁该元素并尝试队列下一个（语义与约束见 BeforeGetFunc）。nil（默认）表示关闭
	// Pre-lend decision hook: invoked outside the pool (holding no pool lock) after dequeue and
	// before returning to the caller; an error/panic destroys the element and the loop tries the
	// next queued one (see BeforeGetFunc for semantics and constraints). nil (the default) disables it
	beforeGet BeforeGetFunc

	// 归还前决策钩子：Put 收到元素后、入队前在池外（不持任何池内锁、停止快检之后）调用；
	// 返回 error/panic 则拒绝归还、销毁该元素并由 Put 透传该 error（语义与约束见 AfterPutFunc）。
	// nil（默认）表示关闭
	// Pre-enqueue decision hook on return: invoked outside the pool (holding no pool lock, after the
	// stop fast check) once Put has received the element and before it is enqueued; an error/panic
	// rejects the return, destroys the element and Put passes the error through (see AfterPutFunc for
	// semantics and constraints). nil (the default) disables it
	afterPut AfterPutFunc

	// 借出时验证开关：true 且未设置 beforeGet 时，normalizeConfig 自动注入 pingFunc 校验作为
	// BeforeGet 预设（TestOnBorrow 即 BeforeGet 的便捷预设，不是独立机制）；beforeGet 已设置时
	// 该开关不生效（用户钩子优先）。false（默认）表示关闭。
	// 性能护栏：开启后每次借出增加一次 pingFunc 调用，pingFunc 涉及网络 IO 时须评估开销
	// Borrow-time validation switch: when true and no beforeGet is set, normalizeConfig injects a
	// pingFunc check as the BeforeGet preset (TestOnBorrow is a convenience preset of BeforeGet, not
	// a separate mechanism); the switch has no effect once beforeGet is set (the user hook takes
	// precedence). false (the default) disables it. Performance guard: enabling it adds one pingFunc
	// call per borrow — evaluate the cost when pingFunc involves network IO
	testOnBorrow bool

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

		// 默认的连接池最大容量
		// Default maximum capacity of the pool
		maxSize: DefaultMaxSize,

		// 默认的最大重试次数
		// Default maximum number of retries
		maxRetries: DefaultMaxPingRetry,

		// 默认的扫描间隔
		// Default scan interval
		scanInterval: DefaultScanInterval,

		// 默认的对象最大存活时间（0 = 关闭年龄淘汰）
		// Default maximum object lifetime (0 = age eviction disabled)
		maxLifetime: DefaultMaxLifetime,

		// 默认的最大存活时间抖动（0 = 不抖动）
		// Default maximum lifetime jitter (0 = no jitter)
		maxLifetimeJitter: DefaultMaxLifetimeJitter,

		// 默认的阻塞获取超时（0 = 不设默认超时）
		// Default blocking-acquire timeout (0 = no default timeout)
		acquireTimeout: DefaultAcquireTimeout,

		// 默认的最小空闲水位（0 = 关闭水位保持）
		// Default minimum idle water level (0 = water-level maintenance disabled)
		minIdle: DefaultMinIdle,

		// 默认的对象最大空闲时间（0 = 关闭空闲淘汰）
		// Default maximum idle time (0 = idle eviction disabled)
		maxIdleTime: DefaultMaxIdleTime,

		// 默认的创建新元素的函数
		// Default function to create a new element
		newFunc: DefaultNewFunc,

		// 默认的验证函数
		// Default validation function
		pingFunc: DefaultPingFunc,

		// 默认的关闭函数
		// Default close function
		closeFunc: DefaultCloseFunc,

		// 默认的借出/归还决策钩子（nil = 关闭，热路径零开销）
		// Default borrow/return decision hooks (nil = disabled, zero cost on hot paths)
		beforeGet: nil,
		afterPut:  nil,

		// 默认的借出时验证开关（false = 关闭）
		// Default borrow-time validation switch (false = disabled)
		testOnBorrow: false,

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

// WithMaxSize 是设置连接池最大容量的方法。容量封顶在途对象总数（已借出 + 池内空闲 + 在途创建
// 预留）：GetWithContext 在途满时挂起等待归还（对齐 pgxpool.MaxConns / database/sql.MaxOpenConns），
// Put 在队列占用满时以 ErrPoolFull 拒绝归还；GetOrCreate 的按需创建不受约束（A1，在途总数可瞬时
// 超限，突破对象同样计入在途）。maxSize<=0 时归一化回落到 DefaultMaxSize
// WithMaxSize is the method to set the maximum capacity of the pool. The capacity caps the total
// in-flight object count (borrowed + idle in the pool + in-flight creation reservations):
// GetWithContext suspends until a return when the in-flight quota is full (aligning with
// pgxpool.MaxConns / database/sql.MaxOpenConns), and Put rejects returns with ErrPoolFull when the queue
// occupancy is full; GetOrCreate's on-demand creation is unconstrained (A1 — the in-flight total may
// momentarily exceed the cap, and break-through objects still count as in-flight). maxSize<=0 is
// normalized back to DefaultMaxSize
func (c *Config) WithMaxSize(maxSize int) *Config {
	c.maxSize = maxSize
	return c
}

// WithScanInterval 是设置扫描全部对象实例间隔的方法
// WithScanInterval is the method to set the interval to scan all object instances
func (c *Config) WithScanInterval(scanInterval int) *Config {
	c.scanInterval = scanInterval
	return c
}

// WithMaxLifetime 是设置对象最大存活时间的方法；对象自入池起存活超过该时长后会被淘汰，
// 用于主动轮换对象以抵御服务端 idle kill、中间件强制断连与凭据轮换。
// 0（默认）表示不限制，即关闭年龄淘汰，行为与未引入该特性时完全一致；负值归一化为 0
// WithMaxLifetime is the method to set the maximum lifetime of an object; an object living longer than
// this duration since it entered the pool is evicted, which proactively rotates objects to survive
// server-side idle kills, middleware-forced disconnects and credential rotation.
// 0 (the default) means unlimited, i.e. age eviction is disabled and behavior is identical to before this
// feature existed; negative values are normalized to 0
func (c *Config) WithMaxLifetime(maxLifetime time.Duration) *Config {
	c.maxLifetime = maxLifetime
	return c
}

// WithMaxLifetimeJitter 是设置最大存活时间随机抖动上界的方法；每个对象的实际存活时长为
// maxLifetime + [0, jitter] 内的一次随机取值，用于打散同批对象的到期时刻，避免集体过期引发创建风暴。
// 0（默认）表示不抖动；负值归一化为 0；超过 maxLifetime/2 时钳制到 maxLifetime/2，防止寿命不可预测
// WithMaxLifetimeJitter is the method to set the upper bound of the random jitter applied to the maximum
// lifetime; each object's effective lifetime is maxLifetime plus one random value drawn from [0, jitter],
// spreading the expiry times of a cohort so they do not all expire at once and trigger a creation storm.
// 0 (the default) means no jitter; negative values are normalized to 0; values above maxLifetime/2 are
// clamped to maxLifetime/2 so the lifetime stays predictable
func (c *Config) WithMaxLifetimeJitter(maxLifetimeJitter time.Duration) *Config {
	c.maxLifetimeJitter = maxLifetimeJitter
	return c
}

// WithAcquireTimeout 是设置阻塞获取默认超时的方法；GetWithContext 在传入的 ctx 不带 deadline 时，
// 以该时长派生一个超时上下文，超时返回 context.DeadlineExceeded，为不显式传超时的调用方提供兜底保护。
// ctx 已带 deadline 时以 ctx 自身的 deadline 为准，该配置不生效（不派生新上下文）。
// 0（默认）表示不设默认超时；负值归一化为 0。生产部署建议显式配置该超时（或始终传入带 deadline 的
// ctx）：默认 0 时无 deadline 的调用会无限期挂起，且在途配额被泄漏（借出未归还）时挂起不可自愈
// WithAcquireTimeout is the method to set the default timeout for blocking acquisition; when the ctx passed
// to GetWithContext carries no deadline, a timeout context of this duration is derived and a timeout returns
// context.DeadlineExceeded, providing a safety net for callers that do not pass an explicit deadline.
// When the ctx already carries a deadline, the ctx's own deadline takes precedence and this setting has no
// effect (no new context is derived). 0 (the default) means no default timeout; negative values are
// normalized to 0. Production deployments should set this timeout explicitly (or always pass a ctx with a
// deadline): with the default 0 a deadline-free call suspends indefinitely, and the suspension cannot
// self-heal once an in-flight slot has been leaked (borrowed and never returned)
func (c *Config) WithAcquireTimeout(acquireTimeout time.Duration) *Config {
	c.acquireTimeout = acquireTimeout
	return c
}

// WithMinIdle 是设置最小空闲水位的方法：后台扫描（每 scanInterval 一轮）发现有效空闲对象数
// （队列中未销毁的对象，不含 tombstone）低于该水位时，在扫描末尾异步补齐至水位——补齐在独立
// goroutine 中执行（不阻塞健康扫描周期），经 reserveSlot 走在途封顶（绝不突破 maxSize），
// 单个补齐 goroutine 以 CAS 标志节流（上一轮未完成时本轮跳过，防止创建风暴），创建失败即停止
// 本轮、依赖下个扫描周期自然重试（失败退避）。补齐入队后按唤醒义务定向唤醒一个 waiter（提升
// 尾延迟）。minIdle 同时也是 MaxIdleTime 空闲回收的下限护栏（回收不打穿该水位）。
// 0（默认）表示关闭水位保持，行为与未引入该特性时完全一致；负值归一化为 0；超过 maxSize 时
// 钳制到 maxSize。建议 initialize == minIdle 作为默认组合（消除首轮扫描前的冷启动窗口）；
// 启用时 NewFunc 必须可用（补齐依赖它创建对象）
// WithMinIdle is the method to set the minimum idle water level: when the background scan (one pass
// per scanInterval) finds fewer effective idle objects (live objects in the queue, tombstones
// excluded) than this level, it asynchronously refills the pool back to the level at the end of the
// pass — the refill runs in a dedicated goroutine (never blocking the health-scan cycle), goes
// through reserveSlot under the in-flight cap (never breaking maxSize), and is throttled by a CAS
// flag so a single refill goroutine runs at a time (a pass is skipped while the previous refill is
// still running, preventing creation storms); a creation failure stops the current round and the
// next scan interval retries naturally (failure backoff). After a refill enqueue, one waiter is
// woken in a targeted fashion per the wake obligation (improving tail latency). minIdle is also the
// floor guard of MaxIdleTime idle reclaim (which never breaks through this level). 0 (the default)
// disables water-level maintenance with behavior identical to before this feature existed; negative
// values are normalized to 0 and values above maxSize are clamped to maxSize. The recommended
// combination is initialize == minIdle (eliminating the cold-start window before the first scan);
// NewFunc must be usable when enabled (the refill creates objects through it)
func (c *Config) WithMinIdle(minIdle int) *Config {
	c.minIdle = minIdle
	return c
}

// WithMaxIdleTime 是设置对象最大空闲时间的方法：自最近一次入队（开始空闲）起持续空闲超过该
// 时长的对象由后台扫描回收（销毁三件套与 ping/lifetime 销毁同构：closeFunc + OnClose +
// destroyedByIdle 分类 + 释放在途配额并唤醒 waiter），tombstone 留队列占容量直至 Get 或
// 下一轮扫描末尾的 tombstone GC 回收（N1，与 lifetime/ping 销毁语义一致）。回收尊重
// minIdle 下限：有效空闲数高于 minIdle 时才允许
// 淘汰（对齐 HikariCP idleTimeout 只在 idle > minimumIdle 时生效的语义），波峰波谷流量下把
// 水位降回合理值而不打穿保温水位。0（默认）表示不限制，行为与未引入该特性时完全一致；负值
// 归一化为 0。与 maxLifetime 无冲突可同时设置：maxLifetime 是对象年龄硬上限（借出归还也会
// 到期轮换），maxIdleTime 是空闲软回收（自最近一次入队起算，借出并归还即重置空闲计时）
// WithMaxIdleTime is the method to set the maximum idle time of an object: an object idling
// continuously for longer than this duration since its most recent enqueue (idle start) is
// reclaimed by the background scan (the destruction triple is isomorphic to the ping/lifetime
// destructions: closeFunc + OnClose + the destroyedByIdle category + releasing the in-flight quota
// and waking a waiter), and the tombstone stays in the queue occupying capacity until a Get —
// or the tombstone GC at the end of the next pass (N1) — reclaims it (same semantics as
// lifetime/ping destructions). The reclaim respects the minIdle
// floor: eviction is allowed only while the effective idle count exceeds minIdle (aligning with
// HikariCP's idleTimeout, which only applies when idle > minimumIdle), bringing the water level
// back down under tidal traffic without breaking the kept-warm floor. 0 (the default) means
// unlimited with behavior identical to before this feature existed; negative values are normalized
// to 0. It composes with maxLifetime without conflict: maxLifetime is the hard age cap (rotation
// happens even across borrows and returns) while maxIdleTime is the soft idle reclaim (measured
// from the most recent enqueue — a borrow-and-return cycle restarts the idle clock)
func (c *Config) WithMaxIdleTime(maxIdleTime time.Duration) *Config {
	c.maxIdleTime = maxIdleTime
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

// WithBeforeGet 是设置借出前决策钩子的方法：元素出队后、返回调用方前，在池外（不持任何池内
// 锁）调用 fn；返回 error（或 panic，被池转为携带 panic 值的 error）则销毁该元素并继续尝试
// 队列下一个，销毁数达到 PingMaxRetries 上限后返回 ErrPoolEmpty。设置后优先于 TestOnBorrow
// 预设（WithTestOnBorrow 不再注入 pingFunc 校验）。钩子内禁止重入池的写操作 API 且应快速返回。
// nil（默认）表示关闭，借出热路径零开销
// WithBeforeGet is the method to set the pre-lend decision hook: fn is invoked outside the pool
// (holding no pool lock) after the element is dequeued and before it is returned to the caller;
// returning an error (or panicking, which the pool converts into an error carrying the panic value)
// destroys the element and continues with the next queued one, returning ErrPoolEmpty once the
// destruction count reaches the PingMaxRetries cap. Once set, it takes precedence over the
// TestOnBorrow preset (WithTestOnBorrow no longer injects the pingFunc check). Re-entering the
// pool's mutating APIs from the hook is forbidden and it must return quickly. nil (the default)
// disables the hook at zero cost on the borrow hot path
func (c *Config) WithBeforeGet(beforeGet BeforeGetFunc) *Config {
	c.beforeGet = beforeGet
	return c
}

// WithAfterPut 是设置归还前决策钩子的方法：Put 收到元素后、入队前，在池外（不持任何池内锁、
// 停止快检之后——Stop 后不执行）调用 fn；返回 error（或 panic，被池转为携带 panic 值的
// error）则池拒绝该归还：销毁元素、不入队，Put 将该 error 原样透传（区别于 ErrPoolFull）；
// 被拒绝的对象已关闭，调用方不得复用或再次归还。钩子内禁止重入池的写操作 API 且应快速返回。
// nil（默认）表示关闭，归还热路径零开销
// WithAfterPut is the method to set the pre-enqueue decision hook on return: fn is invoked outside
// the pool (holding no pool lock, after the stop fast check — it never runs after Stop) once Put has
// received the element and before it is enqueued; returning an error (or panicking, which the pool
// converts into an error carrying the panic value) makes the pool reject the return: the element is
// destroyed, never enqueued, and Put passes that error through as-is (distinct from ErrPoolFull);
// a rejected object has been closed and must not be reused or returned again. Re-entering the pool's
// mutating APIs from the hook is forbidden and it must return quickly. nil (the default) disables
// the hook at zero cost on the return hot path
func (c *Config) WithAfterPut(afterPut AfterPutFunc) *Config {
	c.afterPut = afterPut
	return c
}

// WithTestOnBorrow 是设置借出时验证开关的方法：true 且未设置 beforeGet 时，池自动注入
// pingFunc(element, 0) 校验作为 BeforeGet 预设——ping 失败的元素在借出前被销毁并尝试队列
// 下一个（对齐 Commons Pool2 的 testOnBorrow / HikariCP 的 isValid 语义）。beforeGet 已设置时
// 该开关不生效（用户钩子优先，TestOnBorrow 只是它的便捷预设而非独立机制）。false（默认）
// 表示关闭，行为与未引入该特性时完全一致。
// 性能护栏：开启后每次借出增加一次 pingFunc 调用；pingFunc 涉及网络 IO（如 SELECT 1）时
// 请评估开销——多数场景 MaxLifetime（本地时间戳对比，近零成本）已足够抵御 idle kill，
// TestOnBorrow 适合“上次扫描到本次借出之间”的窗口必须闭合的高可靠场景
// WithTestOnBorrow is the method to set the borrow-time validation switch: when true and no
// beforeGet is set, the pool automatically injects a pingFunc(element, 0) check as the BeforeGet
// preset — an element failing the ping is destroyed before lending and the next queued one is tried
// (aligning with Commons Pool2's testOnBorrow / HikariCP's isValid semantics). The switch has no
// effect once beforeGet is set (the user hook takes precedence; TestOnBorrow is merely a
// convenience preset of it, not a separate mechanism). false (the default) disables it and behavior
// is identical to before this feature existed. Performance guard: enabling it adds one pingFunc
// call per borrow; when pingFunc involves network IO (e.g. SELECT 1) evaluate the cost — in most
// scenarios MaxLifetime (a local timestamp comparison, near-zero cost) suffices against idle
// kills, and TestOnBorrow fits high-reliability scenarios where the window between the last scan
// and the current borrow must be closed
func (c *Config) WithTestOnBorrow(testOnBorrow bool) *Config {
	c.testOnBorrow = testOnBorrow
	return c
}

// normalizeConfig 验证并规范化配置
// normalizeConfig validates and normalizes the configuration
func normalizeConfig(conf *Config) *Config {
	// 如果配置不为空
	// If the configuration is not null
	if conf != nil {
		// P1-1 浅拷贝隔离：归一化在池的私有副本上进行，绝不突变调用方的 Config。原地突变曾把
		// TestOnBorrow 预设注入变成单向棘轮（beforeGet != nil 自锁，复用 Config 时显式
		// WithTestOnBorrow(false)/WithBeforeGet(nil) 均无法撤销旧注入），且并发 New 共享同一
		// 模板 Config 时对派生字段构成写-写数据竞争。字段全为值/函数指针/接口，浅拷贝安全
		//（池内 p.config 只读、无身份比较）；附带收益：New 之后用户修改自己的 Config 不再
		// 影响已建成的活池。配置纯函数纪律：归一化不写调用方对象，派生态只存在于私有副本
		// P1-1 shallow-copy isolation: normalization runs on the pool's private copy and never
		// mutates the caller's Config. In-place mutation once turned the TestOnBorrow preset
		// injection into a one-way ratchet (the beforeGet != nil self-lock made the injection
		// irrevocable for a reused Config — neither WithTestOnBorrow(false) nor WithBeforeGet(nil)
		// could undo it), and concurrent New calls sharing one template Config formed a write-write
		// data race on the derived fields. Every field is a value/function pointer/interface, so the
		// shallow copy is safe (p.config is read-only inside the pool with no identity comparison);
		// the side benefit: after New, user edits to their own Config no longer affect the living
		// pool. Pure-function config discipline: normalization never writes the caller's object and
		// derived state lives only in the private copy
		c := *conf
		conf = &c

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

		// 如果最大容量小于等于0，回落到默认最大容量（必须先于 initialize 钳制执行）
		// If the maximum capacity is less than or equal to 0, fall back to the default maximum capacity (must run before the initialize clamp)
		if conf.maxSize <= 0 {
			conf.maxSize = DefaultMaxSize
		}

		// 如果初始化元素数量超过最大容量，钳制到最大容量
		// If the number of elements to initialize exceeds the maximum capacity, clamp it to the maximum capacity
		if conf.initialize > conf.maxSize {
			conf.initialize = conf.maxSize
		}

		// 如果扫描间隔小于扫描间隔下限，钳制到下限
		// If the scan interval is less than the minimum allowed scan interval, clamp to the minimum
		if conf.scanInterval < MinScanInterval {
			conf.scanInterval = MinScanInterval
		}

		// 如果最大存活时间为负，归零（0 表示关闭年龄淘汰）
		// If the maximum lifetime is negative, normalize it to zero (0 disables age eviction)
		if conf.maxLifetime < 0 {
			conf.maxLifetime = DefaultMaxLifetime
		}

		// 如果最大存活时间抖动为负，归零（0 表示不抖动）
		// If the maximum lifetime jitter is negative, normalize it to zero (0 means no jitter)
		if conf.maxLifetimeJitter < 0 {
			conf.maxLifetimeJitter = DefaultMaxLifetimeJitter
		}

		// 如果抖动超过最大存活时间的一半，钳制到一半，防止寿命不可预测；
		// maxLifetime 为 0 时该钳制同时把抖动归零，年龄淘汰整体关闭（expireAt 恒为 0，判定侧短路）
		// If the jitter exceeds half of the maximum lifetime, clamp it to half so the lifetime stays predictable;
		// when maxLifetime is 0 this clamp also zeroes the jitter, disabling age eviction entirely
		// (expireAt stays 0 and the check short-circuits)
		if halfLifetime := conf.maxLifetime / 2; conf.maxLifetimeJitter > halfLifetime {
			conf.maxLifetimeJitter = halfLifetime
		}

		// 如果阻塞获取超时为负，归零（0 表示不设默认超时）
		// If the blocking-acquire timeout is negative, normalize it to zero (0 means no default timeout)
		if conf.acquireTimeout < 0 {
			conf.acquireTimeout = DefaultAcquireTimeout
		}

		// 如果最小空闲水位为负，归零（0 表示关闭水位保持）
		// If the minimum idle water level is negative, normalize it to zero (0 disables water-level maintenance)
		if conf.minIdle < 0 {
			conf.minIdle = DefaultMinIdle
		}

		// 如果最小空闲水位超过最大容量，钳制到最大容量（必须先于本项执行 maxSize 归一化——
		// 钳制后补齐经 reserveSlot 走在途封顶恒可成功，不产生永久性无效补齐尝试）
		// If the minimum idle water level exceeds the maximum capacity, clamp it to the maximum
		// capacity (the maxSize normalization must run before this clamp — once clamped, refill
		// reservations through reserveSlot can always succeed, so no permanently futile refill
		// attempts are made)
		if conf.minIdle > conf.maxSize {
			conf.minIdle = conf.maxSize
		}

		// 如果最大空闲时间为负，归零（0 表示关闭空闲淘汰；与 maxLifetime 无冲突——
		// maxLifetime 是硬上限、maxIdleTime 是软回收，可同时设置）
		// If the maximum idle time is negative, normalize it to zero (0 disables idle eviction;
		// no conflict with maxLifetime — maxLifetime is the hard cap while maxIdleTime is the
		// soft reclaim, and both may be set together)
		if conf.maxIdleTime < 0 {
			conf.maxIdleTime = DefaultMaxIdleTime
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

		// TestOnBorrow 作为 BeforeGet 预设注入：仅当用户未自定义 beforeGet 时生效（用户钩子
		// 优先，该开关不覆盖）。闭包捕获归一化后的 pingFunc（此时必非 nil），不再回读 Config
		//（New 之后 Config 视为不可变）；retryCount 固定传 0（借出校验是单次判定，
		// 重试预算由 tryAcquire 的销毁计数统一管理）
		// TestOnBorrow preset injection into BeforeGet: effective only when the user set no custom
		// beforeGet (the user hook takes precedence; this switch never overrides it). The closure
		// captures the normalized pingFunc (guaranteed non-nil at this point) and never reads the
		// Config back (the Config is treated as immutable after New); retryCount is fixed at 0 (a
		// borrow check is a one-shot verdict — the retry budget is managed by tryAcquire's
		// destruction counters)
		if conf.beforeGet == nil && conf.testOnBorrow {
			pingFunc := conf.pingFunc
			conf.beforeGet = func(element any) error {
				if !pingFunc(element, 0) {
					return errBorrowCheckFailed
				}
				return nil
			}
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
