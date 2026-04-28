# Task Plan — Mazarin / Mazzy

## TOP OF STACK: `diag/mail-elf-load-hang` — linux per-request goroutines + fti.maz (2026-04-29)

**Branch:** `diag/mail-elf-load-hang`, off `fix/uring-missed-retries@e7422c5`.

**Commit:** `f466010` — `linux: per-request goroutines for delegated syscalls + fti.maz migration`. 12 files, +230/-62.

**What was done:**
- **fti.elf → fti.maz migration (stage 1 of "ONE shepherd binary" cleanup).** Dual-build pattern in `maz/fti/Taskfile.yml` mirroring maildb's. `MazarinMain` shim added to `maz/fti/main.go`. `Taskfile.yml` root + `disk-arm64`/`disk-x86_64` updated. `config/startup.{arm64,amd64}.toml` flipped to `/fti.maz`.
- **Linux dispatcher concurrency.** Migrating fti to .maz exposed a head-of-line block: 3 shepherds stuck on `Readlinkat` for 110+s because linux's file-lane was a single goroutine and one slow `fsclient.call` queued every other delegated syscall behind it (even stateless ones). Fix: file lane spawns `go handler.handle(req)` per `SyscallRequest`. Per-shepherd ordering preserved by `ShepherdFilesystemData.mu`. Cross-shepherd state (`syscallHandler.shepherds`/`orphanHandles`, `pageCache`, `flockTable`) gets short-held mutexes. `FlushAllPagesForInum/SID` converted to snapshot-then-write so `pc.mu` isn't held across fsclient. Notifications (death/stdinDecRef/idleFlush) stay on the reader. `fsclient.Client` was already self-locked.
- **Launch-path checkpoint instrumentation** (separate, supports the original DIVERSION). `kmazarin/ksyscall/runshepherd.go` gained post-unmap / post-FB+constraint-map / pre-loadELF checkpoints. `maz/fs/main.go` gained post-Open / pre-ReadInto / read-done / calling-RunShepherd checkpoints in `launchShepherd`/`launchPluginShepherd` and in `readFileIntoPages`. One-shot per launch.

**Verified across 5×180s G-/H-cadence sweep + initial G2 boot test:**

| Run | Outcome | Notes |
|-----|---------|-------|
| G2  | clean 171s | initial phase-2 boot test (unbounded `go ...`) |
| G3, G4, G5 | clean | unbounded `go ...`, transient ms-scale stalls |
| G6  | SIGSEGV | direct unsynced read of `h.cache.data` — fix `37f1956` |
| H1  | SIGSEGV | runtime unwinder under goroutine churn — fix: worker pool `ef449b5` |
| H2  | steady state then `bug_attr_init_crash` | pre-existing constraint-VM bug, unrelated |
| H3  | clean 180s | numGoroutine=1041 stable |
| H4  | clean 180s | numGoroutine=1041 stable |
| H5  | mid-boot stall | 3 shepherds wedged on per-shepherd lock; underlying fs-reply wedge — see "Not yet done" #3 |

Two real bugs caught and fixed during the sweep: (1) `pageCache.data` direct map access bypassing `pc.mu` in `sysMmapPageFlush`'s diagnostic block (commit `37f1956`); (2) unbounded `go h.handle(req)` was generating ~14k goroutine spawns per 180s run, exposing a runtime crash in `traceback.go:resolveInternal` during copystack of freshly-spawned workers (likely interacting with goroutine leakage when fsclient.call wedged) — replaced with 1024-worker persistent pool (commit `ef449b5`).

**Active plan:**

### A. Shepherd unification — DONE

Stage 2 (`mail.elf` → `mail.maz`, commit `e9247bc`) and stage 3 + cleanup (drop dual-builds + delete `launchShepherd` legacy body, commit `aabdfa2`). All shepherds in startup.toml are now `.maz` plugins launched via `/shepherd.elf`. Disk ships only `.maz` for fti / maildb / mail (plus rachel / linux / fontsvc / linux-ui etc. that were already plugin-only). `launchShepherd` in `maz/fs/main.go` is a single-path function — the legacy ET_EXEC body is gone.

**Verified:** I1 reached full mail-app steady state then hit the pre-existing intermittent attr.Init/exit_group family (unrelated to migration). I2 clean 154s+. J1 (post-cleanup) clean 172s. Mechanically working.

### B. Underlying fs-reply wedge — triage (DEFERRED — pick up after A)

Reproduced in G-fti-maz-1 (3 readlinkats stuck 110s) and H5 (3 shepherds wedged 60s+, sid=20/28/29). Same signature: a worker holds its per-shepherd `shep.mu` waiting for an fs reply that never comes; subsequent same-shepherd syscalls queue. ~1-in-5 rate at 180s. Phase 2 + worker pool prevents system-wide impact (other shepherds continue) but the wedged shepherds hang.

**Hypotheses to investigate:**
1. **fs's single-goroutine serve loop is the bottleneck.** While fs is mid-read on one large file (e.g., 23 MB LoadFile), it can't process incoming `fsIPCCh` requests. If a linux worker is doing fsclient.call (which goes through fs's `fsIPCCh`), it waits for fs's serve loop to come around. If the serve loop is mid-LoadFile for many seconds, the linux worker waits many seconds. Backed by H5's wedge appearing during the rachel-plugin-load phase when fs is reading rachel.maz / fontsvc.maz / etc.
2. **Reply was sent but linux's RespCh demuxer dropped it.** fsclient.Client.RespCh has capacity 4. If multiple replies arrive faster than the dispatch reader pumps them, kernel-side ring fills. Our recent uring fix gives 10ms block-with-deadline; after that the sender sees EAGAIN and the request is lost (no retry on fsclient side). Audit fsclient for "uring.Send EAGAIN swallowed" paths.
3. **Cleanup-on-disconnect race.** If a shepherd dies during fs IPC, fs might tear down state without replying to in-flight requests.

**First steps when picking this up:**
- Add timeout to `fsclient.callLocked` (e.g. 30s). On timeout, log + return error so the worker unwedges.
- Add an instrumentation log in fs's serve loop: when does it pick up an `fsIPCCh` message vs an `fsDelegateCh` message? Are LoadFile delegate operations starving IPC requests?
- Reproduce H5 deterministically (it appeared during heavy concurrent boot LoadFiles — should be reproducible by adding more shepherds to startup.toml or by making the launches more parallel).

**Reminders:**
- The original DIVERSION (mail.elf-load boot hang) hasn't fired in any phase-2 run. Could be: (a) related to the same delegate-saturation pattern that phase 2 fixes, (b) instrumentation perturbed timing, (c) intermittent and we got lucky. Instrumentation is in place for next time.

---

## ARCHIVED: `fix/uring-missed-retries` — kernel-side block-with-deadline (2026-04-28 night)

**Branch:** `fix/uring-missed-retries`, off `feature/mail-dumb` at `68a7254`. Now committed (b907e8d kernel, 4ba1a15 userspace, e7422c5 docs). `diag/mail-elf-load-hang` builds on top.

**Why:** F1-F15 chase identified the root cause of `OpenFontReply EAGAIN`: synchronous UART writes (`klog.Criticalf`, etc.) run with ARM64 SVC's hardware-default DAIF.I=masked, and on `-smp 1` QEMU this IRQ-blocks the entire system for ~7 ms per call. Receiver's userspace reader gets starved → ring fills → sender gets EAGAIN. See `memory/sync_uart_irq_masked.md` for the architectural write-up.

**Architecture (agreed with user):**
- **Kernel-side block with deadline** for both `SyscallUringSend` and `pushStringFull` (`topHalfUartRing` push). 10 ms ceiling, woken early by drainer; deadline expiry surfaces EAGAIN cleanly.
- **First-come-first-served, single blocker per slot.** Second sender returns -EAGAIN immediately.
- **Userspace pacing**: 3-attempt retry in `mazarin/uring/syscall.go` with `nanosleep` (real deadline-queue block, not yield) when a previous attempt returned faster than the per-attempt deadline. No `runtime.Gosched()` anywhere — one was found in `pushStringFull`, removed; userspace had a 256× Gosched retry, removed.
- **`Send`/`Recv`/`Connect`** are one-line ring-0 wrappers; `*WithRing` are the primitives.

**Status:** Steps 1-7 done. Builds clean. F16-F20 sweep: 3 clean reached-steady-state runs (F16 162s, F17 171s, F19 133s) + 2 pre-existing mail.elf-load boot hangs (F18, F20 — same site as B2/B5, A2, C1/C5; not from this branch). Across all 5: 0 retried, 0 FAILED, 0 fontsvc errors, 0 uart-ring dropped, 0 panics. **Fix verified at F-cadence; ready for commit pending diff review.** Uncommitted. See `progress.md` for per-step detail.

- Step 1 (DONE): `ThreadBlockedUringSend` / `ThreadBlockedKernelRingPush` states + `Thread.UringSendBlockedSlotPtr` / `UringSendDeadlineExpired` / `UringIPCSlot.BlockedSenderTID,BlockedSenderPtr` data shape.
- Step 2 (DONE): kernel uring block path — `UringSendKernel` parks userspace senders with 10 ms deadline, single blocker per slot, race-free publish under `schedulerLock`, deadline-expired short-circuit on rewind, `WakeSenderAfterDrain` from `SyscallUringRecv`, `processStaticDeadlinesSchedLockHeld` deadline branch, `CleanupUringIPCForShepherd` wakes blocked senders on receiver death.
- Step 3 (DONE): same block-with-deadline pattern for `pushStringFull` + `topHalfUartRing` consumer, dropped the last kernel `runtime.Gosched()`, added `pushBlockerThreadPtr` + `pushBlockerDeadlineExpired`, `[status]` line gained `uart-ring: dropped=N`.
- Step 4 (DONE, this session): `mazarin/uring/syscall.go` rewrite — `SendWithStats` removed; `SendWithRing` is the primitive with 3-attempt retry, conditional `time.Sleep(10 ms)` pacing (only when prev attempt fast-bounced in <8 ms), single `fmt.Printf` log on retry-success / exhausted failure, silent on first-try success; `Send` is a one-line ring-0 wrapper. Zero `runtime.Gosched()`.
- Step 5 (DONE, this session): `maz/fontsvc/main.go` reverted — `shareCacheAndReply` back to 4-arg signature, `uring.SendWithStats` → `uring.Send`, `OpenFontReply FAILED/retried` instrumentation gone, `itoaBytes` helper dropped, `rawPutsInt` restored to inline form.
- Step 6 (DONE prior session): `$GO tool task` clean; `run-arm64-hvf TIMEOUT=180` clean — kernel stable, `uart-ring: dropped=0`, no retry/FAILED log lines surfaced (idle no-click run).
- Step 7 (DONE 2026-04-29): F16-F20 5×180s ARM64 HVF no-click sweep. F16/F17/F19 clean reached-steady-state; F18/F20 hit pre-existing intermittent mail.elf-load boot hang (not a regression — same site as B2/B5/A2/C1/C5). Across all 5: 0 retried / 0 FAILED / 0 fontsvc / 0 dropped / 0 panic. Fix verified.

**Reminders / non-negotiables:**
- No `runtime.Gosched()` anywhere in this fix — by user policy: "yields cover up bugs that will bite later." Both kernel-side and userspace-side are now Gosched-free.
- No new architecture additions beyond the agreed scope without further discussion.
- Don't commit until user reviews the diff.

---

## DIVERSION: intermittent mail.elf-load boot hang (cross-session) — instrumentation landed, hang didn't fire under phase 2

**Status update (2026-04-29):** Phase-2 boot-test 171s clean — hang did NOT reproduce. Instrumentation (post-unmap / mapped FB+constraint / pre-loadELF in kernel; post-Open / pre-ReadInto / read-done / calling-RunShepherd in fs) is in place on `diag/mail-elf-load-hang@f466010` for the next reproduction attempt. May or may not still happen — phase 2's removal of the linux file-lane head-of-line block could have eliminated the trigger if it was related to delegate-dispatcher saturation. Track but don't actively chase until it recurs.

**Symptom:** Boot reaches `[fs] launching mail from /mail.elf` → `[fs] reading /mail.elf...` → optionally `[RS][RunShepherd] start name=mail pages=6644 bytes=27210767` and/or `[RS][RunShepherd] mail: copied 27210767 bytes from user`, then **silence**. No further log output for the full 180s timeout. No panic, no data abort, no `EE25` / `EXIT_GROUP`. No status lines ever print, so we have no telemetry from the hung run.

**Sites observed (independent of branch / instrumentation):**
- B2, B5 — Option B stale-PTE verifier session (2026-04-27 late afternoon)
- A2 — Option A trailing-`TlbiVMALLE1`-on-munmap session (same)
- C1, C5 — H-T3 free-canary session (2026-04-27 evening)
- F18, F20 — `fix/uring-missed-retries` step-7 sweep (2026-04-29)
- (Plus a baseline-no-instrumentation hit at "[fs] reading /shepherd.elf" earlier — likely the same family but at the prior shepherd in the launch chain.)

**Cross-cutting observations:**
- Hits ~1-in-3 to 1-in-5 of 180s ARM64 HVF runs. Independent of branch (seen on `feature/mail-dumb` baseline AND on this branch's tree).
- Always at the same point in the launch sequence: just after the kernel finishes copying mail.elf bytes and before mail-app's first goroutine runs / first `[mail] main() entered` log.
- Never produces any kernel-side panic or post-mortem print. The system is not dead — it's stuck somewhere that doesn't loop or fault, just doesn't progress.
- Has occurred with very different working-tree states (different probes / instrumentation / branch-specific changes), so it's not gated on any one of those.

**What we DO NOT know yet:**
- Whether it's mail-app userspace stuck during Go-runtime init / dynamic loading, or a kernel-side stall in the post-`copyPagesFromUser` / `loadELF` / first-thread-creation path.
- Whether `[uring:reader] ring1 got msg #2 proto=9` (which fires just before the hang in F18) is related — it appeared in F18 but NOT in F20 immediately before the hang.
- Whether disabling some of the launch-time instrumentation makes it more or less frequent (no controlled study yet).

**Why diverted, not paused:** The boot hang is NOT something this branch's work touches (`fix/uring-missed-retries` is about ring-full block-with-deadline, which doesn't run during ELF load), and it's NOT the bug-B-family target either (which is the mspan-corruption / GC-crash family that fires AFTER mail-app reaches steady state). It's a third, independent, pre-existing issue that's been hiding in the background.

**Plausible candidates (not yet investigated):**
1. **`preGrowStack` interaction with mail.elf** — mail.elf is the largest binary in the launch chain (6644 pages = 27 MB). The preGrowStack workaround (`MEMORY.md` § ".maz Morestack Bug") forces stack to 64 KB; if the goroutine running `mazMain` for mail-app somehow doesn't go through that path on certain timings, the hang would match a stack-growth-into-bad-newstack lockup.
2. **Demand-paging stall during init** — large user-text region (>27 MB) means many demand faults early; if any single fault path can deadlock against the launcher, we'd see exactly this.
3. **Linux shepherd dispatcher-not-yet-ready race** — `[uring:reader] ring1 got msg #2` in F18 before the hang suggests linux shepherd is processing IPCs; if the ordering between "linux is up" and "mail-app starts making syscalls" can flip, an early mail-app syscall might wait for a service that's not registered yet.

**Next-action when this is picked up (revised 2026-04-29):**
- F-cadence reproduction attempt on `diag/mail-elf-load-hang@f466010` (or successor) — 5×180s ARM64 HVF, watch for the silence-after-mail-launch signature. The new checkpoints will say which silent gap the hang is in.
- If hang fires between `copied X bytes from user` and `unmapped N caller pages` → kernel `unmapUserPages` of 6644 pages.
- If between `unmapped` and `mapped FB+constraint` → `CreateProcessPageTable` / `MapUserFramebufferWithL0` / `MapUserConstraintPagesWithL0`.
- If between `mapped FB+constraint` and `pre-loadELF` → `buildSymbolTable` / `findHighestVA`.
- If between `pre-loadELF` and `loadELF ok` → `loadELF` itself.
- If between `loadELF ok` and `created userspace thread` → `CreateUserspaceThread`.
- If between `created userspace thread` and userspace-side `[mail] main()` → kernel never schedules the new thread, OR mail's runtime hangs before first syscall.
- For the fs-side (F18-style) variant: post-Open / pre-ReadInto / read-done split says whether `Open` returned, whether `ReadInto` was entered, and how far the 27 MB read got.

**Status:** Tracked, instrumented. Resume only if the hang recurs on the new branch.

---

## PAUSED: bug B family — VA-collision strongly disconfirmed, GC mspan crash didn't reproduce in 15 boots

**Branch:** `feature/mail-dumb`
**Last commits:** `3942ae8` (A+B font leak fix) → `8a64a92` (caller-first close + checkpoints) → `4460c14` (docs retarget) → `ca7f5f6` (kernel double-free / underflow / loop-progress guards) → `612ed58` (Option B stale-PTE verifier — H-T2 ruled out) → `c4684ad` (free-canary — H-T3a ruled out) → `8b91d34` (maildb console routing) → `24ee044` (Stage 3 page-cache probes) → `b039800` (Stage 4 prep — GOGC=5 + VA-collision probe) → `ade5319` (docs) → `459dab0` (gate VA probe — fix click-induced regression) → `2fbd078` (docs)

### Where we are (2026-04-28)

Stage 4 prep landed. GOGC=5 for mail-app verified active (`gc=6176` in 180s vs prior tens). VA-collision probe in `SyscallSharePages` produced **132 [fontslot:VA] entries from one boot — every VA in `0x500000xxxxxx` (IPC region), none in mail-app's Go heap (`~0xC000000000+`).** Probe was unconditional; clicking once after boot triggered a heavy SharePages burst during body render, the synchronous Criticalfs regressed the system to a kernel `runtime.throw()` / `exit_group` with no panic message visible. Probe now gated behind `vaCollisionProbeEnabled` (default false).

**Provisional read on VA-collision hypothesis:** weakened. The kernel's SharePages target VA picker is consistently picking from the IPC region — Go GC's `findObject` returns nil for non-arena pointers, so marking should never walk into 0x500000xxxxxx. One sample is not conclusive but the consistency is a strong signal. To fully rule out we need a crash run with the probe firing — see Stage 4 plan below.

### Where we are (2026-04-27 evening)

The font close cycle exposes an intermittent kernel bug that always corrupts an mspan struct field in mail-app's heap. Seven diagnostic rounds have ruled out:

- **Buddy double-free / RefCount underflow / unmapLoop hang** (`ca7f5f6` guards silent).
- **H-T2 stale PTE in another shepherd's PT memory** (`612ed58` Option B verifier silent across 5 × 180s, 184K–203K scans/run, 0 hits).
- **H-T1 (specifically: missing trailing TLB flush at `SyscallMunmap`)** — Option A reverted as a no-op (per-page `tlbiVAE1IS` already broadcasts in IS domain; trailing local `TlbiVMALLE1` is redundant).
- **H-T3a kernel write between `BuddyFreeTyped` and reuse** — `c4684ad` free-canary 5 × 180s, ~1.5M+ verifies aggregate, 0 hits, including a confirmed crash repro (C3). The corrupting write is NOT in the free→reuse window.
- **Page-cache audit (Stage 2 read-only)** — protocol invariants I1–I5 in `findings.md` all hold in mainline. Audit surfaced Suspects 5 and 1 for Stage 3.
- **Suspect 5 — `sysMmapPageFlush` `!inumKnown` fallback over-flush** — Stage 3 probe `[pageCache:FALLBACK_ALLFDS]` applied and ran 6 × crash-eligible runs (smoke + 5 × 180s). **0 fires.** Disproven.
- **Suspect 1 — `[pageCache:OVERWRITE]` same-VA coverage gap** — Stage 3 broadened probe (dropped `old.VA != va` predicate) applied same runs. **0 fires.** Disproven.

### Strongest lead: crash timing is locked to a single program point

Every crash across all sessions fires at exactly the same point:
```
[provider] populateSlot client=0 server=4 kind=1 cacheLen=49152 fontDataLen=53504
...
[mail] cache ready, initial rebalance first=-1 last=-1 vis=0
[mem:linux] heap=NkB ...
<crash>
```
This is the mail-app-specific font slot (server=4) being populated, followed immediately by the first collection rebalance. The crash fires during the GC sweep that follows this allocation spike — before any click-driven activity. This temporal lock is the strongest signal we have. Corruption is happening in (or caused by) the populateSlot IPC / initial rebalance window, and the GC discovers it on the very next sweep.

### Active hypothesis: kernel maps font-cache pages at a VA that collides with mail-app's Go heap

H-T3b (stale-handle write after reissue) is no longer the primary frame. The Stage 3 results, combined with the crash-timing lock, point toward a **VA collision** during `populateSlot server=4`: the kernel maps the 12 font-cache pages (49152 bytes) into mail-app's address space at a VA that overlaps Go's heap region. When the GC next sweeps the span that happens to live at that VA, it reads font-file bytes instead of mspan struct data → `nelems`/`nalloc` nonsensical → crash.

This would also explain D1's variant failure: kernel EL1 data-abort write-fault at `FAR=0x9000000` (UART), `ESR=0x96000045` (translation fault L1, write). Kernel page-table pages could themselves be the victim in that run rather than mail-app's heap.

**H-T1' (residual)** — TLB holding stale translation past `tlbiVAE1IS`. Hard to test. Lower priority now.

### Preliminary experiment: GOGC=5 for mail-app

Before Stage 4 probes, run 5 × 180s with mail-app's GC throttle lowered from default 100% to 5% (matching other shepherds). Rationale: if the crash still locks to the same `populateSlot server=4` + initial-rebalance point under aggressive GC, it confirms the corruption is bounded to what happens in that window — not a delayed discovery of an earlier event. File to change: `kmazarin/ksyscall/launch.go` — ensure `GOGC=5` is set for mail.elf (verify it isn't already; CLAUDE.md says shepherds get GOGC=5 but confirm the actual code).

### Stage 4 pivot options (in priority order)

**Option 1 (FIRST, IN PROGRESS) — VA-collision probe.** Implemented in `SyscallSharePages` as `[fontslot:VA]` log, gated by `vaCollisionProbeEnabled` (default false; flip to true in `kmazarin/ksyscall/mailbox.go` for boot-only runs). One boot's data shows all VAs in `0x500000xxxxxx` (IPC region). To finish: re-enable probe, run **boot-only** 5×180s (no clicks — render-time SharePages traffic regresses the system), capture VAs from any crash run. If a crash repros AND VAs remain in 0x500000xxxxxx → VA-collision at SharePages layer fully ruled out → move to Option 3.

**Option 3 (SECOND) — VirtIO DMA target-PA audit.** maildb reads BBolt pages from disk via VirtIO block. If the block driver's DMA descriptor references a PA that was freed and reissued to mail-app as heap, the DMA write would corrupt it. Audit: check whether the DMA target buffer PA is derived from a user-mapped VA (which could be freed mid-request), or from a stable kernel-allocated buffer. Focus on `kmazarin/kvirtio/block*.go` and the descriptor-setup path.

**Option 4 (THIRD) — heap-corruption forensics.** Record exactly which mspan bytes are corrupted and what they contain. The value that overwrites `nalloc` or `nelems` could identify the source (font-file magic bytes, PTE values, IPC header fields). Add a small patch to `runtime.(*sweepLocked).sweep` or a pre-sweep hook that dumps the raw mspan bytes when corruption is detected. This would narrow the mechanism without needing to catch it in the act.

**Option 2 (FOURTH) — H-T1' proper test.** ASID swap on munmap (force TLB invalidation for a specific ASID), or a same-VA read probe immediately post-`tlbiVAE1IS` to verify the invalidation actually reached the CPU's TLB. More invasive than the above; try only if Options 1–3/4 come back clean.

**Side issue surfaced 2026-04-28** — `[maildb] send to SID 29 failed: resource temporarily unavailable` flooded ~151× during fti shepherd launch in E2/E3. Source: `maz/maildb/mail_handler.go::sendMailMsg` (uring.Send EAGAIN). Could be pre-existing (was previously routed through `fmt.Printf` and possibly silent in heavy boot logs) or a new ordering issue between maildb and a freshly-launched mail-app. Audit before drawing further conclusions about Stage 4 results.

### Run plan

1. ✅ Baseline: 5 × 180s → 1 mspan crash, 1 boot hang, 3 clean.
2. ✅ Option B verifier (`612ed58`): 5 × 180s → 1 mspan crash, 2 boot hangs, 2 clean. **H-T2 RULED OUT** (0 hits).
3. ✅ Option A TLB flush at SyscallMunmap end: 5 × 180s → 2 mspan crashes, 1 boot hang, 2 clean. Crash rate unchanged. **Reverted** (no-op vs per-page `tlbiVAE1IS`).
4. ✅ H-T3 Stage 1 sentinel-byte canary (`c4684ad`): 5 × 180s → 1 mspan crash (C3, no clicks), 2 boot hangs, 2 clean (one click-driven). Canary 0 hits across ~1.5M+ verifies. **H-T3a RULED OUT.**
5. ✅ Stage 2 page-cache audit (read-only): protocol invariants I1–I5 hold in mainline; surfaced Suspects 5 and 1.
6. ✅ Stage 3 Suspect 5 + Suspect 1 probes: smoke + 5 × 180s → 2 crashes (D1 kernel EL1 abort, D2 mspan `nelems=341 nalloc=4024`), 3 clean. **0 probe fires across all runs.** Suspects 5 and 1 DISPROVEN.
7. ✅ Stage 4 prep — GOGC=5 plumbing + VA-collision probe. E1 (180s, probe ON, click) → 132 [fontslot:VA] entries all in 0x500000xxxxxx, then click→`KERNEL EXIT GROUP` regression. Probe gated. E2/E3 (180s each, probe OFF, no clicks) → 2 clean runs at GOGC=5 (mail-app gc=6176). Crash rate 0/2 within noise vs baseline 1/5.
8. **NEXT — boot-only 5×180s with `vaCollisionProbeEnabled=true`.** No clicks. Capture probe data from any crash run. Decision tree below.

### Reminders — diagnostic toggles in tree

- **Option B stale-PTE verifier** at `612ed58`. Default `stalePTECheckEnabled = false` in `kmazarin/kmem/stale_pte_check.go`. Telemetry on `[status]` line: `stale-pte: enabled scans hits`. Flip the var to true to re-enable for any future PT-memory diagnostic.
- **Free-canary** at `c4684ad`. Default `freeCanaryEnabled = false` in `kmazarin/kmem/free_canary.go`. Telemetry on `[status]` line: `free-canary: enabled fills verifies hits`. Flip the var to true to re-enable for any future free→reuse-window diagnostic.
- **VA-collision probe** at `b039800`/`459dab0`. Default `vaCollisionProbeEnabled = false` in `kmazarin/ksyscall/mailbox.go`. **Boot-only safe** — heavy SharePages traffic during body-render after click regresses the system to kernel exit_group. Logs `[fontslot:VA] caller=N target=M va=X type=T` per SharePages call.

### Active instrumentation (already in tree)

- `[munmap:FREED]` (kernel) — fires when a PD_SHARED IPC-region page returns to buddy. `8a64a92`.
- `[fontsvc:release]`, `[fontsvc:close] preRelease/postRelease/preSend/postSend/postReply` — `8a64a92`.
- `[provider:close] enter/preMunmapCache/postMunmapCache/preMunmapFont/postMunmapFont/preIPC/postIPC/exit` — `8a64a92`.
- `[BUDDY] DOUBLE FREE!` halt — `ca7f5f6`. Has not fired.
- `[kmem:UNDERFLOW]` — `ca7f5f6`. Has not fired.
- `[unmapLoop] enter/progress/exit` — `ca7f5f6`. Fires for ≥64-page frees.
- Older from `12e5f0d`: `[munmap:PARTIAL]`, `[pageCache:OVERWRITE]`, `[ftruncate:LEAK]`, `[kmem] BLOCKED:`. All silent.

### Superseded / closed hypotheses

- ~~`goFont.ParseTTF` over shared pages causing GC to walk kernel memory~~ — wrong mechanism. Go's `findObject` returns nil for non-arena pointers; marking sets bits, doesn't write mspan fields.
- ~~Buddy double-free / `releasePageByPA` RefCount race~~ — `ca7f5f6` diagnostic confirms neither fires before the crash.
- ~~`releaseTempSlot` TODO leak~~ — *fixed* in `3942ae8`.
- ~~`provider.CloseTemporaryFont` not unmapping~~ — *fixed* in `3942ae8`.
- ~~IPC-then-munmap close order~~ — *fixed* in `8a64a92`.

### Prior GC crashes — earlier hypotheses (now superseded)

The earlier `freeIndex is not valid` hypothesis (Hypothesis 1: `goFont.ParseTTF` over shared pages letting GC walk into kernel memory) was **wrong on the mechanism** (Go's `findObject` returns nil for non-arena pointers; marking doesn't write to mspan fields). Crash signature shifted from `freeIndex` to `sweep increased allocation count` and now fires with **no font activity at all** (boot-time cache rebalance). All evidence now points to the kernel page-free path, not the GC scan.

The mlog (maildb console routing) work earlier in the session is unrelated and untouched here.

### Active instrumentation (in `8a64a92`)

- `[munmap:FREED] sid=N va=X pa=Y preRefCount=R origOwner=O` — fires from `SyscallMunmap` and `unmapUserPages` when a page that was ever PD_SHARED returns to the buddy. `va >= 0x500000000000` (IPC region) and `wasShared` filter keeps this quiet on the happy path.
- `[fontsvc:release] idx=I srvID=S cacheVA=V cachePages=K` — entry of `releaseTempSlot`, before `mem.FreePages`.
- `[fontsvc:close] preRelease/postRelease/preSend/postSend/postReply idx=I` — around `releaseTempSlot` + `sendCloseTempFontReply` in fontsvc's close handler.
- `[provider:close] enter/preMunmapCache/postMunmapCache/preMunmapFont/postMunmapFont/preIPC/postIPC/exit fontID=N srvID=S cacheVA=V cacheBytes=B fontVA=W fontBytes=B` — checkpoint chain around mail-app's close path.
- Older instrumentation still in tree from prior session: `[munmap:PARTIAL]`, `[pageCache:OVERWRITE]`, `[ftruncate:LEAK]`, `[kmem] BLOCKED:` — all silent in current runs.

### Side-issues parked

- `[localRect] NEGATIVE: lw=354 lh=-2691673` rachel layout corruption — likely collateral from a prior crash. Reassess after kernel bug resolved.
- linux-ui console window not appearing in some runs — separate placement/z-order issue.
- `sysFtruncate` cache-discard leak (Bug A from prior session) — not implicated in current symptoms; defer.
- **Rachel-boot hang (race) — exposed by Option B verifier (2026-04-27)**: With `stalePTECheckEnabled=true`, runs B2 and B5 (out of 5) hung at the exact same point — last log line `[shepherd] loading /rachel.maz (sid=0)`, immediately after a 1599-page `unmapLoop exit` for the previous shepherd that loaded rachel.maz (sid=3 in B2, sid=20 in B5). Both saved at `/tmp/B{2,5}-filtered-180s.log`. Hypothesis: the verifier's PT walk reads `proc.ShepherdListInUse[i]` and `PageTableL0PA` without locking, racing with shepherd teardown / rachel-launch. Could be verifier-caused (use-after-free of an L0/L1 PA mid-teardown) or just a pre-existing race exposed by the verifier's added latency. Did NOT reproduce in baseline runs without the verifier. **To be investigated** — needed only if Option A's TLB flush also relies on similar concurrent walks, or if we re-enable Option B for further diagnostics.

---

## PAUSED: fstatat/sysid=44 hang — instrumentation in place

Three clean 180s runs, no hang yet (~1-in-5 expected). Decision tree in
`findings.md`. Resume after GC crash is fixed (don't layer changes on an
unstable baseline).

---

## PAUSED: stability bisect — did `b9fd57f` regress boot reliability?

After landing real temp-pool IPC (`b9fd57f`), 5 × 180s produced 5 distinct
failure modes (fti Fstatat hang, boot panics, `attr.Init: invalid shared page
header`). None touch the changed code. Resume after GC crash is fixed:

- **Stable** → b9fd57f exonerated; gremlins are pre-existing.
- **Unstable** → bisect b9fd57f off; if that stabilizes, split the slot-table
  redesign from the IPC client rewrite and re-apply selectively.

---

## Resumable: Console rewrite (foundation ready)

Grid scrollbar (item 1) is DONE (commit `cc230e5`). Console rewrite (item 2)
not started.

### Spec

- Same logic as the mail header grid's row machinery.
- Fixed row interactors over a 500-line ring buffer.
- Row count determined by viewing-area height — stack full rows only, never
  partial.
- Switch console rows to **DynamicLabel** (drop `consoleLine` mono renderer).
- Exports same attrs as grid: line height, visible line count, total line
  count.
- Scrollbar same shape as grid scrollbar — reuse `GreaterI64Bool` /
  `ThumbFracPermille` / `NonnegSubI64`.

### Files to touch

- `mazarin/mancini/std/console.go` — rewrite to DynamicLabel, dynamic row
  count, 500-line ring buffer.
- `mazarin/mancini/std/console_frame.go` — NEW, analogous to `GridFrame`:
  NeuBox + Console + Scrollbar.
- Callers of `NewConsole` / `NewConsoleWithBox` — switch to `NewConsoleFrame`.

### Attrs to publish (mirror GridTable)

- `LineHeightAttr` — refreshed each Draw.
- `VisibleLineCountAttr` — full rows that fit, computed in Draw.
- `TotalLineCountAttr` — `len(content)`, capped at 500.
- `ScrollOffsetAttr` — lines from buffer start to first visible. Default
  tail-anchored.

### ConsoleFrame scrollbar wiring

```
scrollNeededAttr  = GreaterI64Bool(TotalLineCountAttr, VisibleLineCountAttr)
scrollMaxAttr     = NonnegSubI64(TotalLineCountAttr, VisibleLineCountAttr)
thumbFracAttr     = ThumbFracPermille(VisibleLineCountAttr, TotalLineCountAttr)
scrollbar.Visible             = EqualBool(scrollNeededAttr)
scrollbar.ValueAttr           = console.ScrollOffsetAttr   (shared)
scrollbar.MaxAttr             = scrollMaxAttr
scrollbar.ThumbFracPermilleAttr = thumbFracAttr
```

---

## Resumable: mail-dumb easy part (blocked on stability)

Once GC crash + stability bisect are resolved:

1. **Body display** — HTML body pane in the mail app. Requires temp-font
   fallback chain (`@font-face` → registered buffer → fontsvc OpenFont →
   default sans).
2. **PageUp/PageDown** — extend `GridTable.MoveSelection` / `ScrollBy`.
3. **Mark-read** — `MsgTypeMarkRead` IPC to maildb; update `Flags.IsRead`.
4. **Delete** — `MsgTypeMarkDeleted`; remove from displayed collection.
5. **Polish** — click→body fetch latency audit; prefetch tuning.

### Mail program deferred follow-ups

- Click→body fetch latency / prefetch-ahead audit (5 clicks → 12 body
  fetches; possibly excessive).
- maildb working set bounded check (~140 MB badger LSM; add periodic
  `[maildb:mem]` log).
- linux-ui transient fontsvc-boot wedge (not seen since uring.Send retry fix;
  watch).
