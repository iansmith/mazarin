# Continuation prompt — `fix/uring-missed-retries` step 7 (F16-F20 + commit)

## What this branch is fixing

Sync UART writes (`klog.Criticalf`, `serial.PollWrite`, `.maz` `rawPuts`) run inside ARM64 SVC handlers with `DAIF.I` masked. On `-smp 1` QEMU each ~80-byte sync write IRQ-blocks the system for ~7 ms, starves the linux shepherd ring drainers, and causes the `OpenFontReply EAGAIN` to fontsvc that F2 caught. See `memory/sync_uart_irq_masked.md`.

Architecture (agreed, not negotiable):
- Kernel-side block-with-deadline for both rings (10 ms per attempt, FCFS single blocker).
- Userspace pacing: 3-attempt retry with `time.Sleep` (NOT `runtime.Gosched`) bounded at ~30 ms total wall time.
- `Send`/`Recv`/`Connect` are one-line ring-0 wrappers; `*WithRing` is the primitive.
- **No yields anywhere in this fix.** "Yields cover up bugs that will bite later." Both kernel-side and userspace-side are Gosched-free.

## Where you left off

Branch `fix/uring-missed-retries`, off `feature/mail-dumb@68a7254`. **Steps 1-6 done, uncommitted.** 180 s ARM64 HVF smoke clean: `uart-ring: dropped=0`, no `[uring.Send] retried/FAILED`, no fontsvc errors, no kernel panic.

Per-step state lives in `progress.md` and `task_plan.md`. The user has not yet reviewed the diff, so **don't commit kernel/userspace changes** at the start of the session — wait for review/approval. Tracking-file commits (progress, task_plan, this prompt) may go on top.

## Step 7 — F16-F20 (5 × 180 s ARM64 HVF, no clicks)

**Goal:** confirm the kernel + userspace pacing combination is stable across 5 boots and surface any retry / failure log lines that didn't appear in the single step-6 smoke.

### Concrete plan

1. Run 5 × 180 s ARM64 HVF, no clicks. Save serial logs at `/tmp/F1{6..20}-180s.log`.
2. For each run, post-hoc check via `$GO tool safe-serial-read /tmp/F1N-180s.log | grep -E '...'`:
   - `[uring.Send] ... retried, attempts=N` — pacing kicked in but the send eventually succeeded. Expected to be rare; benign.
   - `[uring.Send] ... FAILED after 3 attempts, err=...` — exhausted retry budget. Real problem; investigate sender / target.
   - `[fontsvc] uring.Send OpenFontReply FAILED:` — propagates from above; should match the FAILED line.
   - `uart-ring: dropped=N` on the `[status]` line — kernel-side pushString backpressure. Must stay 0.
   - Any kernel `EE25` / `KERNEL EXIT GROUP` / panic / data abort.
   - Any userspace panic.
3. Summarize results in a F16-F20 table in `progress.md` like F1-F15 entries.

### Decision tree

- **All 5 clean (0 retries, 0 FAILED, 0 dropped, no panic):** the fix is verified at the F-series cadence. Record results, ask user for go-ahead to commit steps 1-6 (kernel + userspace + tracking).
- **Some retries fire but 0 FAILED:** pacing is doing its job. Record the senders/targets that retried (often a hint about which path is bursting). Still safe to commit.
- **Any FAILED line:** the 30 ms budget is too short for that path, OR there's a real ring-drain hang. Investigate before committing — don't paper over with a longer budget.
- **Any kernel-side `dropped > 0` or panic:** kernel-side regression. Step 3 (`pushStringFull` block-with-deadline) needs another look. Don't commit.
- **Any fti bleve panic recurrence (see `findings.md`):** independent userspace bug. Note in findings, do NOT chase from this branch.

## After step 7 (assuming clean)

8. **Commit prep.** Two logical commits at minimum:
   - Kernel: steps 1-3 (`kmazarin/kmazarin/threads.go`, `uring_ipc.go`, `serial_console.go`, `soft_irq_slots.go`, `kmazarin/ksyscall/uring_ipc.go`, `uring_ipc_asm.go`).
   - Userspace: steps 4-5 (`mazarin/uring/syscall.go`, `maz/fontsvc/main.go`).
   - Tracking: `findings.md`, `progress.md`, `task_plan.md`, `next_session_prompt.md` — separate commit.
   - Show the user the diff before each commit; do not push.

## Project setup (always)

- Required env: `GOTOOLCHAIN=auto GO=/opt/homebrew/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64`.
- Build: `$GO tool task`. Run: `$GO tool task run-arm64-hvf TIMEOUT=N`. Log: `$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log`.
- Always `run-arm64-hvf`, never `run-arm64`.
- Never `go build` directly — Taskfile only.

## Reminders / non-negotiables

- **No `runtime.Gosched()` anywhere in this fix.** Both block paths use staticDeadlineQueue.
- Per-call sync UART (`klog.Criticalf`, `rawPuts`) is reserved for "about to die" only — not debug logging.
- Don't commit kernel/userspace changes until user reviews the diff.

## Side context (don't re-litigate)

- Step 2 verification done (race-free publish, fallback paths, lock discipline; caveat: drainer fast-path read of `slot.BlockedSenderTID` is non-atomic, 10 ms deadline is the safety net for missed-wake — acceptable).
- Step 3 verified by 90 s smoke (kernel stable, `dropped=0`).
- Step 6 verified by 180 s smoke (kernel stable, `dropped=0`, no retries surfaced — idle run).
- Bug-B-family chase paused. F1-F15 disconfirmed VA-collision at SharePages layer (~1850 calls, 0 out-of-range). GC mspan didn't reproduce in 15 boots.
- `findings.md` top section: fti bleve `index out of range [0]` panic surfaced in step-3 smoke. Independent of this branch. Revisit after F16-F20 if it persists.
- `clocks_stdio_delegate_stall.md`, `bug_attr_init_crash.md`, `bug_virtio_emptyirq_hang.md` unrelated and unaffected.
