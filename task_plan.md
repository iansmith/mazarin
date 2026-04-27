# Task Plan — Mazarin / Mazzy

## TOP OF STACK: bug B family — kernel `unmapUserPages` / `ReleasePageByPA` / `BuddyFreeTyped` (2026-04-27)

**Branch:** `feature/mail-dumb`
**Last commits:** `3942ae8` (font leak fix A+B) → `8a64a92` (caller-first close order + diagnostic checkpoints)

### What we know

The font close cycle exposes an intermittent kernel bug. Symptom shifts run-to-run but always lands in the same kernel chain:

- `mem.Munmap` on shared cache pages from mail-app silently hangs (IPC-then-munmap order — *fixed* in `8a64a92` by swapping to munmap-then-IPC).
- `mem.FreePages` of the 1024-page cache block in fontsvc silently hangs (`[fontsvc:release]` fires, `[fontsvc:close] postRelease` does not — `releaseTempSlot` never returns).
- `uring.Send` of the close reply hangs in fontsvc (`postRelease` fires, `postSend` does not — observed in a separate run).
- `fatal error: sweep increased allocation count` mspan corruption in mail-app's bgsweep (cluster `nalloc≈37K-45K, small nelems`) — fires at any heavy-allocation moment, including boot-time cache rebalance with **no font activity at all**.
- `fatal error: freeIndex is not valid` in mail-app's CSS parser (different field of mspan corrupted, same family).

All three converge on `SyscallMunmap` / `SyscallFreePages` → `unmapUserPages` → `ReleasePageByPA` → `BuddyFreeTyped`. The instrumentation in `8a64a92` localized the hang to that chain but doesn't yet identify the specific bug inside it.

### Hypotheses to test (kernel-side instrumentation next)

**H-K1 — Double-free / RefCount underflow.** Concurrent decrement-and-free races between `SyscallMunmap` (mail-app side) and `SyscallFreePages` (fontsvc side) on shared cache pages. `releasePageByPA` reads `desc.RefCount`, decrements, checks `> 0`; without locking, two threads can both hit `RefCount == 0` and both call `BuddyFreeTyped` on the same PA. The shared page count per close (≈88 pages of `numCachePages`) gives many opportunities.

**H-K2 — Stale free-list state.** `BuddyFreeTyped` puts a PA on a free list. If the same PA is already there (from H-K1 or an earlier mishandled free), the linked list cycles. Next `BuddyAllocTyped` walks the cycle forever — the hang location.

**H-K3 — PD_SHARED bit clear path.** `releasePageByPA` does **not** clear `PD_SHARED` when RefCount drops to 0. `SyscallSharePagesWithTarget` rollback path does (`if RefCount <= 1 { Flags &^= PD_SHARED }`) but normal release doesn't. If a freshly-allocated PA inherits a stale `PD_SHARED` flag from `SetPageDescriptor` (which sets fields explicitly, so probably not), or if some path branches on `PD_SHARED` and behaves wrong, that's a candidate.

**H-K4 — Page descriptor cleared mid-loop.** `unmapUserPages` calls `ReleasePageByPA` per page, and one of those calls `ClearPageDescriptor(pa)`. If a subsequent iteration tries to read the descriptor for a page that's just been cleared (concurrent free from another shepherd), `desc.RefCount <= 0` short-circuits silently — but doesn't hang. Less likely culprit.

### Files to touch (kernel diagnostic only — no behavior change)

| File | Change |
|------|--------|
| `kmazarin/kmem/cleanup.go` | `releasePageByPA`: log when RefCount underflows (was 0 before decrement, or goes negative). Log every Nth call with PD_SHARED set. |
| `kmazarin/kmem/buddy.go` | `BuddyFreeTyped`: add a "PA already on free list" check (walk free list head, abort if found) before linking. Log on hit. |
| `kmazarin/ksyscall/user_pages.go` | `unmapUserPages`: log entry/exit so we can see whether the loop ever returns. Log every 256th iteration with current `va`/`pa` to track loop progress when the hang fires. |

### Test strategy

1. Apply kernel instrumentation. Build, run 90s HVF.
2. Click 1 message to trigger close cycle.
3. If the loop log fires N times then stops → hang is at iteration N (specific page in the 1024-page block).
4. If "PA already on free list" log fires → H-K1/K2 confirmed (double-free).
5. If `releasePageByPA` underflow log fires → H-K1 confirmed.
6. Use the data to write a focused fix (likely a lock around the decrement-and-free in `releasePageByPA`, or a double-free guard in `BuddyFreeTyped`).

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
