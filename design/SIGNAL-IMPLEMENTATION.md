# Kmazarin Minimal Linux Signal Implementation

## Goal

Implement enough of the Linux signal mechanism that the Go runtime's existing
signal-based GC stop-the-world (STW) preemption works natively, eliminating the
`mspan` corruption bug caused by `tgkill` being a no-op.

After this work, kmazarin needs only its timer-based thread preemption for
scheduling — the Go runtime handles all goroutine-level preemption through its
own signal path.

## Background: Why Signals Fix the mspan Bug

The Go runtime's GC stop-the-world path is:

```
stopTheWorld → preemptall → preemptone(pp) → preemptM(mp) → signalM(mp, sigPreempt)
                                                              ↓
                                                         tgkill(pid, tid, SIGURG)
```

When `tgkill` is a no-op, goroutines running on other M's never receive the
preemption signal. GC proceeds while goroutines are mid-allocation, corrupting
`mspan` metadata. The fix: make `tgkill` actually deliver SIGURG to the target
thread, which causes the Go runtime's signal handler to inject `asyncPreempt`.

## Architecture Overview

```
 Go runtime calls tgkill(pid, tid, SIGURG)
    ↓  (SVC/INT/EBREAK — caught by exception handler)
 Kmazarin's SyscallTgkill handler
    ↓  (finds target Thread by TID, sets pending signal bit)
 Target thread is next scheduled
    ↓  (signal delivery check in doContextSwitchImpl / SaveThread0AndYield)
 Build signal frame: ucontext + siginfo on gsignal stack
    ↓  (modify ThreadContext: PC=sigtramp, args=(sig, info, ctx))
 Thread resumes at runtime.sigtramp
    ↓  (saves callee-saved regs, calls sigtrampgo)
 sigtrampgo → sighandler → doSigPreempt
    ↓  (checks safe point, calls pushCall to modify ucontext PC)
 sigtramp returns → LR = sigreturn trampoline
    ↓  (SVC rt_sigreturn)
 Kmazarin's SyscallRtSigreturn handler
    ↓  (reads modified ucontext, overwrites exception frame)
 Thread resumes at asyncPreempt (injected by pushCall)
    ↓
 Goroutine yields — GC STW satisfied
```

## Key Single-Core Simplification

On kmazarin (single-core), when Thread A calls `tgkill` targeting Thread B,
Thread B is guaranteed not to be running (A is). Signal delivery happens when
Thread B is next scheduled — deterministic, no IPIs needed. This massively
simplifies the implementation compared to multi-core Linux.

---

## Stage 0: Signal State Infrastructure

**Files to create/modify:**
- `kmazarin/kmazarin/signal.go` (NEW)
- `kmazarin/kmazarin/threads.go` (MODIFY)

### 0.1 Signal Constants

Create `kmazarin/kmazarin/signal.go`:

```go
package main

const (
    _NSIG       = 65  // Linux signal count (1-64 + sentinel)
    _SIGURG     = 23  // Async preemption signal
    _SIGPROF    = 27  // Profiling (stub for now)
    _SI_KERNEL  = 0x80

    _SA_SIGINFO  = 0x00000004
    _SA_ONSTACK  = 0x08000000
    _SA_RESTORER = 0x04000000
    _SA_RESTART  = 0x10000000

    _SIG_BLOCK   = 0
    _SIG_UNBLOCK = 1
    _SIG_SETMASK = 2

    MaxPendingSignals = 64  // One bit per signal
)
```

### 0.2 Signal Action Table

In `signal.go`, add a global signal action table. There is one table shared by
all kernel threads (they are all in the same "process"):

```go
// SignalAction records a registered signal handler.
// Mirrors Linux's struct sigaction fields.
type SignalAction struct {
    Handler  uint64  // Function pointer (sa_handler / sa_sigaction)
    Flags    uint64  // SA_SIGINFO | SA_ONSTACK | SA_RESTART | SA_RESTORER
    Restorer uint64  // sa_restorer (sigreturn trampoline address)
    Mask     uint64  // sa_mask (signals blocked during handler)
}

// signalActions is the global signal action table.
// Index 0 is unused (signal numbers are 1-based).
// Protected by signalActionsLock.
var signalActions [_NSIG]SignalAction
var signalActionsLock Spinlock
```

### 0.3 Per-Thread Signal State

Add these fields to the `Thread` struct in `threads.go` (after `SyscallCloneTLS`):

```go
    // Signal delivery state
    PendingSignals  uint64  // Bitmask of pending signals (bit N = signal N+1)
    SignalSP        uint64  // gsignal stack pointer (top of signal stack)
    SignalStackBase uint64  // gsignal stack base (bottom of signal stack)
    SignalStackSize uint64  // gsignal stack size
    InSignalHandler uint32  // 1 = currently executing a signal handler
```

**Why `SignalSP`?** The Go runtime allocates a 32KB `gsignal` stack per M
during `mpreinit`. When delivering a signal, we build the signal frame on
this stack. We capture the gsignal stack bounds when the M is first seen
(during clone or after init).

### 0.4 Capture gsignal Stack Bounds

We need to know where each thread's gsignal stack is. The Go runtime's
`mpreinit` allocates `mp.gsignal = malg(32*1024)`, giving it a stack with
`.lo` and `.hi` fields.

Add to `PreemptOffsets` in `runtime-patches/preempt.go`:

```go
    MGsignalOffset    uintptr  // Offset of m.gsignal from m pointer
    GStackLoOffset    uintptr  // Offset of g.stack.lo from g pointer  (already have: StackLoOffset)
    GStackHiOffset    uintptr  // Offset of g.stack.hi from g pointer  (already have: StackHiOffset)
```

**Note:** `StackLoOffset` and `StackHiOffset` already exist in `PreemptOffsets`
(they're 0 and 8 respectively — `g.stack` is at offset 0 in `g`, and `stack.lo`
is at offset 0, `stack.hi` at offset 8). We just need `MGsignalOffset` added.

In `kirq/preempt.go`, add:

```go
var PreemptMGsignalOffset uintptr  // m.gsignal offset
```

Initialize it in `InitPreemption()` from the `PreemptOffsets` struct.

### 0.5 Populate Thread Signal Stack Bounds

In `CloneThread` (threads.go ~line 1300), after setting `newThread.MPtr` and
`newThread.GPtr`, populate the signal stack bounds:

```go
    // Capture gsignal stack bounds for signal delivery.
    // Path: m → m.gsignal → gsignal.stack.hi / gsignal.stack.lo
    if newThread.MPtr != 0 {
        mPtr := uintptr(newThread.MPtr)
        gsignalPtr := *(*uintptr)(unsafe.Pointer(mPtr + kirq.PreemptMGsignalOffset))
        if gsignalPtr != 0 {
            stackHi := *(*uintptr)(unsafe.Pointer(gsignalPtr + kirq.PreemptStackHiOffset))
            stackLo := *(*uintptr)(unsafe.Pointer(gsignalPtr + kirq.PreemptStackLoOffset))
            newThread.SignalSP = uint64(stackHi)        // Stack grows down
            newThread.SignalStackBase = uint64(stackLo)
            newThread.SignalStackSize = uint64(stackHi - stackLo)
        }
    }
```

Also do this for the bootstrap thread (Thread 0) during init.

---

## Stage 1: Record Signal Actions (rt_sigaction)

**Files to modify:**
- `runtime-patches/sys_linux_arm64.s` (MODIFY — un-stub rt_sigaction)
- `runtime-patches/sys_linux_amd64.s` (MODIFY — un-stub rt_sigaction)
- `runtime-patches/sys_linux_riscv64.s` (MODIFY — un-stub rt_sigaction)
- `kmazarin/ksyscall/stubs.go` (MODIFY — real SyscallRtSigaction)

### 1.1 Change rt_sigaction Overlay to Issue Real Syscall

The Go runtime's `initsig` calls `rt_sigaction` during startup to register
signal handlers. Currently the overlay returns 0 without doing anything. We need
it to issue a real syscall so kmazarin can record the handler.

**ARM64** — change `runtime-patches/sys_linux_arm64.s` lines 189-193:

```asm
// rt_sigaction — issue real syscall to kmazarin
TEXT runtime·rt_sigaction(SB),NOSPLIT|NOFRAME,$0-36
    MOVD    sig+0(FP), R0       // arg0: signal number
    MOVD    new+8(FP), R1       // arg1: new sigaction ptr
    MOVD    old+16(FP), R2      // arg2: old sigaction ptr (may be nil)
    MOVD    size+24(FP), R3     // arg3: sigset size
    MOVD    $134, R8            // SYS_rt_sigaction on ARM64
    SVC
    MOVW    R0, ret+32(FP)
    RET
```

**x86_64** — change `runtime-patches/sys_linux_amd64.s` (find rt_sigaction, same pattern):

```asm
TEXT runtime·rt_sigaction(SB),NOSPLIT,$0-36
    MOVQ    sig+0(FP), DI       // arg0: signal number
    MOVQ    new+8(FP), SI       // arg1: new sigaction ptr
    MOVQ    old+16(FP), DX      // arg2: old sigaction ptr
    MOVQ    size+24(FP), R10    // arg3: sigset size
    MOVL    $13, AX             // SYS_rt_sigaction on x86_64
    INT     $0x80               // kmazarin syscall trap
    MOVL    AX, ret+32(FP)
    RET
```

**RISC-V** — change `runtime-patches/sys_linux_riscv64.s`:

```asm
TEXT runtime·rt_sigaction(SB),NOSPLIT,$0-36
    MOV     sig+0(FP), A0       // arg0: signal number
    MOV     new+8(FP), A1       // arg1: new sigaction ptr
    MOV     old+16(FP), A2      // arg2: old sigaction ptr
    MOV     size+24(FP), A3     // arg3: sigset size
    MOV     $134, A7            // SYS_rt_sigaction (ARM64 number — check RISC-V: it's 134 too)
    WORD    $0x00100073          // EBREAK → kmazarin trap handler
    MOVW    A0, ret+32(FP)
    RET
```

**IMPORTANT (RISC-V):** The RISC-V overlay uses EBREAK (not ECALL) for syscalls.
Verify the syscall number mapping: RISC-V Linux uses `__NR_rt_sigaction = 134`.
Check `kmazarin/ksyscall/translate_riscv64.go` to ensure 134 maps to
`SysIDRtSigaction`.

### 1.2 Implement Real SyscallRtSigaction

Replace the stub in `kmazarin/ksyscall/stubs.go` (or create a new file
`kmazarin/ksyscall/rt_sigaction.go`):

```go
// SyscallRtSigaction records signal handlers installed by the Go runtime.
//
// Linux signature: rt_sigaction(int sig, const struct sigaction *act,
//                               struct sigaction *oact, size_t sigsetsize)
//
// The Go runtime's sigactiont struct layout (all architectures):
//   sa_handler  uintptr   (offset 0)
//   sa_flags    uint64    (offset 8)
//   sa_restorer uintptr   (offset 16)
//   sa_mask     uint64    (offset 24)
// Total: 32 bytes
//
//go:nosplit
func SyscallRtSigaction(signum, actPtr, oactPtr, sigsetsize, _, _ uint64) int64 {
    sig := int(signum)
    if sig <= 0 || sig >= _NSIG {
        return -22 // EINVAL
    }

    // If oactPtr is non-nil, copy the old action to it
    if oactPtr != 0 {
        oldAction := GetSignalAction(sig)
        writeSignalAction(uintptr(oactPtr), &oldAction)
    }

    // If actPtr is non-nil, install the new action
    if actPtr != 0 {
        var newAction SignalAction
        readSignalAction(uintptr(actPtr), &newAction)
        SetSignalAction(sig, &newAction)
    }

    return 0
}
```

Where `readSignalAction` and `writeSignalAction` read/write the `sigactiont`
struct from/to memory using `unsafe.Pointer`. These functions must handle
kernel-address memory directly (no user/kernel split needed since the Go
runtime runs in kernel space):

```go
//go:nosplit
func readSignalAction(addr uintptr, sa *SignalAction) {
    sa.Handler  = *(*uint64)(unsafe.Pointer(addr))
    sa.Flags    = *(*uint64)(unsafe.Pointer(addr + 8))
    sa.Restorer = *(*uint64)(unsafe.Pointer(addr + 16))
    sa.Mask     = *(*uint64)(unsafe.Pointer(addr + 24))
}

//go:nosplit
func writeSignalAction(addr uintptr, sa *SignalAction) {
    *(*uint64)(unsafe.Pointer(addr))      = sa.Handler
    *(*uint64)(unsafe.Pointer(addr + 8))  = sa.Flags
    *(*uint64)(unsafe.Pointer(addr + 16)) = sa.Restorer
    *(*uint64)(unsafe.Pointer(addr + 24)) = sa.Mask
}
```

`GetSignalAction` and `SetSignalAction` are thread-safe accessors on the global
`signalActions` table (in `signal.go`):

```go
//go:nosplit
func GetSignalAction(sig int) SignalAction {
    return signalActions[sig]
}

//go:nosplit
func SetSignalAction(sig int, sa *SignalAction) {
    signalActions[sig] = *sa
}
```

**No locking needed** for single-core — `initsig` runs sequentially during
startup, and the table is read-only after that.

### 1.3 Verification

After this stage, boot the kernel and check serial output. The Go runtime's
`initsig` will call `rt_sigaction` for each signal. Add a debug print in
`SyscallRtSigaction` to verify it's being called:

```go
serial.RawUARTPuts("[sigaction] sig=")
serial.RawUARTHex64(signum)
serial.RawUARTPuts(" handler=0x")
serial.RawUARTHex64(handler)
serial.RawUARTPuts("\r\n")
```

You should see ~20+ calls during boot. Look specifically for signal 23 (SIGURG)
— that's the one we care about. Record the handler address (it should be the
address of `runtime.sigtramp`).

---

## Stage 2: Record Alternate Signal Stack (sigaltstack)

**Files to modify:**
- `runtime-patches/sys_linux_arm64.s` (MODIFY)
- `runtime-patches/sys_linux_amd64.s` (MODIFY)
- `runtime-patches/sys_linux_riscv64.s` (MODIFY)
- `kmazarin/ksyscall/stubs.go` (MODIFY)

### 2.1 Change sigaltstack Overlay to Issue Real Syscall

Same pattern as rt_sigaction. Change the no-op to issue a real syscall.

**ARM64:**
```asm
TEXT runtime·sigaltstack(SB),NOSPLIT,$0-16
    MOVD    new+0(FP), R0      // new stack_t ptr (may be nil)
    MOVD    old+8(FP), R1      // old stack_t ptr (may be nil)
    MOVD    $132, R8            // SYS_sigaltstack on ARM64
    SVC
    CMN     $4095, R0
    BLS     sigaltstack_ok
    MOVD    $0, R0              // Ignore errors — runtime calls this during init
sigaltstack_ok:
    RET
```

**Note:** The runtime calls `sigaltstack(nil, &old)` to query the current
state, and later `sigaltstack(&new, nil)` to set it. The query call should
return a reasonable result even if we haven't set up anything. Return
SS_DISABLE in the flags field.

### 2.2 Implement SyscallSigaltstack

The `stackt` struct layout (all architectures):
```
ss_sp:    *byte   (offset 0, 8 bytes)
ss_flags: int32   (offset 8, 4 bytes)
pad:      [4]byte (offset 12)
ss_size:  uintptr (offset 16, 8 bytes)
```
Total: 24 bytes.

```go
//go:nosplit
func SyscallSigaltstack(newPtr, oldPtr, _, _, _, _ uint64) int64 {
    t := GetCurrentThread()
    if t == nil {
        return 0  // No thread context, ignore
    }

    // Return current state if oldPtr is non-nil
    if oldPtr != 0 {
        addr := uintptr(oldPtr)
        if t.SignalStackBase != 0 {
            *(*uint64)(unsafe.Pointer(addr)) = t.SignalStackBase  // ss_sp
            *(*int32)(unsafe.Pointer(addr + 8)) = 0               // ss_flags (active)
            *(*uint64)(unsafe.Pointer(addr + 16)) = t.SignalStackSize // ss_size
        } else {
            *(*uint64)(unsafe.Pointer(addr)) = 0                  // ss_sp
            *(*int32)(unsafe.Pointer(addr + 8)) = 2               // SS_DISABLE
            *(*uint64)(unsafe.Pointer(addr + 16)) = 0             // ss_size
        }
    }

    // Install new signal stack if newPtr is non-nil
    if newPtr != 0 {
        addr := uintptr(newPtr)
        sp := *(*uint64)(unsafe.Pointer(addr))
        flags := *(*int32)(unsafe.Pointer(addr + 8))
        size := *(*uint64)(unsafe.Pointer(addr + 16))

        if flags == 2 { // SS_DISABLE
            t.SignalStackBase = 0
            t.SignalSP = 0
            t.SignalStackSize = 0
        } else {
            t.SignalStackBase = sp
            t.SignalSP = sp + size  // Stack grows down — SP starts at top
            t.SignalStackSize = size
        }
    }

    return 0
}
```

**NOTE:** The runtime's `minitSignalStack` calls `sigaltstack(nil, &old)` to
query, then `signalstack(&mp.gsignal.stack)` (which calls `sigaltstack` with
the gsignal stack). However, `signalstack` is defined as:

```go
func signalstack(s *stack) {
    var st stackt
    st.ss_sp = (*byte)(unsafe.Pointer(s.lo))
    st.ss_size = s.hi - s.lo
    st.ss_flags = 0
    sigaltstack(&st, nil)
}
```

This will give us the gsignal stack bounds directly — excellent. We store them
on the Thread and use them during signal delivery in Stage 4.

---

## Stage 3: Implement tgkill — Set Pending Signal

**Files to modify:**
- `runtime-patches/sys_linux_arm64.s` (MODIFY)
- `runtime-patches/sys_linux_amd64.s` (MODIFY)
- `runtime-patches/sys_linux_riscv64.s` (MODIFY)
- `kmazarin/ksyscall/stubs.go` (MODIFY)

### 3.1 Change tgkill Overlay to Issue Real Syscall

**ARM64:**
```asm
// tgkill — send signal to a specific thread
TEXT ·tgkill(SB),NOSPLIT,$0-24
    MOVD    tgid+0(FP), R0     // arg0: thread group id (pid)
    MOVD    tid+8(FP), R1      // arg1: thread id (M.procid)
    MOVD    sig+16(FP), R2     // arg2: signal number
    MOVD    $131, R8            // SYS_tgkill on ARM64
    SVC
    RET
```

**x86_64:**
```asm
TEXT ·tgkill(SB),NOSPLIT,$0-24
    MOVQ    tgid+0(FP), DI
    MOVQ    tid+8(FP), SI
    MOVQ    sig+16(FP), DX
    MOVL    $234, AX            // SYS_tgkill on x86_64
    INT     $0x80
    RET
```

**RISC-V:**
```asm
TEXT ·tgkill(SB),NOSPLIT,$0-24
    MOV     tgid+0(FP), A0
    MOV     tid+8(FP), A1
    MOV     sig+16(FP), A2
    MOV     $131, A7            // SYS_tgkill on RISC-V (same as ARM64)
    WORD    $0x00100073          // EBREAK
    RET
```

### 3.2 Implement SyscallTgkill

Replace the stub:

```go
// SyscallTgkill sends a signal to a specific thread.
// Linux: tgkill(tgid, tid, sig)
//
// For kmazarin: sets a pending signal bit on the target thread.
// The signal is delivered when the thread is next scheduled.
//
//go:nosplit
func SyscallTgkill(tgid, tid, sig, _, _, _ uint64) int64 {
    signum := int(sig)
    if signum <= 0 || signum >= _NSIG {
        return -22 // EINVAL
    }

    // Find the target thread by TID.
    // M.procid is set to gettid() result, and our SyscallGettid returns Thread.TID.
    // So M.procid == Thread.TID.
    targetThread := FindThreadByTID(ThreadId(tid))
    if targetThread == nil {
        return -3 // ESRCH — no such process/thread
    }

    // Set the pending signal bit (atomic — safe from any context).
    // Signal numbers are 1-based; bit 0 = signal 1.
    bit := uint64(1) << uint(signum-1)
    atomicOrUint64(&targetThread.PendingSignals, bit)

    return 0
}
```

`FindThreadByTID` scans the thread list for a thread with matching TID. It
should be `//go:nosplit` and lock-free (just scan the fixed-size array):

```go
//go:nosplit
func FindThreadByTID(tid ThreadId) *Thread {
    for i := 0; i < MaxThreads; i++ {
        t := threadList.Get(i)
        if t != nil && t.TID == tid {
            return t
        }
    }
    return nil
}
```

`atomicOrUint64` does an atomic OR on a uint64. Implement using a CAS loop:

```go
//go:nosplit
func atomicOrUint64(addr *uint64, val uint64) {
    for {
        old := atomic.LoadUint64(addr)
        if atomic.CompareAndSwapUint64(addr, old, old|val) {
            return
        }
    }
}
```

**Note about M.procid:** The Go runtime's `gettid` overlay currently returns a
hardcoded value of 1 on all architectures. However, in the clone child path
(same overlay file), the child calls `gettid` via SVC (which goes through
kmazarin's `SyscallGettid`, returning `GetCurrentThreadTID()`). So:

- Bootstrap M (Thread 0): M.procid = 1 (from hardcoded gettid)
- Clone children: M.procid = Thread.TID (from SyscallGettid via SVC)

**FIX NEEDED:** Change the `gettid` overlay to issue a real syscall instead of
returning 1. Same pattern:

**ARM64:**
```asm
TEXT runtime·gettid(SB),NOSPLIT,$0-4
    MOVD    $178, R8            // SYS_gettid
    SVC
    MOVW    R0, ret+0(FP)
    RET
```

This ensures Thread 0's M.procid also gets the correct TID.

**x86_64:**
```asm
TEXT runtime·gettid(SB),NOSPLIT,$0-4
    MOVL    $186, AX            // SYS_gettid on x86_64
    INT     $0x80
    MOVL    AX, ret+0(FP)
    RET
```

**RISC-V:**
```asm
TEXT runtime·gettid(SB),NOSPLIT,$0-4
    MOV     $178, A7
    WORD    $0x00100073
    MOVW    A0, ret+0(FP)
    RET
```

**ALSO:** The `gettid` call in the clone child path (within the `clone`
function in the overlay) already issues SVC for gettid. This is correct.
Additionally, `minit()` in the Go runtime calls `getg().m.procid = uint64(gettid())`
on every new M. With the real gettid overlay, this will set procid correctly
for all M's including the bootstrap M.

---

## Stage 4: Restore sigtramp and Add sigreturn Trampoline

**Files to modify:**
- `runtime-patches/sys_linux_arm64.s` (MODIFY)
- `runtime-patches/sys_linux_amd64.s` (MODIFY)
- `runtime-patches/sys_linux_riscv64.s` (MODIFY)

### 4.1 Restore sigtramp (ARM64)

Replace the no-op sigtramp with the real implementation from Go runtime
(`bin/old.go.1.25.5/src/runtime/sys_linux_arm64.s`). The real sigtramp:

1. Allocates a 176-byte stack frame
2. Saves callee-saved registers (R19-R28, F8-F15)
3. Optionally calls `load_g` (skipped when `iscgo` is false — our case)
4. Calls `sigtrampgo(sig, info, ctx)` with signal number, siginfo*, ucontext*
5. Restores callee-saved registers
6. Returns (RET)

```asm
// sigtramp — signal handler entry point.
// Called with: R0=signum, R1=siginfo*, R2=ucontext*
// LR set to sa_restorer (sigreturn trampoline)
TEXT runtime·sigtramp(SB),NOSPLIT|TOPFRAME,$176
    // Save callee-saved registers
    MOVD    R19, (8*4)(RSP)
    MOVD    R20, (8*5)(RSP)
    MOVD    R21, (8*6)(RSP)
    MOVD    R22, (8*7)(RSP)
    MOVD    R23, (8*8)(RSP)
    MOVD    R24, (8*9)(RSP)
    MOVD    R25, (8*10)(RSP)
    MOVD    R26, (8*11)(RSP)
    MOVD    R27, (8*12)(RSP)
    // Note: R28 (g) is NOT saved/restored here — it's managed by runtime

    // Save FP registers (F8-F15)
    FMOVD   F8,  (8*14)(RSP)
    FMOVD   F9,  (8*15)(RSP)
    FMOVD   F10, (8*16)(RSP)
    FMOVD   F11, (8*17)(RSP)
    FMOVD   F12, (8*18)(RSP)
    FMOVD   F13, (8*19)(RSP)
    FMOVD   F14, (8*20)(RSP)
    FMOVD   F15, (8*21)(RSP)

    // Save signal number (sigtrampgo will use R0, R1, R2 as args)
    MOVW    R0, 8(RSP)

    // iscgo check — skip load_g since kmazarin doesn't use cgo
    MOVBU   runtime·iscgo(SB), R0
    CBZ     R0, sigtramp_no_cgo
    BL      runtime·load_g(SB)
sigtramp_no_cgo:

    MOVW    8(RSP), R0          // Restore signal number
    // R1 = siginfo* (still in R1 from caller)
    // R2 = ucontext* (still in R2 from caller)
    MOVD    $runtime·sigtrampgo<ABIInternal>(SB), R3
    BL      (R3)

    // Restore callee-saved registers
    MOVD    (8*4)(RSP), R19
    MOVD    (8*5)(RSP), R20
    MOVD    (8*6)(RSP), R21
    MOVD    (8*7)(RSP), R22
    MOVD    (8*8)(RSP), R23
    MOVD    (8*9)(RSP), R24
    MOVD    (8*10)(RSP), R25
    MOVD    (8*11)(RSP), R26
    MOVD    (8*12)(RSP), R27

    FMOVD   (8*14)(RSP), F8
    FMOVD   (8*15)(RSP), F9
    FMOVD   (8*16)(RSP), F10
    FMOVD   (8*17)(RSP), F11
    FMOVD   (8*18)(RSP), F12
    FMOVD   (8*19)(RSP), F13
    FMOVD   (8*20)(RSP), F14
    FMOVD   (8*21)(RSP), F15

    RET
```

**CRITICAL NOTE:** This is a simplified version. The real Go runtime sigtramp
uses macros `SAVE_R19_TO_R28` and `SAVE_F8_TO_F15` which may have slightly
different offsets. **Copy the exact sigtramp from
`bin/old.go.1.25.5/src/runtime/sys_linux_arm64.s`** and adapt it. Read that
file to get the exact register save/restore sequence.

The key requirement is that R0 (signal number), R1 (siginfo*), R2 (ucontext*)
are passed through to `sigtrampgo`, and R28 (g) must be correctly set (it will
be — we set it in the ThreadContext before signal delivery).

### 4.2 Add sigreturn Trampoline (ARM64)

After sigtramp returns (via RET → LR), we need execution to go to a sigreturn
trampoline that issues `rt_sigreturn`. Add this to the ARM64 overlay:

```asm
// sigreturn__sigaction — sigreturn trampoline called after signal handler returns.
// Issues rt_sigreturn syscall to restore the (possibly modified) ucontext.
TEXT runtime·sigreturn__sigaction(SB),NOSPLIT|NOFRAME,$0
    MOVD    $139, R8            // SYS_rt_sigreturn on ARM64
    SVC
    // Should not return — if it does, halt
    MOVD    $0xFFFFFFFF09000000, R0
    MOVW    $'!', R1
    MOVW    R1, (R0)
    B       -1(PC)
```

**IMPORTANT:** The address of `runtime·sigreturn__sigaction` is what gets stored
as `sa_restorer` in the sigaction struct. The Go runtime's `setsig` function
sets `sa.sa_restorer = abi.FuncPCABI0(sigreturn__sigaction)`. With our real
`rt_sigaction` implementation, we'll capture this address.

### 4.3 Restore sigtramp and sigreturn for x86_64

**x86_64 sigtramp** is more complex because of TLS. The key difference:
- g is loaded from TLS (`get_tls(R12); MOVQ g(R12), R14`)
- Stack must be 16-byte aligned for calls
- Return address is on the stack (no LR register)

Copy from `bin/old.go.1.25.5/src/runtime/sys_linux_amd64.s` — find the
`runtime·sigtramp` function and adapt it. Key points:

```asm
TEXT runtime·sigtramp(SB),NOSPLIT|TOPFRAME|NOFRAME,$0
    // Save caller-saved registers using PUSH_REGS_HOST_TO_ABI0
    // ... (copy exact sequence from Go runtime)

    // Load g from TLS
    get_tls(R12)
    MOVQ    g(R12), R14

    // Call sigtrampgo(DI=sig, SI=info, DX=ctx)
    MOVQ    DI, AX              // sig (already in DI from caller)
    MOVQ    SI, BX              // info
    MOVQ    DX, CX              // ctx
    CALL    ·sigtrampgo<ABIInternal>(SB)

    // Restore registers using POP_REGS_HOST_TO_ABI0
    RET
```

**x86_64 sigreturn:**
```asm
TEXT runtime·sigreturn__sigaction(SB),NOSPLIT|NOFRAME,$0
    MOVL    $15, AX             // SYS_rt_sigreturn on x86_64
    INT     $0x80               // kmazarin syscall trap
    INT3                        // Should not return
```

### 4.4 Restore sigtramp and sigreturn for RISC-V

**RISC-V sigtramp** — copy from `bin/old.go.1.25.5/src/runtime/sys_linux_riscv64.s`:

```asm
TEXT runtime·sigtramp(SB),NOSPLIT|TOPFRAME,$64
    MOVW    A0, 8(X2)           // Save signal number
    MOV     A1, 16(X2)          // Save info
    MOV     A2, 24(X2)          // Save ctx

    MOVBU   runtime·iscgo(SB), A0
    BEQ     A0, ZERO, sigtramp_no_cgo_rv
    CALL    runtime·load_g(SB)
sigtramp_no_cgo_rv:

    MOVW    8(X2), A0           // Restore signal number
    MOV     16(X2), A1          // Restore info
    MOV     24(X2), A2          // Restore ctx
    MOV     $runtime·sigtrampgo(SB), A3
    JALR    RA, A3
    RET
```

**RISC-V sigreturn:**
```asm
TEXT runtime·sigreturn__sigaction(SB),NOSPLIT|NOFRAME,$0
    MOV     $139, A7            // SYS_rt_sigreturn (same as ARM64)
    WORD    $0x00100073          // EBREAK
    UNIMP                       // Should not return
```

---

## Stage 5: Signal Delivery in Context Switch

This is the core of the implementation. When a thread with pending signals is
about to be scheduled, we intercept and build a signal frame before resuming it.

**Files to create/modify:**
- `kmazarin/kmazarin/signal.go` (ADD signal delivery logic)
- `kmazarin/kmazarin/signal_frame_arm64.go` (NEW)
- `kmazarin/kmazarin/signal_frame_amd64.go` (NEW)
- `kmazarin/kmazarin/signal_frame_riscv64.go` (NEW)
- `kmazarin/kmazarin/threads.go` (MODIFY — add signal check in context switch)

### 5.1 Signal Delivery Check Point

In `doContextSwitchImpl` (threads.go ~line 2460), AFTER we decide which thread
to switch to (`newThread`) and BEFORE returning `&newThread.Context`, add a
signal delivery check:

```go
    // Check for pending signals on the new thread.
    // If pending, build a signal frame and redirect to sigtramp.
    if newThread.PendingSignals != 0 && newThread.InSignalHandler == 0 {
        DeliverPendingSignal(newThread)
    }
```

Also add the same check in `SaveThread0AndYield` (threads.go ~line 1092),
before returning the context pointer:

```go
    if next.PendingSignals != 0 && next.InSignalHandler == 0 {
        DeliverPendingSignal(next)
    }
    return uint64(uintptr(unsafe.Pointer(&next.Context)))
```

And in `StartFirstThread` / `startFirstThreadImpl`, before returning the
context pointer.

### 5.2 DeliverPendingSignal (Architecture-Independent)

In `signal.go`:

```go
// DeliverPendingSignal checks for pending signals and sets up a signal frame
// for the highest-priority pending signal.
//
// PRECONDITION: thread.PendingSignals != 0
// PRECONDITION: thread.InSignalHandler == 0
// PRECONDITION: scheduler lock held, IRQs disabled
//
//go:nosplit
func DeliverPendingSignal(thread *Thread) {
    pending := atomic.LoadUint64(&thread.PendingSignals)
    if pending == 0 {
        return
    }

    // Find lowest-numbered pending signal (highest priority)
    var signum int
    for i := 0; i < 64; i++ {
        if pending&(1<<uint(i)) != 0 {
            signum = i + 1  // Signals are 1-based
            break
        }
    }

    // Check if we have a handler registered for this signal
    action := GetSignalAction(signum)
    if action.Handler == 0 {
        // No handler — clear the signal and return
        atomicAndUint64(&thread.PendingSignals, ^(uint64(1) << uint(signum-1)))
        return
    }

    // Clear this signal from pending (before delivery)
    atomicAndUint64(&thread.PendingSignals, ^(uint64(1) << uint(signum-1)))

    // Build the signal frame on the thread's signal stack
    // and modify the ThreadContext to enter sigtramp
    BuildSignalFrame(thread, signum, &action)

    thread.InSignalHandler = 1
}
```

`atomicAndUint64` is the AND counterpart of `atomicOrUint64`:

```go
//go:nosplit
func atomicAndUint64(addr *uint64, val uint64) {
    for {
        old := atomic.LoadUint64(addr)
        if atomic.CompareAndSwapUint64(addr, old, old&val) {
            return
        }
    }
}
```

### 5.3 BuildSignalFrame — ARM64

Create `kmazarin/kmazarin/signal_frame_arm64.go`:

This function builds a Linux-compatible `ucontext_t` and `siginfo_t` on the
thread's signal stack, then modifies the ThreadContext to enter `sigtramp`.

The ARM64 `ucontext` layout (offsets from base):
```
Offset  Field                      Size
0       uc_flags                   8
8       uc_link                    8
16      uc_stack.ss_sp             8
24      uc_stack.ss_flags          4
28      (pad)                      4
32      uc_stack.ss_size           8
40      uc_sigmask                 8
48      _pad[120]                  120
168     _pad2[8]                   8
176     uc_mcontext.fault_address  8
184     uc_mcontext.regs[0..30]    248 (31 × 8)
432     uc_mcontext.sp             8
440     uc_mcontext.pc             8
448     uc_mcontext.pstate         8
456     uc_mcontext._pad[8]        8
464     uc_mcontext.__reserved     4096
```
Total: 4560 bytes.

The `siginfo` layout:
```
Offset  Field       Size
0       si_signo    4
4       si_errno    4
8       si_code     4
12      (pad)       4
16      si_addr     8
```
Total: 128 bytes (Linux pads to `_si_max_size`; we only need to write the first
24 bytes and zero the rest, or just write the used fields since the Go runtime
only reads `si_code` and `si_addr`).

```go
//go:build arm64

package main

import "unsafe"

// Signal frame size constants
const (
    arm64UcontextSize = 4560  // Full ucontext_t size
    arm64SiginfoSize  = 128   // Full siginfo_t size
    arm64SignalFrameSize = arm64UcontextSize + arm64SiginfoSize + 16 // +16 for alignment

    // Offsets within ucontext_t
    ucMcontextRegs   = 184    // uc_mcontext.regs[0]
    ucMcontextSP     = 432    // uc_mcontext.sp
    ucMcontextPC     = 440    // uc_mcontext.pc
    ucMcontextPstate = 448    // uc_mcontext.pstate
    ucStack          = 16     // uc_stack offset
    ucSigmask        = 40     // uc_sigmask offset
)

// BuildSignalFrame builds a signal frame on the thread's gsignal stack
// and modifies the ThreadContext to enter sigtramp.
//
//go:nosplit
func BuildSignalFrame(thread *Thread, signum int, action *SignalAction) {
    // Determine signal stack to use
    signalSP := thread.SignalSP
    if signalSP == 0 {
        // No signal stack available — cannot deliver signal
        serial.RawUARTPuts("[signal] ERROR: no signal stack for TID=")
        serial.RawUARTHex64(uint64(thread.TID))
        serial.RawUARTPuts("\r\n")
        return
    }

    // Allocate space on signal stack (grows downward)
    frameSP := signalSP - uint64(arm64SignalFrameSize)
    frameSP &= ^uint64(0xF) // 16-byte align

    // Pointers to siginfo and ucontext within the frame
    siginfoAddr := frameSP
    uctxAddr := frameSP + arm64SiginfoSize

    // Zero the entire frame
    memclrNoHeapPointers(unsafe.Pointer(uintptr(frameSP)), uintptr(arm64SignalFrameSize))

    // --- Populate siginfo ---
    siPtr := uintptr(siginfoAddr)
    *(*int32)(unsafe.Pointer(siPtr))     = int32(signum)  // si_signo
    *(*int32)(unsafe.Pointer(siPtr + 4)) = 0              // si_errno
    *(*int32)(unsafe.Pointer(siPtr + 8)) = _SI_KERNEL     // si_code

    // --- Populate ucontext ---
    ucPtr := uintptr(uctxAddr)

    // Save all general-purpose registers from ThreadContext into ucontext
    for i := 0; i < 31; i++ {
        *(*uint64)(unsafe.Pointer(ucPtr + ucMcontextRegs + uintptr(i)*8)) = thread.Context.X[i]
    }
    *(*uint64)(unsafe.Pointer(ucPtr + ucMcontextSP))     = thread.Context.SP
    *(*uint64)(unsafe.Pointer(ucPtr + ucMcontextPC))     = thread.Context.ELR
    *(*uint64)(unsafe.Pointer(ucPtr + ucMcontextPstate)) = thread.Context.SPSR

    // uc_stack: record the signal stack info
    *(*uint64)(unsafe.Pointer(ucPtr + ucStack))     = thread.SignalStackBase  // ss_sp
    *(*int32)(unsafe.Pointer(ucPtr + ucStack + 8))  = 1                      // SS_ONSTACK
    *(*uint64)(unsafe.Pointer(ucPtr + ucStack + 16)) = thread.SignalStackSize // ss_size

    // --- Modify ThreadContext to enter sigtramp ---
    // R0 = signal number
    thread.Context.X[0] = uint64(signum)
    // R1 = pointer to siginfo
    thread.Context.X[1] = siginfoAddr
    // R2 = pointer to ucontext
    thread.Context.X[2] = uctxAddr
    // SP = signal stack (below the frame we just built)
    thread.Context.SP = frameSP
    // PC = sigtramp address (registered as sa_handler during initsig)
    // The sa_handler for SIGURG is actually runtime.sigtramp, not runtime.sighandler.
    // We get this from the signal action table.
    thread.Context.ELR = action.Handler
    // LR = sa_restorer (sigreturn trampoline)
    thread.Context.X[30] = action.Restorer
    // R28 (g register) should remain as-is — it's the interrupted goroutine's g
    // sigtrampgo will call sigFetchG(c) which reads g from ucontext.regs[28]
    // Both must be consistent.
    // R29 (FP) = 0 for clean frame
    thread.Context.X[29] = 0
    // SPSR remains unchanged (same EL, same interrupt state)
}
```

**CRITICAL DETAIL:** The `action.Handler` field contains the address of
`runtime.sigtramp` (NOT `runtime.sighandler`). This is because the Go runtime
calls `setsig(sig, abi.FuncPCABIInternal(sighandler))` but `setsig` internally
sets `sa.sa_handler = abi.FuncPCABI0(sigtramp)` on Linux. Wait — we need to
verify this.

**VERIFICATION NEEDED:** Read `bin/old.go.1.25.5/src/runtime/os_linux.go` and
find the `setsig` function to see whether it stores `sigtramp` or the passed
`fn` in `sa_handler`. If it stores `fn` (sighandler), then we need to replace
`action.Handler` with the address of `sigtramp`.

**Fallback:** If `setsig` stores `sighandler` (not sigtramp) in `sa_handler`,
then kmazarin needs to find the address of `sigtramp` some other way. Options:
1. Add a `RegisterSigtrampAddr` syscall (like `RegisterAsyncPreempt`)
2. Store `sigtramp` address during boot (via linkname or assembly helper)
3. Always use sigtramp address directly (known at link time)

The safest approach: add a global `var SigtrampAddr uint64` in `signal.go` and
set it from the runtime overlay during startup. In the overlay's `rt_sigaction`,
when the handler is first installed for any signal, capture the restorer and
handler addresses.

Actually, looking at the Go runtime source: `setsig` on Linux does:
```go
var sa sigactiont
sa.sa_sigaction = fn  // The fn passed to setsig
```
NOT `sigtramp`. But the Linux kernel delivers to `sa_sigaction`, which IS the
sigtramp address because `initsig` calls `setsig(i, abi.FuncPCABIInternal(sighandler))`.

Wait, that doesn't make sense — `fn` here would be `sighandler`, not `sigtramp`.

Let me re-read. The Go runtime on Linux has:
```go
//go:nosplit
//go:nowritebarrierrec
func setsig(i uint32, fn uintptr) {
    var sa sigactiont
    sa.sa_flags = _SA_SIGINFO | _SA_ONSTACK | _SA_RESTORER | _SA_RESTART
    sa.sa_handler = abi.FuncPCABI0(sigtramp)  // <-- ALWAYS sigtramp
    ...
}
```

So `setsig` IGNORES the `fn` parameter for `sa_handler` and ALWAYS uses
`sigtramp`. The `fn` parameter might be stored elsewhere for dispatching.

**CONCLUSION:** In the `sigactiont` struct that kmazarin receives via
`SyscallRtSigaction`, `sa_handler` will be the address of `runtime.sigtramp`.
This is exactly what we want — we store it and use it as the signal entry point.

### 5.4 BuildSignalFrame — x86_64

Create `kmazarin/kmazarin/signal_frame_amd64.go`:

x86_64 ucontext layout:
```
Offset  Field                     Size
0       uc_flags                  8
8       uc_link                   8
16      uc_stack.ss_sp            8
24      uc_stack.ss_flags         4
28      (pad)                     4
32      uc_stack.ss_size          8
40      uc_mcontext.gregs[0..22]  184  (23 × 8)
        gregs[0]=R8, [1]=R9, [2]=R10, [3]=R11, [4]=R12, [5]=R13,
        [6]=R14, [7]=R15, [8]=RDI, [9]=RSI, [10]=RBP, [11]=RBX,
        [12]=RDX, [13]=RAX, [14]=RCX, [15]=RSP, [16]=RIP,
        [17]=EFLAGS, [18]=CS, [19]=GS, [20]=FS, [21]=pad, [22]=ERR
224     uc_mcontext.fpregs        8  (pointer)
232     uc_mcontext.__reserved    64
296     uc_sigmask                128
424     __fpregs_mem              ~512
```

BUT — the Go runtime's `sigctxt.regs()` on x86_64 returns `(*sigcontext)`,
which has a DIFFERENT layout from `mcontext.gregs`:
```
sigcontext (x86_64):
  r8, r9, r10, r11, r12, r13, r14, r15   (offsets 0-56)
  rdi, rsi, rbp, rbx, rdx, rax, rcx      (offsets 64-112)
  rsp, rip                                (offsets 120-128)
  eflags, cs, gs, fs                      (offsets 136-142, uint16)
  ...
```

**The Go runtime on Linux x86_64 uses `sigcontext` via:**
```go
func (c *sigctxt) regs() *sigcontext {
    return (*sigcontext)(unsafe.Pointer(&(*ucontext)(c.ctxt).uc_mcontext))
}
```

So it casts `uc_mcontext` directly to `*sigcontext`. The `mcontext` struct
starts at offset 40 in `ucontext`. This means `sigcontext` is AT offset 40.

The `sigcontext` field offsets (from start of sigcontext/mcontext):
```
 0: r8      8: r9     16: r10    24: r11
32: r12    40: r13    48: r14    56: r15
64: rdi    72: rsi    80: rbp    88: rbx
96: rdx   104: rax   112: rcx
120: rsp  128: rip   136: eflags (uint64)
144: cs (uint16) 146: gs (uint16) 148: fs (uint16) 150: __pad0 (uint16)
152: err (uint64)  160: trapno (uint64)  168: oldmask (uint64)
176: cr2 (uint64)  184: fpstate (ptr)    192: __reserved1 (64 bytes)
```

```go
//go:build amd64

package main

import "unsafe"

const (
    amd64UcontextSize    = 936  // Approximate — enough for our needs
    amd64SiginfoSize     = 128
    amd64SignalFrameSize = amd64UcontextSize + amd64SiginfoSize + 16

    // sigcontext starts at uc_mcontext (offset 40 in ucontext)
    amd64SigctxBase = 40

    // Register offsets within sigcontext (from sigcontext start)
    scR8  = 0;  scR9  = 8;  scR10 = 16; scR11 = 24
    scR12 = 32; scR13 = 40; scR14 = 48; scR15 = 56
    scRDI = 64; scRSI = 72; scRBP = 80; scRBX = 88
    scRDX = 96; scRAX = 104; scRCX = 112
    scRSP = 120; scRIP = 128; scEFLAGS = 136
    scCS = 144; scGS = 146; scFS = 148
)

//go:nosplit
func BuildSignalFrame(thread *Thread, signum int, action *SignalAction) {
    signalSP := thread.SignalSP
    if signalSP == 0 {
        serial.RawUARTPuts("[signal] ERROR: no signal stack for TID=")
        serial.RawUARTHex64(uint64(thread.TID))
        serial.RawUARTPuts("\r\n")
        return
    }

    frameSP := signalSP - uint64(amd64SignalFrameSize)
    frameSP &= ^uint64(0xF)

    siginfoAddr := frameSP
    uctxAddr := frameSP + amd64SiginfoSize

    memclrNoHeapPointers(unsafe.Pointer(uintptr(frameSP)), uintptr(amd64SignalFrameSize))

    // Populate siginfo
    siPtr := uintptr(siginfoAddr)
    *(*int32)(unsafe.Pointer(siPtr))     = int32(signum)
    *(*int32)(unsafe.Pointer(siPtr + 8)) = _SI_KERNEL

    // Populate ucontext — save registers from ThreadContext into sigcontext
    scBase := uintptr(uctxAddr) + amd64SigctxBase

    *(*uint64)(unsafe.Pointer(scBase + scR8))  = thread.Context.R8
    *(*uint64)(unsafe.Pointer(scBase + scR9))  = thread.Context.R9
    *(*uint64)(unsafe.Pointer(scBase + scR10)) = thread.Context.R10
    *(*uint64)(unsafe.Pointer(scBase + scR11)) = thread.Context.R11
    *(*uint64)(unsafe.Pointer(scBase + scR12)) = thread.Context.R12
    *(*uint64)(unsafe.Pointer(scBase + scR13)) = thread.Context.R13
    *(*uint64)(unsafe.Pointer(scBase + scR14)) = thread.Context.R14  // g register
    *(*uint64)(unsafe.Pointer(scBase + scR15)) = thread.Context.R15
    *(*uint64)(unsafe.Pointer(scBase + scRDI)) = thread.Context.RDI
    *(*uint64)(unsafe.Pointer(scBase + scRSI)) = thread.Context.RSI
    *(*uint64)(unsafe.Pointer(scBase + scRBP)) = thread.Context.RBP
    *(*uint64)(unsafe.Pointer(scBase + scRBX)) = thread.Context.RBX
    *(*uint64)(unsafe.Pointer(scBase + scRDX)) = thread.Context.RDX
    *(*uint64)(unsafe.Pointer(scBase + scRAX)) = thread.Context.RAX
    *(*uint64)(unsafe.Pointer(scBase + scRCX)) = thread.Context.RCX
    *(*uint64)(unsafe.Pointer(scBase + scRSP)) = thread.Context.RSP
    *(*uint64)(unsafe.Pointer(scBase + scRIP)) = thread.Context.RIP
    *(*uint64)(unsafe.Pointer(scBase + scEFLAGS)) = thread.Context.RFLAGS
    *(*uint16)(unsafe.Pointer(scBase + scCS))  = uint16(thread.Context.CS)

    // Modify ThreadContext to enter sigtramp
    // x86_64 calling convention: DI=sig, SI=info, DX=ctx
    thread.Context.RDI = uint64(signum)
    thread.Context.RSI = siginfoAddr
    thread.Context.RDX = uctxAddr
    // Push restorer as return address on stack
    returnSP := frameSP - 8
    *(*uint64)(unsafe.Pointer(uintptr(returnSP))) = action.Restorer
    thread.Context.RSP = returnSP
    thread.Context.RIP = action.Handler
    // R14 (g) remains as-is
    // FSBase remains as-is
}
```

### 5.5 BuildSignalFrame — RISC-V

Create `kmazarin/kmazarin/signal_frame_riscv64.go`:

RISC-V ucontext layout:
```
Offset  Field                       Size
0       uc_flags                    8
8       uc_link                     8
16      uc_stack.ss_sp              8
24      uc_stack.ss_flags           4
28      uc_stack.ss_size            8 (note: no pad on RISC-V?)
36      uc_sigmask                  128  (usigset: 16 × uint64)
164     uc_x__unused                0
164     uc_pad_cgo_0                8
172     uc_mcontext.sc_regs.pc      8
180     uc_mcontext.sc_regs.ra      8
188     uc_mcontext.sc_regs.sp      8
196     uc_mcontext.sc_regs.gp      8
...     (32 registers × 8 = 256 bytes total for user_regs_struct)
428     uc_mcontext.sc_fpregs       528
```

**CAUTION:** These offsets need to be verified against the actual Go runtime
struct definitions. Read `defs_linux_riscv64.go` carefully and compute offsets
by hand or use `unsafe.Offsetof` in a test.

The pattern is the same: save ThreadContext registers into `user_regs_struct`,
set up siginfo, modify ThreadContext to enter sigtramp with (A0=sig, A1=info,
A2=ctx, RA=restorer).

**RISC-V specific:** The `g` register is X27 (s11), which maps to
`sc_regs.s11` in `user_regs_struct`. sigFetchG reads this.

---

## Stage 6: Implement rt_sigreturn

When the signal handler finishes and sigtramp returns, execution goes to the
sigreturn trampoline which issues `SYS_rt_sigreturn`. This syscall must
restore the (possibly modified) register context from the ucontext on the
signal stack.

**Files to modify:**
- `kmazarin/ksyscall/sysid.go` (ADD SysIDRtSigreturn)
- `kmazarin/ksyscall/dispatch.go` (ADD mapping)
- `kmazarin/ksyscall/rt_sigreturn.go` (NEW)
- `kmazarin/ksyscall/translate_arm64.go` (ADD number mapping)
- `kmazarin/ksyscall/translate_amd64.go` (ADD number mapping)
- `kmazarin/ksyscall/translate_riscv64.go` (ADD number mapping)
- `kmazarin/kmazarin/exceptions_arm64.s` (MODIFY — special handling)
- `kmazarin/kmazarin/exceptions_amd64.s` (MODIFY — special handling)
- `kmazarin/kmazarin/exceptions_riscv64.s` (MODIFY — special handling)

### 6.1 Add rt_sigreturn to Syscall Table

Add `SysIDRtSigreturn` to the SysID enum in `sysid.go`.

Add the syscall number mapping:
- ARM64: SYS_rt_sigreturn = 139
- x86_64: SYS_rt_sigreturn = 15
- RISC-V: SYS_rt_sigreturn = 139

Add `SysIDRtSigreturn: SyscallRtSigreturn` to the dispatch table.

### 6.2 Implement SyscallRtSigreturn

The key challenge: `rt_sigreturn` must restore ALL registers, not just return
a value in R0. The normal syscall return path only sets R0/RAX/A0 from the
return value.

**Approach:** `SyscallRtSigreturn` reads the ucontext from the signal stack,
copies register values back into the current Thread's Context, then signals
the exception handler to use the Thread's Context instead of the exception
frame for return.

Create `kmazarin/ksyscall/rt_sigreturn.go`:

```go
package ksyscall

// SyscallRtSigreturn restores register context from the signal frame's ucontext.
//
// This syscall does NOT return normally. It overwrites the current thread's
// saved register state with the (possibly modified) ucontext, then the
// exception handler restores that state instead of the exception frame.
//
// CRITICAL: After this function sets the SigreturnContext pointer, the
// assembly exception handler MUST check it and load registers from the
// Thread's Context rather than the exception frame.
//
//go:nosplit
func SyscallRtSigreturn(_, _, _, _, _, _ uint64) int64 {
    t := GetCurrentThread()
    if t == nil {
        return -22
    }

    // The ucontext is on the signal stack.
    // When we set up the signal frame, we stored the ucontext at a known location.
    // The signal handler may have modified the ucontext (pushCall changes PC).
    // We need to find the ucontext pointer.
    //
    // Strategy: The SP when rt_sigreturn is called points to somewhere on the
    // signal stack. The ucontext was placed at a known offset from the signal
    // frame base. However, sigtramp allocates its own stack frame, so SP has moved.
    //
    // Better approach: Store the ucontext address on the Thread when we set up
    // the signal frame. The signal handler doesn't move the ucontext.
    RestoreFromSignalFrame(t)

    t.InSignalHandler = 0

    // Signal to the assembly exception handler that it should use the Thread's
    // Context for ERET rather than the exception frame.
    SetSigreturnFlag(t)

    return 0  // Return value is irrelevant — registers are overwritten
}
```

**`RestoreFromSignalFrame`** is architecture-specific. It reads the ucontext
from the address saved on the Thread (add `SignalUctxAddr uint64` to Thread
struct in Stage 0) and copies registers back into `thread.Context`:

**ARM64:**
```go
//go:nosplit
func RestoreFromSignalFrame(t *Thread) {
    ucPtr := uintptr(t.SignalUctxAddr)
    if ucPtr == 0 {
        return
    }

    // Restore GPRs from ucontext.uc_mcontext.regs[0..30]
    for i := 0; i < 31; i++ {
        t.Context.X[i] = *(*uint64)(unsafe.Pointer(ucPtr + ucMcontextRegs + uintptr(i)*8))
    }
    t.Context.SP   = *(*uint64)(unsafe.Pointer(ucPtr + ucMcontextSP))
    t.Context.ELR  = *(*uint64)(unsafe.Pointer(ucPtr + ucMcontextPC))
    t.Context.SPSR = *(*uint64)(unsafe.Pointer(ucPtr + ucMcontextPstate))

    t.SignalUctxAddr = 0  // Consumed
}
```

### 6.3 Assembly Exception Handler Modification

After `DispatchSyscall` returns, the assembly exception handler normally
restores registers from the exception frame and does ERET. For rt_sigreturn,
we need it to instead load registers from the Thread's Context.

**Approach:** Add a per-thread flag (`SigreturnPending uint32`) that the
assembly checks after every syscall dispatch. If set:

1. Clear the flag
2. Load the Thread's Context pointer (same as DoContextSwitch does)
3. Restore ALL registers from the Thread's Context
4. ERET

This reuses existing infrastructure — the `load_context_and_eret` path that
`YieldToReadyThread` and `DoContextSwitch` already use.

**ARM64 exception handler modification** (in `exceptions_arm64.s`, in the SVC
handler after calling DispatchSyscall):

```asm
    // After DispatchSyscall returns in R0:
    // Check if this was rt_sigreturn (context override needed)
    MOVD    $main·currentThread(SB), R1     // or however you get current Thread
    MOVD    (R1), R1                        // R1 = current Thread ptr
    MOVW    <SigreturnPending_offset>(R1), R2
    CBNZ    R2, sigreturn_restore

    // Normal path: store return value and restore from exception frame
    MOVD    R0, 0(RSP)          // Store return value in X0 slot
    B       exception_return    // Normal ERET from exception frame

sigreturn_restore:
    // Clear the flag
    MOVW    $0, <SigreturnPending_offset>(R1)
    // Load context pointer
    ADD     $<Context_offset>, R1, R1   // R1 = &thread.Context
    // Jump to context restore and ERET (reuse existing code)
    B       load_context_and_eret
```

The exact offsets and labels depend on the current assembly structure. The
key insight is that we can reuse the same `load_context_and_eret` path that
`YieldToReadyThread` already uses — it loads all GPRs from a ThreadContext
and does ERET.

**x86_64 and RISC-V** need equivalent modifications in their exception handlers.

---

## Stage 7: Thread Fields Summary

Here is the complete list of fields to add to the Thread struct:

```go
    // Signal delivery state (add after SyscallCloneTLS)
    PendingSignals    uint64  // Bitmask of pending signals (bit N = signal N+1)
    SignalSP          uint64  // gsignal stack top (stack grows down from here)
    SignalStackBase   uint64  // gsignal stack bottom
    SignalStackSize   uint64  // gsignal stack size in bytes
    SignalUctxAddr    uint64  // Address of ucontext in current signal frame
    InSignalHandler   uint32  // 1 = executing signal handler, 0 = normal
    SigreturnPending  uint32  // 1 = rt_sigreturn called, load Context for ERET
```

---

## Stage 8: Testing

### 8.1 Basic Signal Path Test

1. Build and boot ARM64 with TIMEOUT=10
2. Check serial log for `[sigaction]` messages — verify SIGURG handler registered
3. Verify boot completes normally (signal infrastructure doesn't break anything)

### 8.2 GC STW Test

1. Run ARM64 with TIMEOUT=60 and GOGC=5
2. Monitor for mspan corruption crashes
3. The key metric: does the 60-second run survive? Previously it crashed within
   ~30-40 seconds.

### 8.3 Cross-Architecture

1. Run x86_64 with TIMEOUT=60
2. Run RISC-V with TIMEOUT=60
3. Both should boot and run without crashes

### 8.4 Debug Output

Add optional debug output (controlled by a flag) in signal delivery:
```
[signal] deliver SIGURG to TID=2 handler=0xFFFFFFFF41823456
[signal] built frame: siginfo=0x..., uctx=0x..., SP=0x...
[signal] sigreturn from TID=2 PC=0x... → 0x...
```

---

## Implementation Order

1. **Stage 0** — Signal state infrastructure (can be done first, no behavior change)
2. **Stage 1** — rt_sigaction (requires overlay changes, verify at boot)
3. **Stage 2** — sigaltstack (capture gsignal stack bounds)
4. **Stage 3** — tgkill + gettid fix (pending signals are set but not yet delivered)
5. **Stage 4** — Restore sigtramp and sigreturn trampoline (overlay changes)
6. **Stage 5** — Signal delivery in context switch (the main event)
7. **Stage 6** — rt_sigreturn (complete the round trip)
8. **Stage 7** — Testing on all architectures

**Each stage should be tested incrementally.** Stages 0-3 can be tested by
verifying boot doesn't break. Stage 4-6 is where signals actually start
flowing. Stage 7 validates the fix.

---

## Risk Mitigations

### R1: ucontext Layout Mismatch
The Go runtime's `sigctxt` accessor functions use exact offsets into the
ucontext struct. If our layout doesn't match, the runtime reads/writes garbage.

**Mitigation:** Write a test that computes offsets using `unsafe.Offsetof` on
the actual Go runtime types (via linkname), and verify they match our constants.

### R2: Signal During Critical Section
Delivering a signal while a goroutine holds runtime locks (m.locks > 0) could
cause reentrancy issues.

**Mitigation:** Check `m.locks` in `DeliverPendingSignal` (same check we
already do for thread preemption). If locks are held, defer signal delivery
to next schedule.

### R3: Stack Overflow on Signal Stack
The signal frame is ~5KB on ARM64. The gsignal stack is 32KB. If multiple
signals are delivered before the first completes, the stack could overflow.

**Mitigation:** The `InSignalHandler` flag prevents nested signal delivery.
Only one signal frame exists at a time.

### R4: sigtramp Needs Correct g Register
sigtrampgo calls `sigFetchG(c)` which reads g from the ucontext. If we don't
save the correct g in the ucontext, the runtime operates on the wrong goroutine.

**Mitigation:** We save the thread's g register (from ThreadContext) into the
ucontext's register array at the correct offset (R28 for ARM64, R14 for x86_64,
X27/s11 for RISC-V).

### R5: Bootstrap Thread M.procid
The gettid overlay currently returns 1 for all threads, including bootstrap.
This means Thread 0's M.procid = 1, but Thread 0's TID might be 0.

**Mitigation:** Fixed in Stage 3 — change gettid overlay to issue a real syscall.

---

## Files Changed Summary

### New Files
- `kmazarin/kmazarin/signal.go` — Signal constants, action table, delivery logic
- `kmazarin/kmazarin/signal_frame_arm64.go` — ARM64 signal frame builder
- `kmazarin/kmazarin/signal_frame_amd64.go` — x86_64 signal frame builder
- `kmazarin/kmazarin/signal_frame_riscv64.go` — RISC-V signal frame builder
- `kmazarin/ksyscall/rt_sigreturn.go` — rt_sigreturn syscall implementation

### Modified Files
- `kmazarin/kmazarin/threads.go` — Add signal fields to Thread struct + delivery checks
- `kmazarin/ksyscall/stubs.go` — Replace rt_sigaction, sigaltstack, tgkill stubs
- `kmazarin/ksyscall/sysid.go` — Add SysIDRtSigreturn
- `kmazarin/ksyscall/dispatch.go` — Add rt_sigreturn to dispatch table
- `kmazarin/ksyscall/translate_*.go` — Add rt_sigreturn number mapping
- `kmazarin/kirq/preempt.go` — Add MGsignalOffset
- `runtime-patches/preempt.go` — Add MGsignalOffset to PreemptOffsets
- `runtime-patches/sys_linux_arm64.s` — Real rt_sigaction, sigaltstack, tgkill, gettid, sigtramp, sigreturn
- `runtime-patches/sys_linux_amd64.s` — Same for x86_64
- `runtime-patches/sys_linux_riscv64.s` — Same for RISC-V
- `kmazarin/kmazarin/exceptions_arm64.s` — rt_sigreturn context restore
- `kmazarin/kmazarin/exceptions_amd64.s` — rt_sigreturn context restore
- `kmazarin/kmazarin/exceptions_riscv64.s` — rt_sigreturn context restore
