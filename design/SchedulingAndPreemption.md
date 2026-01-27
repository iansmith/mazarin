# Scheduling and Preemption Design for Mazzy/Kmazarin

## Overview

This document describes the complete thread scheduling and preemption system in the Mazzy kernel. It covers:
1. How threads are created and queued
2. How the first thread starts running
3. How timer-based preemption switches between threads
4. How async preemption injects yield points into running goroutines
5. Critical data structures, counters, and invariants

**WARNING**: This system is complex and has many interacting parts. Changes to any component can break scheduling entirely. Read this document thoroughly before modifying scheduling-related code.

## Architecture Summary

```
+------------------+     +-------------------+     +------------------+
|  Thread Creation |---->|   Ready Queue     |---->|  Thread Running  |
|  (SyscallLaunch) |     | (FIFO StaticQueue)|     |  (CurrentThread) |
+------------------+     +-------------------+     +------------------+
                                  ^                        |
                                  |                        v
                         +------------------+     +------------------+
                         | Context Switch   |<----|  Timer IRQ       |
                         | (checkThread-    |     | (preempt_arm64.s)|
                         |  PreemptionImpl) |     +------------------+
                         +------------------+
```

## Two-Level Preemption Hierarchy

The Mazzy kernel implements **two distinct levels of preemption**, each serving a different purpose:

### Level 1: Goroutine Preemption (Within a Thread)

**Purpose**: Allow multiple goroutines within a single priest/thread to share CPU time fairly.

**Mechanism**:
- **Async Preemption**: Modify ELR to inject `asyncPreempt`, which calls into the Go runtime scheduler
- **Cooperative Preemption**: Set `g.preempt=true` and `g.stackguard0=stackPreempt`, causing yield at next function call

**Triggered by**: `NeedsAsyncPreempt` flag when `GoroutinePreemptDeadline` is exceeded (~50ms)

**Effect**: The Go runtime's scheduler picks a different goroutine to run on the same M (OS thread). The thread itself continues running - only the goroutine changes.

**Who handles it**: The priest's own Go runtime (asyncPreempt → asyncPreempt2 → schedule)

```
Thread (M0) running Goroutine A
    |
    | Timer IRQ: goroutine deadline exceeded
    v
Thread (M0) running Goroutine B  (same thread, different goroutine)
```

### Level 2: Thread Preemption (Between Threads/Priests)

**Purpose**: Allow multiple priests (separate processes with their own Go runtimes) to share CPU time fairly.

**Mechanism**: Full context switch - save all registers, switch TTBR0 (page table), load new thread's registers

**Triggered by**: `NeedsThreadPreempt` flag when `ThreadPreemptDeadline` is exceeded (~200ms)

**Effect**: The entire thread is suspended and another thread from the ready queue runs. This is a full OS-level context switch.

**Who handles it**: The Mazzy kernel (`checkThreadPreemptionImpl`)

```
Thread 1 (Priest A) running
    |
    | Timer IRQ: thread deadline exceeded
    v
Thread 2 (Priest B) running  (different thread, different address space)
```

### Why Two Levels?

| Aspect | Goroutine Preemption | Thread Preemption |
|--------|---------------------|-------------------|
| Scope | Within one priest | Between priests |
| Cost | Low (Go scheduler) | High (full context switch + TLB) |
| Frequency | ~50ms | ~200ms |
| Page table | Same | Different (TTBR0 switch) |
| Handler | Priest's Go runtime | Mazzy kernel |
| Flag | NeedsAsyncPreempt | NeedsThreadPreempt |

### Interaction Between Levels

The timer handler checks **both** deadlines on every tick:

```asm
// In preempt_arm64.s:

// Check goroutine deadline first (shorter interval)
MOVD    352(R7), R8  // GoroutinePreemptDeadline
CMP     R8, R9
BLT     check_thread_deadline
MOVW    $1, R8
MOVW    R8, ·NeedsAsyncPreempt(SB)  // Signal goroutine preemption

check_thread_deadline:
// Check thread deadline (longer interval)
MOVD    344(R7), R8  // ThreadPreemptDeadline
CMP     R8, R9
BLT     timer_return
MOVW    $1, R8
MOVW    R8, ·NeedsThreadPreempt(SB)  // Signal thread preemption
```

**Key insight**: Goroutine preemption happens more frequently (every ~50ms) to keep goroutines responsive within a priest. Thread preemption happens less frequently (every ~200ms) because it's more expensive and priests need time to make progress.

### Goroutine Change Detection

When the Go runtime internally switches goroutines (e.g., one goroutine blocks on a channel), the timer handler detects this by comparing the g pointer:

```asm
// Load LastSeenG from thread struct
MOVD    320(R7), R8  // R8 = currentThread.LastSeenG
CMP     R4, R8       // Compare with current g (R4 = X28)
BEQ     same_goroutine

// G changed! The Go runtime switched goroutines.
// Reset goroutine deadline to give new goroutine fair time.
MOVD    R4, 320(R7)  // Update LastSeenG
// ... reset GoroutinePreemptDeadline ...
```

This ensures that when the Go runtime voluntarily switches goroutines, the new goroutine gets a fresh time slice rather than being immediately preempted.

## Critical Data Structures

### 1. Thread Struct (`threads.go`)

```go
type Thread struct {
    State    ThreadState  // ThreadReady, ThreadRunning, ThreadBlockedFutex, etc.
    TID      ThreadId     // Unique thread identifier
    FutexAddr uint64      // For futex blocking
    MPtr     uint64       // Go runtime m pointer (for clone)
    GPtr     uint64       // Go runtime g pointer (for clone)
    EntryFunc uint64      // Entry function (for clone)

    Context  ThreadContext // Saved CPU state (registers, SP, ELR, SPSR)

    // Preemption tracking (CRITICAL for fair scheduling)
    LastSeenG                uintptr // Last g pointer seen (detects goroutine switches)
    StartTick                uint64  // When this thread started its current timeslice
    GoroutineStart           uint64  // When current goroutine started
    ThreadPreemptDeadline    uint64  // Timer tick when thread should be preempted
    GoroutinePreemptDeadline uint64  // Timer tick when goroutine should be preempted

    // Per-thread async preempt address (supports different Go runtimes)
    AsyncPreemptAddr uint64

    // Clone child protection (CRITICAL - see InCloneSetup section)
    InCloneSetup uint32 // 1 = in clone setup, 0 = normal

    // Page table for userspace threads
    PageTableL0PA uintptr // Physical address of L0 page table
    PID           PriestId // Process/priest ID (used as ASID)

    // Runtime accounting
    PreemptElapsed    uint64
    GoroutineElapsed  uint64
    TicksStartedRunning uint64
    TotalTicksRunning   uint64
}
```

### 2. ThreadContext Struct (`threads.go`)

```go
type ThreadContext struct {
    X    [31]uint64 // General purpose registers X0-X30
    SP   uint64     // Stack pointer (SP_EL0 for userspace)
    ELR  uint64     // Exception Link Register (return address)
    SPSR uint64     // Saved Program Status Register
}
```

**Memory Layout** (272 bytes total):
- Offsets 0-240: X[0]-X[30] (31 * 8 bytes = 248 bytes)
- Offset 248: SP
- Offset 256: ELR
- Offset 264: SPSR

### 3. Global State Variables

```go
// Current running thread
var CurrentThread unsafe.Pointer  // *Thread, accessed atomically
var CurrentThreadIdx int32        // Index into threadList, -1 if none

// Thread storage (static allocation, no heap)
var threadList StaticList[Thread] // All threads
var readyQueue StaticQueue[ThreadId] // FIFO queue of ready thread IDs
var blockedQueue StaticQueue[ThreadId] // Futex-blocked threads
var sleepingQueue StaticQueue[ThreadId] // Sleeping threads
```

### 4. Preemption Control Variables (`kirq/preempt.go`)

```go
// Flags set by timer IRQ, checked by exception handler
var NeedsAsyncPreempt uint32  // 1 = goroutine needs async preemption
var NeedsThreadPreempt uint32 // 1 = thread needs preemption (context switch)

// Preemption thresholds (in timer ticks)
var GoroutinePreemptTicks uint64 = 3125000  // ~50ms at 62.5MHz
var ThreadPreemptTicks uint64 = 12500000    // ~200ms at 62.5MHz

// Timer frequency (read from CNTFRQ_EL0)
var SystemTimerFrequency uint64 // Usually 62500000 (62.5MHz) on QEMU

// Runtime structure offsets (for assembly access)
var PreemptStackGuard0Offset uintptr
var PreemptPreemptOffset uintptr
var PreemptGStatusOffset uintptr
var PreemptStackPreemptValue uintptr
var PreemptGRunning uint32  // _Grunning constant (2)
var PreemptGScan uint32     // _Gscan bit mask (0x1000)
var PreemptGMOffset uintptr // Offset of g.m
var PreemptMLocksOffset uintptr // Offset of m.locks
```

## The Three Scheduling Paths

### Path 1: Initial Thread Start (`RunFirstThread`)

**Problem Solved**: When the kernel launches a thread, it's added to the ready queue but nothing is running. The timer IRQ handler only handles preemption (switching FROM a running thread), not initial scheduling.

**Solution**: `RunFirstThread()` explicitly starts the first thread.

**Location**: `kmazarin/kmazarin/abi_stubs_arm64.s` lines 110-176

**Flow**:
```
main.go:simpleMain()
    |
    v
RunFirstThread() [abi_stubs_arm64.s]
    |
    +-- Calls StartFirstThread() [threads.go]
    |       |
    |       +-- IdleLoop() waits for ready thread
    |       +-- Sets CurrentThread, CurrentThreadIdx
    |       +-- Initializes preemption deadlines
    |       +-- Switches TTBR0 to thread's page table
    |       +-- Returns &thread.Context
    |
    +-- Loads context (ELR, SPSR, SP_EL0, X0-X30)
    +-- TLB invalidation
    +-- ERET to userspace
```

**Critical Code** (`startFirstThreadImpl` in threads.go):
```go
func startFirstThreadImpl(sf *SchedulerFunc) uint64 {
    // Wait for a ready thread (blocks until one is available)
    thread := IdleLoop(sf)

    // Set up current thread
    idx := threadToIdx(thread)
    CurrentThreadIdx = idx
    atomic.StorePointer(&CurrentThread, unsafe.Pointer(thread))
    thread.State = ThreadRunning

    // Initialize preemption tracking
    currentTime := sf.CurrentTime(0)
    thread.StartTick = currentTime
    thread.ThreadPreemptDeadline = currentTime + kirq.ThreadPreemptTicks
    thread.GoroutinePreemptDeadline = currentTime + kirq.GoroutinePreemptTicks

    // Switch TTBR0 to thread's page table
    if thread.PageTableL0PA != 0 {
        kmem.SwitchTTBR0WithASID(thread.PageTableL0PA, uint16(thread.PID))
    }

    return uint64(uintptr(unsafe.Pointer(&thread.Context)))
}
```

**Assembly ERET sequence** (abi_stubs_arm64.s):
```asm
TEXT ·RunFirstThread(SB), NOSPLIT|NOFRAME, $0-0
    // Get context pointer from StartFirstThread
    CALL    ·StartFirstThread(SB)
    MOVD    8(RSP), R20  // R20 = context pointer

    // Load ELR_EL1, SPSR_EL1, SP_EL0 from context
    MOVD    256(R20), R0
    MSR     R0, ELR_EL1
    MOVD    264(R20), R0
    MSR     R0, SPSR_EL1
    MSR     $1, SPSel     // Switch to SP_EL1 to safely set SP_EL0
    MOVD    248(R20), R0
    MSR     R0, SP_EL0

    // Load all registers X0-X30
    LDP     0(R20), (R0, R1)
    // ... (all registers)

    // TLB invalidation before ERET
    WORD    $0xD508871F  // TLBI VMALLE1
    DSB     $15
    ISB     $15

    ERET  // Transition to userspace!
```

### Path 2: Timer-Based Preemption

**Location**: `kmazarin/kirq/preempt_arm64.s` (TimerIRQHandlerAsm)

**Flow**:
```
Timer IRQ fires
    |
    v
exceptions_arm64.s: irq_exception_handler
    |
    +-- Save exception frame
    +-- Identify IRQ (GIC IAR)
    +-- If timer IRQ (27):
            |
            v
        TimerIRQHandlerAsm [preempt_arm64.s]
            |
            +-- Re-arm timer for next tick
            +-- Check if offsets initialized
            +-- Get g pointer from X28
            +-- Branch based on g location:
                    |
                    +-- Kernel g (high memory): check goroutine/thread deadlines
                    +-- Userspace g (low memory): check deadlines + set coop preempt
            |
            +-- If deadline exceeded:
                    +-- Set NeedsAsyncPreempt (goroutine)
                    +-- Set NeedsThreadPreempt (thread)
            |
            v
        Back to exceptions_arm64.s
            |
            +-- Check NeedsThreadPreempt:
            |       +-- Call checkThreadPreemptionImpl
            |       +-- Copy new context to exception frame
            |
            +-- Check NeedsAsyncPreempt:
            |       +-- Validate g status (_Grunning)
            |       +-- Check m.locks == 0
            |       +-- Check InCloneSetup == 0
            |       +-- Inject asyncPreempt (modify ELR, push LR/R29)
            |
            +-- Restore registers from exception frame
            +-- ERET
```

**Deadline-Based Preemption** (preempt_arm64.s):

The timer handler tracks two deadlines per thread:
1. **GoroutinePreemptDeadline**: When to preempt the current goroutine (~50ms)
2. **ThreadPreemptDeadline**: When to switch to another thread (~200ms)

```asm
// Thread struct offsets (must match Go code)
//   LastSeenG: 320
//   StartTick: 328
//   GoroutineStart: 336
//   ThreadPreemptDeadline: 344
//   GoroutinePreemptDeadline: 352

same_goroutine:
    // Read current timer counter
    MRS     X9, CNTVCT_EL0

    // Check goroutine deadline
    MOVD    352(R7), R8  // R8 = GoroutinePreemptDeadline
    CMP     R8, R9       // if current >= deadline
    BLT     check_thread_deadline

    // Goroutine deadline exceeded - signal async preemption
    MOVW    $1, R8
    MOVW    R8, ·NeedsAsyncPreempt(SB)

check_thread_deadline:
    MOVD    344(R7), R8  // R8 = ThreadPreemptDeadline
    CMP     R8, R9
    BLT     timer_return

    // Thread deadline exceeded - signal thread switch
    MOVW    $1, R8
    MOVW    R8, ·NeedsThreadPreempt(SB)
```

**Goroutine Change Detection**:

When the Go runtime switches goroutines internally, the g pointer (X28) changes. The timer handler detects this and resets the goroutine deadline:

```asm
    // Load LastSeenG from thread struct
    MOVD    320(R7), R8  // R8 = currentThread.LastSeenG
    CMP     R4, R8       // Compare with current g (R4)
    BEQ     same_goroutine

    // G changed! Reset goroutine deadline
    MOVD    R4, 320(R7)  // Update LastSeenG
    MOVD    R9, 336(R7)  // Reset GoroutineStart
    MOVD    ·GoroutinePreemptTicks(SB), R8
    ADD     R9, R8, R8
    MOVD    R8, 352(R7)  // New GoroutinePreemptDeadline
```

### Path 3: Thread Context Switch (`checkThreadPreemptionImpl`)

**Location**: `kmazarin/kmazarin/threads.go` lines 1372-1465

**Called when**: NeedsThreadPreempt is set by timer handler

**Flow**:
```go
func checkThreadPreemptionImpl(sf *SchedulerFunc, framePtr uint64) uint64 {
    if CurrentThreadIdx < 0 {
        return 0  // No current thread - nothing to preempt
    }

    // Save current thread's context from exception frame
    SaveContextFromFrame(uintptr(framePtr))

    // Mark current thread as ready, add to back of queue
    oldThread.State = ThreadReady
    readyQueue.PushNoDuplicate(oldThread.TID)

    // Find next ready thread
    next := threadFindReadyIdx()
    if next == nil {
        // No other thread ready - continue with current
        return 0
    }

    // Switch to new thread
    CurrentThreadIdx = threadToIdx(next)
    atomic.StorePointer(&CurrentThread, unsafe.Pointer(next))
    next.State = ThreadRunning

    // Initialize new thread's preemption tracking
    next.ThreadPreemptDeadline = currentTime + kirq.ThreadPreemptTicks
    next.GoroutinePreemptDeadline = currentTime + kirq.GoroutinePreemptTicks

    // CRITICAL: Switch TTBR0 if different page table
    if next.PageTableL0PA != 0 && next.PageTableL0PA != oldThread.PageTableL0PA {
        kmem.SwitchTTBR0WithASID(next.PageTableL0PA, uint16(next.PID))
    }

    return uint64(uintptr(unsafe.Pointer(&next.Context)))
}
```

The assembly caller (exceptions_arm64.s) then copies the new context to the exception frame, and ERET loads it.

## Async Preemption Injection

### Purpose

Async preemption forcibly preempts a goroutine that hasn't voluntarily yielded. This is necessary for:
1. Fair CPU distribution among goroutines
2. Preventing infinite loops from starving other work
3. Timely GC and other runtime operations

### How It Works

**Location**: `kmazarin/kmazarin/exceptions_arm64.s` lines 960-1150

When a goroutine exceeds its deadline and NeedsAsyncPreempt is set:

1. **Validate eligibility**:
   - g.atomicstatus == _Grunning (not in syscall, not waiting)
   - m.locks == 0 (not holding runtime locks)
   - InCloneSetup == 0 (not a fresh clone child)
   - Not already in asyncPreempt

2. **Modify user stack** (push original return state):
   ```asm
   // Get user SP and decrease by 16
   MOVD    EXC_FRAME_SP_EL0(RSP), R14
   SUB     $16, R14

   // Store original LR and R29 using STTR (unprivileged store)
   WORD    $0xF80009CC  // sttr x12, [x14] - Store LR
   WORD    $0xF80089CD  // sttr x13, [x14, #8] - Store R29

   // Update SP in exception frame
   MOVD    R14, EXC_FRAME_SP_EL0(RSP)
   ```

3. **Redirect execution**:
   ```asm
   // Set LR to original ELR (so asyncPreempt knows where to return)
   MOVD    R11, EXC_FRAME_X28+24(RSP)  // LR slot in frame

   // Set ELR to asyncPreempt address
   MOVD    R10, EXC_FRAME_ELR_SPSR(RSP)
   ```

4. **ERET**: When exception returns, thread jumps to asyncPreempt instead of original location

### Stack Layout After Injection

```
Before injection:
    SP -> [user data...]

After injection:
    SP-16 -> original LR
    SP-8  -> original R29
    SP    -> [user data...]

New SP = SP-16
ELR = asyncPreempt
LR = original_ELR (so asyncPreempt knows where to return)
```

### Cooperative Preemption (Backup Mechanism)

For userspace threads, the timer handler also sets cooperative preemption flags:

```asm
// Set g.preempt = true
MOVD    ·PreemptPreemptOffset(SB), R5
ADD     R4, R5, R5  // R5 = &g.preempt
MOVD    $1, R6
WORD    $0xF80008A6  // sttr x6, [x5]

// Set g.stackguard0 = stackPreempt
MOVD    ·PreemptStackGuard0Offset(SB), R5
ADD     R4, R5, R5
MOVD    ·PreemptStackPreemptValue(SB), R6
WORD    $0xF80008A6  // sttr x6, [x5]
```

This causes the Go runtime to yield at the next function call (via stack growth check).

## InCloneSetup Protection

### The Problem

When the Go runtime creates a new M (OS thread) via clone, it stores critical values on the child's stack:

```
Parent stores BEFORE clone syscall:
  stack-8:   mp (pointer to M struct)
  stack-16:  gp (pointer to g struct)
  stack-24:  fn (entry function)
  stack-32:  magic (0x1234)
```

The child's first instructions after clone read these values:
```asm
ldr x10, [sp, #-24]    // Load fn
ldr x28, [sp, #-16]    // Load gp (sets g register)
ldr x11, [sp, #-8]     // Load mp
```

**If async preemption fires before these reads**:
```
Async preempt writes:
  new_sp = stack - 16
  [new_sp]   = LR     -> OVERWRITES gp!
  [new_sp+8] = R29    -> OVERWRITES mp!
```

**Result**: Child reads garbage and crashes.

### The Solution

1. **Set flag in CloneThread** (threads.go line 651):
   ```go
   t.InCloneSetup = 1
   ```

2. **Check flag before async preempt** (exceptions_arm64.s line 1025-1031):
   ```asm
   MOVD    main·ThreadInCloneSetupOffset(SB), R11
   ADD     R11, R10, R11  // R11 = &thread.InCloneSetup
   MOVW    (R11), R12
   CBNZ    R12, timer_no_preempt_in_clone_setup
   ```

3. **Clear flag on first syscall** (exceptions_arm64.s lines 320-328, 1432-1440):
   ```asm
   MOVD    main·CurrentThread(SB), R10
   CBZ     R10, skip_clear_clone_setup
   MOVD    main·ThreadInCloneSetupOffset(SB), R11
   ADD     R11, R10, R11
   MOVW    $0, R12
   MOVW    R12, (R11)  // thread.InCloneSetup = 0
   ```

## Critical Invariants and Assumptions

### 1. ERET Behavior

ERET (Exception Return) atomically:
- Loads PC from ELR_EL1
- Loads PSTATE from SPSR_EL1
- Switches to target exception level (EL0 for userspace)
- Switches stack pointer based on SPSR.M[0] (SP_EL0 for EL0t)

**CRITICAL**: SPSR must have correct mode bits (0x0 for EL0t, AArch64).

### 2. TTBR0 Switching

- TTBR0 maps userspace memory (0x0 - 0x0000FFFFFFFFFFFF)
- TTBR1 maps kernel memory (0xFFFF000000000000+)
- Each priest has its own page table (PageTableL0PA)
- ASID (Address Space ID) in TTBR0 allows TLB entries to coexist
- Must switch TTBR0 when switching to a thread with different page table

```go
if next.PageTableL0PA != 0 && next.PageTableL0PA != oldThread.PageTableL0PA {
    kmem.SwitchTTBR0WithASID(next.PageTableL0PA, uint16(next.PID))
}
```

### 3. Timer Counter

- Read via `MRS Xn, CNTVCT_EL0` (virtual timer counter)
- Frequency from `CNTFRQ_EL0` (typically 62.5MHz on QEMU)
- Timer compare value in `CNTV_CVAL_EL0`
- Timer control in `CNTV_CTL_EL0`

**Timer re-arm** (preempt_arm64.s):
```asm
// Calculate ticks for 10ms: freq / 100
MOVD    ·SystemTimerFrequency(SB), R0
MOVD    $100, R1
UDIV    R1, R0, R0

// Read current counter
MRS     X1, CNTVCT_EL0

// Set new compare value
ADD     R0, R1, R1
MSR     X1, CNTV_CVAL_EL0
```

### 4. GIC (Interrupt Controller)

- Timer IRQ is INTID 27 (PPI)
- Must write to GICC_EOIR after handling to allow future interrupts
- IAR read acknowledges interrupt and returns INTID

### 5. g Pointer Location

- Kernel goroutines: g pointer in high memory (0xFFFF...)
- Userspace goroutines: g pointer in low memory (0x0000...)
- Timer handler branches based on this to handle kernel vs userspace differently

```asm
LSR     $48, R4, R5    // Get high 16 bits
MOVD    $0xFFFF, R6
CMP     R5, R6
BEQ     g_in_kernel
B       g_in_userspace
```

### 6. Exception Frame Layout

```
Offset  Content
0       X0
8       X1
...
224     X28 (g pointer)
232     X29 (FP)
240     X30 (LR)
248     (padding)
256     ELR_EL1
264     SPSR_EL1
272     (padding)
280     (padding)
288     SP_EL0
296     (end)
```

Total size: 296 bytes (EXC_FRAME_SIZE)

### 7. Go Runtime Constants

These are obtained via `runtime.PreemptOffsets` linkname:
- `_Grunning` = 2
- `_Gscan` = 0x1000 (bit mask)
- `stackPreempt` = 0xFFFFFADE (magic value for g.stackguard0)

## File Reference

| File | Purpose |
|------|---------|
| `kmazarin/kmazarin/threads.go` | Thread struct, scheduling functions, context save/restore |
| `kmazarin/kmazarin/abi_stubs_arm64.s` | ABI0 stubs including RunFirstThread |
| `kmazarin/kmazarin/exceptions_arm64.s` | Exception vectors, async preempt injection, context switch |
| `kmazarin/kirq/preempt_arm64.s` | Timer IRQ handler, deadline checking |
| `kmazarin/kirq/preempt.go` | Preemption offsets, thresholds, control variables |
| `kmazarin/kmem/paging.go` | TTBR0 switching functions |
| `kmazarin/ksyscall/clone.go` | Clone syscall (sets InCloneSetup) |

## Debugging Tips

### Common Failure Modes

1. **Thread never starts**: Check if `RunFirstThread` is called, verify thread was added to readyQueue

2. **"schedule: holding locks" panic**: Async preemption fired while m.locks != 0. Check m.locks validation in exceptions_arm64.s

3. **Data abort in clone child**: InCloneSetup protection failed. Verify flag is set in CloneThread and checked before async preempt

4. **Thread runs forever without preemption**: Timer IRQ not firing, or deadlines not being checked. Verify timer is enabled and NeedsThreadPreempt is being set

5. **Wrong page table**: TTBR0 not switched on context switch. Verify PageTableL0PA is set and SwitchTTBR0WithASID is called

### Debug Counters

```go
// In kirq package
var TimerIRQCount uint64           // Total timer IRQs
var UserspacePathCount uint64      // Timer hits in userspace path
var UserspaceCoopPreemptCount uint64 // Cooperative preemptions set
var UserspaceGChangedCount uint64  // Goroutine changes detected
var NeedsAsyncPreempt uint32       // Current async preempt flag
var NeedsThreadPreempt uint32      // Current thread preempt flag
```

Print these periodically to verify scheduling is working:
```go
console.KPrintf("[Stats] TimerIRQs=%d UserspaceCoopPreempt=%d\n",
    kirq.TimerIRQCount, kirq.UserspaceCoopPreemptCount)
```

## Testing Checklist

When modifying scheduling code, verify:

- [ ] Single priest with multiple goroutines runs (priestsieve)
- [ ] Multiple priests run concurrently (sievetest x2)
- [ ] Clone children start correctly (no data aborts)
- [ ] Goroutines interleave (multiple IDs in output)
- [ ] No "schedule: holding locks" panics
- [ ] Timer IRQ count increases over time
- [ ] Context switches happen (thread IDs change)

## History

- **Initial Implementation**: Timer-based preemption for kmazarin goroutines
- **Userspace Support**: Added per-thread AsyncPreemptAddr for priests
- **InCloneSetup**: Added to protect clone children from stack corruption
- **RunFirstThread**: Added to solve "first thread never starts" problem when kernel has no current thread to preempt
