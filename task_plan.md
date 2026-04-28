# Task Plan — Mazarin / Mazzy

## TOP OF STACK: bug B family — Stage 4 VA-collision probe gated, awaiting boot-only sweep (2026-04-28)

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
