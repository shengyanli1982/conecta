package conecta

// ---------------------------------------------------------------------------
// 标定/剖析专用基准（2026-11-08 性能剖析轮新增，单文件、不触碰既有基准）
// Calibration-only benchmarks for the 2026-11-08 profiling round (single file,
// existing benchmarks untouched).
//
// 目的 / Purpose:
//  1. Roundtrip_{1,2,4,8}   — Get+Put 往返的 worker 数扩展曲线（并发退化分解的
//     "对照组线性扩展验证"，测量纪律 #3 三件套之一）。形态与
//     BenchmarkPool_ConcurrentGet_8 完全同构（wg + runsPerWorker，无共享轮转
//     计数器——纪律 #10 的 floor 陷阱不适用）。
//  2. WkqRoundtrip_{1,8}    — 裸 workqueue Get+Put 往返对照组：隔离 workqueue
//     全局锁 + lst.NodePool 的成本，不含 conecta 层（element wrapper、size/
//     stats 原子、stopGate）。
//  3. AtomicMix_{1,8}       — 原子构成对照组：复现一次 Get+Put 往返中的争用型
//     原子/RMW 构成（stopGate RLock/RUnlock、size.Add(-1)、size Load+CAS、
//     borrowed/returned.Add、waiterCount.Load、stopped.Load），不含队列锁与
//     element.mu。字段排布近似 Pool 结构（gate/stopped/size/inFlight 相邻），
//     用于给"原子计数 cache-line 弹跳"桶一个独立量级。
//  4. Floor_8               — 空转 harness floor 标定：同样的 wg+runsPerWorker
//     骨架，每 op 仅一次私有 atomic.Load，标定循环+调度地板（纪律 #10）。
//
// 所有对照基准只读生产代码公开 API 或纯 runtime 原语，不修改任何生产文件。
// ---------------------------------------------------------------------------

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	wkq "github.com/shengyanli1982/workqueue/v2"
)

// --- 1) 扩展曲线：Get+Put 往返 @ 1/2/4/8 workers ---------------------------

func BenchmarkCalib_Roundtrip_1(b *testing.B) { benchmarkCalibRoundtrip(b, 1) }
func BenchmarkCalib_Roundtrip_2(b *testing.B) { benchmarkCalibRoundtrip(b, 2) }
func BenchmarkCalib_Roundtrip_4(b *testing.B) { benchmarkCalibRoundtrip(b, 4) }
func BenchmarkCalib_Roundtrip_8(b *testing.B) { benchmarkCalibRoundtrip(b, 8) }

// benchmarkCalibRoundtrip 与 BenchmarkPool_ConcurrentGet_8 逐语句同构：
// 预填充 numWorkers*1000（下限 1000），每 worker 做 Get+Put-back 往返。
// benchmarkCalibRoundtrip is statement-for-statement isomorphic to
// BenchmarkPool_ConcurrentGet_8: pre-fill numWorkers*1000 (floor 1000), each
// worker performs a Get+Put-back round trip.
func benchmarkCalibRoundtrip(b *testing.B, numWorkers int) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	fillSize := numWorkers * 1000
	if fillSize < 1000 {
		fillSize = 1000
	}
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

// --- 2) 裸 workqueue 对照组 -------------------------------------------------

func BenchmarkCalib_WkqRoundtrip_1(b *testing.B) { benchmarkCalibWkqRoundtrip(b, 1) }
func BenchmarkCalib_WkqRoundtrip_8(b *testing.B) { benchmarkCalibWkqRoundtrip(b, 8) }

// benchmarkCalibWkqRoundtrip 直接对 wkq.NewQueue(nil) 做 Get+Put 往返：
// 每次迭代恰好两次全局锁获取（与 conecta Get+Put 往返相同的队列锁构成），
// 无 element wrapper / 无 conecta 侧原子计数。预填充与 Roundtrip 相同的
// numWorkers*1000（下限 1000）以对齐队列长度量级。
// benchmarkCalibWkqRoundtrip drives a raw wkq.NewQueue(nil) with a Get+Put
// round trip: exactly two global-lock acquisitions per iteration (the same
// queue-lock composition as a conecta Get+Put round trip), no element wrapper
// and no conecta-side atomic counters. Pre-fill matches Roundtrip's
// numWorkers*1000 (floor 1000) so queue length is order-aligned.
func benchmarkCalibWkqRoundtrip(b *testing.B, numWorkers int) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	fillSize := numWorkers * 1000
	if fillSize < 1000 {
		fillSize = 1000
	}
	q := wkq.NewQueue(nil)
	defer q.Shutdown()
	for i := 0; i < fillSize; i++ {
		_ = q.Put("conn")
	}

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
				v, err := q.Get()
				if err == nil && v != nil {
					_ = q.Put(v)
				}
			}
		}()
	}
	wg.Wait()
}

// --- 3) 原子构成对照组 ------------------------------------------------------

func BenchmarkCalib_AtomicMix_1(b *testing.B) { benchmarkCalibAtomicMix(b, 1) }
func BenchmarkCalib_AtomicMix_8(b *testing.B) { benchmarkCalibAtomicMix(b, 8) }

// calibState 的字段排布刻意近似 conecta.Pool：gate(stopGate)/stopped/size/
// inFlight 相邻（同 cache line 区），stats 双计数相邻（同 poolStats 区），
// waiterCnt 独立。争用型原子构成 = 一次 Get+Put 往返的生产路径构成：
//
//	Get 侧: stopped.Load, size.Add(-1), waiterCnt.Load(tombstone 探测)
//	Put 侧: gate.RLock/RUnlock, stopped.Load, size.Load+CAS,
//	        borrowed.Add(1), returned.Add(1), waiterCnt.Load(wake 探测)
//
// calibState's field layout deliberately mimics conecta.Pool: gate(stopGate)/
// stopped/size/inFlight adjacent (same cache-line region), the two stats
// counters adjacent (same poolStats region), waiterCnt separate. The contended
// atomic composition equals the production Get+Put round trip's composition
// (see the Get-side / Put-side breakdown above).
type calibState struct {
	gate     sync.RWMutex
	stopped  atomic.Bool
	size     atomic.Int64
	inFlight atomic.Int64 // 不在往返路径上，仅占位复现 Pool 的 size 邻接布局
	stats    struct {
		borrowed atomic.Int64
		returned atomic.Int64
	}
	waiterCnt atomic.Int64
}

func benchmarkCalibAtomicMix(b *testing.B, numWorkers int) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	s := &calibState{}
	s.size.Store(1 << 30) // 保证 CAS 恒成功且不触顶

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
				// Get 侧构成 / Get-side composition
				if s.stopped.Load() {
					break
				}
				s.size.Add(-1)
				_ = s.waiterCnt.Load()
				// Put 侧构成 / Put-side composition
				s.gate.RLock()
				for {
					cur := s.size.Load()
					if s.size.CompareAndSwap(cur, cur+1) {
						break
					}
				}
				s.stats.borrowed.Add(1)
				s.stats.returned.Add(1)
				_ = s.waiterCnt.Load()
				s.gate.RUnlock()
			}
		}()
	}
	wg.Wait()
}

// --- 4) 空转 floor 标定 -----------------------------------------------------

// BenchmarkCalib_Floor_8 用与并发族完全相同的 wg+runsPerWorker 骨架，每 op
// 仅一次 worker 私有 atomic.Load（无跨核争用），标定 harness+循环地板。
// BenchmarkCalib_Floor_8 uses the identical wg+runsPerWorker skeleton with one
// worker-private atomic.Load per op (no cross-core contention), calibrating
// the harness+loop floor.
func BenchmarkCalib_Floor_8(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const numWorkers = 8

	b.ResetTimer()
	b.ReportAllocs()

	runsPerWorker := b.N / numWorkers
	if runsPerWorker < 1 {
		runsPerWorker = 1
	}

	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for range numWorkers {
		var priv atomic.Int64
		go func() {
			defer wg.Done()
			acc := int64(0)
			for range runsPerWorker {
				acc += priv.Load()
			}
			if acc == -1 { // 防消除，恒假 / never true; keeps the load alive
				runtime.KeepAlive(&priv)
			}
		}()
	}
	wg.Wait()
}

// --- 5) teardown 摊销标定：NoStop 对照族 ------------------------------------
//
// 背景：既有 Put 增长族基准（BenchmarkPool_Put / ConcurrentPut_8 / Mixed_8 /
// Mixed_32）通过 defer p.Stop() 收尾。testing 框架的计时窗口为 [ResetTimer,
// fn 返回]，defer 的 Stop→Cleanup→Shutdown 拆卸发生在循环之后、StopTimer 之
// 前，因此其耗时与字节（sync.Pool chain chunk、Shutdown 的节点快照 slice）
// 被摊入 ns/op 与 B/op。alloc profile 已证实：Put 循环内恰为 2 allocs/128B
// （Element 64B + Node 64B），而 benchmem 报 186B——差额 ≈55B/op 即 teardown
// chunk 摊销（16.4MB/300k ops = 54.6B）。
//
// 本组对照基准与原版逐语句同构，仅去掉 defer p.Stop()（池泄漏有意为之，由
// 进程退出回收；须以 -benchtime=Nx 固定 N 单轮运行以限制泄漏量并排除跨轮
// GC 扫描偏置）。与原版同 N 对跑，差值 = teardown 摊销的精确量。
//
// Background: the existing growing-Put benchmarks finalize via defer p.Stop().
// The testing framework's timing window is [ResetTimer, fn return], so the
// deferred Stop→Cleanup→Shutdown teardown (its time and bytes: sync.Pool chain
// chunks, Shutdown's node-snapshot slice) is amortized into ns/op and B/op.
// The alloc profile proves the in-loop cost is exactly 2 allocs/128B while
// benchmem reports 186B — the ~55B/op delta is teardown chunk amortization.
// These controls are statement-identical minus the deferred Stop; run with a
// fixed -benchtime=Nx and compare at equal N to quantify the amortization.

func BenchmarkCalib_Put_NoStop(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	p := newBenchPoolWithMaxSize(b.N + 1024)
	// 有意不 Stop：把拆卸排除出测量窗口 / deliberately no Stop: keeps teardown out of the measured window

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		_ = p.Put("conn")
	}
}

func BenchmarkCalib_ConcurrentPut_8_NoStop(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const numWorkers = 8

	p := newBenchPoolWithMaxSize(b.N + 1024)

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

func BenchmarkCalib_Mixed_8_NoStop(b *testing.B)  { benchmarkCalibMixedNoStop(b, 8) }
func BenchmarkCalib_Mixed_32_NoStop(b *testing.B) { benchmarkCalibMixedNoStop(b, 32) }

// benchmarkCalibMixedNoStop 与 benchmarkConcurrentMixed 逐语句同构，仅去掉
// defer p.Stop()。/ statement-identical to benchmarkConcurrentMixed minus the
// deferred Stop.
func benchmarkCalibMixedNoStop(b *testing.B, numWorkers int) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const fillSize = 1000

	p := newBenchPoolWithMaxSize(b.N + 1024)
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
					v, _ := p.Get()
					if v != nil {
						_ = p.Put(v)
					}
				} else {
					_ = p.Put("conn")
				}
			}
		}()
	}
	wg.Wait()
}

// --- 6) Cleanup 单位成本直接标定 --------------------------------------------

// BenchmarkCalib_Cleanup_1M 直接测量满池 Cleanup 的单位成本（-benchtime=1x
// 运行）：填充 1M 元素在计时窗外，计时窗内仅一次 Cleanup。结果 = 拆卸摊销
// 的独立量级证据（ns/element），与 NoStop 对照族的差值法互相印证。
// BenchmarkCalib_Cleanup_1M measures the unit cost of Cleanup on a full pool
// directly (run with -benchtime=1x): the 1M-element fill sits outside the
// timing window; only one Cleanup is timed. The result is independent
// magnitude evidence for teardown amortization (ns/element), cross-checking the
// NoStop delta method.
func BenchmarkCalib_Cleanup_1M(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const fill = 1_000_000
	p := newBenchPoolWithMaxSize(fill + 1024)
	defer p.Stop()

	b.ReportAllocs()

	for range b.N {
		b.StopTimer()
		fillPool(p, fill)
		b.StartTimer()
		p.Cleanup()
	}
}
