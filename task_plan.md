# Task Plan — Mazarin / Mazzy

## TOP OF STACK: bug B family — stale-PTE / TLB / non-RefCount path into mail-app heap (2026-04-27)

**Branch:** `feature/mail-dumb`
**Last commits:** `3942ae8` (A+B font leak fix) → `8a64a92` (caller-first close + checkpoints) → `4460c14` (docs retarget) → `ca7f5f6` (kernel double-free / underflow / loop-progress guards)

### What we know

The font close cycle exposes an intermittent kernel bug. Symptom shifts run-to-run but always corrupts an mspan struct field in mail-app's heap (`nelems` overwritten by a small int, `nalloc` and `nfreed` go nonsensical). Manifestations seen:

- `fatal error: sweep increased allocation count` (cluster `nalloc≈37K-45K, small nelems`).
- `fatal error: freeIndex is not valid` (different mspan field, same family).
- `mem.Munmap` of shared cache pages silently hangs (IPC-then-munmap order — *fixed* in `8a64a92` by reversing).
- `mem.FreePages` of fontsvc's 1024-page cache block silently hangs.
- `uring.Send` of close reply silently hangs.

The `ca7f5f6` diagnostic run confirmed: **none of the buddy double-free / RefCount underflow / loop hangs fired**. Crash hit at boot during mail-app cache rebalance with zero font activity, zero shared-page release in flight. So the corruption mechanism is **not** a buddy double-free, **not** a `releasePageByPA` race, **not** a hang in the unmap loop. Something else writes to mail-app's mspan struct.

### Refined hypotheses

**H-T1 (PRIMARY) — Stale TLB after `SyscallMunmap`.** `SyscallMunmap` (`kmazarin/ksyscall/munmap.go`) removes PTEs in the loop but **does not** call `kmem.TlbiVMALLE1()` / `DsbISH()` / `IsbSY()` at the end (compare `SyscallFreePages` which does). After munmap returns, the calling shepherd can still reach the page via cached TLB entries until a context switch happens that flushes — and on ARM64 with ASIDs, even context switches don't flush other-ASID entries. If that PA is later returned to buddy and reallocated to mail-app's heap (UserHeap pages), the prior shepherd's stale TLB entry can write through and corrupt whatever mail-app put there.

**H-T2 — Stale PTE in another shepherd's page table.** Variation of H-T1: not a TLB issue but a PTE that wasn't actually removed. `unmapUserPages` calls `UnmapUserPageWithL0`; if that has any silent failure path (returns 0 = "not mapped" instead of actually clearing the PTE for a corner-case state), the PTE persists. Once the PA is reallocated, the stale-PTE shepherd writes through.

**H-T3 — Direct write into a freed page from a non-PTE path.** Kernel scratch mapping (`MapPAToKernelScratch`) used during page zeroing, IPC-page setup, or DMA could write to a PA that's been freed and reallocated. `releasePageByPA` returns the page to buddy *immediately*; if any kernel path holds the kernel-scratch VA past the free, the next allocation gets stomped. DMA writes from VirtIO devices targeting a now-recycled page have the same shape.

**H-T4 — Shepherd-launch buffer cleanup leaves stale state.** `ca7f5f6` log showed sid=21 freeing 1599-page user buffers (ELF load scratch) right before the crash. These are NOT shared pages, but the freed PAs go straight to buddy and could be reissued to mail-app's heap. If the shepherd-launch path has a stale PTE / TLB / scratch-mapping issue, this would surface as mail-app heap corruption near boot.

### Files to touch (option-by-option)

#### Option A — TLB invalidate at end of `SyscallMunmap` (H-T1 fix-test)

A small kernel behavior change to test the stale-TLB theory directly. Mirrors `SyscallFreePages`'s existing TLB-invalidate sequence. If the mspan corruption stops or shifts after this, H-T1 is confirmed.

| File | Change |
|------|--------|
| `kmazarin/ksyscall/munmap.go` | At end of `SyscallMunmap` (after the unmap+release loop, before `return 0`): call `kmem.TlbiVMALLE1()`, `kmem.DsbISH()`, `kmem.IsbSY()`. |

This is architectural (kernel behavior change). User OK required before applying. Low blast radius (TLB flush is correct semantics for munmap), reversible.

#### Option B — Stale-PTE detection at allocation time (H-T1/T2/T4 diagnostic)

A diagnostic-only check that proves whether anyone holds a stale PTE for a freshly-allocated PA. When `BuddyAllocTyped` returns a PA, sweep all shepherds' page tables looking for that PA. Any hit before the new owner's PT setup is a stale-PTE bug. Expensive (O(N shepherds × 4-level PT walk per alloc), maybe O(seconds) at heavy alloc rates) — keep behind a build tag or runtime flag so it's off by default.

| File | Change |
|------|--------|
| `kmazarin/kmem/buddy.go` | After `BuddyAllocTyped` produces a PA, optionally call `verifyNoStalePTEs(pa, order)`. Off by default; enable via boot-arg or env (e.g., `MAZ_KCHECK_STALE_PTE=1`). |
| `kmazarin/kmem/page_descriptor.go` | New helper `verifyNoStalePTEs(pa uintptr, order int)`: walk every live `proc.Shepherd`'s `PageTableL0PA`, scan for PTEs whose PA range overlaps `[pa, pa + 4096<<order)`. Log `[stale-PTE] pa=X holder=N va=Y order=O` per hit, then `klog.Critical` halt if any found. |
| `kmazarin/proc/proc.go` (or wherever shepherd list lives) | Expose an iterator over live shepherds for the helper to walk. |

Diagnostic only, no behavior change. User OK preferred but lower risk than A.

#### Common to both: capture of the corrupting write

If A and B both run and the crash still fires without either firing first, the corruption isn't TLB or stale-PTE — it's H-T3 (kernel-direct write or DMA). Next-tier diagnostic at that point would be `MapPAToKernelScratch` instrumentation: log every scratch mapping, log every `Bzero4K`, and for the specific PAs that get freed, set a sentinel byte pattern at free time and check it at next-alloc time. That's bigger work — only pursue if A and B both come back negative.

### Run plan

1. Boot-without-clicks repro: confirm 3 × 90s runs — does the boot-time mspan corruption reproduce reliably without user input? If so, eliminates timing dependence on clicks and gives a fast iteration cycle.
2. Apply **Option B** first (diagnostic only). Run 3 × 90s. If any `[stale-PTE]` log fires → H-T1/T2/T4 confirmed, fix path obvious.
3. If B is silent: apply **Option A** (TLB flush). Run 5 × 90s. Compare to baseline rate. If crash rate drops noticeably → H-T1 confirmed and A is the fix.
4. If A also doesn't help: pivot to H-T3 instrumentation (kernel-scratch mapping + sentinel-byte canary).

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
