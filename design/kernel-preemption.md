# Kernel Thread Preemption Design

## Problem Statement

Kmazarin uses a hybrid scheduling model:
- **Userspace threads (priests)**: Timer-preempted via the timer ISR. Works correctly.
- **Kernel threads**: Cooperative only. Must voluntarily yield via SVC (futex, nanosleep).

This model has a fatal flaw: when a kernel thread runs Go runtime code that doesn't
make SVCs for an extended period, it monopolizes the CPU. Other threads — including the
kernel idle loop (thread 0) and ready userspace threads — starve indefinitely.

### Observed Failure Mode

1. Thread 0 (idle loop) yields to ready threads via `YieldToReadyThread`
2. Userspace threads run, eventually all block (futex, WaitSoftIRQ)
3. A kernel thread (e.g., sysmon, TID 2) gets scheduled
4. Timer ISR fires, wakes blocked threads via `ProcessDeadlinesTopHalf` — threads
   move to the ready queue
5. Timer ISR checks SPSR, sees EL1 (kernel) — **skips preemption entirely**
6. Kernel thread continues. Ready threads never get CPU time.
7. System deadlocks despite active timer interrupts and ready threads.

Diagnostic data confirms: kernel thread 2 makes tens of thousands of SVCs per
5-second interval (ksvc counter), but none result in a context switch because
`ThreadBlockFutex`/`ThreadBlockSleep` either find no ready thread at that instant
or return EAGAIN (futex value changed).

### Why Kernel Preemption Was Disabled

The ARM64 exception handler explicitly skips preemption for EL1:

```asm
// exceptions_arm64.s:1008-1010
MOVD  (EXC_FRAME_ELR_SPSR+8)(RSP), R10   // R10 = saved SPSR
AND   $0x4, R10, R10                      // EL1 bit (M[2])
CBNZ  R10, timer_no_thread_preempt        // Kernel mode — skip
```

The reason: when a kernel thread is inside an SVC handler, the SVC exception frame
is on SP_EL1. If the timer fires and preempts the thread, a context switch would
leave the SVC frame orphaned on the shared per-CPU exception stack. The next thread's
exceptions would corrupt it.

## Solution: Preempt Kernel Threads When Safe

### Key Insight

Kernel threads alternate between two states:
1. **Running normal Go code** (depth 0) — SP_EL1 is at the top of the exception
   stack. No exception frames are on it. Safe to preempt.
2. **Inside an SVC handler** (depth 1) — SP_EL1 has the SVC exception frame.
   NOT safe to preempt.

By tracking which state the CPU is in, the timer ISR can preempt kernel threads
when safe (depth 0) and skip when unsafe (depth 1).

### Why a Boolean Is Sufficient (Not an Integer)

SVCs cannot nest inside SVC handlers because:
- ARM64 automatically masks IRQs on exception entry (PSTATE.DAIF.I set)
- All syscall handler code is `//go:nosplit` — no Go runtime lock contention
- Kernel locks use custom `ds.Spinlock` (CAS + busy-wait), not Go mutexes
- The only place IRQs are re-enabled inside an SVC handler is for WFI in
  `ThreadBlockFutex`/`ThreadBlockSleep` when no ready thread is found — but
  this does NOT nest another SVC

Therefore, a per-CPU boolean (0 or 1) is sufficient. No integer tracking needed.

### Why Per-Thread Exception Stacks Are NOT Needed

When depth == 0:
- SP_EL1 is at the **top** of the per-CPU exception stack (no frames)
- Timer handler pushes its own frame, does preemption, pops its frame, ERETs
- SP_EL1 returns to the top after ERET
- All threads share the same per-CPU exception stack position at depth 0

Since we only preempt at depth 0, SP_EL1 is always in a known state. There is
nothing thread-specific to save or restore.

## Design

### 1. Per-CPU SVCDepth Flag

Add a `SVCDepth` field to the `PerCPU` struct:

```go
type PerCPU struct {
    // ... existing fields ...
    SVCDepth uint32  // 0 = safe to preempt, 1 = inside SVC handler
}
```

This must be at a **fixed, known offset** so assembly can read it without
calling Go functions. Document the offset in the PerCPU struct comments.

### 2. SVC Entry: Set Depth = 1

In the SVC handler assembly, immediately after saving registers and before
calling any Go code, set the per-CPU SVCDepth to 1:

```asm
sync_exception_handler:
    MSR  $1, SPSel
    SUB  $EXC_FRAME_SIZE, RSP
    // ... save registers ...

    // Mark that we're inside an SVC handler (unsafe to preempt)
    // Load PerCPU pointer (CPU 0 for now; SMP: use TPIDR_EL1)
    MOVD  main·perCPUData(SB), R10
    MOVD  $1, R11
    MOVW  R11, SVCDepth_OFFSET(R10)

    // ... check ESR for SVC, dispatch, etc. ...
```

### 3. SVC Exit: Set Depth = 0

Just before ERET in `sync_return`, clear the flag:

```asm
sync_return:
    // ... restore registers ...

    // Clear SVCDepth — we're leaving the SVC handler
    MOVD  main·perCPUData(SB), R10
    MOVW  ZR, SVCDepth_OFFSET(R10)

    ADD  $EXC_FRAME_SIZE, RSP
    ERET
```

**Important**: The flag must be cleared BEFORE `ADD/ERET` to prevent a window
where the timer could see depth 0 while the SVC frame is still on SP_EL1.
Actually, since DAIF.I is set during `sync_return` (IRQs masked during register
restore), the timer cannot fire. So the exact position relative to ADD is not
critical, but clearing before ERET is logically correct.

### 4. Timer ISR: Check Depth Before Preempting EL1

Replace the current "skip all EL1" check with a depth-aware check:

```asm
// Current code (exceptions_arm64.s:1008-1010):
//   MOVD  (EXC_FRAME_ELR_SPSR+8)(RSP), R10
//   AND   $0x4, R10, R10
//   CBNZ  R10, timer_no_thread_preempt

// New code:
MOVD  (EXC_FRAME_ELR_SPSR+8)(RSP), R10   // R10 = saved SPSR
AND   $0x4, R10, R10                      // EL1 bit
CBZ   R10, check_preemption               // EL0 — always check preemption

// EL1: check if we're inside an SVC handler
MOVD  main·perCPUData(SB), R10
MOVW  SVCDepth_OFFSET(R10), R10
CBNZ  R10, timer_no_thread_preempt        // depth=1, inside SVC — skip

// EL1, depth=0 — safe to preempt. Fall through to preemption check.
check_preemption:
// ... existing NeedsThreadPreempt check + checkThreadPreemptionImpl call ...
```

### 5. checkThreadPreemptionImpl: Allow Kernel Threads

Currently, `checkThreadPreemptionImpl` only searches for userspace threads:

```go
next := findReadyUserspaceThreadSchedLockHeld(oldThread.PID)
```

With kernel preemption enabled, this must change. When the preempted thread is
a kernel thread (PageTableL0PA == 0), search for ANY ready thread:

```go
var next *Thread
if oldThread.PageTableL0PA != 0 {
    // Userspace: prefer other userspace threads (same TTBR0/ASID context)
    next = findReadyUserspaceThreadSchedLockHeld(oldThread.PID)
} else {
    // Kernel: any ready thread is fine
    next = findReadyThreadPreferDifferentPriestSchedLockHeld(oldThread.PID)
}
```

When the NEW thread is a userspace thread, the existing TTBR0 switch code
already handles the page table change. When the new thread is a kernel thread
(e.g., thread 0), TTBR0 doesn't need switching (kernel uses TTBR1).

## Multi-Architecture Support

The SVCDepth concept is architecture-independent. Each architecture has its own
exception stack mechanism, but the pattern is identical:

| Architecture | Exception Stack | Syscall Entry | Syscall Exit | Timer ISR Check |
|---|---|---|---|---|
| ARM64 | SP_EL1 (per-CPU) | `sync_exception_handler` | `sync_return` | `irq_exception_handler` |
| x86_64 | IST in TSS (per-CPU) | `syscallEntry` | `syscallReturn` | `timerHandler` |
| RISC-V | `sscratch` CSR swap (per-CPU) | `trap_entry` (ecall path) | `trap_return` | `trap_entry` (timer path) |

On all three architectures:
- The exception stack is per-CPU, not per-thread
- At depth 0, the stack pointer is at a known position (top)
- The timer/trap handler pushes and pops its own frame symmetrically
- The SVCDepth flag is in the PerCPU struct at a fixed offset

No per-thread exception stacks are needed on any architecture.

## Multi-Core (SMP) Compatibility

This design is **not dependent on single-core**. It extends naturally to SMP:

- `SVCDepth` is in the **PerCPU** struct. Each CPU has its own copy.
- Each CPU's timer ISR reads only its own CPU's SVCDepth. No cross-CPU reads.
- The `schedulerLock` already protects the ready queue for concurrent access.
- On ARM64 SMP, the PerCPU pointer comes from `TPIDR_EL1` (set during CPU boot).
- On x86_64 SMP, the PerCPU pointer comes from `GS` segment or a per-CPU page.
- On RISC-V SMP, the PerCPU pointer comes from `sscratch` or `tp` register.

The only single-core assumption in the CURRENT code is that `main·perCPUData(SB)`
can be used directly (always CPU 0). For SMP, this becomes a per-CPU lookup, but
the SVCDepth logic is unchanged.

## Implementation Checklist

### Phase 1: ARM64

1. Add `SVCDepth uint32` to `PerCPU` struct in `percpu.go`
2. Document the byte offset of `SVCDepth` within PerCPU (for assembly access)
3. Export the offset as a Go constant or global variable readable from assembly
4. In `exceptions_arm64.s` `sync_exception_handler`: set SVCDepth = 1 after
   register save, before Go dispatch
5. In `exceptions_arm64.s` `sync_return`: set SVCDepth = 0 before ERET
6. In `exceptions_arm64.s` timer preemption check: replace unconditional EL1
   skip with SVCDepth-aware check
7. In `checkThreadPreemptionImpl`: allow kernel thread preemption (search for
   any ready thread, not just userspace)
8. Test: verify kernel thread 2 (sysmon) gets preempted, thread 0 (idle loop)
   runs regularly, userspace timer events fire continuously

### Phase 2: x86_64

9. Same SVCDepth set/clear in `exceptions_amd64.s` syscall entry/exit
10. Same depth check in x86_64 timer handler
11. Test on x86_64

### Phase 3: RISC-V

12. Same SVCDepth set/clear in RISC-V trap handler
13. Same depth check in RISC-V timer path
14. Test on RISC-V

## What This Fixes

- Thread 0 (idle loop) will be timer-preempted back onto the CPU regularly,
  allowing it to process deadlines, bridge IRQ flags, and yield to ready threads
- Kernel thread 2 (sysmon) will be preempted when its time slice expires,
  preventing CPU monopolization
- Userspace timer events (dapope clock) will fire continuously instead of
  stopping after 2-3 events
- Input events (keyboard, mouse) will be delivered to userspace

## What This Does NOT Change

- SVC handler execution: IRQs are masked during SVC handlers (ARM64 exception
  entry sets DAIF.I). The SVCDepth flag is a safety check for the case where a
  handler explicitly re-enables IRQs (e.g., WFI in ThreadBlockFutex).
- Userspace preemption: continues to work exactly as before.
- The 1ms implicit futex timeout hack should be removed once this is working,
  as it will no longer be needed.
- The `eventPoller` removal (separate planned change) is orthogonal to this.

## Risks

1. **EL1 → EL0 transition on preempt**: When a kernel thread is preempted and
   the next thread is userspace, the ERET loads the userspace thread's SPSR
   (EL0). The TTBR0 switch must happen before ERET. This already works in the
   existing `checkThreadPreemptionImpl` code.

2. **Thread 0 SPSR**: Thread 0's saved SPSR is EL1t (0x4). When thread 0 is
   preempted and later scheduled back, ERET restores SPSR=0x4 → returns to
   EL1t mode. This is correct — thread 0 runs the idle loop at EL1.

3. **Go runtime state during preemption**: When a kernel thread is preempted
   mid-function, its goroutine state (g, m, p) is preserved in registers
   (saved to ThreadContext). When resumed, registers are restored. Go's
   runtime expects this — it's the same as what happens with async preemption
   on Linux.
