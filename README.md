<div align="center">
	<img src="assets/logo.png" alt="logo" width="500px">
</div>

[![Go Report Card](https://goreportcard.com/badge/github.com/shengyanli1982/conecta)](https://goreportcard.com/report/github.com/shengyanli1982/conecta)
[![Build Status](https://github.com/shengyanli1982/conecta/actions/workflows/test.yaml/badge.svg)](https://github.com/shengyanli1982/conecta/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/shengyanli1982/conecta.svg)](https://pkg.go.dev/github.com/shengyanli1982/conecta)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/shengyanli1982/conecta)

A lightweight, generic connection pool manager for Go. Conecta wraps any object — `net.Conn`, `*sql.DB`, `*redis.Client`, `*grpc.ClientConn`, or your own type — and manages its lifecycle with automatic health checks, creation, and cleanup.

**Features:**

- **Type-agnostic** — pool any `any` object, not just connections
- **Zero dependencies** — standard library only
- **Health supervision** — background goroutine pings objects and removes unhealthy ones with configurable retry limits
- **High performance** — `sync.Pool`-based wrapper reuse, zero-allocation `Get`/`GetOrCreate` hot paths
- **Pluggable queue** — BYO queue implementation; ships with `workqueue` integration
- **Lifecycle callbacks** — observe ping success/failure/close events for metrics and logging

**Requires Go 1.21+.**

## Install

```bash
go get github.com/shengyanli1982/conecta
```

## Quick Start

```go
package main

import (
	"fmt"
	"github.com/shengyanli1982/conecta"
	wkq "github.com/shengyanli1982/workqueue/v2"
)

func newFunc() (any, error) { return "my-connection", nil }

func main() {
	pool, err := conecta.New(wkq.NewQueue(nil),
		conecta.NewConfig().WithNewFunc(newFunc))
	if err != nil {
		panic(err)
	}
	defer pool.Stop()

	// Borrow a connection (creates one if pool is empty)
	conn, _ := pool.GetOrCreate()
	fmt.Printf("got: %v (pool size: %d)\n", conn, pool.Len())

	// Return it to the pool
	pool.Put(conn)
}
```

## How It Works

### Element Lifecycle

```
         NewFunc()                CloseFunc()
            │                          │
   ┌────────▼────────┐      ┌──────────▼──────────┐
   │       Pool      │      │      Destroyed      │
   └─────────────────┘      └─────────────────────┘
            ▲                           ▲
            │                           │
            └── Ping OK ◄──── PingFail──┘
                 └─ retries >= max ─┘
```

Every pooled object is wrapped in an `Element` (from `sync.Pool`). The background `supervise` goroutine periodically pings all elements. Failures accumulate in a retry counter; once it reaches `PingMaxRetries`, the object is destroyed and the `OnClose` callback fires.

### Core Methods

| Method          | Description                                             |
| --------------- | ------------------------------------------------------- |
| `New(q, conf)`  | Create a pool with the given queue and config           |
| `Get()`         | Borrow an element; returns error if pool is empty       |
| `GetOrCreate()` | Borrow an element, or call `NewFunc` if pool is empty   |
| `Put(data)`     | Return an element to the pool                           |
| `Stop()`        | Graceful shutdown: close all elements, stop supervision |
| `Len()`         | Current number of pooled elements                       |
| `Cleanup()`     | Destroy all elements immediately and drain the queue    |

> After `Stop()`, all pool methods return `ErrorQueueClosed`.

### Configuration

Build a `Config` with the fluent builder API:

| Option                  | Description                                 | Default            |
| ----------------------- | ------------------------------------------- | ------------------ |
| `WithNewFunc(f)`        | Factory: `func() (any, error)`              | `DefaultNewFunc`   |
| `WithPingFunc(f)`       | Health check: `func(any, retries int) bool` | `DefaultPingFunc`  |
| `WithCloseFunc(f)`      | Cleanup: `func(any) error`                  | `DefaultCloseFunc` |
| `WithCallback(c)`       | Lifecycle observer (`Callback` interface)   | empty callback     |
| `WithInitialize(n)`     | Pre-create `n` elements at startup          | `0`                |
| `WithPingMaxRetries(n)` | Ping failures before destroying an element  | `3`                |
| `WithScanInterval(ms)`  | Supervision scan interval in milliseconds   | `10000`            |

> **Note:** For long-running services, set `ScanInterval >= 10000ms`. Each scan pings every element, so a large pool with very short intervals creates unnecessary load.

### Queue Interface

Conecta uses an external queue to store elements. Provide any implementation that satisfies:

```go
type Queue = interface {
    Put(value interface{}) error
    Get() (interface{}, error)
    Done(value interface{})
    Len() int
    Values() []interface{}
    Range(fn func(interface{}) bool)
    Shutdown()
    IsClosed() bool
}
```

> When using the [`workqueue`](https://github.com/shengyanli1982/workqueue) package, ensure `WithValueIdempotent` is **disabled** (it is by default) so the same element can be enqueued multiple times.

### Callbacks

Implement `Callback` to observe lifecycle events — useful for metrics, logging, and cleanup tracking:

```go
type Callback interface {
    OnPingSuccess(any)       // health check passed
    OnPingFailure(any)       // health check failed (retries < max)
    OnClose(any, error)      // object destroyed by supervision or Stop()
}
```

## Performance

Optimized with `sync.Pool` wrapper reuse and mutex-free access on the hot path. Benchmarks on Apple M1 Max:

| Benchmark           |  Time/op | Allocs/op | Notes                             |
| ------------------- | -------: | --------: | --------------------------------- |
| `Get`               | ~13.9 ns |   0 alloc | Pool warmup complete              |
| `Put`               |  ~213 ns |   2 alloc | Queue node + Element wrapper      |
| `GetAndPut`         |   ~49 ns |   0 alloc | Round-trip amortizes wrapper cost |
| `GetOrCreate`       | ~17.8 ns |   0 alloc | Falls through to `NewFunc`        |
| `ConcurrentMixed_8` |  ~359 ns |   1 alloc | 8 goroutines, 50% get + 50% put   |
| `Supervise_10`      |  ~638 ns |   0 alloc | 100-element pool scan             |

## Examples

Runnable demos in the [`examples/`](./examples/) directory:

- `simple/` — basic pool usage with a custom struct
- `tcpserver/` — full TCP connection pool with ping, close, and supervision callbacks

## License

[MIT](./LICENSE)
