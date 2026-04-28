# Continuation prompt — `fix/concurrent-boot-wedge`

## Where we are

Branch `fix/concurrent-boot-wedge`, freshly forked off `fix/uring-missed-retries@5352357` (which just absorbed the 8-commit `diag/mail-elf-load-hang` work via fast-forward merge). Working tree clean.

## What's done (in the merged history this branch builds on)

- **Linux dispatcher concurrency** (`f466010`, `37f1956`, `ef449b5`): file lane uses a 1024-worker pool. Per-shepherd ordering via `ShepherdFilesystemData.mu`. `pageCache` / `flockTable` / `syscallHandler` have appropriate locks. Worker pool prevents goroutine-churn that crashed the .maz runtime unwinder.
- **Shepherd binary unification** (`e9247bc`, `aabdfa2`): all startup.toml shepherds (fti / maildb / mail) are `.maz` plugins via `/shepherd.elf`. Disk image no longer ships fti.elf / maildb.elf / mail.elf. Dual-build pattern dropped. `launchShepherd` legacy ET_EXEC body deleted.
- **Launch-path checkpoint instrumentation** in `kmazarin/ksyscall/runshepherd.go` and `maz/fs/main.go` — supports the original DIVERSION's reproduction-attempts; doesn't fire on the happy path.

## The bug we're fixing

Concurrent-boot-wedge — see `task_plan.md` TOP OF STACK for full description. Short form:

- ~1-in-5 of 180 s ARM64 HVF boots, three shepherds get stuck on their per-shepherd `shep.mu` for 60–110 s.
- The lock holder is parked in `fsclient.callLocked` waiting on a fs reply. Same-shepherd syscalls queue behind.
- NOT mail-related — wedged shepherds are whoever's doing fs IPC during the busy fs window. H5 had sid=20/28/29 (rachel-plugin chain).
- Worker pool keeps the system alive, but specific shepherds hang indefinitely.

## Plan (per task_plan.md)

1. **Add a `fsclient.callLocked` timeout** (~30 s) to convert indefinite stalls into recoverable errors. Safety net + telemetry handle. **Architectural — discuss with user before adding** (timeouts are a user-policy boundary).
2. **Per-message-class instrumentation in fs's serve loop** at `maz/fs/main.go:232`. Log when fs picks `fsIPCCh` vs `fsDelegateCh` and the request type. Confirms hypothesis 1: are LoadFile delegate ops starving IPC?
3. **Reproduce H5 deterministically.** Heavy concurrent boot LoadFiles trigger it. Add extra shepherds to startup.toml or parallelize bootSequence to make it ~100% reliable. Without a reliable reproducer we can't measure.

## Hypotheses (priority order)

1. **fs single-goroutine serve loop blocked on a slow LoadFile.** While fs is mid-`file.ReadInto` for a 23 MB file, no `fsIPCCh` requests are processed. Linux's worker parked in `fsclient.callLocked` waits indefinitely.
2. **Reply was sent but linux's `RespCh` demuxer dropped it.** `fsclient.Client.RespCh` capacity 4. Multiple replies arriving fast may EAGAIN at uring layer; possibly lost without retry. Audit fs reply path.
3. **Cleanup-on-disconnect race.** Less likely.

## Setup

- Required env: `GOTOOLCHAIN=auto GO=/opt/homebrew/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64`.
- Build: `$GO tool task`. Run: `$GO tool task run-arm64-hvf TIMEOUT=N`. Log: `$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log`.
- Always `run-arm64-hvf`, never `run-arm64`.

## Reminders

- Don't roll back the worker pool — it's what keeps the wedge from cascading to system death.
- Wedge symptom is `delegate stuck: tid=X/sid=Y/sysid=50/for=N+ms` (sysid=50 is `Readlinkat`, the most common queued syscall).
- Original DIVERSION mail.elf-load hang is separate, deferred, and hasn't fired under phase 2.
- `bug_attr_init_crash.md` is a separate constraint-VM bug; not this work.

## Side context (don't re-litigate)

- Worker pool is 1024 persistent workers reading from `fileLaneWorkItem` channel. Don't replace with `go handler.handle(req)` per request — that crashed under churn.
- Don't reach into `pc.data` etc. directly — go through public methods. The G6 SIGSEGV taught us that lesson.
- fs's serve loop staying single-goroutine is acceptable per user — slow not deadlocking on its own. The wedge is when LINUX waits on it, not when fs waits on something else.
