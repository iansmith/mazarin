# AMD64 GC Investigation: Why Heap Grows Without Bound

## Date: 2026-02-25

## Summary

On x86_64, the Go kernel heap grows at ~27 MB per KernelIdleLoop iteration
(~9 MB/s wall-clock in QEMU TCG) and is never collected. After ~60 seconds,
heapLive reaches 110+ MB. The system becomes unresponsive (1 input event
processed, then hangs) and eventually OOMs.

**Root cause hypothesis (strongly supported by data but not yet confirmed):**
`gcStart()` silently bails out every time because the allocating goroutine is
running on g0 or with `mp.locks > 1`. The GC trigger condition IS met (heapLive
far exceeds the 4 MB trigger threshold), but `gcStart` returns without starting
a GC cycle. This means GC cycle 2 never begins.

## Evidence

### Diagnostic Output (last run, 60s timeout)

```
[GC] c=1 p=0 panic=0 live=22448 egc=1 pct=100 goal=400000 marked=17858
[GC] c=1 p=0 panic=0 live=22448 egc=1 pct=100 goal=400000 marked=17858
[GC] c=1 p=0 panic=0 live=22448 egc=1 pct=100 goal=400000 marked=17858
[GC] c=1 p=0 panic=0 live=22448 egc=1 pct=100 goal=400000 marked=17858
[GC] c=1 p=0 panic=0 live=A7E3C8 egc=1 pct=100 goal=400000 marked=17858
[GC] c=1 p=0 panic=0 live=1AF9EF8 egc=1 pct=100 goal=400000 marked=17858
[GC] c=1 p=0 panic=0 live=3505EF8 egc=1 pct=100 goal=400000 marked=17858
[GC] c=1 p=0 panic=0 live=4EC7EF8 egc=1 pct=100 goal=400000 marked=17858
[GC] c=1 p=0 panic=0 live=68E1EF8 egc=1 pct=100 goal=400000 marked=17858
```

### Field Meanings

| Field | Value | Meaning |
|-------|-------|---------|
| `c=1` | numgc=1 | Only 1 GC cycle ever completed (the boot-time GC) |
| `p=0` | gcphase=0 | GC is in `_GCoff` phase (not running) |
| `panic=0` | panicking=0 | No panic in progress |
| `live=68E1EF8` | heapLive=110 MB | Heap is 110 MB and growing |
| `egc=1` | enablegc=true | GC is enabled |
| `pct=100` | gcPercent=100 | GOGC=100 (set by `debug.SetGCPercent(100)`) |
| `goal=400000` | gcPercentHeapGoal=4 MB | GC should trigger when heap reaches ~4 MB |
| `marked=17858` | heapMarked=96 KB | Only 96 KB was marked live in cycle 1 |

### What This Tells Us

The GC trigger condition in `gcTrigger.test()` (mgc.go:616) checks:
```go
func (t gcTrigger) test() bool {
    if !memstats.enablegc || panicking.Load() != 0 || gcphase != _GCoff {
        return false
    }
    // For gcTriggerHeap:
    trigger, _ := gcController.trigger()
    return gcController.heapLive.Load() >= trigger
}
```

All preconditions are met:
- `enablegc = true` (egc=1)
- `panicking = 0` (panic=0)
- `gcphase = _GCoff` (p=0)
- `heapLive (110 MB) >> trigger (~4 MB)`

So `test()` MUST be returning true. Yet GC never starts cycle 2.

### The Bail-Out in gcStart

`gcStart()` (mgc.go:643) has an early bail-out:

```go
func gcStart(trigger gcTrigger) {
    mp := acquirem()
    if gp := getg(); gp == mp.g0 || mp.locks > 1 || mp.preemptoff != "" {
        releasem(mp)
        return   // <-- SILENTLY RETURNS, NO GC STARTED
    }
    ...
}
```

This is called from `mallocgc` after every span refill. If the allocating code
is running on g0 (system stack), has locks held, or has preemption disabled,
`gcStart` silently returns without starting GC.

**In kmazarin, this is almost certainly always the case** because:
1. Exception handlers run on g0
2. Syscall handlers often acquire locks (scheduler lock, etc.)
3. Many kernel paths set `mp.preemptoff`

## Diagnostic Infrastructure

### kmazarinGCStats (runtime-patches/cgo_mmap.go)

```go
//go:nosplit
func kmazarinGCStats() (numgc uint32, phase uint32, panicVal uint32,
    heapLive uint64, enablegc uint32, gcPct int32,
    percentGoal uint64, heapMarked uint64) {
    var egc uint32
    if memstats.enablegc { egc = 1 }
    return memstats.numgc, gcphase, panicking.Load(),
        gcController.heapLive.Load(), egc,
        gcController.gcPercent.Load(),
        gcController.gcPercentHeapGoal.Load(),
        gcController.heapMarked
}
```

This reads GC state without triggering STW. Exposed to kmazarin main package via:

```go
// kmazarin/kmazarin/runtime_config.go
//go:linkname kmazarinGCStatsNoSTW runtime.kmazarinGCStats
func kmazarinGCStatsNoSTW() (uint32, uint32, uint32, uint64, uint32, int32, uint64, uint64)
```

Called every iteration in `KernelIdleLoop` (threads.go).

### Mmap Bump Allocator Diagnostic (runtime-patches/cgo_mmap.go)

The bump allocator prints a diagnostic every 512th allocation:
```
[mmap] n=<count> ptr=<nextPtr> sz=<alignedLength>
```

The last run showed `[mmap] n=0000000200 ptr=0155562000 sz=0000012000` (512th
allocation, bump pointer at offset 0x155562000 from heap start).

## What Is Allocating All That Memory?

The `heapLive` counter jumps by 11-27 MB between idle loop iterations.
Each iteration calls `runtime.Gosched()` which yields to other goroutines.
Between iterations, goroutines that were yielded to (eventPoller, bottom-half
processors, etc.) run and allocate memory.

**Candidate allocators (not yet confirmed):**
1. `fmt.Sprintf` via `console.KPrintf` -- allocates strings on heap
2. `eventPoller` goroutine -- processes VirtIO input events
3. Bottom-half processors -- handle deferred interrupt work
4. sysmon goroutine -- periodic runtime housekeeping
5. Go runtime internal allocations (mcache refill, span growth)

The specific allocator has NOT been identified. All of these paths likely run
with some combination of locks held or on g0, which prevents gcStart.

## Previous Bugs Fixed During This Investigation

### 1. traceback.go Crash Guard (runtime-patches/traceback.go)

GC stack scanning crashed in `runtime.(*unwinder).resolveInternal` when trying
to scan goroutines whose `sched.sp` was 0 or outside stack bounds. Three guards
were added:

**Guard 1 -- initAt bad sp0 (line ~167):**
```go
if sp0 == 0 { return }
if gp.stack.lo != 0 && (sp0 < gp.stack.lo || sp0 >= gp.stack.hi) {
    println("KMAZARIN: initAt bad sp0 goid=", gp.goid, ...)
    return
}
```

**Guard 2 -- resolveInternal systemstack jump sched.sp=0 (line ~340):**
```go
if gp.sched.sp == 0 {
    u.frame = stkframe{}
    return
}
```

**Guard 3 -- resolveInternal bad fp (line ~405):**
Prevents dereferencing a frame pointer outside stack bounds (was causing a
secondary page fault during crash handling).

### 2. panic.go suppressSerial Fix (runtime-patches/panic.go)

`throw()` sets `suppressSerial = 0` before printing so fatal error messages
reach UART even after the SoftIRQ console has activated and suppressed serial.

### 3. ReadMemStats Hangs

`runtime.ReadMemStats()` calls `stopTheWorld()` which hangs in bare-metal context.
Replaced with direct `go:linkname` access to GC state variables (no STW needed).

## x86_64 Yield Bridge (Completed)

Replaced `INT $0x80` for sched_yield in usleep and futex with direct calls
to `kmazarinYieldImpl` which tail-jumps to `YieldToReadyThread`:

```go
// kmazarin/kmazarin/yield_bridge.go
//go:nosplit
func kmazarinYieldImpl()  // implemented in yield_bridge_amd64.s
```

```asm
// kmazarin/kmazarin/yield_bridge_amd64.s
TEXT *kmazarinYieldImpl(SB), NOSPLIT|NOFRAME, $0-0
    JMP *YieldToReadyThread(SB)
```

```asm
// runtime-patches/sys_linux_amd64.s -- usleep
TEXT runtime*usleep(SB),NOSPLIT,$0-4
    CALL main*kmazarinYieldImpl(SB)
    RET

// futex spin-exhausted path:
    CALL main*kmazarinYieldImpl(SB)
    MOVL $0, ret+40(FP)
    RET
```

### Remaining INT $0x80 on x86_64

Three uses remain, all in `clone` and its child path:
1. `clone` syscall itself (line 364) -- `INT $0x80` with SYS_clone
2. `clone_child` gettid (line 389) -- `INT $0x80` with SYS_gettid
3. `clone_child` exit fallback (line 412) -- `INT $0x80` with SYS_exit

These are targeted for elimination in the next phase of the plan (see
`.claude/plans/cheeky-imagining-porcupine.md`).

## x86_64 Overlay Files

The kmazarin-amd64 overlay (`cmd/gen-overlay/main.go:buildKmazarinAMD64Overlay`)
replaces 12 files:

| Standard Library File | Patch Purpose |
|---|---|
| `runtime/cgo_mmap.go` | Bump allocator for mmap, GC stats |
| `runtime/fds_unix.go` | Stub file descriptor operations |
| `runtime/malloc.go` | arenaBaseOffset for kernel VA space |
| `runtime/mcache.go` | Relaxed sweepgen checks for bare-metal |
| `runtime/os_linux_noauxv.go` | Custom auxv parsing, suppressSerial |
| `runtime/preempt.go` | Kernel preemption offset exports |
| `runtime/sys_linux_amd64.s` | Syscall stubs (yield, futex, clone, etc.) |
| `runtime/tagptr_64bit.go` | Sign extension for kernel VA pointers |
| `runtime/traceback.go` | Crash guards for GC stack scanning |
| `runtime/panic.go` | suppressSerial fix for throw() |
| `internal/runtime/syscall/asm_linux_amd64.s` | Syscall6 via INT $0x80 |
| `syscall/syscall_linux.go` | Skip entersyscall/exitsyscall |

## mcache.go Relaxations

The mcache overlay (`runtime-patches/mcache.go`) has two critical patches:

1. **Skip uncaching for unused spans**: If `allocCount==0` or `nelems==0`,
   skip the uncaching path. This handles exception context where mcache state
   isn't properly initialized.

2. **Relaxed sweepgen check**: Accept `sweepgen+3` (fresh), `sweepgen+1` (stale
   after GC), and `0` (uninitialized) instead of only `sweepgen+3`. This handles
   allocations in exception context where GC state propagation didn't occur.

## Key Memory Layout (x86_64)

| Region | Address | Size |
|--------|---------|------|
| Kernel heap | `0xFFFF800100000000` - `0xFFFF900000000000` | 1 TB VA range |
| arenaBaseOffset | `0xFFFF800000000000` | For Go heap arena math |
| MMIO offset | `0xFFFFFFFF00000000` | Linear map base |
| COM1 UART | `0xFFFFFFFF000003F8` | Via MMIO offset |
| Kernel text | `0xFFFFFFFF42000000` | KernelTextBase |

## GOMAXPROCS = 1

`sched_getaffinity` returns 0 in the overlay (stub), so `getproccount()` returns
1 and GOMAXPROCS=1. With only 1 P, STW should theoretically succeed immediately.

## Possible Solutions (Not Yet Attempted)

### Option A: Force GC from a User Goroutine

Call `runtime.GC()` from a goroutine running on a normal g (not g0). This could
be done from KernelIdleLoop itself, since it runs on a normal goroutine stack.
The key question is whether `runtime.GC()` would succeed or hang (STW issues).

### Option B: Instrument gcStart to Diagnose the Bail-Out

Add a counter in `gcStart` (in the overlay) that counts how many times it bails
out due to the g0/locks/preemptoff check. Print which condition triggered. This
would confirm the hypothesis.

### Option C: Reduce Allocation Pressure

Find and eliminate the allocating code paths. If fmt.Sprintf is the culprit,
replace `KPrintf` calls with `KPrintln`/`KWriteString`. If runtime internals
are allocating, investigate why.

### Option D: Call runtime.GC() Periodically from Idle Loop

Since KernelIdleLoop runs on a normal goroutine (not g0), calling `runtime.GC()`
directly should work. But it calls `gcWaitOnMark` which may hang if the sweep
phase can't complete (due to munmap being a no-op, spans are never freed).

### Option E: Fix the Root Cause in gcStart

Patch the `gcStart` overlay to allow GC to start even from g0/locked contexts,
with appropriate safety checks. This is the most invasive option but would
properly fix the issue.

## Next Steps When Resuming

1. **Confirm the gcStart bail-out**: Add a diagnostic counter in gcStart to
   see exactly which condition causes the bail-out (g0, locks, preemptoff).

2. **Try `runtime.GC()` from KernelIdleLoop**: This is the simplest test --
   add `runtime.GC()` after `runtime.Gosched()` in the idle loop and see if
   it completes or hangs.

3. **Identify the top allocator**: Add a `mallocgc` diagnostic that prints
   the allocation size and caller PC for the largest allocations.

4. **Complete the INT $0x80 elimination**: Replace clone/gettid/exit syscalls
   with direct function calls (Part 2 of the plan in
   `.claude/plans/cheeky-imagining-porcupine.md`).

## Related Files

- `runtime-patches/cgo_mmap.go` -- mmap bump allocator + kmazarinGCStats
- `runtime-patches/sys_linux_amd64.s` -- syscall stubs with yield bridge
- `runtime-patches/traceback.go` -- GC stack scanning guards
- `runtime-patches/panic.go` -- suppressSerial fix
- `runtime-patches/malloc.go` -- arenaBaseOffset for kernel VA
- `runtime-patches/mcache.go` -- sweepgen relaxation
- `runtime-patches/os_linux_noauxv.go` -- auxv parsing, suppressSerial
- `runtime-patches/asm_linux_amd64.s` -- Syscall6 via INT $0x80
- `kmazarin/kmazarin/threads.go` -- KernelIdleLoop with GC diagnostic
- `kmazarin/kmazarin/runtime_config.go` -- linkname bridges
- `kmazarin/kmazarin/yield_bridge.go` -- yield linkname declaration
- `kmazarin/kmazarin/yield_bridge_amd64.s` -- yield trampoline
- `kmazarin/kmazarin/main.go` -- SetGCPercent(100), SetMemoryLimit(64MB)
- `kmazarin/kmazarin/diagnose_amd64.go` -- allgs diagnostic
- `cmd/gen-overlay/main.go` -- overlay file list
- `.claude/plans/cheeky-imagining-porcupine.md` -- SVC/INT/EBREAK elimination plan
