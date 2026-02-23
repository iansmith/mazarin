# Race Condition Investigation: mspan Corruption Under Load

## The Bug

Under heavy keyboard/mouse input (~30-60 seconds), kmazarin panics with:

```
runtime: s.allocCount= 31 s.nelems= 32
fatal error: s.allocCount != s.nelems && freeIndex == s.nelems
fatal error: schedule: holding locks
panic during panic
```

This is a Go runtime mspan metadata corruption — the GC's span bookkeeping
disagrees with reality. `allocCount` says 31 objects are allocated in a 32-slot
span, but `freeIndex` has reached the end (32), meaning the allocator thinks
all slots are used. These two facts contradict each other.

## How We Found It

During the memory overhaul (Stage 1), adding `SetPageDescriptor()` to the
`BuddyAllocTyped` hot path triggered this crash. Bisection showed:

| Configuration | Crash? |
|---------------|--------|
| Clean HEAD (commit f96de97), heavy input, 60s | No |
| Stage 1, all PageDescriptor code disabled | No |
| Stage 1, InitPageDescriptors ON, SetPageDescriptor OFF everywhere | No |
| Stage 1, bump SetPageDescriptor ON, buddy OFF | No |
| Stage 1, buddy SetPageDescriptor ON, **writes removed** (just function call + index math) | **YES** |
| Stage 1, buddy SetPageDescriptor ON, full writes | **YES** |

**Key finding:** The crash occurs even when `SetPageDescriptor` is a near-no-op
(atomic load + index computation, no memory writes). The PageDescriptor memory
writes are innocent. The mere addition of ~10 instructions to the buddy
allocation hot path changes timing enough to expose the race.

## What We Can Rule Out

- **PageDescriptor memory writes**: crash happens with no writes
- **PageDescriptor array allocation**: crash happens even when array is allocated but unused; doesn't happen when array is allocated but buddy path doesn't call SetPageDescriptor
- **Struct size mismatch**: verified `sizeof(PageDescriptor) == 16 == pdEntrySize`
- **Go heap overlap**: PageDescriptor array is in linear map (VA ~0xFFFFFFFFA8xxxxxx), Go heap is at ~0xFFFFFFFF43xxxxxx — no overlap
- **GC write barriers**: all fields are value types (uintptr, uint8, int16), no pointer fields, no slice backing; verified no gcWriteBarrier in the chain
- **nosplit stack overflow**: linker passes on all 3 architectures

## What the Crash Tells Us

The error `s.allocCount != s.nelems && freeIndex == s.nelems` comes from
`runtime/mgcsweep.go` or `runtime/malloc.go`. It means:

1. An mspan has `nelems` slots (e.g., 32)
2. `freeIndex` reached the end (all slots appear used from the allocator's perspective)
3. But `allocCount` (computed from `gcmarkBits.popcnt()`) says fewer are actually marked as allocated

This means either:
- **The GC mark bits were corrupted** (bits cleared that shouldn't have been)
- **The allocBits were corrupted** (bits set that shouldn't have been)
- **allocCount was computed at a moment when bits were being concurrently modified**

## Where to Look

### Theory 1: Context Switch Doesn't Fully Save/Restore GC State

Kmazarin implements its own cooperative+preemptive scheduler for userspace
"priests". When a timer IRQ preempts a goroutine that's in the middle of GC
sweep or allocation:

- The goroutine may hold an `mcache` with a partially-allocated span
- If the context switch doesn't properly save/restore the M's `mcache` state,
  the resumed goroutine might see stale span metadata
- **Look at**: `doContextSwitchImpl`, `YieldToReadyThread`, `StartFirstThread`
  in `threads.go` — do they save/restore `m.mcache`?
- **File**: `kmazarin/kmazarin/threads.go`

### Theory 2: Concurrent M Access to Same Span

Kmazarin uses a single M (M0) that context-switches between threads. The Go
runtime's `mcache` is per-M. If two priest threads are multiplexed on M0 and
both do heap allocations:

- Thread A allocates from span S, gets preempted mid-allocation
- Thread B runs on same M0, allocates from same mcache → same span S
- Thread A resumes, its local view of the span is stale

- **Look at**: Does kmazarin properly handle `mcache` across context switches?
  Normal Go doesn't context-switch goroutines at the M level.
- **File**: `kmazarin/kmazarin/threads.go`, runtime overlay files

### Theory 3: Timer IRQ During GC Sweep

The GC sweep walks spans and updates `allocBits`/`gcmarkBits`. If a timer IRQ
fires during sweep and the handler (or a preempted-to goroutine) tries to
allocate from the same span:

- `mspan.sweep()` is doing `allocBits = gcmarkBits` copy
- Timer fires, new goroutine allocates, reads partially-copied bits
- **Look at**: Is there any protection against preemption during sweep?
- **Files**: Go runtime's `mgcsweep.go` (overlay or stock), IRQ handler paths

### Theory 4: TLS/g Corruption During Context Switch

Kmazarin switches the `g` pointer (goroutine) during context switches. If the
TLS update races with the GC scanning goroutine stacks:

- The GC reads `g` from TLS, follows it to the goroutine, scans its stack
- Context switch updates TLS to a new `g` mid-scan
- The GC now follows pointers from the wrong goroutine's stack
- This corrupts mark bits (objects marked/unmarked incorrectly)
- **Look at**: x86_64 TLS fix (already documented in MEMORY.md — FS_BASE
  write in `load_context_and_iretq`). ARM64 equivalent?
- **Files**: exception handler assembly, `threads.go`

### Theory 5: asyncPreempt Injection During Allocation

Kmazarin injects `asyncPreempt` to preempt goroutines. If this happens while
a goroutine is inside `mallocgc` or `mspan.nextFreeIndex`:

- The goroutine has partially updated span metadata
- asyncPreempt saves the context and yields
- Another goroutine on the same M sees inconsistent span state
- **Look at**: Does `asyncPreempt` check if the goroutine is in a
  `runtime.lock`-protected section?
- **Files**: `kmazarin/kmazarin/threads.go` (asyncPreempt injection logic),
  IRQ handler

## Reproduction

```bash
# Build with buddy SetPageDescriptor enabled in buddy.go (uncomment the call)
GOTOOLCHAIN=auto QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 \
  /opt/homebrew/Cellar/go/1.25.5/libexec/bin/go tool task run TIMEOUT=60

# Aggressively type on keyboard and move mouse in the QEMU window
# Crash typically occurs within 20-40 seconds
```

Without the buddy SetPageDescriptor call, the timing is just lucky enough to
avoid the race window. The call adds ~10 instructions to every page allocation,
which is enough to shift preemption timing.

## Why Input Triggers It

Heavy input generates many VirtIO input interrupts → IRQ top-half processing →
preemption decisions → more context switches → more opportunities to hit the
race window. The input itself doesn't cause the corruption — it just increases
the frequency of context switches and interrupt-driven scheduling decisions.
