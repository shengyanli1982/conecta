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

// newBenchPool creates a pool configured for benchmarking.
// scanInterval=10000ms keeps the background monitor goroutine effectively
// dormant, preventing interference with measurement accuracy.
func newBenchPool() *conecta.Pool {
	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().
		WithNewFunc(benchNewFunc).
		WithPingFunc(benchPingFunc).
		WithCloseFunc(benchCloseFunc).
		WithScanInterval(10000)
	p, err := conecta.New(queue, conf)
	if err != nil {
		panic(err)
	}
	return p
}

// fillPool inserts n "conn" string elements into the pool.
func fillPool(p *conecta.Pool, n int) {
	for i := 0; i < n; i++ {
		_ = p.Put("conn")
	}
}

// ----------------------------------------------------------------------------
// a) BenchmarkPool_Get — single-threaded Get throughput
//
// Pre-fills the pool with 1000 elements; each Get is immediately followed by
// a Put-back to maintain pool capacity throughout the run.
// ----------------------------------------------------------------------------

func BenchmarkPool_Get(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const fillSize = 1 << 20
	p := newBenchPool()
	defer p.Stop()
	fillPool(p, fillSize)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = p.Get()
	}
}

// ----------------------------------------------------------------------------
// b) BenchmarkPool_Put — single-threaded Put throughput
// ----------------------------------------------------------------------------

func BenchmarkPool_Put(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	p := newBenchPool()
	defer p.Stop()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
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

	for i := 0; i < b.N; i++ {
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

	for i := 0; i < b.N; i++ {
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

	p := newBenchPool()
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
	for w := 0; w < numWorkers; w++ {
		go func() {
			defer wg.Done()
			for j := 0; j < runsPerWorker; j++ {
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

	p := newBenchPool()
	defer p.Stop()

	b.ResetTimer()
	b.ReportAllocs()

	runsPerWorker := b.N / numWorkers
	if runsPerWorker < 1 {
		runsPerWorker = 1
	}

	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for w := 0; w < numWorkers; w++ {
		go func() {
			defer wg.Done()
			for j := 0; j < runsPerWorker; j++ {
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

	p := newBenchPool()
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
	for w := 0; w < numWorkers; w++ {
		go func() {
			defer wg.Done()
			for j := 0; j < runsPerWorker; j++ {
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

	for i := 0; i < b.N; i++ {
		_ = p.Len()
	}
}

// ----------------------------------------------------------------------------
// j) BenchmarkElementPool_GetPut — internal ElementPool (sync.Pool) overhead
//
// Because conecta/internal/pool is not importable from external test modules,
// this benchmark faithfully replicates the ElementPool semantics: a sync.Pool
// storing *Element wrappers whose Reset clears data/value fields under a mutex
// before returning the wrapper to the pool.
//
// Real ElementPool code path being benchmarked:
//   Get: pool.Get().(*Element)
//   Put: if e != nil { e.Reset(); pool.Put(e) }
// ----------------------------------------------------------------------------

// benchElement mirrors internal/pool.Element layout.
type benchElement struct {
	mu    sync.Mutex
	data  any
	value int64
}

// reset mirrors (*pool.Element).Reset(): lock, clear fields, unlock.
func (e *benchElement) reset() {
	e.mu.Lock()
	e.data = nil
	e.value = 0
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

	for i := 0; i < b.N; i++ {
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
//   - Real:   queue.Range(func(elem) { elem.Lock(); ping(elem); elem.Unlock() })
//   - Proxy:  for j:=0..N: { v=Get(); "ping"(v); Put(v) }
//
// Both paths perform N element accesses with lock-equivalent synchronisation.
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

	for i := 0; i < b.N; i++ {
		// Phase 1: "Range" — retrieve all elements (analogous to queue.Range).
		elements = elements[:0]
		for j := 0; j < numElements; j++ {
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
		WithScanInterval(300) // minimum allowed by isConfigValid

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

	for i := 0; i < b.N; i++ {
		before := atomic.LoadInt64(&pingCount)
		target := before + int64(numElements)
		// Spin-wait with a small sleep until all 10 elements have been pinged.
		for atomic.LoadInt64(&pingCount) < target {
			time.Sleep(time.Millisecond)
		}
	}
}
