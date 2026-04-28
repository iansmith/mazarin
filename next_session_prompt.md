# Continuation prompt — pivoting to website updates

## Where we are

The mazzy systems-debugging arc is paused for a session. `fix/concurrent-boot-wedge` partial-fix landed, TEMP-DIAGNOSTIC items stripped per the MANDATORY EXIT CRITERION, and the branch has been merged into `fix/uring-missed-retries` and `master`. The user is pivoting to website updates.

## Most recent work (still in mazzy, fully merged into master)

- **Phase 2 + worker pool** in linux's syscall dispatcher (concurrent per-request handling).
- **Shepherd binary unification** — fti, maildb, mail are all `.maz` plugins; `launchShepherd` legacy ET_EXEC body deleted.
- **fs.respond() EAGAIN retry** — fixes a silent reply-drop that became reachable after `fix/uring-missed-retries`.
- **fsclient.Client.RespCh cap 4 → 1** — defensive simplification.

## Open from the systems work (resumable)

- **Concurrent-boot-wedge persists.** L-sweep showed 3/5 wedges at the historical rate even after the EAGAIN-retry fix. The audit was incomplete; the wedge has at least one other failure mode upstream of fsclient. See `task_plan.md` ARCHIVED section under `fix/concurrent-boot-wedge` for the next concrete step (add worker-entry/exit instrumentation in `handler.handle` to locate where wedged requests park).
- **Original DIVERSION mail.elf-load hang** — has not fired under phase 2; instrumentation remains in tree at `kmazarin/ksyscall/runshepherd.go` and `maz/fs/main.go` for the next time it does.
- **`bug_attr_init_crash` family** — pre-existing constraint-VM bug (mail-app `attr.ValueToFlat: unsupported vm type 0`); unrelated to systems work but seen on/off.

## Website updates context

(User pivoting here — concrete tasks not yet specified. Likely related to project documentation, the public-facing site for mazzy, or related.)

## Setup

If returning to mazzy systems work:
- Required env: `GOTOOLCHAIN=auto GO=/opt/homebrew/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64`.
- Build: `$GO tool task`. Run: `$GO tool task run-arm64-hvf TIMEOUT=N`. Log: `$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log`.
- Always `run-arm64-hvf`, never `run-arm64`.

## Reminders carried forward

- Don't roll back phase 2's worker pool or shepherd unification.
- `bug_attr_init_crash.md` exit_group with no panic visible is the same intermittent. Don't chase mid-other-work.
