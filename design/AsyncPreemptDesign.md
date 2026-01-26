# Async Preemption Design for Mazzy/Kmazarin

## Overview

This document describes the async preemption mechanism used in the Mazzy kernel to preempt running goroutines, and the special handling required to protect clone child threads from stack corruption during their setup phase.

## Background: Go Runtime Threading Model

The Go runtime uses an M:N threading model:
- **G** (goroutine): A lightweight user-space thread
- **M** (machine): An OS thread that executes goroutines
- **P** (processor): A logical processor that schedules goroutines onto Ms

When the Go runtime needs to create a new M (OS thread), it uses the `clone(2)` syscall on Linux/ARM64. The runtime's `runtime.clone.abi0` function sets up the stack with critical values before making the syscall.

## The Clone Stack Layout

When the Go runtime calls clone, it prepares the child's stack with values the child needs to read immediately upon starting:

```
Parent stores these values BEFORE clone syscall:
  stack-8:   mp (pointer to M struct)
  stack-16:  gp (pointer to g struct, the g0 for this M)
  stack-24:  fn (entry function, usually runtime.mstart)
  stack-32:  magic (0x1234, for verification)

Child SP after clone = stack (points just above these values)
```

The child's first instructions after returning from clone (with return value 0) are:

```asm
// In runtime.clone.abi0 (child path):
    ldr x10, [sp, #-24]    // Load fn from stack-24
    ldr x28, [sp, #-16]    // Load gp from stack-16 (sets g register)
    ldr x11, [sp, #-8]     // Load mp from stack-8
    // ... then call fn with mp as argument
```

**Critical**: These stack reads happen in userspace immediately after the clone syscall returns. If anything modifies `stack-8` through `stack-32` before the child executes these loads, the child will read garbage and crash.

## Async Preemption Mechanism

### Purpose

Async preemption allows the kernel to forcibly preempt a running goroutine that hasn't voluntarily yielded. This is necessary for:
1. Fair CPU distribution among goroutines
2. Preventing infinite loops from starving other work
3. Timely GC and other runtime operations

### How It Works

When a timer IRQ fires and the kernel determines a goroutine needs preemption:

1. **Check eligibility**: The goroutine must be in `_Grunning` state, not holding locks, not already in asyncPreempt, etc.

2. **Modify user stack**: Push the original LR and R29 onto the user stack:
   ```asm
   // Get user SP and decrease by 16
   MOVD    EXC_FRAME_SP_EL0(RSP), R14    // Original user SP
   SUB     $16, R14                       // New SP (allocate 16 bytes)

   // Store original LR and R29 using STTR (unprivileged store)
   WORD    $0xF80009CC    // sttr x12, [x14]      - Store LR at new_sp
   WORD    $0xF80089CD    // sttr x13, [x14, #8]  - Store R29 at new_sp+8
   ```

3. **Redirect execution**: Set ELR to `asyncPreempt` function address so when ERET executes, the thread jumps to the preemption handler instead of resuming where it was interrupted.

4. **asyncPreempt saves state**: The Go runtime's asyncPreempt function saves all registers and calls the scheduler to potentially switch to another goroutine.

### The Stack Layout After Injection

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

## The Problem: Clone Child Stack Corruption

### The Conflict

When a clone child thread is scheduled for the first time, its stack looks like:

```
stack-8:   mp
stack-16:  gp
stack-24:  fn
stack-32:  magic
SP = stack
```

If a timer IRQ fires and async preemption is triggered BEFORE the child reads fn/gp/mp:

```
Async preempt writes:
  new_sp = stack - 16 = stack-16
  [new_sp]   = LR     -> OVERWRITES gp!
  [new_sp+8] = R29    -> OVERWRITES mp!
```

**Result**: The child's gp and mp values are destroyed. When the child eventually reads from `stack-16` and `stack-8`, it gets garbage (LR and R29 values instead of gp and mp pointers), causing a crash.

### Observed Crash Pattern

The crash typically manifested as:
```
[FRM:0000000000083548 FAR=0000000000000030]
UE:24 - Data Abort from EL0
```

Where 0x83548 was in `runtime.clone.abi0` trying to store to `[x11, #48]` with x11=0 (corrupted mp pointer), causing FAR=0x30 (48 decimal).

## The Solution: InCloneSetup Flag

### Design

Add a per-thread flag `InCloneSetup` that indicates when a thread is a fresh clone child that hasn't yet read its fn/gp/mp values from the stack.

```go
type Thread struct {
    // ... other fields ...

    // Clone child protection - skip async preempt until clone setup completes
    InCloneSetup uint32 // 1 = in clone setup, 0 = normal
}
```

### Implementation

#### 1. Thread Struct Field (threads.go)

```go
type Thread struct {
    // ... existing fields ...

    // Clone child protection - skip async preempt until clone setup completes
    InCloneSetup uint32 // 1 = thread is in clone setup, 0 = normal
}
```

#### 2. Offset Computation (threads.go)

The assembly code needs to access this field. Rather than hardcoding offsets (error-prone), we compute them dynamically:

```go
var ThreadInCloneSetupOffset uintptr

func initThreadOffsets() {
    var t Thread
    ThreadInCloneSetupOffset = unsafe.Offsetof(t.InCloneSetup)
    // ... other offsets ...
}
```

#### 3. Set Flag in CloneThread (threads.go)

When creating a clone child, set the protection flag:

```go
func CloneThread(...) int16 {
    // ... thread setup ...

    // CRITICAL: Set InCloneSetup to protect the clone child from async preempt
    // The child must read fn/gp/mp from the stack before async preempt can safely
    // push LR/R29 there. This flag will be cleared on the child's first syscall.
    t.InCloneSetup = 1

    // ... rest of setup ...
}
```

#### 4. Assembly Check in Async Preempt (exceptions_arm64.s)

Before performing async preempt injection, check the flag:

```asm
    // Get current thread
    MOVD    main·CurrentThread(SB), R10
    CBZ     R10, timer_no_preempt

    // CRITICAL: Check InCloneSetup flag - skip async preempt for clone children
    // Clone children have fn/gp/mp stored on stack that would be overwritten by
    // the async preempt LR/R29 push. Flag is cleared on first syscall.
    MOVD    main·ThreadInCloneSetupOffset(SB), R11
    ADD     R11, R10, R11
    MOVW    (R11), R12
    CBNZ    R12, timer_no_preempt_in_clone_setup

    // ... continue with async preempt injection ...

timer_no_preempt_in_clone_setup:
    // Thread is a clone child still reading fn/gp/mp from stack.
    // Async preempt would overwrite these values with LR/R29.
    // Clear flag and skip - InCloneSetup will be cleared on first syscall.
    MOVW    $0, R10
    MOVW    R10, mazzy/kmazarin/kirq·NeedsAsyncPreempt(SB)
    B       timer_no_preempt
```

#### 5. Clear Flag on First Syscall (exceptions_arm64.s)

The clone child's first syscall indicates it has completed setup (read fn/gp/mp and started executing). Clear the flag in both EL0 and EL1 syscall handlers:

```asm
    // Clear InCloneSetup flag for current thread (if set)
    // This marks the clone child as having completed its setup phase.
    // After this, async preempt is safe because fn/gp/mp have been read from stack.
    MOVD    main·CurrentThread(SB), R10
    CBZ     R10, skip_clear_clone_setup
    MOVD    main·ThreadInCloneSetupOffset(SB), R11
    ADD     R11, R10, R11
    MOVW    $0, R12
    MOVW    R12, (R11)
skip_clear_clone_setup:
```

### Why Clear on First Syscall?

The Go runtime's clone child path:
1. Returns from clone syscall (child gets 0 in x0)
2. **Immediately** reads fn, gp, mp from stack
3. Sets up g register (x28 = gp)
4. Calls fn(mp) - usually runtime.mstart
5. mstart does various initialization, eventually makes syscalls

By the time the child makes its first syscall, it has definitely read the stack values. The first syscall is typically:
- `futex` (for lock operations during runtime init)
- `mmap` (for memory allocation)
- Any other runtime operation

### Race Window Analysis

The vulnerable window is:
```
[Clone syscall returns] -> [Child reads fn/gp/mp from stack]
```

This is a very short window (a few instructions), but timer IRQs can fire at any time. With a 10ms timer tick and multiple clone children being created, the probability of hitting this window is non-negligible.

The InCloneSetup flag ensures that even if a timer IRQ fires during this window, async preemption is skipped, protecting the stack values.

## Alternative Approaches Considered

### 1. Different Stack Layout
Could the Go runtime use a different stack layout that doesn't conflict? No - this would require modifying the Go runtime, which we're trying to avoid.

### 2. Disable IRQs During Clone Setup
We could disable IRQs for the clone child until it completes setup. However:
- Requires tracking when setup completes (same complexity as InCloneSetup)
- Disabling IRQs too long affects system responsiveness
- InCloneSetup is more precise - it only affects async preempt, not all IRQs

### 3. Use Different Stack Offsets for Async Preempt
Could we push LR/R29 at different offsets? No - the Go runtime expects a specific stack layout for asyncPreempt.

### 4. Copy Stack Values Before Scheduling Clone Child
Could the kernel copy fn/gp/mp somewhere safe? This adds complexity and still has race windows.

## Testing

The fix can be verified by:
1. Running multiple priests that create goroutines (which use clone internally)
2. Observing that clone children don't crash with "Data Abort from EL0"
3. Checking that async preemption still works for normal goroutines

## Future Considerations

### Multi-Core
When/if Mazzy supports multiple CPU cores, the InCloneSetup flag needs to be accessed atomically. Currently it's a simple uint32 read/write which is atomic on ARM64, but explicit atomic operations may be needed for clarity.

### Performance
Checking InCloneSetup adds a few instructions to the async preempt path. This is negligible compared to the overall timer IRQ handling cost.

### Debug Output
If debugging clone issues, the `timer_no_preempt_in_clone_setup` label can have debug output added to trace when the flag prevents preemption.

## Files Modified

| File | Changes |
|------|---------|
| `kmazarin/kmazarin/threads.go` | Added InCloneSetup field, initThreadOffsets(), set flag in CloneThread |
| `kmazarin/kmazarin/exceptions_arm64.s` | Added InCloneSetup check in async preempt, clear flag in syscall handlers |

## References

- Go runtime source: `runtime/sys_linux_arm64.s` (clone.abi0)
- ARM64 exception handling: ARMv8-A Architecture Reference Manual
- Go async preemption: `runtime/preempt.go`, `runtime/signal_unix.go`
