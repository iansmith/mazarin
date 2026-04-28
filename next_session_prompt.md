# Continuation prompt — `diag/mail-elf-load-hang` (post-phase-2 + worker pool + sweep)

## Where we are

Branch `diag/mail-elf-load-hang`, 4 commits ahead of `fix/uring-missed-retries@e7422c5`:

- `f466010` — `linux: per-request goroutines for delegated syscalls + fti.maz migration` (12 files, +230/-62)
- `6ae4d63` — initial docs update
- `37f1956` — `linux: route sysMmapPageFlush diagnostic through pageCache.InumsFor` (G6 fix)
- `ef449b5` — `linux: 1024-worker pool replaces per-request goroutine spawning` (H1 fix)

The first commit bundles three things:
1. **Stage 1 of "ONE shepherd binary" cleanup** — fti.elf → fti.maz dual-build + startup.toml flip. fti now launches via `/shepherd.elf` host (same path as maildb). `/fti.elf` still built and on disk for fallback.
2. **Linux file-lane phase 2** — every delegated `SyscallRequest` runs in its own goroutine. Per-shepherd ordering preserved by `ShepherdFilesystemData.mu`. Cross-shepherd state (`syscallHandler.shepherds`/`orphanHandles`, `pageCache`, `flockTable`) gets short-held mutexes. `fsclient.Client` was already self-locked. Notifications (death/stdinDecRef/idleFlush) stay on the serial reader.
3. **Launch-path checkpoint instrumentation** — kernel `runshepherd.go` and userspace `fs/main.go` get one-shot logs at every silent gap in the launch chain. These support diagnosing the original mail.elf-load hang DIVERSION.

**Verified across G2 + 5×180s G-/H-cadence sweep:**
- Worker pool (post-`ef449b5`): H3, H4 clean 180s, numGoroutine=1041 stable. H2 reached steady state then hit pre-existing `bug_attr_init_crash.md`. H5 hit underlying fs-reply wedge (3 shepherds stuck, system stayed alive).
- Pre-worker-pool (G2-G5 with unbounded `go ...`): 4 clean runs, then G6 + H1 SIGSEGV in `traceback.go:377 resolveInternal` under goroutine churn. Both fixed.

## What this branch did NOT do

- Stage 2 (`mail.elf` → `mail.maz`) — deferred but **risk now significantly lower**. Worker pool eliminates the unwinder/leakage risk; mechanically same recipe as fti/maildb. Open: louis14 plugin compat (mail directly imports `louis14/pkg/resource`).
- fs concurrent-readers — fs serve loop is still single-goroutine. User OK'd "slow fs is acceptable." But H5 confirmed an underlying wedge: fs sometimes doesn't reply, hangs the worker that called fsclient on per-shepherd lock; subsequent same-shepherd syscalls queue. Phase 2 + worker pool make this affect only the wedged shepherd, not the system.
- Root cause for the underlying fs-reply wedge. ~1-in-5 rate at 180s. Likely fs's serial loop holding things up. Unproven.
- Reproduction of the original mail.elf-load DIVERSION hang. Did NOT fire in any phase-2 run.

## Suggested next steps

1. **Decide between two directions:**
   - **(A) Stage 2 — mail.maz migration.** Same recipe as fti. 4–5 file edits + boot test. Worker pool makes the safety margin good. After this, `launchShepherd`'s legacy ET_EXEC body becomes dead code (stage 3).
   - **(B) Triage the underlying fs-reply wedge.** Add per-fsclient-call timeout / detection. Or audit fs's serve loop for what could cause a dropped/delayed reply. H5 reproduced it; can probably reproduce on demand to investigate.

2. **If picking (A) and the original mail.elf-load hang reappears**, the new checkpoints localize the silent gap:
   - Between `[RS] copied X bytes from user` and `[RS] unmapped N caller pages` → `unmapUserPages` (6644 pages for mail).
   - Between `unmapped` and `mapped FB+constraint` → page table setup.
   - Between `mapped FB+constraint` and `pre-loadELF` → `buildSymbolTable` / `findHighestVA`.
   - Between `pre-loadELF` and `loadELF ok` → `loadELF` itself.
   - Between `loadELF ok` and `created userspace thread` → thread creation.
   - For F18-style fs-side variant: post-Open / pre-ReadInto / read-done split says where in the 27 MB file read it hangs.

4. **Stage 3 — delete `launchShepherd` legacy body — blocked on stage 2.** Once mail is `.maz`, nothing in startup.toml routes through the legacy ET_EXEC body. ~10 line cleanup.

## Project setup (always)

- Required env: `GOTOOLCHAIN=auto GO=/opt/homebrew/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64`.
- Build: `$GO tool task`. Run: `$GO tool task run-arm64-hvf TIMEOUT=N`. Log: `$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log`.
- Always `run-arm64-hvf`, never `run-arm64`.
- Never `go build` directly — Taskfile only.

## Reminders / non-negotiables

- No `runtime.Gosched()` in the uring-pacing fix (still holds).
- Per-call sync UART (`klog.Criticalf`, `rawPuts`) is reserved for "about to die" only — phase 2's instrumentation uses one-shot per-launch checkpoints, not per-syscall logs.
- Phase 2 changed `pageCache`/`flockTable`/`syscallHandler` locking — concurrent goroutines now access these. If adding new methods, follow the established lock pattern (lock at method entry; for any callback that does fsclient I/O, snapshot under the lock and release before calling). Don't reach into `pc.data` etc. directly — go through public methods. The G6 SIGSEGV was a direct `h.cache.data[...]` access bypassing `pc.mu`.
- **No unbounded `go ...` per request in hot loops on the .maz plugin runtime.** Use a fixed worker pool (1024 is fine — generous, not a real bound). H1 SIGSEGV'd in `traceback.go:resolveInternal` after ~14k goroutine spawns; the runtime unwinder doesn't tolerate that churn rate.

## Side context (don't re-litigate)

- `fix/uring-missed-retries` is committed (e7422c5 / 4ba1a15 / b907e8d). Don't recommit those changes.
- Bug-B-family chase paused. F1-F15 disconfirmed VA-collision at SharePages layer.
- `findings.md` has the fti bleve `index out of range [0]` panic from a step-3 smoke; independent of this branch, untouched.
- The phase-1 wedge (G-fti-maz-1) had 3 readlinkats stuck for 110+s. sysid=50 in the status line is `Readlinkat` (counted iota in `shared/sysid/sysid.go`). `sysReadlinkat` is a one-line `Reply(EINVAL)`. The wait was purely queueing in the file lane.
