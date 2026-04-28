# Continuation prompt — `fix/uring-missed-retries` step 3+ (2026-04-28 night)

## What this branch is fixing

Synchronous UART writes (`klog.Criticalf`, `serial.PollWrite`, `.maz` `rawPuts`) run inside ARM64 SVC handlers with `DAIF.I` masked (hardware default, kernel doesn't unmask). On `-smp 1` QEMU each ~80-byte sync write IRQ-blocks the whole system for ~7 ms — that starves the linux shepherd's ring drainers and causes `OpenFontReply EAGAIN` to fontsvc. See `memory/sync_uart_irq_masked.md`.

Architecture for the fix (agreed, not negotiable):
- Kernel-side block-with-deadline for both rings.
- First-come-first-served, single blocker per ring/slot. Second sender gets immediate `-EAGAIN`.
- Userspace pacing: 3-attempt retry with `nanosleep` (NOT `runtime.Gosched`) bounded at ~30 ms total wall time.
- `Send`/`Recv`/`Connect` are one-line ring-0 wrappers; `*WithRing` is the primitive.
- **No yields.** Per user policy: "yields cover up bugs that will bite later."

## Where you left off

Branch `fix/uring-missed-retries`, off `feature/mail-dumb@68a7254`. Steps 1-2 done (uncommitted), tree builds clean.

**Done:**
1. New thread states (`ThreadBlockedUringSend` = 19, `ThreadBlockedKernelRingPush` = 20). New per-Thread fields `UringSendBlockedSlotPtr`, `UringSendDeadlineExpired`. New per-slot fields `BlockedSenderTID`, `BlockedSenderPtr`. New globals `kernelRingPushBlockerTID`, `softIRQDroppedBytes`.
2. `UringSendKernel` parks userspace senders on `ThreadBlockedUringSend` with 10 ms deadline; `WakeSenderAfterDrain` wakes from drain side; `processStaticDeadlinesSchedLockHeld` handles deadline expiry; `CleanupUringIPCForShepherd` wakes parked senders on receiver death. Linkname stub `wakeSenderAfterDrain` added; `SyscallUringRecv` calls it after `advanceUringHead`.

**Files touched (uncommitted):**
- `kmazarin/kmazarin/threads.go`
- `kmazarin/kmazarin/uring_ipc.go`
- `kmazarin/kmazarin/serial_console.go`
- `kmazarin/ksyscall/uring_ipc.go`
- `kmazarin/ksyscall/uring_ipc_asm.go`

## Remaining steps (verbatim from `progress.md`)

3. **Same pattern for `pushStringFull` + `topHalfUartRing` consumer.** Park thread 0 with `KernelBlockSleep`-style block when `topHalfUartRing` is full. Wake hook in `SyscallWaitSoftIRQ`'s pop path. Drop-and-counter (`softIRQDroppedBytes`) on deadline expiry. Surface counter on `[status]` line.

4. **Userspace rewrite of `mazarin/uring/syscall.go`.** Remove `SendWithStats`. `SendWithRing` becomes the primitive: 3-attempt retry with `nanosleep` pacing on fast-EAGAIN (elapsed < ~10 ms) + single log line on retry-success or final failure. `Send` is `return SendWithRing(target, msg, 0)`. Verify `Recv`/`RecvWithRing` and `Connect`/`ConnectWithRing` already follow the convention (they do, per check this session — leave alone).

5. **Revert vestigial fontsvc instrumentation in `maz/fontsvc/main.go`.** Drop `OpenFontReply retried`/`FAILED ... attempts=N` logs (no longer meaningful), drop `itoaBytes` helper, revert `shareCacheAndReply`'s family/variant/size parameters. Just call `uring.Send`.

6. **Build clean. Smoke run.**

7. **F16-F20 (5×180s ARM64 HVF, no clicks).** Compare against F6-F15 baseline (0 EAGAIN). With pacing, even fast-EAGAIN bouncing should now consume real time, so any retries that DO happen will show up as ~10-30 ms wall-time entries in the log. Watch for `[uring.Send] ... retried` or `... FAILED after 3 attempts`.

## Project setup (always)

- Required env: `GOTOOLCHAIN=auto GO=/opt/homebrew/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64`.
- Build: `$GO tool task`. Run: `$GO tool task run-arm64-hvf TIMEOUT=N`. Log: `$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log`.
- Always `run-arm64-hvf`, never `run-arm64`.
- Never use `go build` directly — Taskfile only.

## Reminders / non-negotiables

- **No `runtime.Gosched()` anywhere in this fix.** Use `KernelBlockSleep` (kernel-side, on deadline queue) or `nanosleep` (userspace, on deadline queue). Both are real off-CPU primitives, not yields.
- The `Gosched()` removed in step 3 is the only one in kernel code; verified via grep at session start.
- Per-call sync UART (`klog.Criticalf`, `rawPuts`) is reserved for "about to die" only — not debug logging.
- Don't commit kernel changes until user reviews the diff. Tracking-file commits (progress, task_plan, this prompt) may go on top.

## Verification of step 2 before extending

Three things to re-check when resuming:
- The race-free publish in `UringSendKernel` (re-check head/tail under schedulerLock before publishing blocked state). Recursive `UringSendKernel(...)` call when drainer drained between first check and publish. **Reasoned correct, not yet observed at runtime.**
- `findNextThreadForBlockSchedLockHeld(senderThread)` returning nil → fallback to immediate EAGAIN. Matches futex precedent but worth verifying the path is reached cleanly.
- `wakeBlockedSenderSchedLockHeld` is called from `WakeSenderAfterDrain`, `processStaticDeadlinesSchedLockHeld` (for `ThreadBlockedUringSend`), and `CleanupUringIPCForShepherd`. Each call site holds `schedulerLock`. Verified.

## Side context (don't re-litigate)

- F1-F15 ran on `feature/mail-dumb`. VA-collision at SharePages layer is strongly disconfirmed (~1850 SharePages, 0 out-of-range). GC mspan crash didn't reproduce in 15 boots. See `memory/sync_uart_irq_masked.md` and progress.md.
- Bug-B-family chase is paused, not abandoned. The current fix removes a known timing perturbation (sync-UART-induced starvation), so a clean F16-F20 baseline is itself useful data.
- `clocks_stdio_delegate_stall.md`, `bug_attr_init_crash.md`, `bug_virtio_emptyirq_hang.md` are unrelated and unaffected.
