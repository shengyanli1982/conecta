package conecta

import (
	"context"
	"fmt"
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
// Put 错误不再被静默吞掉（P3-1）：计数并 panic——容量公式一旦被改动，填充被
// ErrPoolFull 截断会就地失败，而不是悄悄改变基准语义。用 panic 而非 b.Fatal 是为
// 保持签名不含 *testing.B（bench_profile_test.go 亦调用本 helper，本批次禁改），
// 与本文件 newBenchPoolWithMaxSize 的 setup 失败即 panic 惯例一致；fillPool 只在
// ResetTimer 之前的 setup 阶段调用，panic 与 b.Fatal 语义等价（响亮失败且 defer
// 照常执行）。
// Put errors are no longer silently swallowed (P3-1): they are counted and fail
// the run via panic, so if the capacity formula is ever changed, a fill
// truncated by ErrPoolFull fails loudly instead of quietly altering the
// benchmark semantics. panic (rather than b.Fatal) keeps the signature
// testing.B-free — bench_profile_test.go also calls this helper and is
// off-limits in this batch — matching the setup-failure-panics convention of
// newBenchPoolWithMaxSize in this file; fillPool only runs in the pre-ResetTimer
// setup phase, where panic is equivalent to b.Fatal (loud failure, defers still
// run).
func fillPool(p *conecta.Pool, n int) {
	errs := 0
	for range n {
		if err := p.Put("conn"); err != nil {
			errs++
		}
	}
	if errs > 0 {
		panic(fmt.Sprintf("fillPool: %d/%d Put errors", errs, n))
	}
}

// ----------------------------------------------------------------------------
// a) BenchmarkPool_Get — single-threaded steady-state borrow throughput
//
// Steady-state borrow measurement: the pool is pre-filled with 1000 elements,
// and each Get is immediately followed by a Put-back of the same value, so
// the pool capacity stays constant throughout the run and no iteration ever
// degrades into the empty-pool error path.
//
// 等价性披露（P2-2）：池中 value 全为 "conn"，本基准的 Get+Put(借出值) 与
// BenchmarkPool_GetAndPut 的 Get+Put(新 "conn") 逐指令等价（实测差 <1%，噪声级）
// ——两者之间不存在可推导的 "Get 纯侧成本" 差值信号。双基准保留理由：互证对照
// （两条独立路径的读数应重合，偏离即环境或代码异常信号）与历史连续性（基线
// 序列可跨轮对比）。
// Equivalence disclosure (P2-2): every value in the pool is "conn", so this
// benchmark's Get+Put(borrowed value) is instruction-for-instruction equivalent
// to BenchmarkPool_GetAndPut's Get+Put(fresh "conn") (measured delta <1%, noise
// level) — no derivable "pure Get-side cost" difference signal exists between
// the two. Both are kept as a mutual cross-check (two independent paths should
// read identically; divergence flags an environment or code anomaly) and for
// historical continuity (the baseline series stays comparable across rounds).
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

	// 拆卸移出测量窗（MEAS-FIX）：testing 计时窗 = [ResetTimer, fn 返回]，defer 的
	// p.Stop()（Cleanup+Shutdown，O(N)）原本落在窗内，把 ~78ns/41B 的拆卸摊销混入
	// 每 op 读数。先 b.StopTimer() 冻结计时与内存快照，再显式 Stop；上方 defer 保留
	// 作 panic 安全网——Stop 经 once.Do 幂等，双 Stop 无害（框架收尾的自动 StopTimer
	// 亦因 timerOn=false 而为 no-op）。
	// Teardown moved out of the measurement window (MEAS-FIX): the testing timing
	// window is [ResetTimer, fn return], so the deferred p.Stop() (Cleanup+Shutdown,
	// O(N)) used to land inside it, smearing ~78ns/41B of teardown amortisation into
	// the per-op reading. b.StopTimer() first freezes both the clock and the memory
	// snapshot, then Stop runs explicitly; the defer above stays as a panic safety
	// net — Stop is idempotent via once.Do, so the double Stop is harmless (and the
	// framework's trailing automatic StopTimer is a no-op once timerOn=false).
	b.StopTimer()
	p.Stop()
}

// ----------------------------------------------------------------------------
// c) BenchmarkPool_GetAndPut — single-threaded Get+Put round trip
//
// Each iteration: one Get from pre-filled pool, then one Put of a fresh
// string value, simulating a complete use-and-return lifecycle.
//
// 等价性披露（P2-2）：因池中 value 全为 "conn"，本基准与 BenchmarkPool_Get
// 逐指令等价（实测差 <1%）；双基准保留作互证对照与历史连续性，详见 Get 头注释。
// Equivalence disclosure (P2-2): since every pooled value is "conn", this
// benchmark is instruction-for-instruction equivalent to BenchmarkPool_Get
// (measured delta <1%); both are kept as a mutual cross-check and for
// historical continuity — see the Get header comment.
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
// c2) BenchmarkPool_GetAndPut_MaxLifetime — same round trip with age eviction ON
//
// Identical in shape to BenchmarkPool_GetAndPut, but maxLifetime is enabled so
// the enqueue path reads the clock and writes three atomic timestamps while the
// borrow path runs the age check. The lifetime is long enough that nothing
// actually expires during the run. This documents the cost of the enabled
// feature (the disabled path is what BenchmarkPool_GetAndPut measures) and
// guards later batches against regressing it.
// ----------------------------------------------------------------------------

func BenchmarkPool_GetAndPut_MaxLifetime(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().
		WithNewFunc(benchNewFunc).
		WithPingFunc(benchPingFunc).
		WithCloseFunc(benchCloseFunc).
		WithScanInterval(10000).
		WithMaxSize(conecta.DefaultMaxSize).
		WithMaxLifetime(time.Hour).
		WithMaxLifetimeJitter(time.Minute)
	p, err := conecta.New(queue, conf)
	if err != nil {
		b.Fatal(err)
	}
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

	// 拆卸移出测量窗（MEAS-FIX，同 BenchmarkPool_Put）：defer 的 p.Stop()
	//（Cleanup+Shutdown，O(N)）原本落在 [ResetTimer, fn 返回] 窗内，摊销 ~83ns/op；
	// 先 StopTimer 冻结计时与内存快照再显式 Stop，defer 保留作 panic 安全网（Stop
	// 经 once.Do 幂等，双 Stop 无害）。
	// Teardown moved out of the measurement window (MEAS-FIX, same as
	// BenchmarkPool_Put): the deferred p.Stop() (Cleanup+Shutdown, O(N)) used to
	// land inside the [ResetTimer, fn return] window, amortising ~83ns/op; stop
	// the timer first to freeze the clock and memory snapshot, then Stop
	// explicitly — the defer stays as a panic safety net (Stop is idempotent via
	// once.Do, so the double Stop is harmless).
	b.StopTimer()
	p.Stop()
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

	// 拆卸移出测量窗（MEAS-FIX，同 BenchmarkPool_Put）：池随 b.N 净增长（约半数为
	// 纯 Put），defer 的 p.Stop() 原本落在测量窗内，摊销 ~20ns/op（8 workers）/
	// ~35ns/op（32 workers）；先 StopTimer 冻结计时与内存快照再显式 Stop，defer
	// 保留作 panic 安全网（Stop 经 once.Do 幂等，双 Stop 无害）。
	// Teardown moved out of the measurement window (MEAS-FIX, same as
	// BenchmarkPool_Put): the pool grows with b.N (about half of the iterations
	// are pure Puts), so the deferred p.Stop() used to land inside the window,
	// amortising ~20ns/op (8 workers) / ~35ns/op (32 workers); stop the timer
	// first to freeze the clock and memory snapshot, then Stop explicitly — the
	// defer stays as a panic safety net (Stop is idempotent via once.Do, so the
	// double Stop is harmless).
	b.StopTimer()
	p.Stop()
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
// This benchmark keeps a local replica of the ElementPool semantics instead of
// calling into conecta/internal/pool, so the measurement stays independent of
// internal API changes. (Note: the internal package *is* importable from this
// test module — the internal rule is import-path based — and pool_test.go does
// import it for white-box assertions; the replica here is a deliberate choice,
// not a workaround.)
//
// Real ElementPool code path being benchmarked:
//   Get: pool.Get().(*Element)
//   Put: if e != nil { e.Reset(); pool.Put(e) }
//
// 低估方向披露（P3-3）：本基准是单 goroutine Get/Put 循环，恒命中 sync.Pool 的
// per-P private slot；真实 ElementPool 接收多 goroutine 归还（shared/victim 路径
// 成本更高），读数系统性低估真实复用成本。
// Underestimation disclosure (P3-3): this benchmark is a single-goroutine Get/Put
// loop that always hits sync.Pool's per-P private slot, whereas the real
// ElementPool receives returns from multiple goroutines (the costlier
// shared/victim paths) — the reading systematically underestimates the true
// recycling cost.
// ----------------------------------------------------------------------------

// benchElement mirrors internal/pool.Element layout: a mutex guarding the data
// and retries fields, plus the three atomic timestamps that both recycling
// paths (Reset and PutRaw) must clear.
type benchElement struct {
	mu        sync.Mutex
	data      any
	retries   int64
	createdAt atomic.Int64
	expireAt  atomic.Int64
	idleSince atomic.Int64
}

// reset mirrors (*pool.Element).Reset(): lock, clear data/retries, unlock, then
// clear the atomic timestamps (which live outside the mutex).
func (e *benchElement) reset() {
	e.mu.Lock()
	e.data = nil
	e.retries = 0
	e.mu.Unlock()
	e.createdAt.Store(0)
	e.expireAt.Store(0)
	e.idleSince.Store(0)
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
// l) [已移除 — P2-3] BenchmarkSupervise_10_Real 测的是 300ms 定时器周期而非
//    supervise() 的 CPU 工作（288ms/op ≈ scanInterval，supervise 真实遍历+ping
//    派发占比 <0.001%，CPU 成本回归 10 倍读数也不变）——伪性能信号，勿入
//    benchstat 对比。其真实价值（tick → queue.Range → pingFunc 派发 → 计数同步
//    的端到端可用性）已转为带回归牙齿的冒烟测试：pool_test.go 的
//    TestPool_Supervise_RealTickPingsAllElements。
// l) [Removed — P2-3] BenchmarkSupervise_10_Real measured the 300ms timer
//    period, not supervise()'s CPU work (288ms/op ≈ scanInterval; the real
//    traversal + ping dispatch was <0.001% of the reading — a 10× CPU
//    regression would not move it), a pseudo performance signal unfit for
//    benchstat. Its genuine value (the end-to-end tick → queue.Range →
//    pingFunc dispatch → counter sync chain) now lives as a smoke test with
//    regression teeth: TestPool_Supervise_RealTickPingsAllElements in
//    pool_test.go.
// ----------------------------------------------------------------------------

// ----------------------------------------------------------------------------
// m) BenchmarkPool_GetWithContext_Uncontended — GetWithContext fast path with an
//    object readily available in the queue (no waiter registration, no suspension)
//
// Mirrors BenchmarkPool_Get's steady-state shape so the delta against the Get
// baseline is directly attributable to the ctx entry checks (ctx.Err + the
// acquireTimeout gate). Budget: 0 allocs/op and within +25% of the Get
// baseline (58.4 ns/op on the reference machine).
// ----------------------------------------------------------------------------

func BenchmarkPool_GetWithContext_Uncontended(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const fillSize = 1000
	p := newBenchPool()
	defer p.Stop()
	fillPool(p, fillSize)

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		v, _ := p.GetWithContext(ctx)
		if v != nil {
			_ = p.Put(v)
		}
	}
}

// ----------------------------------------------------------------------------
// n) BenchmarkPool_GetWithContext_WaitWake — full suspend → wake → acquire cycle
//
// One iteration (P0-B in-flight cap semantics): G1 pins the single in-flight
// slot inside a gated newFunc → the waiter W suspends (in-flight CAS fails →
// waiter registration) → the gate opens → G1's creation succeeds and the
// reservation converts into a formal in-flight slot (no wake — the cap is now
// held by a live object) → G1's value is Put back, and the wake-after-enqueue
// probe wakes W → W borrows the returned object (never creates: the cap bounds
// the total in-flight count) → the value is Put back once more and a Cleanup
// destroys it, restoring inFlight=0/size=0 for the next iteration (dropping a
// borrowed value would permanently leak its in-flight slot under the cap). The
// two Put legs also exercise the wake-after-enqueue probe, and Cleanup adds the
// destruction-release path.
//
// Staging 演进（P1-1）：旧版以 time.Sleep(20µs) 尽力让 W 进入挂起——sleep 直接计入
// 每 op 墙钟（旧读数 ~31µs 中 ≥64% 是 sleep 而非被测机制），且属 best-effort：高负载
// 下 W 未在 20µs 内抵达 select 时改走"注册后复查"快路径，同一基准测出双峰，跨环境
// 不可比。现改为确定性挂起探测：轮询 Stats().WaitCount——waitForSlot 在注册+复查后、
// 进入 select 前递增 waitCount（累计值），WaitCount 到达 i+1 即 W 已提交挂起（gate
// 未放行前无唤醒源；close 信号电平持续，即使 W 尚未抵达 select 也不丢唤醒），每迭代
// 恰好 +1（仅 W 会挂起，唤醒后重试必成功）。读数即机制本体（2 goroutine 派生 +
// 4 channel 交接 + waiter 注册/close 唤醒 + Cleanup），无固定底噪、无双峰——直接
// 测量 ≈2-3µs；旧审查的 8-11µs 估算按 sleep 恰为 20µs 减法得出，实际 darwin 上
// Sleep(20µs) 超调至 ≈27µs，差额即估算偏差来源。
// Staging evolution (P1-1): the old version staged W's suspension with a
// time.Sleep(20µs) — the sleep counted directly into per-op wall time (≥64% of
// the old ~31µs reading was sleep, not the mechanism), and being best-effort it
// made the benchmark bimodal (under load W could miss the select within 20µs and
// take the post-registration recheck fast path instead), incomparable across
// environments. It is now a deterministic suspension probe polling
// Stats().WaitCount: waitForSlot increments waitCount (cumulative) after
// register+recheck and before entering the select, so WaitCount reaching i+1
// means W is committed to suspension (the gate is still closed — no wake source
// exists; the close-semantics signal is level-persistent, so no wake is lost
// even if W has not reached the select yet), and each iteration adds exactly
// one (only W ever suspends; its post-wake retry always succeeds). The reading
// is now the mechanism itself (2 goroutine spawns + 4 channel handoffs + waiter
// registration/close wake + Cleanup) — no fixed noise floor, no bimodality:
// directly measured at ≈2-3µs; the old review's 8-11µs estimate came from
// subtracting an assumed-exact 20µs sleep, while Sleep(20µs) on darwin actually
// overshoots to ≈27µs, which accounts for the gap.
// Absolute numbers only — recorded for B3/Phase6 observation, no baseline comparison required.
// ----------------------------------------------------------------------------

func BenchmarkPool_GetWithContext_WaitWake(b *testing.B) {
	var (
		armed   atomic.Bool
		entered = make(chan struct{}, 1)
		release = make(chan struct{})
	)

	queue := wkq.NewQueue(nil)
	conf := conecta.NewConfig().
		WithMaxSize(1).
		WithScanInterval(10000).
		WithNewFunc(func() (any, error) {
			if armed.CompareAndSwap(true, false) {
				entered <- struct{}{}
				<-release
			}
			return "conn", nil
		})
	p, err := conecta.New(queue, conf)
	if err != nil {
		b.Fatal(err)
	}
	defer p.Stop()

	ctx := context.Background()
	acquired := make(chan any, 2)

	b.ResetTimer()
	b.ReportAllocs()

	for i := range b.N {
		// G1：门控创建钉住唯一在途槽位 / G1: the gated creation pins the only in-flight slot
		armed.Store(true)
		go func() {
			v, _ := p.GetWithContext(ctx)
			acquired <- v
		}()
		<-entered

		// W：在途 CAS 预留失败 → 注册 waiter 挂起 / W: the in-flight CAS reservation fails → registers and suspends
		go func() {
			v, _ := p.GetWithContext(ctx)
			acquired <- v
		}()
		// 确定性挂起探测（P1-1，替代旧 Sleep(20µs)——其污染与双峰问题见头注释）：
		// waitCount 在注册+复查后、进 select 前递增（累计值），到达 i+1 即 W 已提交挂起
		//（若 W 走复查快路径则永不递增，本循环会死锁而非产出读数——挂起路径构造性保证）；
		// 轮询点队列为空（N≈0），Stats() 的 Range 时间开销可忽略，但其闭包捕获使 Stats
		// 结构体逃逸堆分配，B/op 与 allocs/op 读数含探测自身的仪表化开销（每次数轮询
		// ~数百 B），比对时以 ns/op 为准。
		// Deterministic suspension probe (P1-1, replacing the old Sleep(20µs) — see the
		// header for its pollution and bimodality problems): waitCount is incremented
		// after register+recheck and before the select (cumulative), so reaching i+1
		// means W is committed to suspension (if W took the recheck fast path the
		// counter would never increment and this loop would deadlock instead of
		// producing a reading — the suspend path is guaranteed by construction); the
		// queue is empty at the polling point (N≈0), so Stats()'s Range time cost is
		// negligible, but its closure capture escapes the Stats struct to the heap —
		// the B/op and allocs/op readings carry the probe's own instrumentation
		// overhead (a few hundred bytes per iteration); compare on ns/op.
		for p.Stats().WaitCount < int64(i)+1 {
			runtime.Gosched()
		}

		// 放行门控：G1 创建成功（预留转正式在途，无唤醒——封顶由活对象持有）
		// Open the gate: G1's creation succeeds (the reservation converts into a formal in-flight slot;
		// no wake — the cap is now held by a live object)
		release <- struct{}{}
		v1 := <-acquired

		// Put 归还 → wake-after-enqueue 唤醒 W → W 借出归还对象（在途封顶下不新建）
		// Put return → wake-after-enqueue wakes W → W borrows the returned object (never creates under
		// the in-flight cap)
		if v1 != nil {
			_ = p.Put(v1)
		}
		v2 := <-acquired

		// 收尾：归还 v2（第二次 wake-after-enqueue 探测，无 waiter 空转）后 Cleanup 销毁——
		// 恢复 inFlight=0/size=0 的迭代起点；丢弃借出值会永久泄漏其配额的在途槽位
		// Settle: Put v2 back (a second wake-after-enqueue probe, spinning with no waiters) and destroy
		// it via Cleanup — restoring the inFlight=0/size=0 iteration start; dropping a borrowed value
		// would permanently leak its in-flight slot
		if v2 != nil {
			_ = p.Put(v2)
		}
		p.Cleanup()
	}
}
