# Continuation prompt — `diag/mail-elf-load-hang` (post-phase-2)

## Where we are

Branch `diag/mail-elf-load-hang`, 1 commit ahead of `fix/uring-missed-retries@e7422c5`:

- `f466010` — `linux: per-request goroutines for delegated syscalls + fti.maz migration` (12 files, +230/-62)

The commit bundles three things:
1. **Stage 1 of "ONE shepherd binary" cleanup** — fti.elf → fti.maz dual-build + startup.toml flip. fti now launches via `/shepherd.elf` host (same path as maildb). `/fti.elf` still built and on disk for fallback.
2. **Linux file-lane phase 2** — every delegated `SyscallRequest` runs in its own goroutine. Per-shepherd ordering preserved by `ShepherdFilesystemData.mu`. Cross-shepherd state (`syscallHandler.shepherds`/`orphanHandles`, `pageCache`, `flockTable`) gets short-held mutexes. `fsclient.Client` was already self-locked. Notifications (death/stdinDecRef/idleFlush) stay on the serial reader.
3. **Launch-path checkpoint instrumentation** — kernel `runshepherd.go` and userspace `fs/main.go` get one-shot logs at every silent gap in the launch chain. These support diagnosing the original mail.elf-load hang DIVERSION.

**Verified:** 171s ARM64 HVF run (G-fti-maz-2). All four shepherds reached steady state. `delegate stuck:` empty on every status line. `uart-ring: dropped=0`. No panic / EE25 / KERNEL EXIT.

## What this branch did NOT do

- Stage 2 (`mail.elf` → `mail.maz`) — deferred. Mail uses `userspace-overlay` only; switching to `merged-shepherd-overlay` may surface real behavioral changes in louis14 / GridTable / attr.
- fs concurrent-readers — fs serve loop is still single-goroutine. User explicitly OK'd "slow fs is acceptable" since fs's blocking points are kernel-level (no userspace cycles) and can't deadlock.
- Root cause for the phase-1 wedge that motivated phase 2 (linux file lane stuck on a hung `fsclient.call` from fs that never replied). Phase 2 makes this only stall the one goroutine. If it recurs we should chase.
- Reproduction of the original mail.elf-load DIVERSION hang. Did NOT fire in G-fti-maz-2; instrumentation is in place for next time.

## Suggested next steps

1. **G-cadence stability check.** 5×180s ARM64 HVF on the current branch to confirm phase 2 is stable across reboots and to look for any new failure modes from the concurrent dispatcher. Watch `delegate stuck:` under click-driven load (open emails, body fetches).

2. **If stable: decide on branch destination.** Either:
   - Merge `diag/mail-elf-load-hang` into `feature/mail-dumb` so subsequent work picks up phase 2.
   - Continue iterating on the branch (stage 2: mail.maz, fs concurrent reads).

3. **If the original mail.elf-load hang reappears**, the new checkpoints localize the silent gap:
   - Between `[RS] copied X bytes from user` and `[RS] unmapped N caller pages` → `unmapUserPages` (6644 pages for mail).
   - Between `unmapped` and `mapped FB+constraint` → page table setup.
   - Between `mapped FB+constraint` and `pre-loadELF` → `buildSymbolTable` / `findHighestVA`.
   - Between `pre-loadELF` and `loadELF ok` → `loadELF` itself.
   - Between `loadELF ok` and `created userspace thread` → thread creation.
   - For F18-style fs-side variant: post-Open / pre-ReadInto / read-done split says where in the 27 MB file read it hangs.

4. **Optional but valuable: stage 2 — mail.elf → mail.maz.** Once stage 1 is observed stable, the migration unifies `launchShepherd` body with `launchPluginShepherd`. `launchShepherd`'s legacy ET_EXEC branch becomes dead code. Mail is the largest binary in the launch chain, so the read shifts from a single 27 MB read of `/mail.elf` (direct ET_EXEC) to a 7 MB read of `/shepherd.elf` (via `launchPluginShepherd`) plus a 27 MB LoadFile of `/mail.maz` from the running shepherd. That's a different I/O shape — diagnostically interesting either way.

## Project setup (always)

- Required env: `GOTOOLCHAIN=auto GO=/opt/homebrew/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64`.
- Build: `$GO tool task`. Run: `$GO tool task run-arm64-hvf TIMEOUT=N`. Log: `$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log`.
- Always `run-arm64-hvf`, never `run-arm64`.
- Never `go build` directly — Taskfile only.

## Reminders / non-negotiables

- No `runtime.Gosched()` in the uring-pacing fix (still holds — phase 2 doesn't add any).
- Per-call sync UART (`klog.Criticalf`, `rawPuts`) is reserved for "about to die" only — phase 2's instrumentation uses one-shot per-launch checkpoints, not per-syscall logs.
- Phase 2 changed `pageCache`/`flockTable`/`syscallHandler` locking — concurrent goroutines now access these. If adding new methods, follow the established lock pattern (lock at method entry; for any callback that does fsclient I/O, snapshot under the lock and release before calling).

## Side context (don't re-litigate)

- `fix/uring-missed-retries` is committed (e7422c5 / 4ba1a15 / b907e8d). Don't recommit those changes.
- Bug-B-family chase paused. F1-F15 disconfirmed VA-collision at SharePages layer.
- `findings.md` has the fti bleve `index out of range [0]` panic from a step-3 smoke; independent of this branch, untouched.
- The phase-1 wedge (G-fti-maz-1) had 3 readlinkats stuck for 110+s. sysid=50 in the status line is `Readlinkat` (counted iota in `shared/sysid/sysid.go`). `sysReadlinkat` is a one-line `Reply(EINVAL)`. The wait was purely queueing in the file lane.
