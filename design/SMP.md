# Kmazarin SMP Safety Analysis and Implementation Plan

## Overview

This document captures the SMP (Symmetric Multi-Processing) safety analysis and implementation plan for the kmazarin scheduler. It is designed to persist across conversation sessions.

---

## Current Status

### Completed Work

1. **IRQ Protocol Fixes** (Completed)
   - Fixed `channels.go`: `AllocateChannel()`, `QueueKernelAsync()`, `DequeueKernelAsync()`
   - Fixed `soft_irq.go`: `softIRQWakeDispatcher()`, `softIRQEnqueue()`, `GetPendingSoftIRQ()`, `RegisterSoftIRQDispatcher()`
   - Pattern: `SaveAndDisableIRQs()` → `schedulerLock.Lock()` → work → `schedulerLock.Unlock()` → `RestoreIRQs()`

2. **Eliminated CurrentThreadIdx** (Completed)
   - Removed redundant `CurrentThreadIdx` variable entirely
   - All code now uses `CurrentThread` pointer with `threadToIdx()` when index is needed
   - Pattern: `t := (*Thread)(atomic.LoadPointer(&CurrentThread))` instead of `threadList.Get(int(CurrentThreadIdx))`

3. **TLB Shootdown for Aggressive ASID Reuse** (Completed)
   - Added `Priest.ThreadCount` field to track live threads per priest
   - Added `TLBI ASIDE1IS` instruction in `kmem/asm_barriers_arm64.s` for per-ASID TLB invalidation
   - Added `kmem.TlbiASIDE1IS(asid)` wrapper function with proper barriers
   - Modified `createUserspaceThreadImpl()` to initialize `ThreadCount = 1`
   - Modified `CloneThread()` to increment priest's `ThreadCount` for userspace threads
   - Modified `threadExitImpl()` to decrement `ThreadCount` and release priest when it hits 0
   - Added `releasePriestSchedLockHeld()` function that:
     - Performs TLB shootdown via `TLBI ASIDE1IS` before releasing ASID
     - Zeros priest struct for security
     - Returns priest ID to allocator for immediate LIFO reuse
   - The StaticAllocator already uses LIFO (stack-based), so released IDs are immediately reused
   - This enables aggressive ASID reuse to expose TLB-related bugs early

4. **Removed Unsafe Thread Reaping** (Completed)
   - Removed `reapOneFutexThreadSchedLockHeld()` function entirely
   - The Go runtime parks idle M's via futex_wait and expects them to remain alive
   - Forcibly killing parked M's corrupts runtime scheduler state (`nmidle` count)
   - Runtime doesn't check `futex_wake` return values - assumes wake succeeded
   - Increased `MaxThreads` from 16 to 512 to accommodate parked M's
   - Go runtime creates new threads when:
     - `startm()` calls `mget()` and gets nil (no parked threads)
     - Blocking syscalls trigger `handoffp()` which calls `startm()`
     - sysmon detects long-running syscalls (>10μs initially, up to 10ms)
   - Runtime prefers reusing parked threads over creating new ones

### Pending Work

1. **Update unified memory pool design document**
3. **Consolidate three unifiedPoolStart computations into one**
4. **Fix or remove the 192KB stack reservation at end of RAM**
5. **Remove embedded ELF loader from cardinal (elf_loader.go)**
6. **Have cardinal sense number of processors and pass to kmazarin**

---

## Spinlock Implementation (Verified SMP-Safe)

The spinlock in `kmazarin/ds/spinlock.go` and `spinlock_arm64.s` uses proper ARM64 atomics:

```asm
// Lock - acquire semantics
loop:
    LDAXRW (R0), R1      // Load-Acquire Exclusive
    CBNZ R1, spin        // If locked, spin
    MOVW $1, R2
    STLXRW R2, (R0), R3  // Store-Release Exclusive
    CBNZ R3, loop        // Retry if store failed

// Unlock - release semantics
    STLRW ZR, (R0)       // Store-Release (barrier before unlock)
```

**Verdict**: Correctly implements acquire/release semantics for SMP.

---

## IRQ Locking Protocol

**Rule**: Always disable IRQs BEFORE acquiring schedulerLock to prevent same-core deadlock.

```go
savedDAIF := SaveAndDisableIRQs()
schedulerLock.Lock()
// ... critical section ...
schedulerLock.Unlock()
RestoreIRQs(savedDAIF)
```

**Why**: If a thread holds schedulerLock and gets interrupted, the IRQ handler would try to acquire the same lock, causing deadlock. Disabling IRQs first prevents this.

---

## CurrentThread Pointer Safety

### Why CurrentThread (atomic pointer) is SMP-Safe

The pattern `(*Thread)(atomic.LoadPointer(&CurrentThread))` followed by `threadToIdx()` is safe because:

1. **Thread structs live in static array**: `threadListData[MaxThreads]Thread` is allocated at startup and never moves
2. **Pointer arithmetic is deterministic**: `threadToIdx()` computes `(ptr - &threadListData[0]) / sizeof(Thread)`
3. **Atomic load provides snapshot**: Even if another core changes CurrentThread, we operate on our loaded snapshot
4. **Under scheduler lock**: Most operations that access CurrentThread are already inside schedulerLock

```go
func threadToIdx(t *Thread) int32 {
    if t == nil {
        return -1
    }
    base := uintptr(unsafe.Pointer(&threadList.Data[0]))
    ptr := uintptr(unsafe.Pointer(t))
    return int32((ptr - base) / unsafe.Sizeof(Thread{}))
}
```

### When CurrentThread is Updated

CurrentThread is updated atomically in these places:
- `InitThreads()` - sets initial thread
- `SaveThread0AndYield()` - context switch
- `startFirstThreadImpl()` - first thread startup
- `threadExitImpl()` - thread cleanup
- `doContextSwitchImpl()` - preemption/blocking switch

All updates use `atomic.StorePointer()` under schedulerLock.

---

## TLB Shootdown Implementation (COMPLETED)

### Problem

With aggressive ASID (Address Space Identifier) reuse, stale TLB entries on other cores can cause:
- Wrong address translations
- Security issues (accessing another process's memory)
- Crashes from invalid mappings

### Solution: Per-Priest TLB Shootdown

Instead of complex generation tracking, we use a simpler approach:

1. **Track threads per priest**: `Priest.ThreadCount` field
2. **On priest exit**: When the last thread exits, perform TLB shootdown for that ASID
3. **LIFO reuse**: StaticAllocator already uses LIFO, so released IDs are immediately reused

### Implementation Details

**Assembly** (`kmem/asm_barriers_arm64.s`):
```asm
// tlbiASIDE1ISAsm - Invalidate TLB entries by ASID Inner Shareable
// Invalidates all TLB entries matching the given ASID across all CPUs.
TEXT ·tlbiASIDE1ISAsm(SB), NOSPLIT, $0-8
    MOVD    asid+0(FP), R0
    LSL     $48, R0, R0      // ASID goes in bits [63:48]
    WORD    $0xD5088720      // TLBI ASIDE1IS, X0
    RET
```

**Go wrapper** (`kmem/paging.go`):
```go
func TlbiASIDE1IS(asid uint16) {
    dsbISH()              // Ensure all prior memory ops complete
    tlbiASIDE1ISAsm(asid) // Invalidate TLB entries for this ASID
    dsbISH()              // Ensure TLBI completes
    isbSY()               // Synchronize instruction stream
}
```

**Priest release** (`threads.go`):
```go
func releasePriestSchedLockHeld(priestIdx int16, pid PriestId) {
    // TLB shootdown before releasing ASID
    kmem.TlbiASIDE1IS(uint16(pid))

    // Release priest slot and ID
    priestListInUse[priestIdx] = false
    priestListData[priestIdx] = Priest{} // Zero for security
    priestIdAllocator.Release(pid)       // LIFO = immediate reuse
}
```

### TLB Flush Instructions (ARM64) Reference

```asm
// Flush TLB entry by ASID (inner shareable)
TLBI ASIDE1IS, <Xt>    // Xt[63:48] = ASID

// Flush all TLB entries (inner shareable)
TLBI VMALLE1IS

// Flush by address (inner shareable)
TLBI VAE1IS, <Xt>    // Xt = VA + ASID

// Barrier after TLB operations
DSB ISH
ISB
```

### IPI Mechanism (Not Needed for TLB Shootdown)

The `TLBI *IS` (Inner Shareable) instructions broadcast TLB invalidation to all CPUs
in the inner shareable domain automatically. No explicit IPI is needed.

For future IPI needs (other than TLB):
1. Write target core set to a shared location
2. Send SGI (Software Generated Interrupt) via GIC SGIR register
3. Target cores check mask, execute handler, acknowledge
4. Sending core waits for acknowledgments

---

## Futex Race Prevention

The futex implementation includes a double-check to prevent missed wakeups:

```go
func threadBlockFutexImpl(sf *SchedulerFunc, futexAddr uint64, expectedVal uint32) uintptr {
    savedDAIF := sf.DisableAndSaveDAIF()
    schedulerLock.Lock()

    // Re-check value UNDER LOCK to prevent race
    actualVal := *(*uint32)(unsafe.Pointer(uintptr(futexAddr)))
    if actualVal != expectedVal {
        // Value changed - don't block
        schedulerLock.Unlock()
        sf.EnableAndRestoreDAIF(savedDAIF)
        return 0 // Return to caller immediately
    }

    // Safe to block - value hasn't changed
    t.State = ThreadBlockedFutex
    // ... proceed with blocking ...
}
```

**Why This Matters**: Without the re-check, a wake could arrive between the caller's check and the scheduler blocking, causing the thread to sleep forever.

---

## Thread State Transitions

All state transitions are protected by schedulerLock:

```
ThreadNew ──────────────────────────────────────────────┐
    │                                                    │
    v                                                    │
ThreadReady ←──── ThreadBlockedFutex ←───────────────┐  │
    │         ←── ThreadBlockedSoftIRQ               │  │
    │         ←── ThreadSleeping                     │  │
    │         ←── ThreadBlockedSlot                  │  │
    │                                                │  │
    v                                                │  │
ThreadRunning ───────────────────────────────────────┘  │
    │                                                    │
    v                                                    │
ThreadExited ────────────────────────────────────────────┘
                       (slot released)
```

---

## Files Modified for CurrentThreadIdx Removal

### Production Code
- `threads.go` - Core scheduler, removed variable and all usages
- `channels.go` - Added sync/atomic import, updated getCurrentThreadPIDWrapper
- `soft_irq.go` - Updated ThreadBlockSoftIRQ, RegisterSoftIRQDispatcher
- `soft_irq_slots.go` - Updated BlockOnSlot
- `linkname_impl.go` - Updated getCurrentThreadForKsyscall
- `ksyscall/dispatch.go` - Removed dead linkname

### Test Code
- `clone_test.go` - Removed save/restore of CurrentThreadIdx
- `scheduler_test.go` - Removed save/restore, use threadToIdx for checks
- `scheduler_scenarios_test.go` - Removed from struct and methods
- `soft_irq_slots_test.go` - Removed from struct and methods
- `preemption_test.go` - Removed save/restore

---

## Files Modified for TLB Shootdown

### Production Code
- `kmazarin/kmazarin/threads.go`:
  - Added `Priest.ThreadCount` field
  - Modified `createUserspaceThreadImpl()` - set `ThreadCount = 1`
  - Modified `CloneThread()` - increment `ThreadCount` for userspace threads
  - Modified `threadExitImpl()` - decrement `ThreadCount`, release priest when 0
  - Modified `reapOneFutexThreadSchedLockHeld()` - handle priest cleanup
  - Added `releasePriestSchedLockHeld()` - TLB shootdown and priest release
- `kmazarin/kmem/asm_barriers_arm64.s` - Added `tlbiASIDE1ISAsm()` instruction
- `kmazarin/kmem/paging.go`:
  - Added `tlbiASIDE1ISAsm()` forward declaration
  - Added `TlbiASIDE1IS()` wrapper function with barriers

---

## Verification

After all changes:
1. Production build: `go build ./kmazarin/kmazarin` - PASSES
2. Full system test: `$GO tool task run TIMEOUT=10` - PASSES (priests launch correctly)
3. Unit tests: Have linkname stub issues (separate problem)

---

## Fresh SMP Analysis (2026-02-03)

This section contains a comprehensive analysis of SMP issues found in the current codebase.

### CRITICAL: Global Variables That Must Be Per-CPU

| Variable | Location | Problem |
|----------|----------|---------|
| `CurrentThread` | threads.go:285 | Each CPU needs its own current thread |
| `topHalfIRQNum` | bottom_half.go:79 | CPU1 overwrites CPU0's IRQ context |
| `topHalfKbd/Mouse` | bottom_half.go:83-84 | Shared device context corrupted |
| `syscallSwitchTarget` | threads.go:299 | CPU0 returns to CPU1's context |
| `syscallELR/SPSR` | threads.go:305,311 | CPU1 corrupts CPU0's return address |
| `currentProcess` | launch.go:104 | Both priests loaded to same pointer |
| `softIRQDispatcherTID` | soft_irq.go:59 | Only ONE dispatcher for all CPUs |
| `NeedsAsyncPreempt` | preempt.go:115 | Single preemption flag for all CPUs |

### CRITICAL: Race Conditions in Scheduling

**Two CPUs Picking Same Thread**
- `findReadyThreadSchedLockHeld()` can return same thread to both CPUs
- Thread state `ThreadReady → ThreadRunning` not atomic across all modifications
- Result: Same thread executes on two CPUs simultaneously

**CurrentThread TOCTOU Race** (threads.go:285, 1934)
- CPU0: Loads `CurrentThread = T1`
- CPU1: Stores `CurrentThread = T2`
- CPU0: Saves context to T1, but T1 is no longer running
- Result: Corrupted saved state

**Context Save Race** (threads.go:1933 - SaveContextFromFrame)
- Two CPUs call `SaveContextFromFrame()` with different exception frames
- Both load same `CurrentThread` pointer
- Both write their frame data to same Thread struct
- Last write wins, corrupting context

### HIGH: Ring Buffer Races (bottom_half.go)

**softIRQRing SPSC Not Truly Safe**
```
ringPush() at line 146-154:
  Line 152: r.events[tail&mask] = ev    // Non-atomic write!
  Line 153: atomic.StoreUint32(&r.tail) // Publish AFTER write

RingDrain() at line 159-172:
  Line 167: buf[n] = r.events[head&mask] // Reads while producer writing!
```
- Producer on CPU0 writing event while consumer on CPU1 reads same slot
- Result: Corrupted events, dropped data

### HIGH: Soft IRQ Dispatcher Issues

**Single Dispatcher Thread** (soft_irq.go:59)
- `softIRQDispatcherTID` is global - only ONE dispatcher for entire system
- CPU0 and CPU1 both try to wake same dispatcher
- Both modify same thread's state (Thread.State, Thread.FutexAddr)

**Slot blockedTID Race** (soft_irq_slots.go:144)
```go
tid := slot.blockedTID  // NON-atomic read at line 144
// ... another CPU writes blockedTID at line 217 ...
threadList.FindByIdAll(int32(tid))  // Uses stale TID
```

### HIGH: Timer/Preemption Races

**Preemption Flags Are Global** (preempt.go:115-123)
- `NeedsAsyncPreempt` - single flag for all CPUs
- CPU0 timer sets flag, CPU1 reads it and triggers preemption on wrong thread

**Preemption Hash Collision** (preempt.go:61)
- `preemptTickCounts[1024]` shared, indexed by g pointer hash
- Two goroutines on different CPUs hash to same index
- Unrelated goroutine incorrectly preempted

### HIGH: TTBR0/ASID Coordination

**Concurrent TTBR0 Writes** (threads.go:826, 904, 1900, 2111)
- CPU0 and CPU1 both context switch to different threads
- No inter-CPU coordination for page table switches
- TLB may contain entries for both priests

### MEDIUM: Channel/Message Races

**Per-Priest Message Slot** (channels.go:95)
- `priestPendingMessage[priest]` - single slot per priest
- CPU0 queues message, CPU1 queues different message to same priest
- One message lost

### MEDIUM: IRQ Pending Flags (bottom_half.go:72)

- `irqPendingFlags[1020]` - array of flags
- CPU0 sets flag[27], CPU1 sets flag[27] simultaneously
- One write lost, IRQ dropped

### Summary: Required Per-CPU Data Structure

```go
type PerCPU struct {
    // Current execution context
    CurrentThread    *Thread

    // Syscall return state
    SyscallSwitchTarget uintptr
    SyscallELR          uint64
    SyscallSPSR         uint64

    // IRQ handling
    TopHalfIRQNum    uint64
    TopHalfKbd       topHalfDev
    TopHalfMouse     topHalfDev

    // Preemption
    NeedsAsyncPreempt  uint32
    NeedsThreadPreempt uint32

    // Timer
    LocalTickCounter uint64
}

// Access via MPIDR_EL1 register
func GetPerCPU() *PerCPU {
    cpuID := readMPIDR() & 0xFF  // Aff0 field
    return &perCPUData[cpuID]
}
```

---

## Completed Work (2026-02-03)

1. **Created PerCPU struct** - `kmazarin/kmazarin/percpu.go` and `percpu_arm64.s`
   - Struct holds per-CPU data: currentThread, TopHalfIRQNum, NeedsAsyncPreempt
   - MPIDR_EL1 reader for CPU ID extraction
   - Helper functions for getting per-CPU data
2. **Migrated CurrentThread to PerCPU** - Updated all writes to use `SetCurrentThreadGlobal()`
   - Updates both per-CPU and global for backward compatibility with assembly
3. **Migrated topHalfIRQNum to PerCPU** - `NonTimerIRQTopHalf()` copies global to per-CPU
4. **Migrated NeedsAsyncPreempt to PerCPU** - Added linkname accessors in main package
   - `kirq/timer.go` syncs global to per-CPU and clears both

---

## Future Work

### SMP Boot Sequence (High Priority)

The current system boots only on the primary CPU. For proper SMP:

1. **Cardinal detects CPU count** - Use PSCI or device tree to determine number of CPUs
2. **Secondary CPUs stay powered off** - QEMU virt uses PSCI; secondaries are off until CPU_ON
3. **Primary CPU boots kmazarin** - Normal boot sequence
4. **Kmazarin signals ready** - Initialize per-CPU data, ready queues, etc.
5. **Wake secondary CPUs via PSCI CPU_ON** - Pass start address (entry point)
6. **Secondary CPU entry point**:
   - Initialize its own exception vectors, stack pointers
   - Initialize its per-CPU data
   - Enable timer IRQ for this CPU
   - Enter idle loop waiting for threads

**PSCI CPU_ON** (for QEMU virt):
```asm
// Power on secondary CPU
// X0 = function ID (0xC4000003 for CPU_ON 64-bit)
// X1 = target MPIDR (target CPU's MPIDR_EL1 value)
// X2 = entry point address
// X3 = context ID (passed to entry point in X0)
MOV X0, #0xC4000003
MOV X1, <target_mpidr>
LDR X2, =secondary_entry
MOV X3, #0
HVC #0  // Or SMC #0 depending on conduit
```

### Per-CPU Ready Queues (Design Discussion)

Currently, there's a single global `readyQueue` protected by `schedulerLock`. For SMP, consider:

**Option A: Single Global Queue with Lock**
- Simple, current design
- All CPUs contend on one lock
- Risk: Two CPUs can pick the same thread (race condition)
- Fix: Add `RunningOnCPU` field to Thread, check before picking

**Option B: Per-CPU Ready Queues**
- Each CPU has its own ready queue
- When thread becomes ready, enqueue to specific CPU's queue
- Work stealing: idle CPU can steal from another CPU's queue
- Reduces lock contention significantly
- More complex to implement

**Option C: Hybrid - Global + Per-CPU**
- Global queue for unbound threads
- Per-CPU queues for CPU-affinity threads
- Balance between simplicity and scalability

**Recommendation**: Start with Option A + `RunningOnCPU` fix. Add per-CPU queues later
if lock contention becomes a bottleneck. The current spinlock is SMP-safe with proper
acquire/release semantics.

### Thread RunningOnCPU Tracking (DEFERRED)

Attempted to add `RunningOnCPU int16` field to Thread struct to prevent race condition
where two CPUs pick the same thread. Implementation caused boot crashes. Investigation:
- Struct field addition itself is fine (offsets computed dynamically)
- Issue may be with calling `GetCPUID()` during early initialization
- Need to defer RunningOnCPU assignment until after per-CPU is fully initialized

### Remaining SMP Safety Work

1. ~~Create PerCPU struct indexed by MPIDR_EL1~~ ✓
2. ~~Migrate CurrentThread to PerCPU~~ ✓
3. ~~Migrate topHalfIRQNum to PerCPU~~ ✓
4. ~~Migrate NeedsAsyncPreempt to PerCPU~~ ✓
5. Add "thread already running on another CPU" check (RunningOnCPU - DEFERRED)
6. Per-CPU exception stacks in cardinal boot
7. Per-CPU soft IRQ dispatcher (or work-stealing queue)
5. Per-CPU soft IRQ dispatcher (or work-stealing queue)
6. Consider lock-free ready queue for high contention scenarios
7. Profile spinlock wait times under SMP load
8. Implement actual SMP boot (currently single-core only)
