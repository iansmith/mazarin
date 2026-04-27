# Task Plan — Mazarin / Mazzy

## TOP OF STACK: bug B family — page-cache writeback path re-audit (2026-04-27)

**Branch:** `feature/mail-dumb`
**Last commits:** `3942ae8` (A+B font leak fix) → `8a64a92` (caller-first close + checkpoints) → `4460c14` (docs retarget) → `ca7f5f6` (kernel double-free / underflow / loop-progress guards) → `612ed58` (Option B stale-PTE verifier — ruled out H-T2) → (next: free-canary commit)

### Where we are (2026-04-27 PM)

The font close cycle exposes an intermittent kernel bug that always corrupts an mspan struct field in mail-app's heap. Five diagnostic rounds have ruled out:

- **Buddy double-free / RefCount underflow / unmapLoop hang** (`ca7f5f6` guards silent).
- **H-T2 stale PTE in another shepherd's PT memory** (`612ed58` Option B verifier silent across 5 × 180s, 184K–203K scans/run, 0 hits).
- **H-T1 (specifically: missing trailing TLB flush at `SyscallMunmap`)** — Option A reverted as a no-op (per-page `tlbiVAE1IS` already broadcasts in IS domain; trailing local `TlbiVMALLE1` is redundant). Stronger H-T1 tests not yet attempted.
- **H-T3a kernel write between `BuddyFreeTyped` and the next `BuddyAllocTyped` of the same PA** — sentinel-byte canary 5 × 180s, ~1.5M+ verifies aggregate, 0 hits, including in C3 which reproduced the mspan crash (`nelems=1008 nalloc=23628` at boot during mail-app initial cache rebalance, no clicks). The corrupting write is **not** happening in the free→reuse window.

### Active hypotheses (post free-canary)

**H-T3b — Kernel path with a stale handle writes AFTER the freed PA has been reissued and legitimately used.** The PA gets freed → canary'd → allocated to mail-app → mail-app's first write overwrites the canary with normal data → THEN a kernel path with a stale PA-derived pointer writes through. The canary is gone by the time of the corrupting write, so this case is invisible to the existing probe. The most plausible channel for this: **the linux page-cache writeback path** (`maz/linux/page_cache.go`, `sysWrite`, `flushWriteBuf`, `updateCachedPages`, `flushAndCleanupPages`, `handleFlushReply`). Earlier session (`12e5f0d`) added a partial-munmap range guard there to fix one variant of this bug — a different variant may still exist.

**H-T1' (residual)** — TLB cache holding a translation past `tlbiVAE1IS` (HW erratum or barrier ordering). Hard to test; revisit only if the page-cache audit comes back empty.

**H-T2** — RULED OUT.

### Plan: Stage 2 (READ-ONLY) — re-audit the page-cache writeback path

**This stage is investigation, not a code change.** Read the relevant files, map the dual-mapping flow (kernel → linux handler → kernel-side flush reply), and look for any path where:
1. A cached page entry holds a `(sid, inum, offset, va, pa)` tuple,
2. That underlying PA can be released to buddy via a release path (e.g. munmap, ftruncate, shepherd death) without removing the cache entry, **and**
3. A subsequent `sysWrite` / `flushWriteBuf` / `updateCachedPages` lookup matches the stale entry and writes through the now-recycled PA.

Files to read (no edits unless we surface a concrete bug for review):

| File | Why |
|------|-----|
| `maz/linux/page_cache.go` | The cache structure + add/lookup/remove/range APIs. Where ownership semantics live. |
| `maz/linux/syscalls.go` | `sysWrite`, `sysFtruncate`, `sysMmapPageFlush` — every path that mutates the cache. |
| `maz/linux/main.go` | `flushWriteBuf`, `updateCachedPages` (if present) — the writers that consult the cache and write through PA. |
| `kmazarin/ksyscall/munmap.go` + `kmazarin/ksyscall/mmap_writeback.go` | Kernel side of the flush IPC: `flushAndCleanupPages` populates `DelegateCallInfo`, `handleFlushReply` releases pages. |
| `kmazarin/ksyscall/cleanup.go` (and shepherd-death paths) | Death-time cleanup: does it tell linux to drop its cached entries? Look for any window where shepherd dies but linux retains entries. |

Specific things to look for:
- A code path that calls `cache.Add` with an entry whose VA may already be in the cache from a previous use (`[pageCache:OVERWRITE]` instrumented but silent — confirm coverage).
- `sysFtruncate` was flagged as discarding `RemoveRange` results without telling the kernel to drop the handler-side mappings (Bug A in `findings.md`). Re-examine whether the `[ftruncate:LEAK]` warning is firing in any reproducer log.
- An `sysWrite` that consults the cache by `(fd, offset)` and writes to the cached PA without re-validating that PA still belongs to the original mapping — race window between cache lookup and PA write.
- A `flushAndCleanupPages` that returns before the handler completes, with the kernel proceeding to free the PA while linux still has entries.
- Any path where linux writes to the cached PA AFTER receiving a `MmapPageFlush` (i.e., after the kernel has released).

Deliverable from Stage 2: a written audit (in this file or `findings.md`) listing each cache-mutation path with a yes/no + justification on whether it can leave a stale entry pointing to a freed PA. Concrete fix proposals only after the audit is reviewed.

### Run plan

1. ✅ Baseline: 5 × 180s → 1 mspan crash, 1 boot hang, 3 clean.
2. ✅ Option B verifier (`612ed58`): 5 × 180s → 1 mspan crash, 2 boot hangs, 2 clean. **H-T2 RULED OUT** (0 hits).
3. ✅ Option A TLB flush at SyscallMunmap end: 5 × 180s → 2 mspan crashes, 1 boot hang, 2 clean. Crash rate unchanged. **Reverted** (no-op vs per-page `tlbiVAE1IS`).
4. ✅ H-T3 Stage 1 sentinel-byte canary: 5 × 180s → 1 mspan crash (C3, no clicks), 2 boot hangs, 2 clean (one click-driven). Canary 0 hits across ~1.5M+ verifies. **H-T3a (free→reuse window) RULED OUT.**
5. **NEXT — Stage 2 page-cache audit (read-only).** Read the listed files, produce a written audit of cache-mutation paths, present concrete fix candidates for review.
6. Stage 3 (only after audit): apply the most likely fix or add targeted instrumentation around the suspect path; 5 × 180s.

### Reminders — diagnostic toggles in tree

- **Option B stale-PTE verifier** at `612ed58`. Default `stalePTECheckEnabled = false` in `kmazarin/kmem/stale_pte_check.go`. Telemetry on `[status]` line: `stale-pte: enabled scans hits`. Flip the var to true to re-enable for any future PT-memory diagnostic.
- **Free-canary** (next commit). Default `freeCanaryEnabled = false` in `kmazarin/kmem/free_canary.go`. Telemetry on `[status]` line: `free-canary: enabled fills verifies hits`. Flip the var to true to re-enable for any future free→reuse-window diagnostic.

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
