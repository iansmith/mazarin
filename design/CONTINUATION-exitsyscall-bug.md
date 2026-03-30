# Continuation Prompt: exitsyscall P-reacquisition Bug

## Resume this task

Continue investigating and fixing the bug where Go's `runtime.exitsyscall()` permanently parks an M in the fs shepherd, preventing block I/O completion and blocking the clocks shepherd's timezone loading.

## Bug Summary

The clocks shepherd calls `time.LoadLocation("America/New_York")` → Go runtime opens the zoneinfo file via the linux shepherd → IPC to fs shepherd → fs reads ext2 blocks via async block I/O (BlockSubmit + WaitSoftIRQ). The **first ReadBlock during this IPC request** completes the I/O in the kernel but the userspace goroutine never resumes.

## Root Cause Identified (narrowed to exact failure point)

The failure sequence, confirmed via breadcrumbs in the serial log:

```
r1Br2SbIWwSDXf
```

- `r1` — ReadBlock entry (flock/cmd/fs/main.go:467)
- `B` — BlockSubmit OK (kernel, block_submit.go:146)
- `r2` — about to call WaitSoftIRQ via Syscall6 (fs/main.go:476)
- `S` — WaitSoftIRQ kernel entry, ring empty (softirq.go:64)
- `b` — BlockOnSlot, thread blocks (softirq.go:91)
- `I` — block IRQ fires, NonTimerIRQTopHalf (bottom_half.go:281)
- `W` — WakeSlotForIRQ wakes thread, SVC rewound (soft_irq_slots.go:169)
- `w` — futex WAKE woke a thread (somewhere in the system, futex.go:140)
- `S` — WaitSoftIRQ re-executes after wake (softirq.go:64)
- `D` — drain events successful (softirq.go:69)
- `X` — SVC returned to userspace, about to call exitsyscall (syscall_linux.go:102)
- `f` — **exitsyscall called stopm → futexsleep → FUTEX_WAIT, M parked** (futex.go:126)
- **No `x` ever appears** — exitsyscall never completes
- **No `r3` ever appears** — ReadBlock never returns

The M is permanently parked on a futex. Nobody ever wakes it.

## What We Know

1. **RawSyscall6 works**: When WaitSoftIRQ bypasses entersyscall/exitsyscall (uses RawSyscall6 directly), both reads complete: `r1Br2SbIWSDr3r1Br2SbIWSDr3`. Confirmed.

2. **Pre-SVC breadcrumbs cause Heisenbug**: Adding diagBreadcrumb SVCs before entersyscall causes extra HVF VM exits that inject the block IRQ sooner. WaitSoftIRQ never blocks → exitsyscall gets P back immediately → works. This is why adding `e` and `E` breadcrumbs "fixed" the bug.

3. **The problem only occurs when WaitSoftIRQ actually blocks**: When the SVC blocks via BlockOnSlot + SetSyscallSwitchTarget (context switch away), and then the thread is woken and returns, exitsyscall fails to reacquire a P and parks permanently.

4. **Futex implementation is clean**: Audited the kernel futex — matches Linux semantics, no implicit timeouts, proper atomic check-and-block, correct wake behavior.

5. **GOMAXPROCS=1 in shepherds**: Only one P. When entersyscall releases it, sysmon's retake/handoffp puts it on the idle list if no work. When exitsyscall runs, it should get the idle P back via `exitsyscallfast_pidle`. But instead it goes to `exitsyscall0` → `stopm` → futexsleep.

## The Key Question

**Why can't exitsyscall reacquire the P?** With GOMAXPROCS=1, when the goroutine was in a blocking syscall:
- `entersyscall()` released the P (set status _Psyscall)
- sysmon's `retake()` calls `handoffp()` → puts P on idle list (_Pidle)
- When the SVC returns, `exitsyscall()` should find the idle P via `pidleget()`

But it doesn't. Instead it parks the M via stopm/futexsleep. Possible reasons:
- Another M grabbed the P first (race)
- P is in GC stop-the-world state (_Pgcstop)
- P was handed to a different M by sysmon
- The P status is in an unexpected state
- sysmon is running on the only available P

## Hypothesis to Test

The most likely cause: **sysmon or GC stole the P and is currently using it** when exitsyscall runs. With GOGC=5 (aggressive GC), the GC frequently needs all Ps. If a GC STW cycle starts while the thread is blocked, the P transitions _Pidle → _Pgcstop. When exitsyscall runs during STW, no P is available, and the M parks forever because STW completion logic doesn't know about this parked M.

## Current State of the Code

### Diagnostic breadcrumbs in place (all marked TODO: DIAGNOSTIC)

1. **Userspace overlay** `mazarin/overlay/userspace/syscall_linux.go` lines 94-108:
   - `Syscall6` has post-SVC breadcrumbs only (pre-SVC ones removed because Heisenbug)
   - `diagBreadcrumb('X')` after SVC returns, before exitsyscall
   - `diagBreadcrumb('x')` after exitsyscall returns
   - Only fires for SysWaitSoftIRQ (0x100A) slot 0 (block device)
   - Assembly helper `diagBreadcrumb` in `mazarin/overlay/userspace/asm_linux_arm64.s`

2. **Kernel futex handler** `kmazarin/ksyscall/futex.go`:
   - `serial.PollWrite('f')` on FUTEX_WAIT that blocks a thread (line 126)
   - `serial.PollWrite('w')` on FUTEX_WAKE that wakes at least one thread (line 141)
   - Added `import "mazzy/kmazarin/serial"` (line 6)

3. **fs shepherd** `flock/cmd/fs/main.go`:
   - ReadBlock has `r1`, `r2`, `r3`, `r!` breadcrumbs (lines 467, 476, 485, 472/481)
   - `waitReads` and `ReadBlock` both use `sys.WaitSoftIRQ` (Syscall6 path, not RawSyscall6)
   - `"mazzy/shared/mazzy"` import was removed (no longer needed)

4. **Kernel overlay comments** `runtime-patches/syscall/syscall_linux.go`:
   - Stale "Go function calls" comments were updated to clarify this is the kernel overlay only

### Files to understand

- `mazarin/overlay/userspace/syscall_linux.go` — shepherd's Syscall6 with entersyscall/exitsyscall
- `mazarin/overlay/userspace/asm_linux_arm64.s` — diagBreadcrumb assembly, defaultSyscallHandler
- `mazarin/overlay/userspace/runtime/sys_linux_arm64.s` — shepherd runtime (futex, clone, usleep, etc.)
- `kmazarin/ksyscall/futex.go` — kernel futex handler
- `kmazarin/ksyscall/softirq.go` — WaitSoftIRQ kernel handler
- `kmazarin/kmazarin/soft_irq_slots.go` — BlockOnSlot, WakeSlotForIRQ
- `kmazarin/kmazarin/threads.go` — ThreadBlockFutex, ThreadWakeFutex, SetSyscallSwitchTarget
- `flock/cmd/fs/main.go` — fs shepherd ReadBlock, waitReads
- `shared/mazzy/mazzy.go` — syscall number definitions

### Tasks

- Task #6 (pending): REVERT — restore WaitSoftIRQ to use Syscall after diagnostic (currently using Syscall, breadcrumbs active)
- Task #7 (pending): Fix root cause — exitsyscall P reacquisition failure

### Build and run

```bash
export GOTOOLCHAIN=auto GO=/Users/iansmith/sdk/go1.25.5/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
$GO tool task run-arm64-hvf TIMEOUT=20
$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log
```

## Suggested Next Steps

1. **Add breadcrumbs inside exitsyscall** to distinguish WHY the P can't be acquired:
   - Is `exitsyscallfast` failing because P status != _Psyscall? (sysmon already took it)
   - Is `exitsyscallfast_pidle` failing because no idle P? (someone else has it)
   - Is the goroutine going to `exitsyscall0` and `stopm`?
   - What is the P status when exitsyscall runs?

2. **Check if GC STW is the culprit**: The `[E]` stats show `GC=4:25` or similar. With GOGC=5, GC runs very frequently. Check if the goroutine parks during a GC cycle.

3. **Check sysmon behavior**: sysmon runs on its own M/thread. When it steals the P via retake→handoffp, what does it do with it? If sysmon is still running when exitsyscall tries to get the P back, the P isn't idle.

4. **Consider the wake/schedule race**: After WakeSlotForIRQ rewinds the SVC and the thread resumes, the thread re-enters the kernel (SVC re-executes), drains, and returns. The SVC return goes through the kernel's exception return path. If sysmon steals the P during this narrow window (between SVC return and exitsyscall's CAS on P status), the M parks. With only one P and aggressive GC, this race could be common.

## User Directives (MUST follow)

- **Show changes to syscalls/entersyscall/exitsyscall to user for approval first**
- **No polling or timeouts without discussion** — these are architectural changes
- **NEVER disable async preemption or GC**
- **Always use run-arm64-hvf** (TCG is 100x slower)
- **Use $GO tool safe-serial-read** for serial logs (never cat/Read directly)
