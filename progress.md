# Progress Log

## Session: 2026-04-29 (Opus, continued) — fix/concurrent-boot-wedge: K-cadence sweep + audit confirms hypothesis 2

### Branch state

`fix/concurrent-boot-wedge`, 3 commits ahead of `fix/uring-missed-retries@5352357`:
- `65f8cac` — docs pivot
- `322e3a8` — TEMP-DIAGNOSTIC fsclient.callLocked timeout + fs serve-loop instrumentation
- `fef642c` — drop fs:serve IPC pick logs (UART overload from K1)

### K-cadence sweep results

| Run | Outcome | Detail |
|-----|---------|--------|
| K1 | wedge + 738 UART drops | 2 threads on sid=21 (maildb) stuck on Fstatat 148s+. fs alive throughout, serving IPCs from sender=28. UART overload prevented `[fsclient:TIMEOUT]` from being visible. |
| K2 | clean 161s | uart-ring dropped=0 |
| K3 | clean 172s | clean |
| K4 | clean 171s | `for=0ms` state-reporter glitch in kernel — not a wedge, just noise |
| K5 | userspace `fatal error: missing deferreturn` | plugin runtime panic-recovery bug, separate from wedge |
| K6 | clean 172s | clean |

1 wedge in 6 runs (~17%), within historical 1-in-5 range.

### Audit findings (hypothesis 2 confirmed by source)

Trace through `maz/fs/fsipc.go:178-183` and `mazarin/uring/reader.go:198`:

1. Linux's ring-0 dispatcher does **blocking** `route.ch <- typed` to one of: `wmCh`(8), `fontReplyCh`(8), `delegateCh`(8), `fsClient.RespCh`(4).
2. If any channel fills (likely `wmCh`/`fontReplyCh` during boot WM/font spike), dispatcher blocks, stops draining ring 0.
3. Kernel ring 0 fills → `uring.Send` to ring 0 returns EAGAIN after 30 ms pacing budget (per `fix/uring-missed-retries`).
4. **`fs.respond()` logs `[fs:ipc] Send response... failed: %v` and DROPS the reply.** No retry.
5. Linux's worker parked on `<-c.RespCh` waits forever for the discarded reply.
6. Worker holds `shep.mu`. Subsequent same-shepherd syscalls queue. `delegate stuck`.

This is a regression introduced by `fix/uring-missed-retries`: pre-fix, `uring.Send` retried forever via Gosched and the drop path in `respond()` was effectively unreachable. The new bounded backpressure (3 attempts × 10ms = 30ms) made the drop path reachable, and the wedge surfaced.

### Channel-depth analysis (with user)

User asked: are 4/8 arbitrary? Answer: yes, mostly defensive scaffolding. `RespCh` cap 4 is unmotivated — at most 1 in flight by `c.mu` construction. `wmCh`/`fontReplyCh` are bursty so buffer is plausible but the size is unjustified. `delegateCh` was sized for pre-phase-2 single-goroutine consumer; phase 2 made it obsolete.

The buffers don't fix the underlying consumer-stall — they just delay the cascade by N messages. Smaller (or 0) buffers expose the bug faster; bigger ones hide it longer.

### Plan (4-step sequence agreed with user)

1. **Capture direct evidence** — K7-K9 with current quieter instrumentation. Watch for `[fs:ipc] Send response... failed: EAGAIN` and `[fsclient:TIMEOUT]`.
2. **Minimal fix in `respond()`** — bounded retry on EAGAIN (e.g., 100 × 30 ms). One-line change in `maz/fs/fsipc.go:178`.
3. **Rationalize channel depths** — shrink `RespCh` to cap 1 (or 0); audit `wmCh`/`fontReplyCh` for "always-ready"; shrink as appropriate.
4. **Architectural change deferred** — kernel-ring drain decoupled from per-protocol routing is the right long-term answer; separate discussion, not this branch.

### MANDATORY EXIT CRITERION (recorded in task_plan.md)

The TEMP-DIAGNOSTIC timeout in `fsclient.callLocked`, the stale-drain logic, the `[fs:serve]` instrumentation, and the `[fsclient:TIMEOUT]` log all MUST be removed before this branch merges. Per user policy "polling or timeouts = architectural change."

### Stopping point

About to execute step 1 (K7-K9 evidence capture) and then steps 2+3 (the actual fix).

---

## Session: 2026-04-29 (Opus, continued) — merge diag/mail-elf-load-hang → fix/uring-missed-retries; new branch fix/concurrent-boot-wedge

### Branch state

- `diag/mail-elf-load-hang` (8 commits) fast-forward-merged into `fix/uring-missed-retries`. Both now point at `5352357`. All work on the diag branch is preserved with its full history.
- New branch `fix/concurrent-boot-wedge` created off `5352357`. Working tree clean. Ready for B work.

### Why the merge here

Phase 2 + worker pool + shepherd unification is logically a unit (each enabled the next). Keeping it separate made sense during diagnosis. With the runtime crashes fixed and the wedge bug isolated as its own thing, a clean merge keeps history readable. The wedge fix gets its own branch since it's a different surface (linux↔fs interaction).

### Tracking updates

- `task_plan.md`: TOP OF STACK pivoted to `fix/concurrent-boot-wedge` with full hypotheses + plan. Previous TOP OF STACK (`diag/mail-elf-load-hang`) moved to ARCHIVED with all sweep tables intact.
- `next_session_prompt.md`: rewritten for `fix/concurrent-boot-wedge`. References the merged history as context.
- Memory: `shepherd_binary_unification.md` and `linux_dispatcher_concurrent.md` already final in earlier session.

### Stopping point

Ready to begin B (concurrent-boot-wedge fix) on `fix/concurrent-boot-wedge`. First step per plan: discuss with user whether to add the `fsclient.callLocked` timeout (architectural — user policy on timeouts).

---

## Session: 2026-04-29 (Opus, continued) — mail.maz migration + drop dual-builds + delete launchShepherd legacy body

### Branch state

`diag/mail-elf-load-hang`, 7 commits ahead of `fix/uring-missed-retries@e7422c5`. New this session:
- `e9247bc` — `stage 2: mail.elf → mail.maz migration`
- `aabdfa2` — `stage 3 + cleanup: drop dual builds and launchShepherd legacy body`

### Stage 2 — mail.maz migration

Mechanically copied the pattern from fti / maildb:
- `mazarin/apps/mail/main.go`: added `var MazEntryPoint func() = MazarinMain; func MazarinMain() { main() }` shim.
- `mazarin/apps/mail/Taskfile.yml`: switched from `userspace-overlay`-only to `merged-shepherd-overlay` + mazgo + mazlink dual-build (initially).
- `Taskfile.yml`: added `MAIL_MAZ` / `MAIL_MAZ_AMD64`.
- `config/startup.{arm64,amd64}.toml`: flipped `/mail.elf` → `/mail.maz`.

Boot tests:
- I1: reached full steady state — `[mail] main() entered`, scratch dir, screen dimensions, AppStart, backing store ready, sent CreateCollection, collection created (19 messages), `cache ready, initial rebalance`, then `KERNEL EXIT GROUP` with no panic visible. Same intermittent attr.Init/exit_group family as in earlier runs. NOT a stage-2 regression.
- I2: clean 154s+. `delegate stuck:` empty, `uart-ring: dropped=0`, `numGoroutine=1041` stable in linux.

### Stage 3 + dual-build cleanup

Per user direction ("dual-builds because that is silly"):

1. `maz/fti/Taskfile.yml`, `maz/maildb/Taskfile.yml`, `mazarin/apps/mail/Taskfile.yml`: replaced with plugin-only build pattern matching rachel/linux/fontsvc. Dropped the legacy ET_EXEC `go build -tags mazhost` step.
2. Root `Taskfile.yml`: removed `FTI_ELF`, `FTI_AMD64_ELF`, `MAILDB_ELF`, `MAILDB_AMD64_ELF`, `MAIL_ELF`, `MAIL_AMD64_ELF` variables. Removed corresponding entries from disk-arm64 + disk-x86_64 sources, mkext2 args, and disk-staging-amd64 cp commands. Disk image no longer ships `fti.elf` / `maildb.elf` / `mail.elf`.
3. `maz/fs/main.go`: deleted `launchPluginShepherd` (renamed `launchShepherd` to take its place). Legacy ET_EXEC body removed. `launchShepherd` is now a single-path function that loads `/shepherd.elf` with the plugin path as arg.

Boot test:
- J1: clean 172s ARM64 HVF run. All 3 shepherds reach `main()` entered, `[mail] cache ready, initial rebalance`, no panic, `uart-ring: dropped=0`.

Net diff for stage 3 + cleanup: −118/+26 lines.

### What remains on the branch

- **B (deferred): underlying fs-reply wedge.** ~1-in-5 rate, 3 shepherds wedged on per-shepherd lock with readlinkats queued. Triage approach in `task_plan.md` (timeout in `fsclient.callLocked`, audit fs's serve loop fairness between `fsDelegateCh` and `fsIPCCh`, reproduce deterministically).
- **Open: bug_attr_init_crash family.** Caught in I1; pre-existing, separate from launch path. Tracked in `MEMORY.md`.
- **Open: original DIVERSION mail.elf-load hang.** Hasn't fired in any phase-2 run. Instrumentation is in place if it returns.

### Stopping point

7 commits on `diag/mail-elf-load-hang`. Ready for either further stability runs, B triage, or branch destination decision (merge into `feature/mail-dumb`).

---

## Session: 2026-04-29 (Opus, continued) — G/H sweep + 1024-worker pool

### Branch state

`diag/mail-elf-load-hang`, 4 commits ahead of `fix/uring-missed-retries@e7422c5`:
- `f466010` — initial fti.maz + per-request goroutines + checkpoints (prior session entry)
- `6ae4d63` — initial docs (prior session entry)
- `37f1956` — `linux: route sysMmapPageFlush diagnostic through pageCache.InumsFor` (this session)
- `ef449b5` — `linux: 1024-worker pool replaces per-request goroutine spawning` (this session)

### G-/H-cadence sweep results

| Run | Outcome | Notes |
|-----|---------|-------|
| G2  | clean 171s | unbounded `go ...`, original phase-2 boot test (last session) |
| G3  | clean | unbounded, transient ms-scale stalls |
| G4  | clean | unbounded, transient |
| G5  | clean | unbounded, transient |
| G6  | SIGSEGV | direct unsynced read of `h.cache.data` from `sysMmapPageFlush` diagnostic block; fixed `37f1956` |
| H1  | SIGSEGV | same `traceback.go:377 resolveInternal` PC, different stack — runtime unwinder failing during copystack of freshly-spawned worker; goroutine ID 14253 at crash time = ~14k spawns; fixed by worker pool `ef449b5` |
| H2  | reached steady state (all 3 main entered + mail cache rebalance), then `bug_attr_init_crash.md` panic in mail-app | pre-existing constraint-VM bug, unrelated to phase 2 |
| H3  | clean 180s | numGoroutine=1041 stable across all status lines (1024 workers + 17 baseline) |
| H4  | clean 180s | numGoroutine=1041 stable |
| H5  | mid-boot stall (62s+) | 3 shepherds (sid=20, 28, 29) wedged, sysid=50 (Readlinkat) for 58+s — same fs-reply wedge as G-fti-maz-1 |

### Two real bugs found + fixed

**1. `pageCache.data` direct access (G6)** — `sysMmapPageFlush`'s `[pageCache:FALLBACK_ALLFDS]` and `[pageCache:DRAIN]` diagnostic block at `syscalls.go:1344` was reading `h.cache.data[callerSID]` directly, bypassing the `pc.mu` lock that the rest of pageCache now takes. Concurrent `Add`/`Remove*` calls plus this unlocked read = Go's "concurrent map" detection trips, traceback unwinder hits invalid state, SIGSEGV.

Fix: added `pageCache.InumsFor(sid)` accessor that snapshots inum keys under `pc.mu`, routed the diagnostic through it.

**2. Unbounded goroutine spawning (H1)** — `go handler.handle(req)` per request was creating ~14k goroutines per 180s run. Most exited cleanly, but any handler that wedged inside `fsclient.call` (waiting for an fs reply that never came) leaked its goroutine forever. The runtime unwinder hit `traceback.go:resolveInternal +0x238 PC=0xbc2e8` during copystack of a fresh worker — same crash on different goroutines (G6 was on `getShepherd`'s mapaccess, H1 was on `fsclient.Stat`'s `Mutex.Lock`). User suggested goroutine leakage as root cause; consistent with what we see.

Fix: 1024 persistent worker goroutines parked on a buffered `fileLaneWorkItem` channel. Reader pushes work items carrying `req` + `isStdinRead` flag. Workers serve one request at a time and return to the channel. No goroutine churn. `numGoroutine` stays flat at 1041 across runs.

### What we learned about mail.maz feasibility

User asked. Honest read: stage 2 risk is significantly LOWER post-worker-pool than it was before:

- The runtime unwinder edge case (the biggest unknown) is gone with bounded concurrency.
- Goroutine leakage is impossible — workers always return to the channel.
- The `bug_attr_init_crash.md` we caught in H2 fired in mail-app userspace at the constraint VM (`attr.ValueToFlat`) — completely independent of launch path. Migrating mail to .maz neither helps nor hurts this.
- The original DIVERSION mail.elf-load hang has NOT reproduced in any phase-2 run.

Remaining mail.maz risks: louis14 plugin compat (mail directly imports `louis14/pkg/resource`; other shepherds reach louis14 indirectly), userspace-overlay → merged-shepherd-overlay switch (strict superset, audited via fti/maildb success).

### Underlying fs-reply wedge (NOT fixed)

H5 reproduced the same wedge that motivated phase 2 in the first place. Three shepherds (during boot's parallel rachel-plugin-load phase) had workers stuck holding their per-shepherd `shep.mu` for 58+s, with subsequent fast syscalls queued. Phase 2 + worker pool make this affect ONLY the wedged shepherds (others continue). The system doesn't crash. But specific shepherds can hang indefinitely. ~1-in-5 rate at 180s.

Likely cause: fs's still-single-goroutine serve loop holds up some fs IPC reply for long enough that the linux-side worker times out / wedges. Unproven. Low-priority for now; might want fs concurrent-readers eventually.

### Stopping point

Branch has 2 new commits this session (`37f1956`, `ef449b5`) on top of last session's 2 commits. Tracking files updated. Worker pool stable across 2 of the 4 H-runs that reached steady state; 1 hit a pre-existing unrelated bug; 1 hit the underlying wedge.

### Next session direction

User decision pending: proceed with mail.maz migration (stage 2) or continue triaging the underlying fs-reply wedge.

---

## Session: 2026-04-29 (Opus) — `diag/mail-elf-load-hang`: fti.maz + linux per-request goroutines

### Branch state

New branch `diag/mail-elf-load-hang`, off `fix/uring-missed-retries@e7422c5` (which was committed at session start — confirmed `git status` clean despite progress.md saying otherwise; the "uncommitted" wording in tracking files was stale). One commit: `f466010 — linux: per-request goroutines for delegated syscalls + fti.maz migration` (12 files, +230/-62).

### What ran

1. **DIVERSION investigation entry.** Read F18/F20 logs; discovered the two hang sites are NOT the same. F18 hangs in fs's `readFileIntoPages` for `/mail.elf` (27 MB, fs-side stall, before kernel sees RunShepherd). F20 hangs in the silent kernel-side gap between `[RS] mail: copied 27210767 bytes from user` and the would-be `[RS] loadELF ok` (covers `unmapUserPages`, `CreateProcessPageTable`, `Map*WithL0`, `buildSymbolTable`, `findHighestVA`, `loadELF`). Original task_plan next-action assumed both were post-thread-creation; revised plan.

2. **Launch-path instrumentation landed.** Per-call checkpoints added (kernel: post-unmap / mapped FB+constraint / pre-loadELF; fs: post-Open / pre-ReadInto / read-done / calling-RunShepherd). All `klog.Criticalf`/`fmt.Printf`, one-shot per launch.

3. **Stage 1 of "ONE shepherd binary" cleanup: fti.elf → fti.maz.** Per user direction (option B). `maz/fti/Taskfile.yml` adopts maildb's dual-build pattern (.elf + .maz). `maz/fti/main.go` gets `var MazEntryPoint func() = MazarinMain; func MazarinMain() { main() }` shim. Root `Taskfile.yml` declares `FTI_MAZ`/`FTI_MAZ_AMD64`, adds them to disk-image sources + mkext2 args. `config/startup.{arm64,amd64}.toml` flips `path = "/fti.elf"` → `"/fti.maz"`.

4. **First boot test (G-fti-maz-1) HANG.** fti.maz launched mechanically — kernel side fully clean through `created userspace thread tid=0x396`. shepherd.elf for fti reached `[shepherd] loading /fti.maz`, fs hit `[fs] read: pre-ReadInto /fti.maz size=23035900`, then system stuck for 100+s with `[status]` continuing every 20s. 3 shepherds in delegate stuck — fti, maildb, mail — all on `sysid=50` for 110+s. Investigation: sysid=50 is `Readlinkat` (counted iota in `shared/sysid/sysid.go`), which is a one-line `Reply(EINVAL)`. They're not stuck IN sysReadlinkat; they're queued behind something else in linux's file-lane single-goroutine.

5. **Linux dispatcher concurrency investigation.** `maz/linux/main.go:230` confirmed: file-lane is `go func() { for raw := range delegateCh { ... } }()` — single goroutine. Comment explicitly says "preserves existing serialization required by syscallHandler's per-shepherd maps and by fsclient's shared data area." Reading `mazarin/fsclient/client.go` revealed fsclient.Client has its OWN `mu sync.Mutex` and `callLocked` requires the lock; so fsclient is already concurrent-safe at the call level — the "single goroutine" comment was conservative.

6. **Phase 2 implementation (per user-confirmed plan, 2-step discussion).**
   - Reasoning: linux's file lane CAN deadlock on userspace cycles (waiting for fs which waits for linux for stdout writes etc.); fs's serve loop CAN'T deadlock on userspace (its blocking points are kernel-level: blockdev IRQ, ext2 metadata, kernel syscalls). So linux phase-2 is the urgent fix; fs is "slow but acceptable." User explicitly OK'd "slow fs is acceptable now."
   - Concretely: file lane spawns `go handler.handle(req)` per `SyscallRequest`. Per-shepherd ordering preserved by adding `mu sync.Mutex` to `ShepherdFilesystemData` (held across the entire dispatch). Cross-shepherd state gets short-held mutexes: `syscallHandler.mu` for `shepherds`/`orphanHandles` maps, `pageCache.mu` for cache state (all public methods locked), `flockTable.mu` for flock state. `FlushAllPagesForInum/SID` rewritten to snapshot tuples under lock then call `write()` (fsclient) outside — avoids holding cache mutex across IPC. Notifications (death/stdinDecRef/idleFlush) stay on the reader goroutine — cheap and ordering-sensitive. `cleanupShepherd` snapshots fs handles under lock then closes them outside.

7. **Phase 2 boot test (G-fti-maz-2) — 171s clean.** All four shepherds reach steady state: `[fti] main() entered` → `bleve index created` → `dispatcher started` → `Ready=true`. `[mail] main() entered`. `[maildb] main() entered` → `fti: complete — 317 docs, 4813005 bytes in 18.612s`. mail-app rendering "Mail" window blit #2500. End-of-run status: `delegate stuck:` empty, `uart-ring: dropped=0`, no panic / EE25 / KERNEL EXIT.

### Files touched (all in commit `f466010`)

- Kernel-side instrumentation: `kmazarin/ksyscall/runshepherd.go` (3 new `klog.Criticalf` checkpoints).
- fs-side instrumentation: `maz/fs/main.go` (4 new `fmt.Printf` checkpoints across `launchShepherd`/`launchPluginShepherd`/`readFileIntoPages`).
- fti migration: `maz/fti/main.go` (MazarinMain shim), `maz/fti/Taskfile.yml` (dual-build pattern), `Taskfile.yml` (FTI_MAZ vars + disk-image), `config/startup.arm64.toml`, `config/startup.amd64.toml`.
- Linux phase 2: `maz/linux/syscalls.go` (syscallHandler.mu, getShepherd/cleanupShepherd locking, orphanHandles helpers, handle() takes per-shepherd lock), `maz/linux/fdtable.go` (mu in ShepherdFilesystemData), `maz/linux/page_cache.go` (mu, all methods locked, Flush*PagesFor* snapshot pattern), `maz/linux/flock.go` (mu, lock acquire/release/releaseAll), `maz/linux/main.go` (file lane spawns goroutines, sidIncRef/sidDecRef wrap each).

### What this does NOT include

- Stage 2 (`mail.elf` → `mail.maz`) — bigger lift due to userspace-overlay vs merged-shepherd-overlay difference. Deferred.
- fs concurrent-readers — slow but not deadlocking; user explicitly accepted "slow fs is OK now."
- Root cause for the G-fti-maz-1 wedge (linux's file lane stuck on a hung `fsclient.call` from fs that never replied). Phase 2 makes this only stall the one goroutine — others proceed. If it recurs, investigate.
- Reproduction of the original DIVERSION mail.elf-load hang. Did NOT fire in this session's run; instrumentation is now in place for next time it does.

### Stopping point

Tracking files updated. Branch `diag/mail-elf-load-hang` ahead of `fix/uring-missed-retries` by 1 commit. Not pushed.

### Next session direction (open)

- More G-cadence runs (180s × 5) on `diag/mail-elf-load-hang` to confirm phase 2 is stable across reboots.
- Watch for `delegate stuck:` patterns under load (clicks, body fetches).
- Decide whether to merge `diag/mail-elf-load-hang` into `feature/mail-dumb` or keep iterating on the branch.
- If the original mail.elf-load hang reappears, the new instrumentation localizes which silent gap it's in.

---

## Session: 2026-04-29 (Opus) — Step 7 of `fix/uring-missed-retries` (F16-F20 sweep)

### Branch state

Still on `fix/uring-missed-retries` off `feature/mail-dumb@68a7254`. Steps 1-6 from prior sessions still uncommitted; this session ran the F16-F20 sweep over the unchanged tree.

### Step 7 — F16-F20 results (5 × 180s ARM64 HVF, no clicks)

| Run | Outcome | Uptime | retried | FAILED | fontsvc FAIL | uart-ring dropped | panic |
|-----|---------|--------|---------|--------|--------------|-------------------|-------|
| F16 | clean   | 162s   | 0       | 0      | 0            | 0                 | none  |
| F17 | clean   | 171s   | 0       | 0      | 0            | 0                 | none  |
| F18 | hang    | —      | 0       | 0      | 0            | (no status)       | none — hung mid-boot at `[fs] reading /mail.elf` |
| F19 | clean   | 133s   | 0       | 0      | 0            | 0                 | none  |
| F20 | hang    | —      | 0       | 0      | 0            | (no status)       | none — hung mid-boot just after `start name=mail pages=6644` |

**3/5 reached steady-state, all 3 clean. 2/5 hit the pre-existing intermittent mail.elf-load boot hang** (same site as B2/B5, A2, C1/C5 from earlier bug-B-family sessions; tracked there, NOT introduced by this branch). No regression markers across any run: 0 retried, 0 FAILED, 0 fontsvc OpenFontReply FAILED, 0 uart-ring dropped, 0 kernel panics / data aborts / EXIT_GROUP. Side observation by user during F17 ("stderr messages with 'Send'") could not be reproduced in the saved log — full grep on `Send` / `send` / `EAGAIN` / `failed` / `error` / `resource temporarily` returned nothing past the UEFI prelude. User acknowledged uncertainty and chose to trust the markers.

### Verdict

Per the step-7 decision tree (in `next_session_prompt.md`): **fix verified at the F-cadence.** No retries fired, so we have no positive evidence that the pacing path is exercised under no-click load — the kernel-side block-with-deadline is doing its job and the rings are not back-pressuring under idle traffic. The two boot hangs are not a regression; they are the same pre-existing intermittent we see in baseline runs without this branch.

Combined steps 1-6 status (kernel + userspace pacing + fontsvc revert) is **ready for commit pending diff review.**

### Stopping point

Tracking files updated. No commits this session yet. Awaiting user review of:
- Kernel step 1-3 diff: `kmazarin/kmazarin/threads.go`, `uring_ipc.go`, `serial_console.go`, `soft_irq_slots.go`, `kmazarin/ksyscall/uring_ipc.go`, `uring_ipc_asm.go`.
- Userspace step 4-5 diff: `mazarin/uring/syscall.go`, `maz/fontsvc/main.go`.

After review, two-or-three commits as outlined in `next_session_prompt.md` "After step 7" section.

---

## Session: 2026-04-28 late night (Opus) — Steps 4-6 of `fix/uring-missed-retries` (userspace pacing + fontsvc revert + smoke)

### Branch state

Still on `fix/uring-missed-retries` off `feature/mail-dumb@68a7254`. Steps 1-3 from the prior session were already in the working tree. This session added steps 4-6. Everything **uncommitted**, awaiting user diff review.

### Step 4 — `mazarin/uring/syscall.go` rewrite (DONE)

- **`SendWithStats` removed entirely.** It was the F-series fontsvc instrumentation; the kernel-side block-with-deadline now subsumes that need.
- **`SendWithRing` rewritten as the primitive** with a real off-CPU pacing loop:
  - Up to `sendMaxAttempts = 3` attempts.
  - Per-attempt timing via `time.Now()` / `time.Since`. If the syscall returned EAGAIN in less than `sendFastBounceMs = 8 ms`, treat it as a fast bounce (kernel didn't park us — another sender already there, or no thread to switch to) and `time.Sleep(sendPerAttemptMs * time.Millisecond)` (10 ms) before retrying. If it took ≥8 ms, the kernel already held us for ~the full 10 ms deadline → retry immediately.
  - Total wall-time budget ~30 ms.
  - **No `runtime.Gosched()` anywhere.** `runtime` import dropped; `fmt` and `time` added.
  - Single `fmt.Printf` log on retry-success: `[uring.Send] target=N ring=R retried, attempts=M`.
  - Single `fmt.Printf` log on exhausted failure: `[uring.Send] target=N ring=R FAILED after 3 attempts, err=...`.
  - **Clean first-try success is silent** (no log).
- **`Send(target, msg)`** is now a one-line wrapper: `return SendWithRing(target, msg, 0)`.
- `Recv` / `RecvWithRing` / `Connect` / `ConnectWithRing` / `Setup` / `Release` left untouched (already follow ring-0 wrapper convention).

### Step 5 — `maz/fontsvc/main.go` revert (DONE)

- Restored original `shareCacheAndReply(conn, connIdx, senderSID, fontID)` signature (dropped `variant, size, family` params).
- `handleOpenFont` reverted to call `shareCacheAndReply(conn, connIdx, senderSID, fontID)`.
- Restored original `uring.Send` call + simple `[fontsvc] uring.Send OpenFontReply FAILED: ...` log path. Dropped the `OpenFontReply FAILED senderSID=N fontID=M variant=V size=S attempts=A family=F err=...` and `OpenFontReply retried ...` log paths (no longer meaningful — `uring.Send` does the equivalent).
- `itoaBytes` helper deleted.
- `rawPutsInt` restored to its original inline form.

### Step 6 — Build clean + 180s smoke (DONE)

- `$GO tool task` clean (kmazarin built, ESP image, ext2 disk image — all expected output).
- `$GO tool task run-arm64-hvf TIMEOUT=180` clean:
  - Kernel stable across the full run (no `EE25` / `KERNEL EXIT GROUP` / halt markers).
  - `uart-ring: dropped=0` on every `[status]` cycle through 161 s uptime.
  - **Zero `[uring.Send] retried` lines.** Zero `[uring.Send] FAILED` lines. Idle no-click run, so no ring-pressure conditions surfaced.
  - Zero `[fontsvc]` / fontsvc EAGAIN errors.
  - No fti bleve panic this run (the smoke run from step-3 did hit it; not chasing — see `findings.md`).
- `[status]` end-of-run digest: `syscalls=3796415 timer=324Hz ctx_switches=31707`, `va-probe: inIPC=132 outIPC=0`, `uart-ring: dropped=0`. Within prior baseline ranges.

### Step 7 — REMAINING

F16-F20: 5 × 180 s ARM64 HVF, no clicks. Compare to F6-F15 baseline (0 EAGAIN). With pacing, fast-EAGAIN bouncing now consumes real wall time, so any retries that DO happen surface as `[uring.Send] ... retried` log lines. Watch for those + final `... FAILED after 3 attempts`.

### Notes / non-negotiables held

- `runtime.Gosched()` deleted from both kernel-side (`pushStringFull`, prior session) and userspace-side (this session). The fix has zero yields.
- All sleeps are real off-CPU `time.Sleep` blocks → kernel parks the goroutine on its deadline queue, P is handed off, other goroutines can run.
- Logging via `fmt.Printf` (async, buffered through linux Write delegate) — NOT `klog.Criticalf` / `serial.PollWrite` / `rawPuts`. Per `memory/sync_uart_irq_masked.md` synchronous UART writes are reserved for "about to die".

### Resumption

See `next_session_prompt.md` (rewritten for step 7).

---

## Session: 2026-04-28 night (Opus) — Branched `fix/uring-missed-retries`, kernel block-with-deadline impl in flight

### Branch state

- New branch `fix/uring-missed-retries` off `feature/mail-dumb` at `68a7254`.
- One committed predecessor on `feature/mail-dumb`: `68a7254` (instrumentation: counter-based VA probe + OpenFontReply EAGAIN logging + linux dispatcher-started logs).
- This session's kernel work is **uncommitted** on `fix/uring-missed-retries`, awaiting review.

### Architecture decision (recorded in `memory/sync_uart_irq_masked.md`)

Synchronous UART writes (`klog.Criticalf`, `serial.PollWrite`, `.maz` `rawPuts`) are taken with ARM64 SVC's hardware-default DAIF.I=masked. With QEMU `-smp 1`, a 7 ms PL011 spin IRQ-blocks the entire system. This is why F2's `OpenFontReply EAGAIN` happened: the receiver's reader was starved while the kernel held the CPU.

Fix architecture: **kernel-side block-with-deadline** for both rings + **userspace pacing** (no `runtime.Gosched()` anywhere).

### Step 1 — data-only state (DONE)

- `kmazarin/kmazarin/threads.go`: added `ThreadBlockedUringSend` (state 19) and `ThreadBlockedKernelRingPush` (state 20). Added `Thread.UringSendBlockedSlotPtr` and `Thread.UringSendDeadlineExpired`.
- `kmazarin/kmazarin/uring_ipc.go`: added `UringIPCSlot.BlockedSenderTID` and `BlockedSenderPtr`; init initialises BlockedSenderTID = -1.
- `kmazarin/kmazarin/serial_console.go`: added `kernelRingPushBlockerTID int32` and `softIRQDroppedBytes uint64` (data only — wiring in step 3).

### Step 2 — uring kernel-side block (DONE, uncommitted)

- `kmazarin/kmazarin/uring_ipc.go` `UringSendKernel` rewritten:
  - Userspace senders (senderSID >= 0) park on `ThreadBlockedUringSend` with 10 ms deadline if ring full and no other sender is parked.
  - Kernel-internal senders (senderSID < 0, `KernelWriteToRing`) keep immediate `-EAGAIN`.
  - Single blocker per slot (Scenario D); second sender returns `-EAGAIN` immediately.
  - Race-free publish: re-check head/tail under `schedulerLock` before publishing blocked state.
  - On rewind+retry, `UringSendDeadlineExpired` flag short-circuits to `-EAGAIN`.
  - `wakeBlockedSenderSchedLockHeld` helper (rewind+ready) is shared by drain wake and cleanup paths.
- New `WakeSenderAfterDrain(sid, ringIdx)` — separate from `advanceUringHead` (the latter is `//go:nosplit`); called from `SyscallUringRecv` after each `advanceUringHead` to wake any parked sender.
- `kmazarin/ksyscall/uring_ipc_asm.go`: linkname stub for `wakeSenderAfterDrain`.
- `kmazarin/ksyscall/uring_ipc.go` `SyscallUringRecv`: calls `wakeSenderAfterDrain` post-advance in both drain branches.
- `kmazarin/kmazarin/threads.go` `processStaticDeadlinesSchedLockHeld`: new branch for `ThreadBlockedUringSend` — clears slot blocker, sets `UringSendDeadlineExpired`, rewinds, readies.
- `kmazarin/kmazarin/uring_ipc.go` `CleanupUringIPCForShepherd`: also wakes senders parked on a dying receiver's slot (sets deadline-expired so the rewind retry surfaces an error instead of re-blocking).

Full tree builds clean (`$GO tool task`).

### Steps 3-7 — REMAINING

3. **Same pattern for `pushStringFull` + `topHalfUartRing` consumer.** Park thread 0 with `KernelBlockSleep` when `topHalfUartRing` is full. Wake from `SyscallWaitSoftIRQ`'s pop path. Drop-and-counter on deadline expiry.
4. **Userspace rewrite of `mazarin/uring/syscall.go`** — remove `SendWithStats`, make `SendWithRing` the primitive with 3-attempt retry + nanosleep pacing on fast-EAGAIN + single log line. `Send` is the one-line ring-0 wrapper.
5. **Revert `maz/fontsvc/main.go` instrumentation I added** (`itoaBytes`, attempts logging, `shareCacheAndReply` extra args). Just call `uring.Send` and let it handle retry + logging.
6. **Build, smoke test.**
7. **Run F16-F20 and compare to F6-F15 baseline.**

### Key things to verify in the existing step-2 work before extending

- The race window: producer's first head/tail check is under producer lock (atomic), and the BLOCKING publish takes scheduler lock and re-checks head/tail under it. Drainers advance head atomically (no lock) then take schedulerLock for the wake. The "drain happens between producer's first check and publish" case is handled by the recursive call to `UringSendKernel(...)` after the under-sched-lock re-check fails.
- `findNextThreadForBlockSchedLockHeld(senderThread)` returning nil falls back to immediate EAGAIN — matches futex-block precedent.
- `staticDeadlineQueue.Insert` after `Remove` because an already-blocked thread might have a leftover deadline (defensive, mirrors AddDeadlineStatic's behavior).
- `t.Context` X1/X2 are not overwritten across SVC, so only `SoftIRQSlotArg` (= arg0/targetSID) needs RestoreSyscallArg0 on rewind.

### Next-session resumption

See `next_session_prompt.md`.

---

## Session: 2026-04-28 late evening (Opus) — F6-F10 + smarter probe + OpenFont instrumentation

### What changed in tree (uncommitted)

1. **`kmazarin/ksyscall/mailbox.go`**: replaced per-call `klog.Criticalf` probe (132×/boot synchronous UART) with atomic counters + range check. `Criticalf` now only fires if a target VA falls **outside** `[0x500000000000, ...)` (smoking-gun case). Counters surfaced on `[status]` line as `va-probe: inIPC=N outIPC=M minVA=X maxVA=Y`. `vaCollisionProbeEnabled` flipped to true.
2. **`kmazarin/kmazarin/threads.go`**: extended `[status]` line with `va-probe:` line.
3. **`mazarin/uring/syscall.go`**: added `SendWithStats(target, msg, ringIdx) (attempts int, err error)` — same retry logic as `SendWithRing` but returns the EAGAIN-retry count so callers can log when they hit ring-full pressure.
4. **`maz/fontsvc/main.go`**: replaced `uring.Send` in `shareCacheAndReply` with `SendWithStats`. Logs `[fontsvc] OpenFontReply FAILED` (with senderSID/fontID/variant/size/attempts/family/err) on exhausted-retry failures, and `[fontsvc] OpenFontReply retried` on successful retries. **No log on clean first-try success** — keeps synchronous UART traffic bounded to actual EAGAIN events. Added `itoaBytes` helper to consolidate multi-fragment logs into single `rawPuts` calls.
5. **`maz/linux/main.go`**: added one-line `[linux] uring dispatcher ring=N started` log per dispatcher (uses `fmt.Printf` → buffered Linux Write delegate, so async).

### F6-F10 results (180s each, no clicks, probe ON with corrected range)

| Run | Outcome | mail-font slot | va-probe inIPC | OUT-OF-RANGE | OpenFontReply FAILED | retried |
|-----|---------|----------------|----------------|--------------|----------------------|---------|
| F6  | clean 164s | yes | 132 | 0 | 0 | 0 |
| F7  | clean 162s | yes | 132 | 0 | 0 | 0 |
| F8  | **panic** (attr.Init/D1-variant) | yes | (no status) | 0 | 0 | 0 |
| F9  | mid-boot hang (mail.elf load) | no (didn't reach) | (no status) | 0 | 0 | 0 |
| F10 | clean 172s | yes | 132 | 0 | 0 | 0 |

Combined with F1-F5 + E1: **7 successful mail-font-slot populates × 132 SharePages = ~924 IPC-region picks, 0 out-of-range across all runs.** minVA=`0x500000000000`, maxVA=`0x500001d3e000` exactly the same in every clean boot — kernel's IPC bump-pointer picker is fully deterministic.

### F8 — different bug, captured incidentally

```
panic: attr: ValueToFlat failed: flat: unsupported vm type 0 for conversion
goroutine 1 [running]:
mazzy/mazarin/attr.(*Attribute[...]).evaluate
mazzy/mazarin/attr.(*Attribute[...]).Get
mazzy/mazarin/mancini.(*LayoutAttributes).FullDamage
main.main() at mazarin/apps/mail/main.go:408
```

Followed by kernel `EE25 status=2 / FAIL EL=1 FAR=0x9000000 ELR=...458BE918 ESR=0x96000045` — UART data abort variant of D1.

This matches the **attr.Init constraint page crash** bug B-family member already in `MEMORY.md`. Different mechanism than the GC mspan crash (`sweep increased allocation count`).

### Findings

- **VA-collision hypothesis at SharePages layer is now strongly disconfirmed.** The corrected probe ran across 7 successful boots × 132 calls each (~924 SharePages) with **zero** out-of-range picks. The IPC bump pointer is reliably allocating from `0x500000000000+`, never near the Go heap (`0xC000000000`).
- **The mspan-corruption "bug B" target did not reproduce in F1-F10** (10 boots, 8 reached steady state). Historical baseline was 1/5; current is 0/8 even-keeled. Combined with E2/E3 from earlier in the session, GOGC=5 plus the small timing perturbations from the probe instrumentation appear to have masked or mitigated the corruption window. We need a different way to provoke it.
- **OpenFont EAGAIN didn't recur in F6-F10.** Yesterday's F2 had one occurrence (`senderSID=1` linux). The instrumentation is in place (FAILED + retried logs with attempts) but the condition didn't fire across these 5 runs. Need either more samples or boot-time pressure variation to catch it.
- **F8 caught a sister bug** — attr.Init crash, separate from the GC mspan crash. Already tracked in `MEMORY.md` as `bug_attr_init_crash.md`.

### Stopping point — instrumentation landed but uncommitted

All edits unstaged. The probe is `enabled=true` in mailbox.go. No commits this session.

### Next-step options

A. **More runs** to catch either the GC mspan crash or another OpenFontReply EAGAIN. Both are intermittent at <1/5 rate, so 10+ more runs would be needed for confidence.

B. **Pivot to Stage 4 Option 3 (VirtIO DMA target-PA audit)** in `kmazarin/kvirtio/block*.go`. The VA-collision hypothesis is now strongly disconfirmed; DMA-PA collision is the next candidate for the mspan crash.

C. **Provoke EAGAIN deliberately** — e.g. add a temporary boot-time delay before linux's ring-0 dispatcher starts so OpenFont replies arrive while the ring is unattended. Would let us verify whether the EAGAIN is purely "reader not yet running" vs something else.

D. **Investigate the F8 attr.Init crash** as its own thread.

---

## Session: 2026-04-28 evening (Opus) — Boot-only F1-F5 sweep, probe ON, 0 crashes, all VAs IPC-region

### What ran

Flipped `vaCollisionProbeEnabled = true` in `kmazarin/ksyscall/mailbox.go`. Built. Ran 5×180s ARM64 HVF, no clicks. Saved logs at `/tmp/F{1..5}-180s.log` and decoded text at `/tmp/F{1..5}-text.log`.

### Per-run summary

| Run | uptime | Crash | populateSlot server=4 | [fontslot:VA] | VAs outside IPC | SID 29 fail flood |
|-----|--------|-------|----------------------|---------------|-----------------|-------------------|
| F1  | 165s   | no    | yes (1×)             | 132           | 0               | 0                 |
| F2  | 162s   | no    | no                   |  98           | 0               | 0                 |
| F3  | 161s   | no    | yes (1×)             | 132           | 0               | 0                 |
| F4  | 161s   | no    | yes (1×)             | 132           | 0               | 0                 |
| F5  | 161s   | no    | yes (1×)             | 132           | 0               | 0                 |

Total: 626 [fontslot:VA] entries across 5 boots, all in IPC region. VA prefix span seen: `0x500000000xxx` through `0x500001d3xxxx` — i.e. up to ~30 MB into the IPC region. None even close to Go heap (`0xC000000000+`).

### Findings

- **0 crashes in 5 runs.** Baseline (no probe) was 1/5; F1-F5 with probe ON gave 0/5. Either GOGC=5 has dropped the crash rate below baseline noise, or boot-only traffic alone isn't enough to trigger the corruption (E1's crash needed a click-driven render burst, but that's gated now).
- **Every observed VA is in the IPC region** (`>= 0x500000000000`, well below `0xC000000000`). 4/5 runs reached the historic crash trigger point `populateSlot client=0 server=4 kind=1` without crashing. Combined with E1's 132 same-region VAs, this is now 5 boots × server=4 hits, all IPC-region — strong consistency.
- **Per the decision tree, "fully ruled out" requires a crash run + IPC-region VAs.** No crash repro in F1-F5, so the formal disconfirmation isn't reached. But the consistency makes VA-collision at the SharePages layer increasingly improbable.
- **`[maildb] send to SID 29 failed` flood DID NOT recur** in any of F1-F5. The 151-line E2 burst from yesterday's session may have been timing-sensitive; not actively regressing now. No mlog bisect needed at this time.
- **Probe stayed safe** boot-only: no click-induced regression in any run.

### Stopping point — no commits this session yet

Probe still flipped to `true` in working tree. Diff is only the one-line toggle in `kmazarin/ksyscall/mailbox.go`. Awaiting user decision on next direction.

### Next-step options

A. **F6-F10 (more samples)** to chase a crash with the probe firing. Decision tree says 0/5 → run 5 more before drawing conclusions. ~15 min runtime.

B. **Pivot now to Option 3 (VirtIO DMA target-PA audit)** given the 5/5 consistency of IPC-region VAs across server=4 hits. Files: `kmazarin/kvirtio/block*.go`, descriptor-setup path. Question: does maildb's BBolt read use a DMA target-PA derived from a user VA (could be freed mid-request) or a stable kernel buffer?

C. **Hybrid**: revert probe flip + run 5 baseline (probe off) to compare crash rate. If baseline is still 0-1/5 at GOGC=5, the timing has genuinely shifted and we need a different way to provoke the crash before any further VA work.

---

## Session: 2026-04-28 (Opus) — Stage 4 prep landed, VA-collision probe regression caught + fix, preliminary VA data favors disconfirmation

### What ran

1. **GOGC=5 plumbing**: `config/startup.arm64.toml` `gc_percent = 5` for mail.elf, plus `maz/fs/main.go` plumbing of `StartupConfig.GCPercent` through `launchShepherd`/`launchPluginShepherd` as `__MAZZY_GCPERCENT=N` (already consumed in `kmazarin/ksyscall/launch.go`). Verified active: mail-app reaches `gc=3337` in 90s, `gc=6176` in 180s — way above prior baselines.

2. **VA-collision probe (commit `b039800`)**: unconditional `klog.Criticalf("[fV]", "[fontslot:VA] caller=%d target=%d va=%x type=%s", ...)` in `SyscallSharePages` after a successful map. Intended to capture target VAs when fontsvc shares font-cache pages into mail-app, then compare them against the Go heap range (`~0xC000000000+`).

3. **E1 (180s, probe ON, user clicked once)**: probe fired 132×, all VAs in `0x500000xxxxxx` (IPC region). `populateSlot server=4` fired at boot — no crash. After click → `[mail:click]` → `[click-agent] Click on *std.RowPercentage` → `[mail] body: 40246 bytes variant=1` → `KERNEL EXIT GROUP — halting` with NO panic message visible. The kernel's own runtime called `exit_group` (per `kmazarin/ksyscall/exit.go::SyscallExitGroup` PID==0 branch). Body rendering after click triggers a heavy SharePages burst; the synchronous Criticalf writes regressed the system.

4. **Probe fix (commit `459dab0`)**: gated behind `vaCollisionProbeEnabled` (default false). Boot-time runs can flip the var to true if more data is needed.

5. **E2 (180s, probe OFF, no click)**: clean. `populateSlot server=4` fired at boot. `gc=6176` for mail-app. ~151 lines of `[maildb] send to SID 29 failed: resource temporarily unavailable` during fti shepherd launch — needs investigation but not crash-causing.

6. **E3 (180s, probe OFF, no click)**: clean. Same `gc=6176` ballpark. No crashes, no exits.

### Findings

- **VA-collision hypothesis at SharePages layer is provisionally weakened.** All 132 [fontslot:VA] entries from E1 have the form `va=500000xxxxxx` with `type=FontCache`, `caller=fontsvc-sid`, `target=mail-app-sid`. None fall in mail-app's Go heap range. Go's `findObject` returns nil for non-arena pointers, so the GC marker should never walk into 0x500000xxxxxx. One boot's data is not conclusive, but the VAs are consistently picked from a non-overlapping region. To fully rule out, would need a crash-run with the probe firing — which requires re-architecting the probe to not stall under heavy traffic (option: write into a ring buffer + dump on crash, not synchronous Criticalf).

- **Probe-induced regression**: kernel `runtime.throw()` after click likely caused by Criticalf contention or a stack-budget issue in a hot path. No panic message reached UART before exit_group; either stderr was unhealthy at fault time or throw was bypassed. Not investigated further — fix is to gate the probe.

- **GOGC=5 timing lock not yet tested**: 0 crashes in E2 + E3 (only 2 boot-only samples). Baseline crash rate is 1/5 at 180s, so 0/2 is within noise. Need 5+ boot-only samples to compare crash rate against baseline. Not run this session.

- **`[maildb] send to SID 29 failed`**: 151 lines during fti shepherd launch in E2. Source is `maz/maildb/mail_handler.go::sendMailMsg` (uring.Send returning EAGAIN). May be a pre-existing race exposed by the maildb mlog routing change (now visible in serial because mlogErrorf does both console + Println), or a real ordering issue between maildb and a freshly-launched mail-app shepherd. To audit later.

### Stopping point

Three commits this session:
- `b039800` — kernel+fs: Stage 4 prep — GOGC=5 mail-app + VA-collision probe (regressed click)
- `8b91d34`, `24ee044`, `ade5319` — committed earlier this session (maildb console routing, Stage 3 probes, docs)
- `459dab0` — kernel: default VA-collision probe off (fixes the regression)

### Next session

1. Re-enable `vaCollisionProbeEnabled = true` for **boot-only** 5×180s sweep (no clicks). Boot SharePages traffic is moderate; render-time traffic is the killer. Capture VAs from a crash run if the boot-time mspan crash reproduces.
2. If the boot-only crash rate is meaningfully lower under GOGC=5, that's a clue (corruption may be GC-pressure-modulated).
3. If a boot-time crash repros and VAs are still in `0x500000xxxxxx` region: VA-collision at SharePages layer fully ruled out. Pivot to **Stage 4 Option 3** (VirtIO DMA target-PA audit) per `task_plan.md`.
4. Audit the `[maildb] send to SID 29 failed` flood — confirm it's not a regression from this session's mlog routing change. Compare with a pre-`8b91d34` baseline if uncertain.

---

## Session: 2026-04-27 (evening, Sonnet) — Stage 3 probes applied, Suspects 5+1 disproven, crash timing lock identified

### What ran

1. **Stage 3 probe application**: Applied two diagnostic-only edits.
   - `maz/linux/page_cache.go:78` — dropped `old.VA != va` predicate from `[pageCache:OVERWRITE]` check; now logs every overwrite with `same-VA=true/false` annotation.
   - `maz/linux/syscalls.go` (in `sysMmapPageFlush`, `!inumKnown` branch) — added `[pageCache:FALLBACK_ALLFDS]` log (with inum count) and `[pageCache:DRAIN]` per-inum loop before `FlushAllPagesForSID`/`RemoveAllBatch` fallback fires.
   Both compile cleanly. Build successful.

2. **Smoke run (90s)**: Crash reproduced (`nelems=256 nalloc=35111`) with **0 probe fires**. Crash fired immediately after `populateSlot client=0 server=4 kind=1` + `initial rebalance first=-1 last=-1 vis=0`.

3. **Five 180s diagnostic runs** (D1–D5): D1 = kernel EL1 data-abort write `FAR=0x9000000 ESR=0x96000045` (translation fault L1 at UART address); D2 = mspan crash `nelems=341 nalloc=4024`; D3–D5 = clean. **0 probe fires across all 6 crash-eligible runs.**

### Findings

- **Suspects 5 and 1 disproven.** The `[pageCache:FALLBACK_ALLFDS]` and (broadened) `[pageCache:OVERWRITE]` probes never fired in any run, including both crash runs. The `sysMmapPageFlush` fallback-flush path and the `cache.Add` overwrite path are not involved in the corruption.

- **Crash timing lock is the primary signal.** Every crash — across all seven diagnostic rounds including this one — fires at the same program point: `populateSlot client=0 server=4 kind=1 cacheLen=49152 fontDataLen=53504` → `cache ready, initial rebalance first=-1 last=-1 vis=0` → `[mem:linux]` → crash. No probe has ever fired before the crash. No crash has ever fired before this point. The corruption window is bounded to what happens between `populateSlot server=4` and the GC sweep that follows the initial rebalance.

- **D1 kernel EL1 abort**: write fault at `VA=0x9000000` (UART), L1 translation missing. This is a different failure mode than the mspan crash — it suggests the kernel's own page tables may be corrupted in some runs, not just mail-app's heap.

- **Primary hypothesis reframed**: VA collision — the kernel maps the 12 font-cache pages at a VA in mail-app's address space that overlaps Go's heap region. The GC's next sweep reads font-file bytes as mspan struct fields → nonsensical `nelems`/`nalloc` → crash.

### Next session

1. **GOGC=5 for mail-app** (verify launch.go sets it; if not, add it). Run 5 × 180s. If timing lock holds under aggressive GC → corruption window confirmed bounded to `populateSlot server=4` → initial-rebalance.
2. **VA-collision probe** (Stage 4 Option 1): log the VA being mapped into mail-app during `populateSlot server=4`; check against Go heap range. One `klog.Logf` in the kernel map-user-pages path.
3. See `task_plan.md` Stage 4 pivot options (ordered) for the full decision tree.

---

## Session: 2026-04-27 (late afternoon, Opus) — Option B run, H-T2 ruled out, Option A next

### What ran

1. **Baseline reproducibility check (no instrumentation)**: 3 × 90s ARM64 HVF, no clicks. 0 crashes. Boot-without-clicks does not reliably reproduce the mspan crash at 90s — the historical ~1-in-5 rate is at 180s.
2. **Option B implementation** (`612ed58`): new file `kmazarin/kmem/stale_pte_check.go` adds a diagnostic that walks every live shepherd's userspace page table on every `BuddyAllocTyped` of a user-side page type, looking for a leaf PTE still mapping the just-allocated PA. Telemetry counter (`stalePTEScans`/`stalePTEHits`) surfaced on the periodic `[status]` log line as `stale-pte: enabled scans hits`. Halt-on-first-hit via `klog.Criticalf("S!P!", ...)` mirroring `buddyDoubleFreeHalt`.
   - **nosplit discipline (caught at build time):** `BuddyAllocTyped` is `//go:nosplit` (called from exception-handler chains via `allocPTPage`). The walker must stay nosplit too, which means no `SerialPuts`/`SerialHex16`/`klog` calls from inside the walk (those nested chains bust the budget). Resolution: walker records first hit into package-level globals (`stalePTEHitSID`/`stalePTEHitVA`/`stalePTEHitLeafPA`) and returns; non-nosplit `stalePTEHalt` then formats via `klog.Criticalf` and freezes.
   - **Cost-of-scan triage (caught at first run):** unfiltered, the walker is so expensive that 180s of wall time only reaches fti launch (boot normally finishes in ~10s). Resolution: filter at the call site to user-side page types only (`PageUserText`/`PageUserROData`/`PageUserData`/`PageUserHeap`/`PageUserStack`/`PageFontCache`/`PageIPCBuffer`/`PageSharedIPC`). Kernel-type allocs vastly dominate boot churn and are not the suspected manifestation surface (the bug is mspan corruption in mail-app's heap = `PageUserHeap`).
3. **Option B diagnostic run (verifier on, 5 × 180s)**: B1 154s/183K-scans/0-hits/clean, B2 hung at rachel-boot, B3 114s/203K-scans/0-hits/**reproduced mspan crash** (`nelems=128 nalloc=31291`), B4 168s/181K-scans/0-hits/clean, B5 hung at rachel-boot.
4. **Baseline run (verifier off, 5 × 180s)**: 1 mspan crash (`nelems=341 nalloc=26649`), 1 early-boot hang at `[fs] reading /shepherd.elf`, 3 clean. Crash rate matches the historical 1/5.

### Findings

- **H-T2 (stale PTE in another shepherd's PT memory) ruled out.** Across ~370K user-side allocs scanned by the verifier — including in B3 which reproduced the crash — zero stale PTEs were detected. If a PTE had been left behind at munmap, the verifier would have caught it the moment that PA was reissued.
- **H-T1 (stale TLB) survives as the primary suspect.** The verifier walks PT memory, not TLB caches, so silence here is consistent with H-T1 — a PTE that has been cleared but whose translation still lives in the CPU's TLB cache will not show up in any PT walk.
- **Two side-issues parked:**
  - Rachel-boot hang in B2/B5 — exposed only with the verifier enabled. Could be verifier-induced (use-after-free of an L0 PA mid-teardown when the walker reads `proc.ShepherdListInUse[i]`/`PageTableL0PA` without locking) or pre-existing race exposed by latency. Did NOT reproduce in the verifier-off baseline runs.
  - Early-boot hang at `[fs] reading /shepherd.elf` (baseline run 1) — pre-existing, not introduced by Option B.

### Late-session: Option A applied, then reverted as a no-op

Added `TlbiVMALLE1 + DsbISH + IsbSY` at the end of `SyscallMunmap` (after the per-page unmap+release loop), mirroring `SyscallFreePages`. 5 × 180s ARM64 HVF: A1 163s clean, A2 boot-hang, A3 mspan crash (`nelems=100 nalloc=22790`), A4 161s clean, A5 mspan crash (`nelems=36 nalloc=44307`). 2/5 crashes vs baseline 1/5 — within noise, and the change is functionally a no-op since `UnmapUserPage`/`UnmapUserPageWithL0` already do per-page `tlbiVAE1IS` (inner-shareable broadcast — propagates to all CPUs in the IS domain). A trailing local `TlbiVMALLE1` after a broadcast doesn't add coverage.

**H-T1 (stale TLB) is weakened, not conclusively ruled out.** The cleanest H-T1 test would need ASID swap on munmap or a same-VA-access probe immediately post-`tlbiVAE1IS` to verify it actually invalidated. That's bigger work. For now, pivot to H-T3.

### Late-session: H-T3 Stage 1 sentinel-byte canary — H-T3a ruled out

New file `kmazarin/kmem/free_canary.go`. At `BuddyFreeTyped` (after `buddyInsertFree`) and at `buddyAddRange` (init-time bootstrap pool population), paint the freed block with `0xDEADBEEFDEADBEEF`, skipping the first 8 bytes used by the buddy free-list next-pointer. At `BuddyAllocTyped` (after `buddyRemoveFree`, before split), verify the pattern is intact byte-for-byte. Mismatch → capture pa/offset/expected/found into globals, halt via `klog.Criticalf("K!W!", ...)`. Telemetry on `[status]` line: `free-canary: enabled fills verifies hits`. Same nosplit discipline as Option B verifier.

**Bootstrap fix caught in smoke test:** the very first `BuddyAllocTyped` after `InitBuddyAllocator` was popping a bootstrap-populated pool page that had never been canary-filled, triggering a halt very early — before klog/serial were ready, so the halt path itself faulted (HSHEFAIL FAR=0x20). Fixed by also calling `fillFreeCanary` inside `buddyAddRange` so all pool pages start canary'd.

5 × 180s ARM64 HVF: C1 boot-hang, C2 short clean (~20s+, 346K verifies), C3 boot mspan crash (`nelems=1008 nalloc=23628` at mail-app initial cache rebalance, no clicks, fired before first periodic [status] print), C4 163s click-driven clean (162K fills / 362K verifies), C5 boot-hang. **0 canary hits across ~1.5M+ verify operations including the C3 crash run.**

**H-T3a ruled out.** The corrupting write is NOT happening between `BuddyFreeTyped` and the next `BuddyAllocTyped` of the same PA. The most likely surviving mechanism is **H-T3b: kernel writes AFTER the freed PA has been reissued and legitimately used** — the canary is overwritten by the new owner's first store, then a kernel path with a stale handle writes through. The canary cannot see this case.

The most plausible channel for H-T3b is the **linux page-cache writeback path** (`sysWrite` / `flushWriteBuf` / `updateCachedPages` / `flushAndCleanupPages` / `handleFlushReply`). Earlier session `12e5f0d` added a partial-munmap range guard there; a different variant may still exist.

### Late-session: Stage 2 read-only page-cache audit complete

Read `maz/linux/page_cache.go`, `maz/linux/syscalls.go` (relevant excerpts: sysClose, sysFtruncate, sysWrite, flushWriteBuf, sysMmapPageFlush, mmap-fill handler), `maz/linux/main.go` (delegate-handler goroutine setup), `kmazarin/ksyscall/munmap.go`, `kmazarin/ksyscall/mmap_writeback.go`, plus traced direct `BuddyFreeTyped` callers and `RefCount` mutators.

**Result: protocol invariants I1–I5 (in `findings.md`) hold in mainline.** No straightforward bug found. But surfaced two specific paths worth targeted instrumentation:

- **Suspect 5 (PRIMARY) — `sysMmapPageFlush` `!inumKnown` fallback over-flushes.** Lines 1271–1302 of `maz/linux/syscalls.go`: when kernel sends a non-allFDs MmapPageFlush IPC for a fd that has been freed by a prior sysClose (close-before-munmap), the handler falls back to `FlushAllPagesForSID` + `RemoveAllBatch`, draining the cache for **every inum the sid has cached** — including ones whose file mappings are still live in the caller's PT. Code comment at lines 1268–1273 documents the fallback as "better than leaking" but the cure is itself the corruption mechanism if other live file mappings exist for the sid.
- **Suspect 1 — `[pageCache:OVERWRITE]` coverage gap.** Current overwrite log fires only when `cache.Add` finds an existing entry with a *different* VA (`old.VA != va` at `page_cache.go:78`). A re-fault into the same offset that lands on the same handler VA wouldn't fire.

### Next session

**Stage 3: instrument Suspects 5 and 1, run 5 × 180s.** Both are ~1-line diagnostic-only probes. See `task_plan.md` TOP OF STACK for files-to-touch table. If `[pageCache:FALLBACK_ALLFDS]` fires + crash reproduces in same run → Suspect 5 confirmed; design and propose a fix. If `[pageCache:OVERWRITE]` (broadened) fires → Suspect 1 confirmed; investigate the re-fault path. If both silent + crash reproduces → pivot to H-T1' (ASID swap on munmap) or VirtIO DMA target-PA audit.

### Stopping point

Six commits on the bug-B-family chain: `3942ae8`, `8a64a92`, `4460c14`, `ca7f5f6`, `612ed58`, `c4684ad`. Tracking docs current. Continuation prompt for the next session at `next_session_prompt.md`.

**Diagnostic toggles in tree:**
- `stalePTECheckEnabled` (`kmazarin/kmem/stale_pte_check.go`) — default false. Telemetry: `stale-pte: enabled scans hits` on [status] line.
- `freeCanaryEnabled` (`kmazarin/kmem/free_canary.go`) — default false. Telemetry: `free-canary: enabled fills verifies hits` on [status] line.

---

## Session: 2026-04-27 (afternoon, Opus) — font leak fix + bug B family localized to kernel

### Findings

The earlier `freeIndex is not valid` crash was attributed to "GC walks `*goFont.Face`'s slice into shared kernel pages and corrupts mspan". **Mechanism is wrong** — Go's `findObject` returns nil for non-arena pointers (shared pages live at 0x500000000000+, Go arenas at 0xC000000000); marking sets bits, doesn't write to mspan struct fields. `[versai:timing]` log prefix in `mancini/std/web_interactor.go` is just hardcoded text; the crashing process is **mail-app** (running mail.elf with louis14 + WebInteractor), not versai.

The actual bugs surface as a family of intermittent kernel hangs / mspan-corruption crashes in the page-management chain. Same root, multiple symptoms.

### Changes

- **A+B leak fix** (commit `3942ae8`): fontsvc `releaseTempSlot` calls `mem.FreePages` on the 4 MB cache pages (was a TODO). provider `CloseTemporaryFont` calls `mem.Munmap` on shared cache+fontData VAs (was just nil'ing the slot, leaving RefCount ≥ 1 forever). Verified: no Buddy OOM, slot reuse works, no errors from new call sites.

- **Caller-first close order + diagnostic checkpoints** (commit `8a64a92`): `provider.CloseTemporaryFont` now munmaps **before** sending the close IPC. Previous order had mail-app's `mem.Munmap` silently hanging after fontsvc's `releaseTempSlot` ran ahead of it. Added checkpoint logs at every step of close (mail-app and fontsvc sides) plus `[munmap:FREED]` in the kernel for shared-page-returns-to-buddy. PD_SHARED filter cuts kernel log volume ~100×.

### Runs

| Build | Symptom | Localization |
|-------|---------|--------------|
| Pre-A+B | 32 kind=1 populates, 27 560 `no free font slots`, no crash | Permanent slot exhaustion confirmed |
| A only (measure→OpenTemp) | 418 kind=2 populates × 4 MB = 1.67 GB → Buddy OOM | Fixed permanent leak, exposed `releaseTempSlot` TODO leak |
| A+B | Slots reuse, no OOM; `sweep increased allocation count` mspan corruption hit | Bug B family still present |
| A+B + caller-first | First close completed cleanly. Close 2 hung at fontsvc reply (`uring.Send`?). | Hang shifted to fontsvc-side reply path |
| A+B + checkpoints | First close hung in fontsvc `releaseTempSlot` (kernel `mem.FreePages` of 1024 pages). | Localized to `unmapUserPages` → `ReleasePageByPA` → `BuddyFreeTyped` |
| A+B + checkpoints (different boot) | mspan corruption at boot during cache rebalance, **no font activity** | Bug fires from any heavy alloc, not font-specific |

### Conclusion

A+B is a real fix and is committed. The remaining symptoms (mspan corruption crashes, kernel hangs) are a single pre-existing kernel bug in the page-free chain. `fontsvc.mem.FreePages(cacheBase, 1024)` is the highest-frequency way to hit it because it frees a large contiguous block including shared pages with active RefCount on the other shepherd, but the bug fires independent of font activity (boot-time cache rebalance produced an mspan crash with zero font calls).

### Next target

**Kernel-side instrumentation of `BuddyFreeTyped` + `releasePageByPA`**, per the new task_plan.md TOP OF STACK. Hypotheses H-K1..H-K4 there. Key suspect: concurrent decrement-and-free race in `releasePageByPA` between `SyscallMunmap` and `SyscallFreePages` on shared pages; if confirmed, fix is a lock around the RefCount manipulation or a double-free guard in `BuddyFreeTyped`.

### Late-session: kernel diagnostic pass (commit `ca7f5f6`)

Added bug-B-family kernel guards on the suspected double-free / RefCount-race path:

- `BuddyFreeTyped`: `buddyContainsPA` walk + `buddyDoubleFreeHalt` halt-with-marker if a PA is already on the free list before insertion.
- `releasePageByPA`: `[kmem:UNDERFLOW]` log when called on a page with `RefCount<=0`.
- `unmapUserPages`: `[unmapLoop] enter/progress/exit` for ≥64-page frees, with per-256-iteration `va`/`pa` checkpoints to localize a hang inside the loop.

**Diagnostic run result: all guards silent.** Crash fired at boot during mail-app's initial cache rebalance, `nelems=512 nalloc=41380`, with **zero font activity** in the run. 16 `[unmapLoop]` events fired (sid=21 shepherd-launch buffer cleanups, 1599 pages each, NOT IPC region). No `DOUBLE FREE`, no `kmem:UNDERFLOW`, no `[provider:close]`, no `[fontsvc:release]`.

That eliminates H-K1 (double-free), H-K2 (stale free-list), and the `releasePageByPA` race as the corrupting mechanism. The corruption is happening from a path that doesn't go through the kernel page-management RefCount accounting at all.

### Refined theory for next session

Three new hypotheses replacing H-K1..H-K4:

- **H-T1 (PRIMARY): stale TLB after `SyscallMunmap`.** That path doesn't `TlbiVMALLE1`+`DsbISH`+`IsbSY` after removing PTEs. `SyscallFreePages` does. A shepherd that munmapped a page can still write to it via stale TLB until something else flushes; if the PA is reallocated to mail-app's heap, that stale write corrupts the new owner.
- **H-T2: stale PTE in another shepherd's page table** — silent failure of `UnmapUserPageWithL0` leaves the entry mapped. Same write-through-stale-mapping outcome.
- **H-T3: direct kernel-side write into a freed page** — kernel scratch mapping, DMA, page-cache writeback hitting a recycled PA.

**Note on the `[unmapLoop]` data:** the 16 large unmaps that DID fire were all sid=21 ELF-load scratch buffer cleanups (1599-page user-region frees, no PD_SHARED). They go straight to buddy and could be reissued to mail-app's heap. If those frees leave stale TLB or PTE state, that's a candidate trigger for the boot-time crash.

### Stopping point (this session)

Four commits clean:
- `3942ae8` — A+B font leak fix.
- `8a64a92` — caller-first close order + diagnostic checkpoints.
- `4460c14` — tracking docs retargeted (freeIndex GC-walk hypothesis retracted, kernel page-free chain named as target).
- `ca7f5f6` — kernel double-free / underflow / loop-progress guards (silent in run, ruling out H-K1..H-K4).

louis14 `pkg/text/measure.go` change owned by louis14-side Claude; per memory rule we do not edit louis14 from mazzy.

Tracking files updated with focused next-session plan including Option A (TLB flush in `SyscallMunmap`, H-T1 fix-test) and Option B (stale-PTE detection at `BuddyAllocTyped` time, H-T1/T2/T4 diagnostic). See `task_plan.md` TOP OF STACK for run plan and files-to-touch tables.

---

## Session: 2026-04-27 — maildb console routing + freeIndex versai crash

### maildb console routing (Task 1 complete)

- Created `maz/maildb/mlog.go`: `mlogInfo` (console-only, drops if full) +
  `mlogErrorf` (console red + stdout fallback). Set via `mlogSetSink`.
- `mazarin/maildbio/maildbio.go`: added `StatusLine{Text string, IsError bool}`;
  changed `StatusCh`/`StatusChannel()` from `chan string` to `chan StatusLine`;
  bumped buffer 16→256.
- `maz/maildb/main.go`: `mlogSetSink` call added; `notifyStatus` closure
  removed; all `fmt.Printf`/`Println` swept to `mlogInfo`/`mlogErrorf` except
  two pre-sink startup lines.
- `maz/maildb/mbox_import.go`: throttled "Stored"/"Indexed" to every 50th
  message; "Storage done: N items stored" + "Indexing done: N items indexed"
  at end of each operation; removed `notify func(string)` params throughout.
- `maz/maildb/mail_handler.go`, `maz/maildb/collection.go`: swept all
  `fmt.Printf`/`Println` to `mlogInfo`/`mlogErrorf`.
- `maz/mail-ui/main.go`: drain updated to consume `StatusLine`; error lines
  rendered in `console.StderrColor()`.

### Run results (2 × 180s ARM64 HVF)

| # | Uptime | Result | Notes |
|---|--------|--------|-------|
| 1 | 180s   | clean  | mlog changes stable; mail console output correct; 2× body click ok |
| 2 | 180s   | CRASH  | `fatal error: freeIndex is not valid` in versai on 2nd message click |

Second run crash sequence:
```
[mail] body: 56448 bytes variant=1          ← second message click
[provider] populateSlot client=1..8 kind=1  ← temp font slot population
fatal error: freeIndex is not valid          ← versai heap corrupted
```

### Root cause analysis — `freeIndex is not valid` in versai

See `findings.md` for full details. Short form:

`populateSlot` (`mazarin/fontcache/provider.go:150`) calls
`goFont.ParseTTF(bytes.NewReader(fontData))` where `fontData` is
`unsafe.Slice` over a shared kernel page — not Go heap. The returned
`*goFont.Face` has internal slice headers pointing into those shared pages.
Go GC walks those as live heap pointers, reads raw font binary data as
pointer-sized values, and corrupts mspan structs (here: `freeIndex` field).

Secondary issues confirmed in source:
- `releaseTempSlot` (fontsvc/main.go:992) leaks cache pages (TODO comment confirms, no `mem.FreePages`)
- fontData dual-mapping: versai holds VA-A (its own AllocPagesSlice) AND
  VA-B (fontsvc reshares the same pages back), RefCount=3 per page, neither
  mapping ever unmapped

### Stopping point

`findings.md` updated with full 4-hypothesis analysis. `progress.md` and
`task_plan.md` updated (this entry). Fix work not yet started.

---

## Session: 2026-04-26 (Opus) — partial-munmap fix + ftruncate warning

Re-audit of the dual-mapping flow on the linux handler side identified two
concrete bugs that fit the GC-crash symptoms much better than Sonnet's
"live dual-mapping concurrent write" hypothesis. See `findings.md` for the
crash analysis.

### Bug B fix (proper) — range-aware `MmapPageFlush`

- `kmazarin/ksyscall/delegate.go`: added `FlushOffset` / `FlushLength` to
  `DelegateCallInfo`.
- `kmazarin/ksyscall/mmap_writeback.go`: `flushAndCleanupPages` takes
  `(startOffset, length uint64)`; passed in `Args[2]`/`Args[3]` of MmapPageFlush
  IPC (initial round + round-2+ resend).
- `kmazarin/ksyscall/munmap.go`: passes
  `fm.FileOffset + (alignedAddr - fm.StartVA)` and `alignedLength`. Warns
  `[munmap:PARTIAL]` when `alignedLength != fm.Length`.
- `WriteBackSharedMmapOnDeath`: passes `0, 0` (length=0 retains "all").
- `maz/linux/page_cache.go`: new `RemoveRangeOffsetBatch(sid, inum, startOffset,
  length, max)`.
- `maz/linux/syscalls.go::sysMmapPageFlush`: reads `Args[2]/Args[3]`; uses
  range-bounded `RemoveRangeOffsetBatch` when `length > 0`. Death cleanup
  (`fd == 0xFFFFFFFF` or unknown inum) keeps `RemoveAllBatch`.

### Bug A (warning, fix deferred)

`sysFtruncate` logs `[ftruncate:LEAK]` with the count of orphaned cache
entries. Full fix requires a new kernel-side "drop-these-handler-VAs" IPC.

### Instrumentation (silent on happy path)

- `[pageCache:OVERWRITE]` — `cache.Add` replaces an entry with a different VA.
- `[munmap:PARTIAL]` — covered above.

### Run results (2 × 120s ARM64 HVF)

| # | Uptime | Result | Notes |
|---|--------|--------|-------|
| 1 | 51s    | clean  | mail booted, body fetched, GC ran 256+ cycles |
| 2 | 103s+  | clean  | mail-ui body display, then unrelated `attr.Set slot=527` panic |

No GC crash. None of the new warnings fired. Pre-fix recurrence was ~1-in-5;
clean runs are consistent with either fix-correct or not-yet-triggered.

### Stopping point

Committed (`12e5f0d`). Ready for 5 × 180s discrimination runs.
