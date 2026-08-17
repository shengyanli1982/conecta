package conecta

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shengyanli1982/conecta"
	wkq "github.com/shengyanli1982/workqueue/v2"
)

// ----------------------------------------------------------------------------
// Benchmark helpers
// ----------------------------------------------------------------------------

// benchNewFunc creates a new connection object (string type).
func benchNewFunc() (any, error) { return "conn", nil }

// benchPingFunc always reports healthy so that the background monitor
// performs the minimum work and does not interfere with measurements.
func benchPingFunc(_ any, _ int) bool { return true }

// benchCloseFunc is a no-op close function.
func benchCloseFunc(_ any) error { return nil }

// newBenchPool 创建默认容量上限（conecta.DefaultMaxSize）的基准池，
// 是 newBenchPoolWithMaxSize 的默认容量版本（委托实现，行为完全等价）。
// scanInterval=10000ms 使后台监控 goroutine 保持近似休眠，避免干扰测量精度。
// newBenchPool creates a benchmark pool with the default capacity cap
// (conecta.DefaultMaxSize); it is the default-capacity variant of
// newBenchPoolWithMaxSize (implemented by delegation, behaviorally identical).
// scanInterval=10000ms keeps the background monitor goroutine effectively
// dormant, preventing interference with measurement accuracy.
func newBenchPool() *conecta.Pool {
	return newBenchPoolWithMaxSize(conecta.DefaultMaxSize)
}

// newBenchPoolWithMaxSize 创建一个显式设置容量上限的基准池，
// 供池规模随 b.N 增长（或预填充量超过默认上限）的基准使用。
// newBenchPoolWithMaxSize creates a benchmark pool with an explicit capacity
// limit, for benchmarks whose pool grows with b.N (or whose pre-fill exceeds
// the default cap).
func newBenchPoolWithMaxSize(maxSize int) *conecta.Pool {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().
		WithNewFunc(benchNewFunc).
		WithPingFunc(benchPingFunc).
		WithCloseFunc(benchCloseFunc).
		WithScanInterval(10000).
		WithMaxSize(maxSize)
	p, err := conecta.New(queue, conf)
	if err != nil {
		panic(err)
	}
	return p
}

// fillPool inserts n "conn" string elements into the pool.
func fillPool(p *conecta.Pool, n int) {
	for range n {
		_ = p.Put("conn")
	}
}

// ----------------------------------------------------------------------------
// a) BenchmarkPool_Get — single-threaded steady-state borrow throughput
//
// Steady-state borrow measurement: the pool is pre-filled with 1000 elements,
// and each Get is immediately followed by a Put-back of the same value, so
// the pool capacity stays constant throughout the run and no iteration ever
// degrades into the empty-pool error path. For the pure Get-side cost of the
// borrow-and-return round trip, refer to the difference against GetAndPut.
// ----------------------------------------------------------------------------

func BenchmarkPool_Get(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const fillSize = 1000
	p := newBenchPool()
	defer p.Stop()
	fillPool(p, fillSize)

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		v, _ := p.Get()
		if v != nil {
			_ = p.Put(v)
		}
	}
}

// ----------------------------------------------------------------------------
// b) BenchmarkPool_Put — single-threaded Put throughput
// ----------------------------------------------------------------------------

func BenchmarkPool_Put(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// 池随 b.N 无界增长：注入 b.N + 1024 的容量上限（统一公式余量，本基准无预填充），
	// 避免默认 maxSize=1024 把超限迭代短路到 ErrPoolFull 拒绝路径，改变测量语义。
	// The pool grows unbounded with b.N: inject a b.N + 1024 capacity cap
	// (unified-formula margin; this benchmark has no pre-fill) so the default
	// maxSize=1024 never short-circuits iterations onto the ErrPoolFull
	// rejection path and changes the semantics.
	p := newBenchPoolWithMaxSize(b.N + 1024)
	defer p.Stop()

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		_ = p.Put("conn")
	}
}

// ----------------------------------------------------------------------------
// c) BenchmarkPool_GetAndPut — single-threaded Get+Put round trip
//
// Each iteration: one Get from pre-filled pool, then one Put of a fresh
// string value, simulating a complete use-and-return lifecycle.
// ----------------------------------------------------------------------------

func BenchmarkPool_GetAndPut(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	p := newBenchPool()
	defer p.Stop()
	fillPool(p, 1000)

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		v, _ := p.Get()
		_ = v
		_ = p.Put("conn")
	}
}

// ----------------------------------------------------------------------------
// d) BenchmarkPool_GetOrCreate — GetOrCreate from an empty pool via newFunc
//
// Pool starts with zero pre-filled elements; every call falls through to
// the configured newFunc, measuring the "worst-case" creation path.
// ----------------------------------------------------------------------------

func BenchmarkPool_GetOrCreate(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().
		WithNewFunc(benchNewFunc).
		WithPingFunc(benchPingFunc).
		WithCloseFunc(benchCloseFunc).
		WithScanInterval(10000)
	p, err := conecta.New(queue, conf)
	if err != nil {
		b.Fatal(err)
	}
	defer p.Stop()
	// Pool is intentionally empty: WithInitialize is not set.

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		v, _ := p.GetOrCreate()
		_ = v
	}
}

// ----------------------------------------------------------------------------
// e) BenchmarkPool_ConcurrentGet_8 — 8 goroutines performing Get concurrently
//
// Pool is pre-filled with 8000 elements (1000 per worker) before timing
// starts. Each Get is followed by a Put-back to keep the pool stocked.
// ----------------------------------------------------------------------------

func BenchmarkPool_ConcurrentGet_8(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const (
		numWorkers = 8
		fillSize   = numWorkers * 1000 // 8000
	)

	// 预填充 8000 个元素超过默认 maxSize=1024：显式放宽容量上限为
	// max(b.N+1024, fillSize)；max 保证低 b.N 的预热轮次也能容纳全部填充量，
	// 避免填充被 ErrPoolFull 静默截断（正式测量轮次 b.N 远大于填充量时取 b.N+1024）。
	// The 8000-element pre-fill exceeds the default maxSize=1024: widen the cap
	// explicitly to max(b.N+1024, fillSize); max guarantees the fill fully fits
	// even in warm-up rounds with a small b.N, so the fill is not silently
	// truncated by ErrPoolFull (b.N+1024 dominates in measured rounds).
	p := newBenchPoolWithMaxSize(max(b.N+1024, fillSize))
	defer p.Stop()
	fillPool(p, fillSize)

	b.ResetTimer()
	b.ReportAllocs()

	runsPerWorker := b.N / numWorkers
	if runsPerWorker < 1 {
		runsPerWorker = 1
	}

	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for range numWorkers {
		go func() {
			defer wg.Done()
			for range runsPerWorker {
				v, _ := p.Get()
				if v != nil {
					_ = p.Put(v)
				}
			}
		}()
	}
	wg.Wait()
}

// ----------------------------------------------------------------------------
// f) BenchmarkPool_ConcurrentPut_8 — 8 goroutines performing Put concurrently
// ----------------------------------------------------------------------------

func BenchmarkPool_ConcurrentPut_8(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const numWorkers = 8

	// 池随 b.N 无界增长：注入 b.N + 1024 的容量上限（统一公式余量，本基准无预填充），
	// 避免默认 maxSize=1024 把超限迭代短路到 ErrPoolFull 拒绝路径，改变测量语义。
	// The pool grows unbounded with b.N: inject a b.N + 1024 capacity cap
	// (unified-formula margin; this benchmark has no pre-fill) so the default
	// maxSize=1024 never short-circuits iterations onto the ErrPoolFull
	// rejection path and changes the semantics.
	p := newBenchPoolWithMaxSize(b.N + 1024)
	defer p.Stop()

	b.ResetTimer()
	b.ReportAllocs()

	runsPerWorker := b.N / numWorkers
	if runsPerWorker < 1 {
		runsPerWorker = 1
	}

	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for range numWorkers {
		go func() {
			defer wg.Done()
			for range runsPerWorker {
				_ = p.Put("conn")
			}
		}()
	}
	wg.Wait()
}

// ----------------------------------------------------------------------------
// g) BenchmarkPool_ConcurrentMixed_8 — 8 goroutines, 50% Get + 50% Put
//
// Simulates a realistic connection pool workload: each worker alternates
// between Get (consumed) and Put (provisioned) operations.
// ----------------------------------------------------------------------------

func BenchmarkPool_ConcurrentMixed_8(b *testing.B) {
	benchmarkConcurrentMixed(b, 8)
}

// ----------------------------------------------------------------------------
// h) BenchmarkPool_ConcurrentMixed_32 — 32 goroutines, 50% Get + 50% Put
//
// High-contention variant that stresses lock contention in the underlying
// workqueue and Element pools.
// ----------------------------------------------------------------------------

func BenchmarkPool_ConcurrentMixed_32(b *testing.B) {
	benchmarkConcurrentMixed(b, 32)
}

// benchmarkConcurrentMixed is the shared implementation for mixed-workload
// concurrent benchmarks. It pre-fills the pool, then spawns numWorkers
// goroutines that alternate between Get and Put operations.
func benchmarkConcurrentMixed(b *testing.B, numWorkers int) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const fillSize = 1000

	// 预填充后池仍随 b.N 净增长（约半数迭代为纯 Put）：注入 b.N + 1024 的容量
	// 上限（余量覆盖填充量），避免默认 maxSize=1024 把超限迭代短路到
	// ErrPoolFull 拒绝路径，改变混合负载的测量语义。
	// After the pre-fill the pool still grows with b.N (about half of the
	// iterations are pure Puts): inject a b.N + 1024 capacity cap (margin covers
	// the fill) so the default maxSize=1024 never short-circuits iterations onto
	// the ErrPoolFull rejection path and changes the mixed-workload semantics.
	p := newBenchPoolWithMaxSize(b.N + 1024)
	defer p.Stop()
	fillPool(p, fillSize)

	b.ResetTimer()
	b.ReportAllocs()

	runsPerWorker := b.N / numWorkers
	if runsPerWorker < 1 {
		runsPerWorker = 1
	}

	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for range numWorkers {
		go func() {
			defer wg.Done()
			for j := range runsPerWorker {
				if j%2 == 0 {
					// 50% Get (with Put-back to keep pool stocked)
					v, _ := p.Get()
					if v != nil {
						_ = p.Put(v)
					}
				} else {
					// 50% Put (adds new element)
					_ = p.Put("conn")
				}
			}
		}()
	}
	wg.Wait()
}

// ----------------------------------------------------------------------------
// i) BenchmarkPool_Len — Len() call overhead
//
// Pre-fills the pool with 1000 elements so Len() traverses a non-trivial
// internal count, exposing any per-element traversal cost.
// ----------------------------------------------------------------------------

func BenchmarkPool_Len(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	p := newBenchPool()
	defer p.Stop()
	fillPool(p, 1000)

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		_ = p.Len()
	}
}

// ----------------------------------------------------------------------------
// j) BenchmarkElementPool_GetPut — internal ElementPool (sync.Pool) overhead
//
// Because conecta/internal/pool is not importable from external test modules,
// this benchmark faithfully replicates the ElementPool semantics: a sync.Pool
// storing *Element wrappers whose Reset clears data/retries fields under a mutex
// before returning the wrapper to the pool.
//
// Real ElementPool code path being benchmarked:
//   Get: pool.Get().(*Element)
//   Put: if e != nil { e.Reset(); pool.Put(e) }
// ----------------------------------------------------------------------------

// benchElement mirrors internal/pool.Element layout: a mutex guarding the
// data and retries fields.
type benchElement struct {
	mu      sync.Mutex
	data    any
	retries int64
}

// reset mirrors (*pool.Element).Reset(): lock, clear fields, unlock.
func (e *benchElement) reset() {
	e.mu.Lock()
	e.data = nil
	e.retries = 0
	e.mu.Unlock()
}

func BenchmarkElementPool_GetPut(b *testing.B) {
	b.ReportAllocs()

	sp := &sync.Pool{
		New: func() any { return new(benchElement) },
	}

	// Warm the pool so the first Get returns a cached entry
	// (matching real-world warm-path behaviour).
	sp.Put(sp.Get())

	b.ResetTimer()

	for range b.N {
		e := sp.Get().(*benchElement)
		e.reset()
		sp.Put(e)
	}
}

// ----------------------------------------------------------------------------
// k) BenchmarkSupervise_10 — supervise() equivalent workload for 10 elements
//
// Because supervise() is an unexported method on Pool (inaccessible from
// external test modules), this benchmark simulates its core behaviour using
// the public API: iterate 10 elements via repeated Get, apply a "ping check"
// (type assertion), then return all elements to the pool via Put.
//
// The background monitor is suppressed (scanInterval=10000ms) to prevent
// real supervise() calls from interfering with the simulated measurement.
//
// This proxy benchmark closely mirrors the actual supervise() workload:
//   - Real:   for elem := range queue.Values() { elem.Lock(); ping; claim; elem.Unlock() }
//   - Proxy:  collect all elements via Get with a "ping" each, then return all via Put
//
// Both paths visit all N elements exactly once behind per-element
// synchronisation: the real path walks a short-lock Values() snapshot guarded
// by element mutexes (claiming exhausted elements), while the proxy path uses
// Get/Put as lock-equivalent queue operations.
// ----------------------------------------------------------------------------

func BenchmarkSupervise_10(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const numElements = 10

	p := newBenchPool()
	defer p.Stop()
	fillPool(p, numElements)

	// Pre-allocate the buffer that holds elements during the "ping" phase.
	// Reusing it across iterations avoids per-iteration slice allocations.
	elements := make([]any, 0, numElements)

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		// Phase 1: retrieve all elements via Get — mirrors the Values() snapshot
		// traversal of the real implementation (proxy semantics: the Get sequence
		// simulates the snapshot walk).
		elements = elements[:0]
		for range numElements {
			v, err := p.Get()
			if err != nil {
				break
			}
			// "Ping check": type-assert to string, analogous to calling pingFunc.
			_ = v.(string)
			elements = append(elements, v)
		}
		// Phase 2: Return all elements to the pool (analogous to Range
		// leaving elements in-place; we must Put them back since we used Get).
		for _, v := range elements {
			_ = p.Put(v)
		}
	}
}

// ----------------------------------------------------------------------------
// l) BenchmarkSupervise_10_Real — actual supervise() cycle measurement
//
// This supplementary benchmark fires the real supervise() by setting the
// minimum scanInterval (300ms). It measures the wall-clock time for one
// complete supervision cycle (tick + queue.Range + 10× pingFunc dispatch).
//
// Each b.N iteration awaits 10 ping calls via atomic counter synchronisation.
// Typical output: ~300ms/op (dominated by the timer period, not the CPU work).
// ----------------------------------------------------------------------------

func BenchmarkSupervise_10_Real(b *testing.B) {
	const numElements = 10
	var pingCount int64

	countingPingFunc := func(_ any, _ int) bool {
		atomic.AddInt64(&pingCount, 1)
		return true
	}

	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().
		WithNewFunc(benchNewFunc).
		WithPingFunc(countingPingFunc).
		WithCloseFunc(benchCloseFunc).
		WithScanInterval(300) // minimum allowed by normalizeConfig

	p, err := conecta.New(queue, conf)
	if err != nil {
		b.Fatal(err)
	}
	defer p.Stop()
	fillPool(p, numElements)

	// Let the first tick fire and settle before we start the timer.
	time.Sleep(350 * time.Millisecond)

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		before := atomic.LoadInt64(&pingCount)
		target := before + int64(numElements)
		// Spin-wait with a small sleep until all 10 elements have been pinged.
		for atomic.LoadInt64(&pingCount) < target {
			time.Sleep(time.Millisecond)
		}
	}
}
