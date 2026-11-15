// Package pool 提供池内部使用的 Element 包装器与 ElementPool 复用池（内部实现细节）。
// Element 除数据与重试计数外还持有 createdAt/expireAt/idleSince 三个 UnixNano 时间戳，
// 全部走 atomic 读写、不受 mu 保护也不参与 stopGate→queue.lock→element.mu 锁序；
// wrapper 经 ElementPool 复用，因此 Reset 与 PutRaw 两条回收路径都会清零这些时间戳。
// Package pool provides the Element wrapper and ElementPool reuse pool for internal use within the pool (internal implementation details).
// Besides the data and retry count, Element carries three UnixNano timestamps: createdAt, expireAt and idleSince.
// All of them are read and written atomically, are not guarded by mu and stay outside the stopGate→queue.lock→element.mu lock order;
// since wrappers are reused through ElementPool, both recycling paths (Reset and PutRaw) clear these timestamps.
package pool
