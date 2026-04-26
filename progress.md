# Progress Log

## Session: 2026-04-26 (evening) — Opus review of the four hypotheses

User asked Opus to evaluate the four follow-on hypotheses Sonnet
listed at end of the afternoon session. Read code in
`kmazarin/ksyscall/`, `kmazarin/kmem/`, `kmazarin/proc/`, and the
ARM64 exception handler to verify each.

### TTBR0 hypothesis (Sonnet's main lead) — almost certainly wrong

`SyscallMunmap` reads TTBR0 via `kmem.UnmapUserPage` →
`readCurrentL0PA()`. Sonnet's claim: TTBR0 is wrong because "SVC
handlers run on a kernel worker goroutine."

Tracing `exceptions_arm64.s:233-360` shows:
- SVC fires → EL1h with SP_EL1
- Handler swaps `x28` (Go's g register) to `kmazarinG0Addr` so Go
  thinks it's on g0
- Calls `SyscallDispatch` directly, nosplit, on the **same CPU thread**
- **TTBR0 is NEVER changed** — there is no `SwitchTTBR0WithASID`
  anywhere between SVC entry and the syscall handler returning

Independent confirmation: `HandleUserPageFault` reads TTBR0 for
every demand-fault and the ZERO_VERIFY_FAIL probe never fires. If
TTBR0 were wrong on synchronous syscalls, demand-paging would
already be broken.

The misleading comment in `stubs.go:279` ("SVC handler runs on a
kernel worker goroutine") is what led Sonnet astray. The actual
reason `SyscallMadvise` uses stored L0PA is defensive coding, not a
TTBR0 bug. `SetSyscallSwitchTarget` does affect later
queue-then-resume work, but mmap/munmap/madvise/mprotect run
inline before any context switch.

**Don't waste a session on the SyscallMunmap TTBR0 patch as Sonnet
described.** It's harmless but won't move the needle.

### Hypothesis ranking after review

**#1 (zero-on-alloc gaps): RULED OUT.** Every demand-fault / IPC-page
allocation path zeroes via scratchVA + `CleanPageCache` before user
visibility:
- `HandleUserPageFault`, `allocPTPage`, `AllocAndMapUserPageWithL0`,
  `allocAndCopyCallerData`, `allocEmptyDataPage`,
  `allocAndCopyCallerString`, `handleFileMappedPageFault`,
  `SyscallAllocPages`.
- `MapContiguousUserPages` and `AllocAndMapUserPageNoZero` skip
  zeroing intentionally (DMA pages filled by hardware, code pages
  fully overwritten by ELF copy). Neither touches the GC arena.

**#2 (refcount/dual-mapping): STRONGEST LEAD.** `MapPageInProcess`
(`paging.go:2336`) installs a PTE without bumping `desc.RefCount`.
Most call sites manually `desc.RefCount++` before calling. **One
critical site does not:** `delegate.go:833` (MmapPageFill reply
path).

Lifecycle of an MmapPageFill page:
1. `allocEmptyDataPage` → `AllocPage(PageSharedIPC, handlerSID)` →
   RefCount=1, owner=handler
2. `MapPageInProcess(handlerSID, va, pa)` → handler now mapped,
   RefCount still 1
3. Handler reads file data; replies
4. `delegate.go:833`:
   `MapPageInProcess(callerSID, callerBufVA, pa)` →
   **caller now ALSO mapped, RefCount STILL 1**, no PD_SHARED flag
5. Page is now in 2 PTEs but the kernel thinks it has 1 ref.

The design comment at `delegate.go:838` says: "Handler-side pageCache
tracks the dual mapping. On munmap/death, flushAndCleanupPages
sends IPC rounds to flush dirty pages and return handler VAs for
unmapping — no kernel-side tracking needed."

That works for some paths but **leaks elsewhere**:
- `SyscallMadvise` userspace path (`stubs.go:281-294`) has NO
  fileBacked check — unconditionally calls `ReleasePageByPA(pa)`,
  decrementing RefCount 1→0 on a still-mapped-elsewhere page.
- `CleanupShepherdPages` Phase 1 (`cleanup.go:55-65`) walks all
  spans and releases every leaf page; relies on
  `WriteBackSharedMmapOnDeath` having run first to unmap from the
  handler.

If a dual-mapped page's RefCount hits 0 while the handler still has
a PTE, the page returns to buddy → eventually re-allocated to
another shepherd (e.g. mail's GC arena) → handler writes through
stale PTE (page-cache update or cleanup-in-flight) → corruption
in the new owner's memory.

The recently-applied PD_PINNED fix on the constraint shared block
(`constraint.go:166-174`) is a **symptom** of the same family of
bug: system-shared pages without proper refcount tracking get
released by per-shepherd cleanup. Constraint pages got pinned;
MmapPageFill dual mappings were not addressed.

**Why the symptom matches:** mail crashes very early (right after
"cache ready"), and the corruption is in the GC bitmap arena,
which is allocated during heap setup. fti and maildb run before
mail and use file mmap → linux shepherd accumulates dual-mapped
pages → some get released early (madvise or cleanup edge case) →
mail's first heap allocation lands on one.

**#3 (cache aliasing scratch↔userVA): RULED OUT.** ARM64 D-cache
is PIPT-equivalent by architecture. `DC CIVAC` cleans by VA → PA
translation; flushing at scratchVA covers the line for that PA
regardless of which user VAs alias it. `HandleUserPageFault`'s
ZERO_VERIFY_FAIL probe (which reads back via scratchVA after the
cache flush) never fires.

**#4 (bump-allocator overlap): UNLIKELY.** Per-shepherd
`BumpPointer` is monotonic, starts at 0x200000000000.
`userBumpAlloc` checks `findSpanOverlapEnd`. `bumpAllocForShepherd`
uses a separate range at 0x500000000000. Go's heap arena hint
(0xC000000000) sits below `userMmapStart`; the kernel redirects
the first sysReserveOS, then all subsequent MAP_FIXED uses the
returned VA. The `scanForStalePTEs` debug check at `mmap.go:191`
would log `[mmap:STALE]` if this triggered; Sonnet didn't see
those messages.

### Recommended next steps (written into continuation_bisect.md)

1. **Probe** — add a user-VA ZERO_VERIFY in `HandleUserPageFault`
   (read back first 8 bytes via the user VA, not scratchVA, after
   TLB invalidate). If non-zero, we have direct evidence of a stale
   PTE writing to a "fresh" page.
2. **If probe fires** — fix dual-mapping refcount:
   - `delegate.go:833`: bump RefCount + set PD_SHARED before
     MapPageInProcess.
   - `SyscallMadvise`: add fileBacked check (mirror `SyscallMunmap`).
3. **Verify** — 60s/120s/180s runs.

### Files changed this session

`continuation_bisect.md` (rewrote with refined hypothesis ranking
and probe-first plan), `progress.md` (this entry), `task_plan.md`
(updated TOP OF STACK).

---

## Session: 2026-04-26 (afternoon) — GC crash investigation; MAP_FIXED bug found + fixed, crash persists

### Context
Continuing from the morning session (below). The bisect plan was superseded by a more
fundamental kernel bug — a `fatal error: sweep increased allocation count` crash in the
mail shepherd's GC bgsweep goroutine. Bisect is paused until the GC crash is resolved.

### Crash signature (mail shepherd, bgsweep)

```
[mail] collection created: collId=1 size=9
[mail] cache ready, initial rebalance first=-1 vis=0
runtime: nelems=36 nalloc=15375 previous allocCount=1 nfreed=50162
fatal error: sweep increased allocation count
  runtime.(*sweepLocked).sweep → mgcsweep.go:685
  runtime.sweepone → mgcsweep.go:396
  runtime.bgsweep → mgcsweep.go:300
```

Crash occurs immediately after "cache ready" (serial log line ~279), before any mail
body fetches. The mail shepherd has done almost no work. The crashed goroutine is
bgsweep — Go's concurrent background sweeper.

### Mathematical analysis of `nelems=36 nalloc=15375`

With `nelems=36`, `countAlloc()` does `bytes=divRoundUp(36,8)=5`, loop `i=0 < 5, i=8
not < 5 → one iteration`, max `nalloc = 64`. Getting 15375 is impossible. The serial
ring buffer dropped characters; real `nelems ≈ 15375` (making 241 loop iterations,
max 15424 > 15375 — consistent). `allocCount=1` is real (it's the printed value,
small and unambiguous). `nfreed = uint16(1 - 15375) = 50162` confirms this.

Interpretation: a GC span had only 1 allocated object but 15375 gcmarkBits set after
the concurrent mark phase. Either the gcmarkBits arena was pre-populated with garbage,
or the GC mark phase marked 15374 phantom objects.

### Bug found: MAP_FIXED in kmazarin doesn't implement Linux semantics

`SyscallMmap` userspace MAP_FIXED path (`ksyscall/mmap.go` lines ~94–104):
- Recorded the new span (removeSpan + addSpan)
- Did NOT unmap existing PTEs at `[addr, addr+length)`

Real Linux MAP_FIXED atomically replaces any existing mapping — old physical pages are
released, new demand-faulted pages come in zeroed. Without this, Go's `sysMapOS`
(which calls `mmap(v, n, PROT_RW, MAP_ANON|MAP_FIXED|MAP_PRIVATE)`) could get a range
where stale physical pages from a previous GC arena allocation remain mapped,
containing old mark-bit data.

**Fix applied** (`kmazarin/ksyscall/mmap.go`): added `unmapFixedRange(addr, length)`
helper (`//go:noinline`, not nosplit) that uses `shepherd.PageTableL0PA` +
`UnmapUserPageWithL0` to release existing PTEs before re-recording the span. Pattern
matches the existing `SyscallMadvise` userspace path. Builds clean.

### Crash persists after MAP_FIXED fix

60s run after fix: clean (mail finishes, fti completes, no crash).
120s run after fix: crash reproduces with same signature.

This means either:
1. MAP_FIXED was not the only source of stale pages, OR
2. The stale page reaches the gcBitsArena through a different path (madvise + page
   reuse without proper cache flush, or munmap path using wrong TTBR0), OR
3. The GC mark bits are being overwritten by something AFTER the mark phase

### Additional observations

- `[localRect] NEGATIVE: lw=354 lh=-2691673 ix1=486.7 iy1=2691760.0` appears in the
  log after the mail crash — rachel's layout engine seeing astronomically wrong floats.
  Could be a separate constraint corruption (the `bug_attr_init_crash.md` family) OR
  a consequence of the mail shepherd dying mid-render.
- `[maildb] send to SID 23 failed: resource temporarily unavailable` — 50+ times.
  maildb is hammering the mail shepherd's dead uring. Expected after the crash.
- `HandleUserPageFault` already has a `ZERO_VERIFY_FAIL` post-zeroing check (reads
  back first 8 bytes at scratchVA after CleanPageCache+DSB). No failures visible in
  serial log — zeroing at scratchVA is working correctly.
- `SyscallMunmap` uses `kmem.UnmapUserPage()` (reads hardware TTBR0), not
  `UnmapUserPageWithL0(l0PA)`. Since SVC runs on a kernel worker goroutine, TTBR0
  may be wrong shepherd's page table. If munmap silently walks the wrong table, it
  frees no pages → PTE stays → stale data persists for next MAP_FIXED remap.

### Current hypothesis (most likely)

`SyscallMunmap` is walking the wrong page table (hardware TTBR0 on kernel worker
goroutine ≠ calling shepherd's L0PA). It fails to unmap/release stale pages. When Go
calls `sysMapOS` (MAP_FIXED) on a range that was previously `sysFreeOS`'d (munmap),
the MAP_FIXED handler now unmaps correctly (our fix), but there may be a window where
`sysMunmap` didn't actually remove the PTE, making the MAP_FIXED fix hit the right
range but find no PTE to remove.

Additionally: Go uses `sysUnusedOS` (madvise) far more than `sysFreeOS` (munmap) for
GC arena reclamation. The madvise path uses `shepherd.PageTableL0PA` correctly. So
the madvise path alone may be clean.

**Need to investigate**: whether the TTBR0 issue in `SyscallMunmap` is live, and
whether it's the remaining source of stale pages.

### Files modified this session

`kmazarin/ksyscall/mmap.go` — `unmapFixedRange()` helper + call in MAP_FIXED path.

### Current branch: `feature/mail-dumb`

---

## Session: 2026-04-26 (morning) — temp-pool real IPC landed, two follow-up fixes, stability concerns

Continuing from yesterday's louis14 integration. Today landed the
real temp-pool IPC client work, found and fixed two correctness bugs
the user surfaced via interactive testing, and identified a stability
regression that warrants a bisect before further forward progress.

### Code shipped

- **`b9fd57f` font: real temp-pool IPC + harfbuzz-safe table-based
  fontID translation.** End-to-end IPC for the fontsvc temp pool plus
  the harfbuzz-array overflow fix. Highlights:
  - Wire types: `wm.RegisterFontBuffer` / `Reply`,
    `wm.UnregisterFontBuffer` / `Reply` (MsgTypes 108–111).
  - Fontsvc: `registeredBytes [64]` table; `handleRegisterFontBuffer`
    stores the (family, variant) → mapped pages mapping;
    `handleOpenTemporaryFont` consults it before filesystem path;
    `parseFaceFromBytes` extracted; `CleanupShepherdFonts` extended.
  - `FontSvcInjector` gained two new request handlers +
    `RegisterCleanupHandler` for rachel→fontsvc death notification.
  - Rachel's dispatch routes the new types; rachel's
    `handleShepherdDeath` invokes the captured cleanup callback.
  - **Client `FontSvcGlyphProvider` redesign:** replaced
    `fonts [MaxFonts=32]` with `slots [textshape.MaxFonts=256]`. Each
    slot stores `serverFontID int32` (whatever fontsvc returned —
    opaque to the client) and `kind slotKind` (set explicitly when
    populated, never derived from arithmetic). Client-side fontIDs
    are slot indices, always small ints < 256; server-side fontIDs
    are looked up on every IPC send. The wire's `0x1000` temp tag
    stays purely server-side; client and server pool sizes can
    change independently without breaking translation.
  - `RegisterBuffer` pushes bytes via `AllocPagesSlice` + memcpy +
    `SharePagesWithTarget(rachelSID)` + `SendRegisterFontBuffer`.
    Failure → falls back to in-process `openRegistered` path.
  - `OpenTemporaryFont` real IPC; `CloseTemporaryFont` real IPC for
    `kindTemporary` slots, no-op for `kindPermanent`.
  - `textshape.maxFonts` → `MaxFonts` (exported) so fontcache
    sources its slot table size from the same constant.
  - FontCache helpers: `SendOpenTemporaryFont`,
    `SendCloseTemporaryFont`, `SendRegisterFontBuffer`,
    `SendUnregisterFontBuffer`.

- **`e3b7159` fix(grid): publish scroll attrs on visibleCount
  transition.** `GridTable.Draw` was updating its cached
  `visibleCount` field on the 0→N initial-draw transition without
  calling `publishScrollAttrs`, so `VisibleRowCountAttr` stayed at 0
  and `cache.Rebalance` short-circuited (it gates on `visCount == 0`).
  Result: no headers were fetched, no body fetch ever triggered,
  scrollbar drags couldn't kick the prefetch. 6-line fix —
  `gt.publishScrollAttrs()` after `gt.buildSlotPool(newVC)`.

- **`7af236c` fix(mail): run cache.Rebalance + body fetch after every
  iteration.** The drainDirty loop at the top of mail-app's main
  event loop was discarding `eagerCh` notifications that fired
  synchronously from the wmCh handler — a click's
  `setSelected → publishScrollAttrs` queues a dirty notification on
  the OnEager channel mid-handler, then the next iteration's drain
  drops it before the eagerCh case can run. Past runs that did fetch
  bodies got lucky on async timing. Fix: move the
  `cache.Rebalance` + `requestBody` checks out of the eagerCh case
  and run them after every iteration regardless of which select
  case fired. Both calls are idempotent.

### 180s smoke runs (5 attempts)

| # | Commit  | Result |
|---|---------|--------|
| 1 | b9fd57f | fti hung at 14s, Fstatat (sysid=44) stuck on linux delegate. |
| 2 | b9fd57f | fti completed in 17s; user clicked but bodies didn't render. (Bug found → e3b7159 + 7af236c.) |
| 3 | e3b7159 | fti hung at 4s, same Fstatat stuck. Couldn't reach click path. |
| 4 | e3b7159 | fti completed late; user reported "very long pause", scrollbar didn't kick cache. (Symptom that drove 7af236c.) |
| 5 | 7af236c | Two boot panics: `language: tag is not well-formed` in maildb plugin init, `attr.Init: invalid shared page header` in mail-ui. mail-ui never started. |

Five distinct failure modes is a lot. The two correctness fixes
(`e3b7159` + `7af236c`) are sound — the bugs they target reproduce
deterministically by inspection and the fixes are minimal. But they
haven't been validated end-to-end yet because every run since has
hit a different gremlin.

### Stability concern

The fti-Fstatat hang and the boot panics don't touch the code
I've been changing. They're in the .maz plugin loader and the
kernel↔shepherd attr-shared-page handshake — well below the
event-loop / grid / fontcache layer. But the rate went up after
`b9fd57f`.

`pushBytesToFontsvc` (the new `RegisterBuffer` path) does
`AllocPagesSlice` + `SharePagesWithTarget`. None of the failing runs
loaded `@font-face` (no clicks reached HTML rendering), so this code
isn't being exercised. Yet the system is more fragile.

The slot table grew from `[32]` to `[256]` per shepherd — ~1.5KB
extra pointer slots. Negligible memory; mention only because it's
the other observable change.

### Next step (recorded in task_plan.md TOP OF STACK)

**Bisect.** Revert `b9fd57f` on a side branch keeping `cc230e5`
(GridFrame scrollbar) + `37b4abe` (DrawContext temp-font surface
with safe fallback) as the baseline. Run 3–5 180s sessions.

- Stable → `b9fd57f` introduced the regression. Re-apply more
  carefully (split slot-table redesign from IPC client rewrite).
- Still unstable → gremlins are pre-existing
  (`mazlink_funcval_dead_reloc_bug.md` family); my changes are
  exonerated; chase the loader / attr-page issue separately.

The body-fetch fix (`7af236c`) is independent of the temp-pool
work — only touches `mazarin/apps/mail/main.go` — and would survive
a bisect.

### Files modified today (mazzy)

`shared/wm/uring_font.go`, `mazarin/textshape/{harfbuzz,layout,draw_impl,draw_context,rasterize}.go`,
`mazarin/fontcache/{provider,protocol,fontcache,internal_provider}.go`,
`maz/fontsvc/main.go`, `maz/rachel/main.go`,
`mazarin/mancini/std/grid_table.go`, `mazarin/apps/mail/main.go`,
`task_plan.md`, `progress.md`.

### louis14 state (untouched today)

The user has WIP in `pkg/render/render.go` (Phase 13g.2 snap-debug
diagnostic) and `pkg/visualtest/debug_phase13g_test.go` — not part
of this work. Yesterday's louis14 commit (`f41f5c4d` —
`OpenTemporaryFont` / `CloseTemporaryFont` switch) is unchanged.

---

## Session: 2026-04-25 (very late) — louis14 temp-font integration

louis14 reached a stable state (Phase 13e′/13f LayoutUnit migration
settled, mail-app:arm64 rebuilds cleanly), so we applied the call-site
changes from `design/louis14_temp_fonts_plan.md` end-to-end with the
mazzy plumbing they need.

### Code shipped

**mazzy side (interface plumbing, additive):**

- `mazarin/textshape/layout.go::TextLayout` — interface gains
  `OpenTemporaryFont(req)` and `CloseTemporaryFont(fontID)`.
  `HarfBuzzTextLayout` implements both: open delegates to
  `provider.OpenTemporaryFont(req, nil)`, close to
  `provider.CloseTemporaryFont`. Shaper registration extracted into
  `registerOpenedFont` shared by OpenFont and OpenTemporaryFont (and
  bounds-checked so future temp-pool fontIDs with the 0x1000 bit
  don't overflow the fixed-size `tl.fonts` table).
- `mazarin/textshape/draw_context.go::DrawContext` — interface gains
  `OpenTemporaryFont(family, variant, size)` and `CloseTemporaryFont`.
  Doc comments call out the permanent-pool no-op semantics so callers
  can defer-close indiscriminately.
- `mazarin/textshape/draw_impl.go` — DrawContextImpl implements the
  new methods, forwarding to TextLayout. Metrics-cache population
  factored into `cacheFontMetrics` shared by OpenFont and
  OpenTemporaryFont.
- `mazarin/fontcache/provider.go::FontSvcGlyphProvider.OpenTemporaryFont`
  — third path (fonts not in permanent pool, not registered) now
  falls back to `OpenFont` instead of returning ENOSYS. Fonts go to
  the permanent pool; CloseTemporaryFont is a no-op for them. Makes
  the new API safe for callers (louis14) before the real temp-pool
  IPC lands.

**louis14 side (`pkg/render/render.go`):**

- `Renderer` gains `tempFontIDs []int32` field — collects fontIDs
  opened during the current Render call.
- `openFont` switches `r.dc.OpenFont` → `r.dc.OpenTemporaryFont` and
  appends each returned fontID to `tempFontIDs`.
- New `closeTempFonts()` helper releases each ID via
  `dc.CloseTemporaryFont` and drops the per-render `fontCache` so the
  next render re-opens cleanly.
- Both public entry points (`Render`, `RenderEmbedded`) reset
  `tempFontIDs[:0]` on entry and `defer r.closeTempFonts()`.

### Build / smoke status

- `$GO tool task` (full ARM64 chain): clean — diplomat, kmazarin,
  rachel, fontsvc, fs, linux, **mail.elf** (which had been blocked on
  louis14's WIP layout migration earlier today).
- `$GO tool task run-arm64-hvf TIMEOUT=30`: boots clean. fontsvc
  registers handlers, font index loads, mail-app starts. No panics,
  no `[fontsvc] no free font slots`, 0 SCORCH, 0 EISDIR. fti
  completes its 317-doc indexing in ~17s, heap stable
  (linux=6MB, fti=4MB).

### Behavior at runtime

Until `FontSvcGlyphProvider`'s real temp-pool IPC implementation
lands, the new path is functionally equivalent to the old:

- measure.go opens fonts via `OpenFont` (permanent pool).
- render.go opens via `OpenTemporaryFont`. The provider's
  permanent-first dedupe matches measure's open and returns the same
  fontID. CloseTemporaryFont on those is a no-op.
- @font-face fonts go through `findRegistered` → `openRegistered`
  (in-process, allocates from the local provider table). Close is
  also no-op for those (they're permanent-range from the provider's
  point of view).

So the louis14 changes are exercising the new entry points end-to-end
without yet exercising the temp pool. The temp pool kicks in once
`FontSvcGlyphProvider.OpenTemporaryFont` gets the real IPC path
(SharePagesWithTarget for caller bytes, mask 0x1000 on receive,
local temp slot table). Tracked in task_plan.md under the resumable
temp-font work.

### Files modified

mazzy: `mazarin/textshape/{layout.go, draw_context.go, draw_impl.go}`,
`mazarin/fontcache/provider.go`, `task_plan.md`, `progress.md`.

louis14: `pkg/render/render.go`.

---

## Session: 2026-04-25 (evening) — GridFrame scrollbar (constraint-driven)

Implemented the mail-header grid scrollbar (item 1 of two urgent UX
items the user pulled forward over the temp-font work). Visibility,
maximum scroll, and thumb fraction all flow through constraint
programs over the grid's published attrs — no per-frame imperative
wiring beyond geometry positioning.

### Code shipped (mazzy)

- `mazarin/mancini/greater_i64_bool.{vgo,vbc.go}` — new constraint
  program: `a > b` returning bool. Drives scrollbar `Visible`.
- `mazarin/mancini/nonneg_sub.{vgo,vbc.go}` — new constraint program:
  `max(a-b, 0)` returning int64. Drives scrollbar `MaxAttr`.
- `mazarin/mancini/thumb_frac_permille.{vgo,vbc.go}` — new constraint
  program: `visible * 1000 / total` returning int64 (permille,
  0..1000). Drives scrollbar `ThumbFracPermilleAttr`. Edge-clamped:
  total ≤ 0 or visible ≥ total → 1000; visible ≤ 0 → 0.
- `mazarin/mancini/bind.go` — three new helpers:
  `GreaterI64Bool(aURI, bURI)`, `NonnegSubI64(aURI, bURI)`,
  `ThumbFracPermille(visibleURI, totalURI)`.
- `mazarin/mancini/std/scrollbar.go::Scrollbar` — adds optional
  `ThumbFracPermilleAttr`. When set, ThumbFrac = permille / 1000
  (constraint-driven). When nil, falls back to the legacy pixel-mode
  `mainAxis / (mainAxis + max)` calc used by ScrollerVertical.
- `mazarin/mancini/std/grid_table.go::GridTable` — promotes three
  internal fields to published attrs:
  - `TotalRowsAttr` (written by SetTotalRows)
  - `ScrollOffsetAttr` (written by ScrollBy / MoveSelection /
    clampScroll; *shared with the GridFrame scrollbar's ValueAttr*
    so drags and arrow keys feed the same source of truth)
  - `RowHeightAttr` (refreshed each Draw)
  - `Draw` pulls ScrollOffsetAttr back at top so external mutators
    (scrollbar drag) take effect on the next frame.
- `mazarin/mancini/std/grid_table.go::GridFrame` — embeds a vertical
  Scrollbar at the right edge:
  ```
  scrollNeededAttr = GreaterI64Bool(TotalRowsAttr, VisibleRowCountAttr)
  scrollMaxAttr    = NonnegSubI64(TotalRowsAttr, VisibleRowCountAttr)
  thumbFracAttr    = ThumbFracPermille(VisibleRowCountAttr, TotalRowsAttr)
  scrollbar.Visible             = EqualBool(scrollNeededAttr)   ← user-required
  scrollbar.ValueAttr           = grid.ScrollOffsetAttr (shared)
  scrollbar.MaxAttr             = scrollMaxAttr
  scrollbar.ThumbFracPermilleAttr = thumbFracAttr
  ```
- `GridFrame.Draw` shrinks the grid subtree's effective draw width by
  the scrollbar track width when `scrollbar.Visible()` (the
  constraint-driven gate) is true; positions the scrollbar at the
  right edge after dividers.

### Design choice: ThumbFrac via constraint

The user pushed back on an early draft that branched inside
`Scrollbar.Draw` (row-mode vs pixel-mode). Cleaner answer: source
ThumbFrac entirely from the constraint network so the scrollbar has
no idea what its consumer is doing. `ThumbFracPermilleAttr` is the
single hook; `ThumbFracPermille(visible, total)` is the program; both
are shared between GridFrame and the (paused) ConsoleFrame work.

### Build / smoke status

- `$GO tool task mancini:build`: clean.
- rachel / versai / calc / clocks all build clean against the new
  mancini surface.
- Smoke test deferred to the louis14 unblock (mail-app:arm64 was
  blocked on louis14's WIP layout migration at the time).

### Console rewrite (item 2) — PAUSED

Foundation building blocks (the three constraint programs +
ThumbFracPermilleAttr on Scrollbar) are general — ConsoleFrame will
reuse them line-for-line. Detailed resume notes in `task_plan.md`
under "Resumable: Console rewrite". User explicitly paused this to
unblock mail-app via louis14 changes.

---

## Session: 2026-04-25 (evening, continued) — Diversion #7 temp fonts foundation

After closing Diversion #6 (committed `70e50d2`) the verification run
exposed `[fontsvc] no free font slots` repetition late in a click-heavy
session. This session lands the **mazzy-side foundation** of the
"mushy middle" temp font design — permanent pool 64 + temp pool 32,
explicit `OpenTemporaryFont`/`CloseTemporaryFont` lifecycle, fontIDs
for temp slots OR'd with `0x1000` so dumps unambiguously identify the
pool.

### Code shipped

- `shared/wm/uring_font.go`: new IPC types
  `OpenTemporaryFont`/`OpenTemporaryFontReply`/`CloseTemporaryFont`/
  `CloseTemporaryFontReply` + four new `MsgType*` constants (104–107) +
  `TempFontIDBase` / `TempFontIDMask` / `IsTempFontID`. Encode/Decode
  symmetric, dispatch wired through `DecodeFontRequest` and
  `DecodeFontResponseFromPayload`.
- `mazarin/textshape/types.go`: `GlyphProvider` interface gained
  `OpenTemporaryFont(req, data) (FontMetrics, error)` and
  `CloseTemporaryFont(fontID) error`.
- `mazarin/textshape/rasterize.go::DirectGlyphProvider`: real
  implementation — permanent-first dedupe via `findCachedFont`,
  `Munmap` on close, slot clear. Used by louis14 standalone test
  harness.
- `mazarin/fontcache/provider.go::FontSvcGlyphProvider`: stub returning
  `OpenTemporaryFont: temp pool IPC not yet implemented`. Will be
  filled in next pass with `SharePagesWithTarget` + `0x1000` mask.
- `mazarin/fontcache/internal_provider.go::InternalGlyphProvider`:
  stub falls through to existing `OpenFont` path / registered-buffer
  path (rachel doesn't render HTML so the temp pool isn't load-
  bearing here yet).
- `mazarin/fontcache/protocol.go::FontSvcInjector`: two new methods
  `RegisterOpenTemporaryFontHandler` / `RegisterCloseTemporaryFontHandler`,
  matching `FontSvcInit` fields and impls. `wm` is now imported so the
  `wm.OpenTemporaryFont` struct can be passed by value across the
  shepherd→fontsvc callback boundary (no interface boxing → no cross-
  .maz type assertion failure).
- `maz/rachel/main.go`: dispatcher `OnFunc(ipc.ProtoFontRequest, …)`
  switch extended with `case wm.OpenTemporaryFont` / `case
  wm.CloseTemporaryFont` that forward to the new injected callbacks
  (nil-checked).
- `maz/fontsvc/main.go`:
  - `MaxTempFonts = 32` + `tempFonts[32]` + `tempFontOwner[32]`,
    initialized to `-1` in `init()`.
  - `allocTempFontID` / `findCachedTempFont` / `resolveFontID` (masks
    off `wm.TempFontIDBase` and routes to temp vs permanent table).
  - `handleOpenTemporaryFont`: permanent-first dedupe, temp-pool
    dedupe, `allocTempFontID`, helpers `resolveFamilyPath` /
    `loadAndParseFont` / `buildTempCache` / `shareCacheAndReplyTemp`.
    `msg.FontDataVA != 0` (caller-shared bytes path) currently
    returns `-ENOSYS` — the kernel-side bytes path lands next.
  - `handleCloseTemporaryFont`: validates fontID range, refuses
    cross-shepherd close (`-EPERM`), calls `releaseTempSlot`.
  - `CleanupShepherdFonts(deadSID)`: exported for rachel's
    `handleShepherdDeath` to call. Wiring deferred (see follow-ups).
  - `releaseTempSlot`, `sendOpenTempFontError`,
    `sendCloseTempFontReply`.
  - Existing `handleRequestGlyph` / `internalGlyphByGID` updated to
    use `resolveFontID` so temp fontIDs (with `0x1000` bit) work for
    tier-2 glyph requests.

### Build status

- `$GO tool task fontsvc:arm64`: clean.
- `$GO tool task` (default ARM64 chain through rachel.maz, linux.maz,
  fs.elf, kmazarin, disk-staging): clean **up to** `mail-app:arm64`.
- `mail-app:arm64`: blocked on louis14 uncommitted Phase 13e′/13f
  `LayoutUnit` migration (`pkg/layout/flex_layout.go` callers haven't
  been updated to call `.Float64()` on the new `LayoutUnit`-returning
  `ResolveInlineSize` / `ResolveBlockSize`). This is the user's WIP
  in louis14 and is unrelated to the temp-fonts work — per
  CLAUDE.md / user directives I'm not touching louis14 without
  explicit go-ahead.

### Coordination doc

`design/louis14_temp_fonts_plan.md` (created earlier in the session)
spells out the louis14 call-site changes: switch HTML renderer's
`openFont` to `OpenTemporaryFont`, defer-close at render scope end,
`fontIDCache` keying. Awaiting user trigger to apply.

### Still to do

1. `FontSvcGlyphProvider` real IPC: SharePagesWithTarget for caller
   bytes, mmap of fontsvc's tier-1 cache pages on receive, mask
   `0x1000` bit on receive.
2. `InternalGlyphProvider` callback wiring via two new
   `FontSvcInjector` methods (rachel internal use, not load-bearing
   today but worth having for symmetry).
3. Hook `CleanupShepherdFonts(deadSID)` into rachel's
   `handleShepherdDeath`.
4. fontsvc bytes-path (`msg.FontDataVA != 0`) — kernel mapping
   primitive needed.
5. louis14 call-site changes per `design/louis14_temp_fonts_plan.md`
   — user-coordinated.
6. Smoke test once mail-app builds again.

---

## Session: 2026-04-25 — task #8 Pin/Unpin landed, partial verification

Implemented the inode-lifecycle Pin/Unpin plan from findings.md
"Fix plan — inode lifecycle (approved 2026-04-25)". All four
implementation steps landed exactly as specified.

### Code shipped

- `shared/fs/ext2/reader.go`: `pinMu sync.Mutex`, `inodeRefs
  map[uint32]int`, `pendingFreeSet map[uint32]bool` on `FileSystem`;
  initialized in `MountRW` (read-only mounts leave maps nil and the
  Pin/Unpin no-op on `!fs.writable`).
- `shared/fs/ext2/pin.go` (new): `PinInode`, `UnpinInode`,
  `isInodePinnedLocked`, `markPendingFreeLocked`, `reclaimInode`
  helper. UnpinInode runs deferred reclaim only when refs hit 0
  AND inum is in `pendingFreeSet`.
- `shared/fs/ext2/writer.go`: `Remove`, `WriteFile` overwrite
  branch, `Rename` target-overwrite branch — all reordered to
  remove the dirent first, then if `isInodePinnedLocked` mark
  `pendingFreeSet` and skip; else free inline as before. Immediate-
  free behavior unchanged on each path (Remove still zeroes inode,
  Rename still doesn't — preserved verbatim).
- `maz/fs/fsipc.go`: `ipcOpen` calls `fsys.PinInode(h.inum)` after
  `conn.allocHandle` succeeds, both Create branch and regular open.
  `ipcClose` widened to take `mt *mountTable` and calls
  `mt.getFS(h.kind).UnpinInode(h.inum)` before `freeHandle`.
  Audit confirmed `freeHandle` has only one caller (ipcClose).
- mutex chosen (not lock-free) per follow-up notes — fs.maz is
  sequential today but the fs.maz concurrency diversion will
  exercise this immediately.

### Build status

- `$GO tool task fs:arm64`: clean.
- `$GO tool task fs:x86_64`: clean.
- `$GO tool task` (default arm64 — diplomat + kmazarin + disk +
  ESP): clean.

### Verification (180s ARM64 HVF)

Boot reaches mail. fti indexes 14 docs cleanly, then:

```
[fti] SCORCH ASYNC ERROR: source: merger, persist error:
merging err: open /tmp/fti-30/bleve/store/00000000001d.zap:
no such file or directory (path=/tmp/fti-30/bleve/store)
```

After the SCORCH error, badger-side body fetch still served one
click successfully (`[mail] body: 138219 bytes variant=1`), but
subsequent clicks froze the UI — fti is in `corrupted` state and
mail-ui's body path waits on a fti that can't reply cleanly.

ENOENT-on-zap-reopen recurred *with* Pin/Unpin in place.

### Honest read

The fix is in shape exactly as planned, but the bleve symptom
recurred. We have no signal yet whether the deferred-reclaim path
fired even once: PinInode/UnpinInode/defer-free are silent. Two
possibilities, no way to distinguish without instrumentation:

1. Defer fires but ENOENT comes from a path the fix doesn't cover
   (e.g. real bleve unlink, or fsync ordering).
2. Defer never fires — handles are already closed by the time
   dirents are lost; root cause is something other than
   unlink-while-open.

### Next step (planned, not started)

Add one-shot instrumentation in:

- `PinInode` (refs 0→1) — emit `[ext2:pin] inum=N`.
- `UnpinInode` deferred-reclaim branch — emit
  `[ext2:unpin defer-reclaim] inum=N`.
- The pinned branch in `Remove`/`WriteFile`-overwrite/`Rename`-
  overwrite — emit `[ext2:defer-free] op=... inum=N path=...`.

Re-run, then update findings.md / task_plan.md based on which of
the three outcomes (defer fires for .zap / never fires / fires
for non-zap only) we observe.

### Commits

- `a04ce0a chore: remove RISC-V architecture support` (R1–R6 +
  x86_64 OOM fix from R6).
- `2d22496 feat: kernel/mail plumbing for bleve-on-tmpfs` (this
  session's Pin/Unpin + the earlier pipe-buffered Write fd-gating
  + ring split + linux-ui notify channel + x86_64 boot OOM fix +
  doc updates).

---

## Session: 2026-04-24 (later still) — linux-ui notify fix + bleve EISDIR found

Continued Diversion #3 (Windows-not-shown). Two real bugs landed and one
new bug surfaced.

### Pipe-buffered Write fd-gating — FIXED

After implementing pipe-buffered Write delegation (kernel returns byte
count immediately, no caller block), an Fstatat would EFAULT with its
data page found unmapped, then Go's MkdirAll would panic
`mkdir /tmp/fti-N/bleve/store: not a directory`.

Root cause: kernel pipe-buffered ALL Writes regardless of fd. fd>2 Writes
go to linux's *file lane* which calls `req.Reply(...)`. That spurious
SyscallReply landed on whatever delegate the caller-TID was *currently*
blocked on (often a later Fstatat), unmapping that other syscall's data
page and waking the caller with the wrong return value.

Fix: `pipeBuffered := id == sysid.Write && arg0 <= 2` in `DelegateSyscall`.
60s HVF run: 0 EFAULTs, 0 panics, fti indexed 51+ documents cleanly.

### Hack/print audit — clean

Audited linux/fs/bleve paths for hacks added during the chase. Removed:
`SysVerifyMapping` syscall + handle()-side defensive check (dead weight
once the spurious-Reply root cause was fixed); `[mkdirat:short]` /
`[linux] getdents64 marshalled` / `[linux] idle hint`/`idle flush` debug
Printfs; `[fs:tmp]` tmpTrace + `isTmpPath`; `[ext2:resolve]`
ResolvePathDbg hook. No actual hacks found in fti/maildb/bleve paths;
those are clean clients of the syscall surface. Real fixes kept:
fd-gating, `SyscallReleaseDelegatePage`, `CallerDead` flag, ext2
sparse-block fix in file.go::Write.

### linux-ui window disappearing — FIXED

User-reported: linux-ui window appears for 1-2s then vanishes from view.
Linux shepherd itself is alive throughout (still indexes mail).

Diagnostic: added per-SID blit-rate dump in rachel and 5s heartbeat in
`linuxapp.runLoop`. Found:
- linux-ui blits 28 times in first 5s, then ZERO afterward.
- `linuxapp.runLoop` heartbeat NEVER fires — goroutine blocked in
  the select forever.

Root cause: `linuxapp.runLoop`'s select watches `wmCh`/`eagerCh`/
`notifyCh`. linux-ui's `BuildResult` returned no `NotifyCh` (replaced
with a never-firing dummy by the framework). After startup constraints
settle, `eagerCh` and `wmCh` go quiet; the loop sleeps forever even
though `writeCh` is full of fti/maildb stdout.

Fix: matched the pattern mail-ui already uses via maildbio.
Added `NotifyChannel()` to `linuxio.LinuxIO`; linux-ui creates the
channel in `MazarinShepherd` and returns it via `BuildResult.NotifyCh`;
linux shepherd's `lineAccumulator` does a non-blocking poke after each
`writeCh` send.

Confirmed by `[rachel:geom] blit#500 sid=N title="Linux Console"` in a
post-fix 90s HVF run (vs. ceiling of ~28 before).

### Bleve scorch merger EISDIR panic — investigation

With linux-ui no longer dropping output, fti's recurring panic became
visible. Investigation followed the EAGAIN-on-current.mbox thread first,
which uncovered + fixed a separate ring-saturation bug, then exposed
the actual scorch failure mode.

#### linux ring 1 single-reader bottleneck — FIXED (2026-04-25)

Probes added at the EAGAIN return point in `DelegateSyscall` showed
linux's ring 1 stuck at fill=64 (capacity), head advancing only 1-3
messages per 100ms while the kernel was firing many sends per ms.
Diagnosis: ring 1 reader is a single goroutine doing **blocking**
channel sends to either `stdoutCh` (cap 32) or `delegateCh` (cap 8).
When `delegateCh` fills (file lane busy on fs IPC), the reader blocks
on the send, which also stops draining pipe-buffered Writes that ride
the same ring. Ring fills → kernel-side EAGAIN → "Resource Temporarily
Unavailable on current.mbox" / mmaptest FAILs / "throbber stops" /
"click no response" all originate here.

Fix (option 2 chosen by user — split traffic classes onto separate rings):
- New kernel syscall `SysRegisterStdioWriteRing` (0x1042) sets a global
  `stdioWriteRingIdx`. `DelegateSyscall` checks it on every Write and
  routes fd<=2 Writes to that ring index instead of the handler's
  default ring.
- linux now creates ring 1 + ring 2 via `uring.Setup`. Ring 1 carries
  blocking delegates (incl. fd>2 Write); ring 2 carries pipe-buffered
  stdio Writes only. Each ring has its own dispatcher / reader goroutine.
- linux's `MazarinMain` registers all syscalls on ring 1, then calls
  `sys.RegisterStdioWriteRing(2)`.

60s ARM64 HVF run after fix: 0 EAGAINs, 0 "Resource Temporarily
Unavailable", all mmaptest cases PASS, mbox import working, fti
indexing through 28+ docs before scorch's own bug bites.

#### Bleve scorch directory-mmap panic — captured (2026-04-25)

With the EAGAIN cascade gone, the dirent verifier left in tree caught
the actual smoking gun:

```
[fs:read EISDIR] handle=220 path="/tmp/fti-1/bleve" inum=12 ftype=2 isDir=true
[mmap-fill] sid=1 fd=6 offset=0 READ ERR: errno -21
[fti] PANIC in handleIndexDocument ... — marking index corrupted
```

**Bleve mmap'd the index DIRECTORY** (`/tmp/fti-1/bleve`), not a `.zap`
file. Mazzy's `SyscallMmap` does not validate fd kind, so the mmap
succeeded; first page fault returned EISDIR from fs.Read; kernel mapped
a zeroed page anyway and woke the caller; bleve read zeros where it
expected a segment header → nil deref → panic. On real Linux, `mmap()`
of a directory fd returns `ENODEV` upfront so bleve never enters a
corrupt state. Mazzy's recovery path on EISDIR-from-fault is wrong
(should be SIGBUS / error, not "map zero page and continue").

The "no such file or directory" symptom from earlier sessions was a
*downstream* effect of the same panic: once h.corrupted=true sticks,
subsequent merge/persist iterations try to open files scorch THOUGHT it
created in earlier rounds and fail with ENOENT.

### Bleve source review — bleve does NOT mmap directories (2026-04-25)

User asked to confirm bleve's actual mmap behavior on Linux. Read:

- `zapx/v15/segment.go::ZapPlugin.Open` — `os.Open(zapPath)` then
  `mmap.Map(f, mmap.RDONLY, 0)`. Path is always a `.zap` file.
- `bleve/v2/index/scorch/persister.go` lines 807, 1019 — opens
  segments via `s.segPlugin.Open(<dir>/<epoch>.zap)`.
- `bleve/v2/index/scorch/scorch.go` — `bolt.Open(s.path + "/root.bolt")`.
  bbolt then mmaps the bolt file's fd, not the dir.
- bleve also calls `os.ReadDir(s.path)` for `diskFileStats` (opens dir
  for `Readdirnames`, then closes — no mmap).

**Bleve never mmaps a directory on Linux. The bug is on our side.**

### Refined diagnosis — inode lifecycle, not mmap validation

Re-running with `[fs:open-dir]` and `[fs:mkdir-ok]` traces produced
*three* distinct symptoms that all share one root cause:

1. **EISDIR on mmap-fill**: handle was opened against a directory
   (legitimately, by `os.ReadDir`), the user-fd got closed, but a
   different mmap path now references that stale handle.
2. **EISDIR on write to `.zap`**: fs.maz opened the file at inum N
   (regular file); by write-time the on-disk inode at N has
   `Mode = TypeDir` because some `Mkdir`'s `allocInode` reused N
   while the file's open handle was still live.
3. **ENOENT on reopen**: same allocator/handle race — inode
   recycled, dirent gone with it.

Dirent verifier (left in tree) **never fires** — block-level
invariants are intact. The bug is at the inode allocation /
lifecycle level: `freeInode` + `allocInode` are not coordinated
with file-handle lifetime.

### Fix plan (approved) — Linux unlink-while-open semantics

Tracked as task #8 (re-scoped from "reject mmap on dir fds"):

- ext2: add `inodeRefs` + `pendingFreeSet` to `FileSystem`; new
  `PinInode`/`UnpinInode`/`isInodePinned`. `UnpinInode` does
  deferred reclaim when refs hit 0 AND inum is in `pendingFreeSet`.
- ext2: modify `Remove` / `WriteFile` overwrite / `Rename` overwrite
  to always remove dirent first, then defer free if pinned.
- fs.maz: `ipcOpen` Pins after handle alloc; `ipcClose` Unpins
  before freeHandle.

`allocInode` needs no change — pinned inums keep their bitmap bit
set, so the allocator naturally skips them.

### Follow-ups intentionally NOT in #8 (separate tasks)

- **#9 fs.maz OnDeath cleanup** — fs.maz registers OnDeath today
  but the callback only logs the death; it doesn't iterate the dead
  shepherd's `conn.handles[]` to free + unpin. With #8 in place,
  shepherd-death-with-open-handles leaks Pin refs → pinned inodes
  never reclaim. Bounded growth (per-boot only), not blocking.
- **#10 Memory-fault-then-kill design** — kernel currently maps a
  zeroed page on `MmapPageFill` error instead of `SIGBUS`-ing the
  offender. After #8 the path is mostly unreachable, but it's still
  a correctness gap. Needs architectural design before coding.
- **#11 ext2 hardlink semantics** — `Remove` doesn't decrement
  `LinksCount`. We don't make hardlinks today, so this is latent.

## Session: 2026-04-24 (later) — Two diversions queued before mail-dumb

User called for two diversions before resuming mail-dumb easy part:

1. **Remove RISC-V entirely** from the system — code, configs, tooling,
   Taskfile targets, docs.
2. **Design (no implementation) fs.maz multi-in-flight requests.**

Order: RISC-V removal first (cuts the surface area we'd otherwise have to
design around), fs.maz concurrency design second.

### RISC-V removal — survey done, plan recorded

Surveyed the riscv64 footprint with the Explore agent. Result captured in
findings.md "RISC-V Footprint Survey 2026-04-24". Headlines:

- ~98 `*riscv64*` source files (45 pure-tag, deletable; remainder
  named files in diplomat/kmazarin/runtime-patches/etc.)
- 23 combined-tag files needing surgical edits.
- 11 Taskfile targets across root + diplomat + kmazarin Taskfiles.
- 3 TOML configs (`config/{kernel,rachel,startup}.riscv64.toml`).
- Standalone dir `kmazarin/arch/riscv64/` (PLIC).
- Tooling references in mkesp, gen-ast-stubs, fix-go-elf, sysid.
- 7 CLAUDE.md references; ~7 memory files referencing riscv.
- No mazlink/mazdl entanglement (confirmed; legacy .maz path only).

Plan recorded in task_plan.md as "RISC-V Removal — Phases R1–R6":
R1 build/run plumbing → R2 pure-arch source delete → R3 combined-tag
edits → R4 tooling → R5 docs/memory → R6 verification (arm64 + x86_64
boot, mazlink smoke).

### fs.maz concurrency — placeholder section in task_plan.md

Open design questions listed: per-request data slot pool, ReqID demux,
ext2 thread-safety scope, backpressure, interaction with the existing
two-lane delegate handler. To be expanded after R6.

### Verification of plan before coding

- Confirmed with user: "RISC-V removal first, then fs.maz design."
- No code touched yet; awaiting user sign-off on the R1–R6 plan.

### R1–R6 execution

All six phases completed in one sitting.

- **R1 (build/run plumbing):** root + diplomat + kmazarin Taskfiles
  pruned; 11 root tasks deleted plus per-userspace `riscv64:` blocks
  (script-stripped from 18 sub-Taskfiles). `config/{kernel,rachel,
  startup}.riscv64.toml` deleted. `$GO tool task --list-all` clean.
- **R2 (pure-arch source delete):** 88 named `*_riscv64.go|.s` files
  deleted, plus `kmazarin/arch/riscv64/`, `diplomat/main/fdt_parse.go`
  (pure-tag), and three `runtime-patches/` RISC-V overlays
  (sys_linux_riscv64.s, rt0_linux_riscv64.s,
  diplomat-linux/trampoline_riscv64.s). Stale `.task/checksum/*riscv64*`
  cache cleared. `kmazarin/kmazarin/go_abi_macros_riscv64.h` deleted.
- **R3 (combined-tag surgical edits):** dropped riscv64 + loong64 from
  build tags via Python helper across kmazarin/ds/*, kmazarin/kmazarin/
  channels/scheduler/soft_irq/uring_ipc/etc, ksyscall/clone.go,
  runtime-patches/{cgo_mmap,mmap,tagptr_64bit,os_linux_noauxv}.go,
  mazarin/overlay/userspace/runtime/{cgo_mmap,maz_moduledata,proc}.go,
  mazarin/sys/maz_name_other.go. Six `_other`/`_stub` files lost their
  `!riscv64` tag entirely. `runtime-patches/sigaction.go` deleted (was
  RISC-V-only EBREAK workaround). `proc.go` lost its dead loong64/
  riscv64 case branches.
- **R4 (tooling + shared constants):** dropped RISC-V from cmd/elf-diff,
  cmd/elf2pe, cmd/fix-go-elf (deleted inject.go + removed -no-bootstrap
  flag), cmd/gen-overlay (deleted buildKmazarinRISCV64Overlay), cmd/
  maz-reloc (deleted scanTextRISCV64, isThinStubRISCV64, RelocJAL_RISCV,
  RelocJ_RISCV), cmd/mkesp. shared/sysid: dropped RiscvHWProbe;
  ksyscall/sysid + dispatch + stubs.go updated; hwprobe_stub.go +
  hwprobe_init_stub.go + main.go initHWProbeFromDTB call removed.
  saveTextChecksum no-op stubs removed from platform_arm64/amd64.
  elfMachineRISCV64 + RISC-V findFile fallback gone from elf_loader.go.
- **R5 (docs + memory):** CLAUDE.md scrubbed (env vars, run examples,
  monitor port 4447, "Current Status" RISC-V block, all 7 references
  gone). MEMORY.md: stale RISC-V mentions cleaned, new
  `riscv_removed.md` topic file added under Topic Files. Five other
  memory files patched (dma_clump_continuation, phase5_linux_plugin_done,
  zero_guard_false_positive). riscv_crash_investigation.md deleted.
- **R6 (verification):**
  - ARM64 default build: clean (`$GO tool task` builds diplomat +
    kmazarin + disk image + ESP image successfully).
  - **ARM64 HVF 45s boot: PASS** — kernel → fs → linux → linux-ui →
    helloworld; status tick at uptime=30s, syscalls=5884, threads
    running=1 ready=0 futex=6 sleep=4 — same shape as pre-removal.
  - **mazlink-smoke (arm64): PASS** — Phase 4 exit criteria 1-4 ok.
  - **x86_64 boot: FIXED during R6** (was pre-existing). See "x86_64
    boot OOM fix" section below.
  - `git grep` residue: stock-Go `runtime-patches/diplomat-linux/
    elf_file.go` (loong64/riscv64 relocation cases) and
    `runtime-patches/malloc.go` (riscv64 vmaSize branches), plus
    `cmd/gen-ast-stubs/main.go` skip-list — all intentional (overlay of
    stock Go; minimizing divergence from upstream). Doc files
    (findings.md, task_plan.md, progress.md, MEMORY.md, etc.) retain
    the removal record by design.

### x86_64 boot OOM fix (during R6)

**Symptom:** `[kmem] Buddy OOM order=0 total=ffffffff20000 alloc=a00 free=0
peak=0` immediately after "Jumping to kmazarin..." on every x86_64 boot,
followed by an instant page fault at `0xFFFF800100000000`.

**Root cause:** Two-part regression layered over each other.

1. **Linear-map cap underflow.** `kmazarin/kmem/buddy.go:95-100` clamps
   `end` to `linearMapMaxPA = 0x100000000` (4 GB) but does NOT clamp
   `start`. On the test box's QEMU TCG x86_64 with `-m 8G`, UEFI
   `AllocatePages(AllocateAnyPages, ...)` handed back the unified-pool
   physical range above 4 GB (e.g. `0x1E0000000-0x280000000`). Buddy
   stored `start=0x1E0000000`, end was clamped to `0x100000000`,
   `end-start` underflowed in `uint64` to `0xFFFFFFFF20000000`,
   `totalPages = 0xFFFFFFFF20000`. Every `AllocPage(order=0)` returned 0.
2. **No fallback when contiguous low RAM is short.** Even with the
   AllocateMaxAddress fix below, UEFI on this box can't satisfy a
   2.5 GB *contiguous* request below 4 GB (low RAM is fragmented after
   UEFI loads itself, diplomat, kmazarin, and the heap pool). Result:
   alloc fails entirely, `vm.UnifiedPoolStart=0`/`End=0`, kmazarin
   falls back to FramePool (16 MB heap pool), VirtIO GPU framebuffer
   (`order=0xd` = 32 MB) and InitConstraintPages (`order=0xc` = 16 MB)
   immediately OOM, fs launch fails with error -7.

**Fix (two changes in `diplomat/main/`):**

- `pagetable_amd64.go::allocatePhysPages`: switched
  `AllocateAnyPages` → `AllocateMaxAddress` with seed
  `physAddrResult = linearMapMaxPA - 1`. This mirrors the existing
  ARM64 `pagetable_arm64.go` workaround documented in memory note
  "QEMU ARM64 virt Memory: UEFI `AllocateAnyPages` can return >4GB —
  use `AllocateMaxAddress` with `linearMapMaxPA - 1`". It was simply
  not applied to amd64.
- `kernelvm_amd64.go::PrepareKernelVM`: added a
  `[2.5GB, 1.5GB, 1GB, 512MB, 256MB]` retry-down loop for the unified
  pool allocation. First size that UEFI can satisfy wins; only the
  whole list failing produces "Unified pool alloc FAILED". On the test
  box the 2.5 GB allocation fails and 1.5 GB succeeds; `Unified pool:
  0x17D6C000-0x77D6C000 (0x60000 pages)`.

**Verification (90s TCG x86_64 boot):** boots cleanly through
`Jumping to kmazarin...` → buddy alive (no OOM) → VirtIO GPU init
(1800x1200) → VirtIO Input scan (kbd + tablet) → fs → linux → linux-ui →
helloworld → fti → rachel → `[rachel:wm] event loop started`. Same
boot depth as ARM64 HVF. Status ticks at 30s and 50s; 22K syscalls;
no panics. Window does not yet appear, but that's the same situation
as ARM64 — separate problem.

**Why this regression existed in the first place:** the memory note
about `AllocateMaxAddress` was an ARM64-specific finding that never
got cross-applied. x86_64 worked historically only when its UEFI
happened to give us low-RAM pages by coincidence; once UEFI's
allocator behavior changed (or QEMU's memory map changed) the
allocation crept above 4 GB and the cap silently rotted into a wrap.

### Resume point

Next: fs.maz concurrency design (paper only, no implementation). After
that, mail-dumb easy part resumes. Window-not-appearing is a known
problem on both arches — not addressed here.

---

## Session: 2026-04-24 (even later) — Windows-not-shown diversion, scheduling bug surfaced

### User direction
Take a detour before fs.maz concurrency design: debug why windows are
not shown on ARM64 HVF (background color visible, no window chrome).
Constraint: do not change GOMAXPROCS (kernel not proven multi-core
safe, and bumping it would only mask the bug until it bites later).

### Baseline (60s ARM64 HVF)
`[rachel:wm] event loop started` fires but no `[rachel:wm] AppStart`
ever arrives. Linux shepherd reaches `[linux] Ready=true`, loads
linux-ui, but linux-ui never calls `AnnounceToWM`.

### Diagnosis via instrumentation

1. **linux-ui MazarinMain goroutine never starts.** Added a print at
   the top of MazarinMain and inside `mazhost.runWithLargeStack` —
   neither fired. `go mazhost.RunMaz(uiMain)` enqueued a goroutine
   that was never scheduled.

2. **Adding `runtime.Gosched()` right after `go mazhost.RunMaz(uiMain)`
   unblocked it** — linux-ui's MazarinMain and Bootstrap then ran
   through to the first font IPC.

3. **Making `mazhost.LaunchMaz("helloworld")` async** (run in its own
   goroutine) was then needed so linux's main goroutine would reach
   the stdin drainer (its first blocking channel op) rather than
   spinning CPU-bound in `mazdl.OpenBytes` during the helloworld
   plugin load.

4. **Preloading `fonts.csv` in `fontsvc.MazarinMain`** (user suggestion)
   moved the CSV `sys.LoadFile` out of the handleOpenFont callback.
   With that, linux-ui reaches Bootstrap's font loading, and fs reads
   `AtkinsonHyperlegibleMono-Regular.otf`.

5. **Next stall: fs's `fmt.Printf` about the .otf read blocks in Write
   delegation.** Kernel probes (now reverted) showed:
   - fs's `syscall.Write` → kernel → `uringSendKernel` to linux ring 1
     succeeds (`sendResult=0`).
   - Linux's ring 1 reader thread is woken via the slot's BlockedTID
     wake path (`[DLG:wake] woke tid=X`).
   - BUT the reader goroutine never runs its handler (no
     `[uring:reader] ring=1 #N proto=...` trace after the wake).

### Fstatat DataVA fault — FIXED (2026-04-24)

Two-part fix for the page fault hit by linux's file-lane handler when
it tried to read a delegate's `r.dataVA`:

1. **Root cause: caller-death cleanup unmapped live delegate pages.**
   `CleanupDelegateForDeadShepherd` Part 1 called `reclaimDataPage`
   when a caller died mid-delegate, but the caller's uring msg could
   still be queued in the handler's ring. The handler popped the msg
   later and faulted on the dangling handler-side VA.

   Fix: added `CallerDead bool` to `DelegateCallInfo`. Cleanup Part 1
   now just sets `CallerDead=true` and leaves the page mapped. The
   handler's `SyscallReply` runs the reclaim code as normal but skips
   `wakeDelegateCallerThread` (no blocked caller to wake).

2. **Defense-in-depth in the linux handler.** New kernel syscall
   `SysVerifyMapping` (0x1042). Linux's `handle()` calls it before
   dispatching to sys{Fstatat,Openat,Mkdirat,...}. If `r.DataVA()`
   is unmapped, reply `EFAULT` — matches Linux's pipe-side "bad
   pointer" semantics and keeps the handler from crashing on any
   delegate-page-unmapped condition we haven't yet enumerated.

With both fixes: no more `unexpected fault` in linux; linux-ui
window appears and stays up. `[fti] FATAL mkdir ... not a directory`
still fires — separate fti↔tmpfs bug where bleve's mkdir-of-subdir-
after-openat-of-sibling returns ENOTDIR. Not tracked here.

---

### Pipe-buffered Write delegation — IMPLEMENTED (2026-04-24)

Added `SysReleaseDelegatePage` kernel syscall (0x1041). Modified
`DelegateSyscall` so that for `sysid.Write` it skips the
`blockForDelegatedSyscall` path and returns byte-count immediately
(Linux-pipe semantics — writer doesn't wait for reader). The
handler's stdout lane calls `sys.ReleaseDelegatePage(v.DataVA(), 1)`
after consuming the bytes, replacing the previous `v.Reply(len)`.

All three earlier workarounds REVERTED:
- `runtime.Gosched()` after `go mazhost.RunMaz(uiMain)` — gone.
- Async `go mazhost.LaunchMaz("helloworld")` — back to synchronous.
- fontsvc `MazarinMain` `ensureFontIndex()` preload — gone.

**Result (ARM64 HVF):** linux-ui window NOW APPEARS (user confirmed
seeing it for 1-2 seconds). `AppStart` → `BackingStoreReady` flow
completes. fs's `fmt.Printf` inside `readFileIntoPages` no longer
blocks fs's thread, so rachel's fontsvc `sys.LoadFile` can get its
reply, linux-ui gets its font, Bootstrap finishes, window renders.

**Separate latent bug surfaces** (tracked as task #3): Fstatat
from fti faults when the linux file-lane handler reads
`r.dataVA`. Fault occurs whether or not my Release is called
(verified by leak-mode test). It's a pre-existing bug uncovered
by the system now progressing past the old deadlock point.

---

### Interpretation (after user challenge — corrected, earlier in session)

My earlier "runnable-G-without-M" diagnosis was misleading — that
dump came from a probe-induced panic, not the normal stall. Go's
wakep works fine on Mazzy. Retracted.

**Real diagnosis:** Linux emulation semantic gap in `write(fd=1)`.
On Linux, stdout writes to a pipe buffer and return near-instantly
(writer doesn't block on reader). On Mazzy, `syscallWriteDelegate`
→ `DelegateSyscall` → `blockForDelegatedSyscall` holds the caller
in `ThreadBlockedDelegate` until the handler shepherd replies —
writer and reader synchronously coupled across the delegate
boundary, not buffer-decoupled like a pipe.

That synchronous coupling is only a deadlock when it closes a
cycle. The cycle we hit at linux-ui's first font request:

1. Rachel's uring-reader goroutine calls `sys.LoadFile` inside
   the `handleOpenFont` callback → rachel's reader blocked.
2. fs's `handleLoadFile` → `readFileIntoPages` calls `fmt.Printf`
   → after linux is Ready, Write delegates → fs blocked.
3. linux-ui's Bootstrap → `NewConsole` → `fc.OpenFaceByName` →
   waiting on rachel's reply → linux-ui blocked.

Linux itself is mostly fine — main is in stdin drainer, stdout
lane goroutine is runnable — but fs's delegated Write holds
the head of the chain until linux replies, which can't fully
unblock the cycle because rachel is stuck behind fs which is
stuck behind linux which is (indirectly) stuck behind rachel.

**Why this stopped working recently (needs git-blame):** the
cycle didn't exist until some recent change around fontsvc's
lazy-load-on-first-OpenFont, or fs's `fmt.Printf` inside
`readFileIntoPages`, or linux-ui's load ordering. All three
are newer than the kernel delegate mechanism they're tickling.

**Why each workaround coincidentally sidesteps it:**
- `runtime.Gosched()` after `go RunMaz(uiMain)` shifts the timing
  of linux-ui's first font IPC relative to linux's Write-handler
  registration.
- Async `LaunchMaz("helloworld")` prevents linux main from tying
  up CPU while linux-ui tries to complete the font handshake.
- fontsvc preloading `fonts.csv` eliminates one edge of the
  cycle entirely.

**Correct fix direction: close the emulation gap.** Make Write
delegation pipe-like — `DelegateSyscall` for Write copies bytes
into a shared ring and returns immediately, instead of blocking
the caller until the handler replies. The handler drains the
ring asynchronously. This matches Linux semantics and fs's
`fmt.Printf` during delegate handling stops being a deadlock
primitive. Revert all three workarounds once the fix lands.

### Changes kept in the tree (workarounds, to be removed once real fix lands)
- `maz/linux/main.go`: `runtime.Gosched()` after `go mazhost.RunMaz(uiMain)`;
  `mazhost.LaunchMaz("helloworld")` now runs in its own goroutine.
- `maz/fontsvc/main.go`: `MazarinMain` now calls `ensureFontIndex()` so
  `fonts.csv` is loaded before any IPC callback fires (pre-loading is
  arguably a legitimate design choice, but its effect here is to hide
  the Write-delegate stall for the CSV leg).

### Changes reverted at end of session
Probe instrumentation in: `mazarin/mazhost/load.go`, `maz/linux-ui/main.go`,
`mazarin/mancini/linuxapp/linuxapp.go`, `mazarin/fontcache/index.go`,
`maz/fs/main.go`, `mazarin/uring/reader.go`, `kmazarin/ksyscall/write.go`,
`kmazarin/ksyscall/delegate.go`, `kmazarin/kmazarin/uring_ipc.go`.

---

## Session: 2026-04-24 — Fs↔Linux delegate deadlock FIXED

### Diversion from mail-dumb easy part

Discovered a prior workaround in `maz/fs/fsipc.go:tmpTrace` routing fs
diagnostics through `sys.UartWriteString` to avoid an IPC deadlock. Root
cause: linux's delegate worker is a single goroutine that blocks on
`fsclient.RespCh` while fs processes a request; fs's `fmt.Printf` during
that handler would try to `Write(fd=1)` back to linux, which queues behind
the blocked worker, which can't drain until fs responds, which can't happen
because fs is waiting for its Write to be replied. See findings.md
"Fs ↔ Linux Delegate Handler Deadlock" for the full cycle analysis.

### Fix: two-lane delegate handler

Peeled `Write(fd ≤ 2)` off the file lane onto its own stdout lane.

**`maz/linux/main.go`:**
- `sidStates` guarded by `sync.Mutex` (only shared state between lanes).
  `sidIncRef` / `sidDecRef` / `handleDeathNotification` all take the lock
  briefly; cleanup/DeathAck happen outside the lock so fsclient calls don't
  run under it.
- `startUringDispatchers` now takes a `stdoutCh chan sys.SyscallRequest`.
  Ring-1 dispatch switched from `On(ProtoFSDelegateReq, ... delegateCh)` to
  `OnFunc` that inspects the payload: `Write` with `fd ≤ 2` → `stdoutCh`,
  everything else → `delegateCh`.
- `startUringDelegateHandler` now spawns two goroutines:
  - **Stdout lane** consumes `stdoutCh`: sidIncRef, copy bytes,
    `v.Reply(len)`, sidDecRef, push to `dataCh`. Never touches fsclient.
  - **File lane** consumes `delegateCh`: unchanged logic, handles file
    syscalls (may block on fsclient), stdin, death/idle notifications.

**`maz/fs/fsipc.go`:**
- `tmpTrace` rewritten to use `fmt.Printf` (was `sys.UartWriteString`).
- Two other `sys.UartWriteString` calls in `processRequest` and `respond`
  swapped to `fmt.Printf`.
- Big "must use UartWriteString" comment removed (no longer true — now
  references findings.md for the full background).
- `mazzy/mazarin/sys` import dropped (unused).

**`maz/fs/main.go`:**
- `readFileIntoPages` timing log switched to `fmt.Printf` (runs in the
  steady-state LoadFile handler).
- Boot-sequence and OnDeath `sys.UartWriteString` calls left as-is (run
  pre-linux-ready or when linux may be dying).

### Verification

- `linux:arm64`, `linux:x86_64`: clean builds.
- `fs:arm64`, `fs:x86_64`, `fs:riscv64`: clean builds.
- `linux:riscv64` fails at `mazarin/mazdl.applyRelative` etc. — verified
  pre-existing via git stash + rebuild (mazdl RISC-V port lagging per
  memory, not related to this change).
- ARM64 HVF 60s run: boots through kernel → fs → rachel → linux → prefs →
  helloworld; two status ticks at 30s/50s; no hangs. `[fs] /rachel.maz:
  ... 41ms` etc. confirms `fmt.Printf` from fs handlers is reaching the
  serial console via the stdout lane.
- Bleve `/tmp` write path (the original deadlock trigger) only fires when
  mail app launches; not exercised in boot. Structural fix is correct;
  runtime exercise left for the next mail-dumb session.

### Files changed

- `maz/linux/main.go` — sidStates mutex; stdoutCh; two-lane delegate
  handler; OnFunc-based ring-1 dispatch.
- `maz/fs/fsipc.go` — fmt.Printf in tmpTrace + two other sites; comment
  rewrite; `sys` import removed.
- `maz/fs/main.go` — fmt.Printf for readFileIntoPages timing log.
- `findings.md` — new top section on the IPC cycle + fix.
- `task_plan.md` — pivot section flagging the diversion.
- `progress.md` — this entry.
- `.claude/.../memory/feedback_no_workaround_deadlocks.md` — new feedback
  memory: don't paper over IPC cycles with UART bypasses.

### Resume point

mail-dumb easy part — body display, PageUp/PageDown, mark-read, delete.
See "Stashed: mail-dumb easy part" in task_plan.md for pre-diversion
status.

---

## Session: 2026-04-23 — mail-dumb hard part COMPLETE

### Smart Cache Phases S1–S4 implemented and verified

All four phases are done and verified in a 90s ARM64 HVF run.

**Phase S1 (GridTable virtual scroll):**
- `rowFactory`, `slotPool`, `slotWidgets`, `slotLabels`, `poolEpoch`, `scrollOffset`, `visibleCount`, `TotalRows`
- `buildSlotPool(count)`, `computeVisibleCount(h, rh)`, `applyScrollToSlots()`, `publishScrollAttrs()`
- `FirstVisibleMsgNumAttr`, `LastVisibleMsgNumAttr`, `VisibleRowCountAttr`
- `GridFrame` forwarding: `SetTotalRows`, `SetRowFactory`, `ScrollBy`, `MoveSelection`, three attr accessors

**Phase S2 (MailCache + MailRow):**
- `MailCache`: `entries map[uint32]*KeyHeaderEntry`, sliding window `[windowLo, windowHi]`,
  single in-flight request, `Rebalance`, `HandleResponse` (KeyHeaders + CollectionAdd/Remove),
  `OnUpdated`/`OnExpired` callbacks, `nextReqId` via UnixNano + counter
- `MailRow` (`mail_row.go`): virtual row backed by cache; `Sender/Subject/Date` return "…" on nil entry;
  `MsgNum() / SetMsgNum()` satisfy both `GridRow` and structural `MsgNumSetter`

**Phase S3 (wire main.go):**
- `handleCreateCollectionResp`: creates `MailCache`, calls `SetCollection`, wires `OnUpdated`/`OnExpired`,
  calls `SetTotalRows` + `SetRowFactory`, fires initial `Rebalance` (no-op if vis=0)
- `handleMailResponse`: routes `RespCreateCollection` to setup fn; everything else to `cache.HandleResponse`
- `mailRespCh` handler: `redraw` BEFORE `Rebalance` (eagerCh drain-race fix — see findings.md)
- `eagerCh` handler: `Rebalance` then `redraw` (correct — reads already-published vis attr)

**Phase S4 (batch unpack):**
- `handleKeyHeaders` in `MailCache`: reads `Count × KeyHeaderEntrySize` bytes from `TargetVA` via
  unsafe pointer cast, stores `eCopy` in entries map (stable pointer), frees pages via `mem.FreePages`

**Arrow key navigation:**
- `GridTable.MoveSelection(delta)`: moves `selectedMsgNum`, clamps, scrolls to keep visible,
  publishes attrs, damages all
- `mail/main.go`: `wm.KeyPress` case intercepts `ActionUp`/`ActionDown` → `gridFrame.MoveSelection(±1)`

**Verification:**
- 90s ARM64 HVF runs: 100/100 docs indexed, blit#1000 reached, no panics
- All 14 rows display with real email data (Rob at Cockroach Labs, GoDaddy, etc.)
- Arrow key scroll confirmed smooth by user ("smooth like butter")
- Debug print `[grid] visibleCount %d→%d` removed from grid_table.go

**What's next:** mail-dumb easy part — body display, PageUp/PageDown, mark-read, delete.

---

## Session: 2026-04-23 — Smart cache design + plan (PLANNED, not yet coded)

### Design session: smart mail cache Phases S1–S4

Full design worked out in conversation; plan written to `task_plan.md` §Smart Cache
and `findings.md` §Smart Cache Architecture.

**Key decisions made:**
- Virtual scroll: GridTable has a fixed pool of `visibleCount` slot widgets; same
  objects are reused across scrolls via `SetMsgNum` (MsgNumSetter interface)
- Pool size = integer `(contentH - headerH) / rowHeight` — no partial rows
- Font size change rebuilds pool (epoch-stamped widget names avoid URI collisions)
- GridTable publishes three new value attrs: `firstVisible`, `lastVisible`, `visibleCount`
- Cache window: `[max(0, firstVisible - readAhead×visCount), min(collSize-1, lastVisible + readAhead×visCount)]`
- `readAhead = 2` (constant); one in-flight request at a time
- `MailCache.Get(msgNum)` is synchronous; nil → show "…" placeholder
- `MailCache.Rebalance()` triggered by eagerCh in main after attrs update
- `VirtualMailRow.Sender/Subject/Date` read from cache; `SetMsgNum` called by grid on scroll
- CollectionAdd/Remove: evict affected cache range, update collSize, let Rebalance refetch
- `selectedMsgNum int64` (not `selectedRow GridRow`) in GridTable to survive pool rebuilds
- Old MailRow file deleted; MailCache owns all maildb I/O and reqId tracking
- `OnExpired func()` on MailCache for ErrCollectionExpired path

**Implementation order decided:** S4 (batch unpack) → S2 (VirtualMailRow + MailCache) → S1 (GridTable scroll) → S3 (wire main.go)

**Open items before coding:**
- Confirm keyboard event type (`wm.KeyboardPress`?) and key constants for arrow/page keys
- Decide: keep or delete `mail_row.go` (delete preferred)
- Verify `mem.FreePages` signature matches what cache will call for batch page release

---

## Session: 2026-04-23 — Phase 3 multi-selection set + ValueCollI64 (COMPLETE)

### Phase 3 complete: ValueCollI64 infrastructure + GridTable multi-selection

**New kernel region and syscall:**
- `kmazarin/kmem/constraint.go`: added `RegionValueCollSlots=32`, `MaxValueCollEntries=256`,
  `RegionValueCollCap=8192`; `RegionValueCollOff` appended after trie region;
  `ConstraintPageVersion` 3→4. `SharedPageHeader` gains `ValueCollRegionOff uint32` and
  `ValueCollCapacity uint32` at byte offset 64.
- `shared/mazzy/mazzy.go`: `SysAttrWriteCollI64 = MazzySyscallBase + 45 // 0x102D`
  (slot 44 was already SysRequestWindowManager — not 44 as originally specced in findings.md).
- `kmazarin/ksyscall/mazzy.go`: dispatch table entry `45: SyscallAttrWriteCollI64`.
- `kmazarin/ksyscall/constraint_mgr.go`: `valueCollRegionOff`, `valueCollNextSlot`,
  `valueCollSlotCount` fields; `allocValueCollSlot()` bump allocator.
- `kmazarin/ksyscall/constraint_syscall.go`: `SyscallAttrWriteCollI64` — validates slot,
  finds or reuses existing ValueColl slot (by checking CachedValue ElemType==TypeI64),
  copies int64s from userspace 8 bytes at a time via WalkUserPageTable, writes
  `flat.NewI64(v)` items, stores CollRef, dirty-propagates.

**Userspace flat layout:**
- `mazarin/vm/flat/layout.go`: added `ValueCollections []byte` to `PageRegion`;
  `ReadCollectionElement` dispatches by `ref.ElemType`: TypeStr → `Collections`,
  others → `ValueCollections`.
- `mazarin/vm/flat/layout_shared.go`: `SharedPageVersion` 3→4; parses new
  `ValueCollRegionOff`/`ValueCollCapacity` header fields; slices `ValueCollections`
  region into `PageRegion`.

**Userspace attr layer:**
- `mazarin/sys/constraint.go`: `AttrWriteCollI64(slot, values, isConstraintResult)`.
- `mazarin/attr/attribute.go`: `isCollI64 bool` field; `Set()` isCollI64 branch casts
  via `unsafe.Pointer` to `[]int64` and calls `sys.AttrWriteCollI64`.
- `mazarin/attr/attribute_value.go`: `ValueCollI64(uri, initial) *Attribute[[]int64]`
  with `isCollI64: true`; `toT` reads via `sharedPR.ReadCollectionElement`.

**GridTable multi-selection:**
- `mazarin/mancini/std/grid_table.go`: new fields `selectedSet map[GridRow]bool`,
  `SelectedSetAttr *attr.Attribute[[]int64]`, `SelectedSetCountAttr *attr.Attribute[int64]`,
  `SelectedSetPagesAttr *attr.Attribute[int64]`.
- `setSelected`: shift-click detected via `hid.Shift(int64(ev.Mods))`. Normal click resets
  set; shift-click toggles row (primary cannot be removed). `publishSelectedSet()` called
  after either path.
- `publishSelectedSet()`: sets CountAttr (true count), PagesAttr (ceil(count/512) inline),
  and SetAttr (nil / [MaxInt64] sentinel / []int64 of MsgNum values). Sentinel fires when
  >256 items selected.
- `GridFrame` accessors: `SelectedSetAttr()`, `SelectedSetCountAttr()`, `SelectedSetPagesAttr()`.
- `RefreshSelected` also calls `publishSelectedSet()`.

**Build verification:** `mancini:build`, `mail-app:arm64`, `kmazarin:arm64`, `kmazarin:x86_64`,
`mail-app:x86_64`, `mail-ui:arm64` — all pass clean.

---

## Session: 2026-04-22 — Phase 2 click wiring + SelectedAttr (COMPLETE)

### Phase 2 complete: click routing + SelectedAttr

- `RowPercentage`: added `SelectionState int`, `OnClick func(*mancini.InputEvent)` fields.
  Implemented `Click(*mancini.InputEvent) bool` — calls `OnClick` if set.
  `Draw`: fills selection background before children (state 1 = `Highlight()` α160, state 2 = `Accent()` α120).
- `GridTable`: added `selectedIdx int` (init -1) and `SelectedAttr *attr.Attribute[int64]`
  (URI `layout:///NAME/int64/grid/selected`, initial -1).
  `AddRow` wires `rp.OnClick` closure capturing row index → calls `gt.setSelected(idx, ev)`.
  `setSelected`: updates `selectedIdx`, publishes `rows[rowIdx].MsgNum()` to `SelectedAttr`,
  calls `DamageAll()`.
  `Draw`: sets `rp.SelectionState = 1` for selected row, 0 for all others.
- `GridFrame`: added `SelectedAttr() *attr.Attribute[int64]` accessor.
- `mail_row.go`: removed `onRowSelected` field, removed from `NewMailRow` signature, removed `Select()`.
- `main.go`: updated all 3 `NewMailRow` calls to drop `onRowSelected` arg; removed `onRowSelected` func.
- Build: `task mail-app:arm64` and `task mail-app:x86_64` both pass clean.

---

## Session: 2026-04-22 — smart caching prep plan + Phase 2 start

Planned Phases 1–3 for smart caching UI prep.
Full specs in `task_plan.md` §Smart Caching Prep and `findings.md` §Smart Caching Prep.

Design decisions made this session:
- Phase 3 selectedSet exported as `ValueCollI64` (proper collection type), not string
- `SysAttrWriteCollI64` syscall needed (slot 44); new `RegionValueColl` in constraint page; `ConstraintPageVersion` 3→4
- Sentinel `math.MaxInt64` in collection when >256 items selected
- `SelectedSetCountAttr` (ValueI64) always holds true count; `SelectedSetPagesAttr` (ConstraintI64) via `computeneededpages.vgo`
- Colors: `pal.Highlight()` for primary selection, `pal.Accent()` for set members
- `hid.ModShift` confirmed in `shared/hid/`
- `*MailRow` is the only `GridRow` implementor — no audit needed

### Phase 2 partial: GridRow interface + MailRow

- Added `MsgNum() uint32` to `std.GridRow` interface (`mazarin/mancini/std/grid_table.go:18`)
- `MailRow.MsgNum()` was already present at line 77 — interface change was zero additional code
- Build verified: `task mail-app:arm64` passes clean

---

## Session: 2026-04-22 — write/mmap coherence fix runtime-verified

### Verification: ARM64 HVF 120s run

Goal: confirm Bug #1 (fti bleve persister panic / maildb mmap coherence test failure) is
actually fixed by the `sysMmapPageFill` flush-before-read change landed 2026-04-21.

**Result: CONFIRMED FIXED.**

Mmap coherence test suite (all PASS):
- `[mmaptest] PASS: mmap read-back matches initial content`
- `[mmaptest] PASS: pread sees mmap-written data (mmap→read coherence)`
- `[mmaptest] PASS: mmap sees pwrite-written data (write→mmap coherence)`
- Badger-like 1MB + 64MB pattern tests both PASS
- Write-first page fault test (copy/MOVOU, 16 pages) PASS
- No `[maildb] WARNING: mmap coherence test FAILED` line anywhere in output

Bleve indexing:
- 100/100 documents indexed cleanly, 4.4s total, 0.89 MB/s
- No `persisterLoop` panic, no `corrupted` flag, no `IndexError` messages

Mail app:
- `createCollection: collId=1 filter=0 size=21`; rows loaded with correct senders

Bug #1 is closed. Remaining open bugs: #2 (intermittent VirtIO block stall) and #3
(GridTable no RemoveRow).

---

## Session: 2026-04-21 (continued 9) — write/mmap coherence fix

### Fix: write buffer not flushed before mmap page fill (FIXED)

**Root cause:** `sysWrite` (sequential write path) buffers data in `fdEntry.writeBuf` without
writing to ext2 immediately. When a shepherd then `mmap`'d the same fd and triggered a page
fault, `sysMmapPageFill` read directly from ext2 — which had zeros because the write buffer
was never flushed. This caused bleve's scorch persister to read back zeros from freshly
written `.zap` segment files, panic in `persisterLoop`, and set `h.corrupted = true`.

The same coherence gap was caught by maildb's startup mmap coherence test (Test 1 uses
`syscall.Write`, not `syscall.Pwrite` — the pwrite path already flushed through ext2 directly).

**Fix:** Added write buffer flush at the top of `sysMmapPageFill` (after the nil check on `e`,
before `req.DataBuf()`). If `len(e.writeBuf) > 0`, calls `h.flushWriteBuf(callerPID, fd, e)`
before reading from ext2. This makes write→mmap coherent for both the maildb test and bleve.
File: `maz/linux/syscalls.go`.

Both `linux:arm64` and `linux:x86_64` build cleanly after the change.

---

## Session: 2026-04-21 (continued 8) — title bar clamp + fti error dedup

### Fix: window title bar pushed off-screen by drag (FIXED)

**Root cause:** `moveWindowTo` in `maz/rachel/main.go` had no lower bound on `ta.y`.
The LR anchor-box clamp only fires when the bottom 100px would leave the screen; for a
tall window (e.g. 1200px) this allows `ta.y` to go as low as −1076. Serial log confirmed
`ta.y=13 < borderTop=24` after a drag, placing `face.top = −9` → title bar invisible.

**Fix:** Added `if newY < bT { newY = bT }` after the LR-box top clamp so `ta.y` is
always ≥ `borderTop=24`, keeping `face.top ≥ 0`. Confirmed working by screenshot showing
"Mail" title bar visible after drag.

### Fix: MailDB window floods with repeated fti error notifications (FIXED)

**Root cause:** When the FTI shepherd marks its bleve index corrupted (due to an
AnalysisWorker goroutine panic or a stale `.zap` segment from a read-only ext2 disk),
every subsequent `IndexDocument` request returns `IndexError("bleve index corrupted after
internal panic")`. `maildb/mbox_import.go` `waitForOne` called `notify()` for every error
— 35+ identical messages filled the MailDB window.

**Underlying bleve panic cause (corrected):** `/tmp` is on the ramdisk, which resets on
each QEMU boot — stale bleve state is NOT the cause. The panic is happening within a
single run. Bleve scorch mmaps `.zap` segment files after writing them (`blevesearch/mmap-go`).
The write/mmap coherence bug (same one detected by maildb's startup test: pwrite does not
update the mmap page cache) causes bleve to read back zeros from its newly written segment,
dereference a nil pointer, and panic in `persisterLoop`. Scorch's `recover()` catches this
and fires `ErrAsyncPanic` → `h.corrupted = true`.

**Fix:** Added `lastErrMsg string` and `lastErrCount int` dedup fields to `ftiTracker`
struct. `waitForOne` now shows the first occurrence of each unique error message; subsequent
identical messages are suppressed (displayed every 50th as "Index error (Nx): ...").
File: `maz/maildb/mbox_import.go`.

---

## Session: 2026-04-21 (continued 7) — run verification + linuxapp cleanup

### Run confirmed (ARM64 HVF, 60s)

All three windows visible: Linux Console (sid=8, 800×400), MailDB (sid=9, 800×400),
Mail (sid=5, 900×1162). Mail app loaded 35 MailRows from the initial collection
(collId=1, size=35) with correct senders, plus received CollectionAdd for message 35
as import continued. Resize drag worked: user dragged the left edge of the Mail window
from 900→1029px wide; `dragEndResize` fired, BackingStoreReady sent to app.

**Root cause of first-run failure:** `linux-ui.maz` was stale in the disk image.
When only individual targets (`task rachel:arm64`, `task mail-app:arm64`) are built
before `task run-arm64-hvf`, the Taskfile does not always rebuild `linux-ui.maz`
because the disk's checksum may already be newer than the partial rebuild. Fix: always
run `task linux-ui:arm64` (or a broader rebuild) before `run-arm64-hvf` when linuxapp.go
changes.

### Cleanup: debug prints in Bootstrap

Removed 9 `rawPuts("[linuxapp] dbg: ...")` lines added during hang investigation.
Also removed `[linuxapp] dirtyTicks=…` spam (was every 10 eager ticks) and its `dirtyTicks`
counter variable from `runLoop`.

### Known minor: mail app position tracking stale after resize

`[mail:click]` debug print captures `bsr.AppX, bsr.AppY` from the initial
`BackingStoreReady`. After a left-edge resize, `AppX` moves left but the captured
value stays at the original 886. Only the click debug log is affected — the interactor
coordinate mapping uses the constraint system (correct). Fix deferred: pass new `AppX`/`AppY`
to the drain callback or update the capture on `BackingStoreReady` in runLoop.

### Open: mmap coherence test fails in maildb

`[maildb] WARNING: mmap coherence test FAILED` — badger's mmap read-back returned
zeros instead of the expected 'A' byte. Maildb continues running via direct-I/O path;
no functional crash observed. Root cause unknown — may be ext2/fsclient coherence gap
between write and mmap, or a missing msync/cache flush. Needs investigation.

---

## Session: 2026-04-21 (continued 6) — rachel window decoration + resize drag fix

### Change: fsclient shared data area 4KB → 64KB

`mazarin/fsclient/client.go`: `dataPages` constant 1 → 16 (16 × 4096 = 65536 bytes).
`sys.SharePages(fsSID, localVA)` → `sys.SharePagesWithTarget(fsSID, localVA, dataPages)`.
Added `DataLen() int` method. Linux shepherd's `flushWriteBuf` now uses `h.fs.DataLen()`
as chunk size instead of the hardcoded 4096. Effect: 64KB write buffer flushes in one IPC
round-trip instead of 16. ARM64 HVF indexing: 9–46ms/doc (was limited by context-switch cost
at 4KB chunk size).

### Change: rachel resize handle borders (shadow margins 2→14px)

`mazarin/mancini/theme/wmtheme.go`: `ShadowBottom/Left/Right` all changed from 2 to 14 to
accommodate the 12-pixel resize handle semi-circles (radius 12 + 2px groove margin = 14).
Without this, handles were drawn outside the decoration area and not visible.

### Change: rachel groove + handle draw order

`maz/rachel/blit.go`:
- Added `drawAppGroove()`: 2px inset bevel rectangle drawn around the app content area,
  using `pal.Mid()` (outer) and `pal.Midlight()` (inner). Lines are offset 2px inward
  from the border zone boundary so they fall inside `applyDecorations`' copy range.
- `renderDecorOnce` draw order: DrawBox → `drawAppGroove` → `DrawTitleBar` → `drawResizeHandles`
  (handles only when `depth == mancini.Inset` i.e. focused window).

### Change: applyDecorations called on every Blit

`maz/rachel/main.go` `case wm.Blit`: added `applyDecorations(ta, focused)` before
`timedBlitWindow`. The app writes its entire backing store (including border zones) into
the shared memory, overwriting groove and handle pixels. Re-stamping on every Blit restores
them before compositing to the GPU framebuffer.

### Change: windowVisibleRect uses full buffer for focused windows

`maz/rachel/blit.go`: `windowVisibleRect` returns `image.Rect(ox, oy, ox+bsWidth, oy+bsHeight)`
for focused windows (was returning `faceScreenRect` which excluded the 14px border zones).
This ensures the border zones (groove, handles, shadow) are included in the GPU blit regions.

### Fix: resize drag produces no visual feedback

**Root cause:** `mazarin/mancini/linuxapp/linuxapp.go` `runLoop` did not handle
`wm.WindowResized` or `wm.BackingStoreReady` messages. When rachel sends `WindowResized`
during a resize drag, the app silently dropped it (no SetSize, no redraw, no Blit back).
Rachel's Blit handler condition `dragIsResize && dragActive && msg.DrawnWidth > 0` was
never satisfied → no visual update.

**Fix:**
- `runLoop` signature extended with `dc mancini.DrawContext, bsImg *image.RGBA, leftInset, topInset float64`.
- `Bootstrap` passes these four values to `runLoop`.
- New `resizeDC(newW, newH int)` closure: `dc.Pop()` + `dc.Push()` + `Translate` + `DrawRectangle` + `Clip()` to update the clip rect and win dimensions.
- `wm.WindowResized`: calls `resizeDC(AppWidth, AppHeight)` + `redraw()` + `continue`.
- `wm.BackingStoreReady` (in-loop, from resize start/end): if `BackingStoreAddr != 0` remaps
  `bsImg.Pix/Stride/Rect` to the new buffer; then `resizeDC` + `redraw()` + `continue`.

---

## Session: 2026-04-21 (continued 5) — bleve sync writes + x86_64 TCG analysis

### Change: removed unsafe_batch from bleve fti config

`maz/fti/main.go` `bleve.NewUsing` previously passed `"unsafe_batch": true`, which
makes `Index()` return before segment data is flushed to disk. This was removed so
bleve waits for each flush to hit disk before returning.

### ARM64 HVF sync write RTT (60s run, 100 emails)

Measured disk write (sysid=66, 4096-byte pwrite) delegate RTTs with unsafe_batch off:
- n=25 samples, min=30µs, **median=39µs**, avg=425µs, max=6254µs
- 3.6× more write operations than with unsafe_batch (scorch flushes smaller segments)
- Throughput: 0.70 MB/s (was 2.98 MB/s with unsafe_batch) — expected cost of durable writes
- All writes completing correctly; 100 docs imported and indexed within 60s

### x86_64 TCG analysis: why sync writes take 15–30s per document

Ran x86_64 TCG with same 60s timeout. Key observations:
- ctx_switches: 8.5/sec (vs 265/sec ARM64 HVF — 31× lower)
- DLG:W #1 RTT: **349ms** (vs 8µs on ARM64 HVF — 43,000× slower)
- fti.elf read: 19.7MB in **15.97s** (1.24 MB/s)
- System did not reach bleve indexing within 60s

Root cause of 15–30s per document (not a bug):
1. TCG makes overall system 12–50× slower (expected).
2. Low scheduler throughput (8.5 ctx_switches/sec → ~118ms per switch) means each
   delegated syscall costs ~236ms in wall time.
3. Bleve sync writes issue ~82 pwrite() calls per 41KB document segment.
4. 82 × 236ms ≈ 19s per document — matches observed 15–30s.

This is inherent to TCG, not a kernel/IPC bug. x86_64 TCG + bleve sync writes is
acceptable as a dev/debug target; production paths (ARM64 HVF, bare-metal x86_64)
are unaffected.

Also identified: x86_64 ext2 file loading is inconsistent (30µs–3314µs/page) due to
file fragmentation on disk image — fragmented files require 8–19 single-sector VirtIO
requests per 4KB page instead of one multi-sector request.

---

## Session: 2026-04-21 (continued 4) — delegate IPC RTT measurements + fti diagnostics

### Goal
Capture real measured latency numbers for Write/Pwrite64 delegate round-trips and
confirm fti indexing throughput with `unsafe_batch: true`.

### Fix: [DLG:W] timing not appearing in log

`klog.Logf` routes through the linux shepherd's soft-IRQ uring ring.  During the
initial bleve write burst (~580 Pwrite64 calls) the ring fills and messages are
silently dropped — the `[DLG:W]` lines never reached UART.

**Fix in `kmazarin/ksyscall/delegate.go`:** Changed `[DLG:W]` timing log from
`klog.Logf` to `klog.Criticalf`, which writes directly to UART and survives ring
saturation.  Sampling policy kept: log first 3 Write/Pwrite64 delegates unconditionally,
then every 64th (`n <= 3 || n&63 == 1`).

### Investigation: fti.elf hang at [fs] reading /fti.elf... (300s run)

A 300s run stalled at `[fs] reading /fti.elf...` for the full 300 seconds (197 lines
total output).  Investigation ruled out: AllocPages limits (4519 < 32768), ext2
double-indirect block handling (correct), IOUring ring sizes (SQCapacity=32, batch 8 fine),
DMA scratch clump validity.

**Root cause of THAT specific hang:** Stale disk.img.  The Taskfile `method: checksum`
for `disk-arm64` had not detected that kmazarin.elf changed (not listed as an explicit
source for that target).  The disk image was built against a prior binary state that
caused the hang.  Touching `maz/fs/main.go` forced a full rebuild; the next 120s run
loaded fti.elf successfully (4642 blocks, all batches).

**Separate open bug (issue #2):** An intermittent VirtIO block stall (~1 in 3 cold runs)
can hang the fs read path permanently with no timeout or retry.  This is distinct from
the stale-image issue.

### Measured delegate IPC RTT (ARM64 HVF, 120s run, 98 emails indexed)

- **Write (sysid=10), warm:** 10–50µs typical; ~255µs on first boot writes
- **Pwrite64 (sysid=66), bleve journal flushes:** 64–290µs typical
- **GC-induced outliers:** entry #449 = 2590µs, entry #641 = 8392µs
- **fti throughput:** 2.84 MB/s cumulative, per-doc 146µs–1.5ms

### Diagnostic cleanup

- Removed `delegateReplyCount uint64` and the top-of-`SyscallReply` 20-entry
  `klog.Criticalf` diagnostic block from `kmazarin/ksyscall/delegate.go`
- Removed `[fs:dbg]` per-batch and `[dma:batch]` counter prints from `maz/fs/main.go`
  (added temporarily to diagnose the fti.elf hang, removed after confirmation)

---

## Session: 2026-04-21 (continued 3) — x86_64 mail display + Taskfile dep fixes

### Issue #8 confirmed FIXED
300s run with RDMSR-fix + cross-page UringIPCMsg fix completes cleanly; no
`morestack on g0`, no `SendOpenFont FAILED`, no exit code 2.

### Fix: CollectionAdd double-counting race (Issue #9)

**Root cause:** `createCollection` called `countDateIndex()` BEFORE acquiring `cs.mu`.
The import goroutine could commit a message, yield to the dispatch goroutine which ran
`createCollection` and counted the new message, then resume and call `addMessage` for the
same message — incrementing `totalSize` again.

**Fix 1 — `collection.go` `createCollection`:** Moved `countDateIndex()` call inside `cs.mu`
so the count is always captured while holding the same lock that `addMessage` uses. Any
message committed before this lock is acquired is counted in `totalSize`; any `addMessage`
for those messages will see `currentCount <= coll.totalSize` and skip.

**Fix 2 — `collection.go` `addMessage`:** Added a `countDateIndex()` call at the top of
`addMessage` (inside `cs.mu`). For each FilterAll collection, if `currentCount <= coll.totalSize`,
the message was already counted at `createCollection` time → skip, no spurious CollectionAdd.

### Fix: Stale KeyHeaders during CollectionAdd row shift

**Root cause:** `handleCollectionAdd` shifted displaced MailRows' `msgNum` by +1 but left
their in-flight `KeyHeaders` request using the old position. Maildb served data for the new
occupant of position N (the inserted message), not the original row.

**Fix — `mail_row.go`:** Added `IsLoading() bool` and `RefreshRequest(newReqId [16]byte) [16]byte`.
`RefreshRequest` cancels the old request ID, fires a new `KeyHeaders` request using the updated
`msgNum`, and returns the old request ID for removal from the lookup table.

**Fix — `main.go` `handleCollectionAdd`:** For every shifted row where `IsLoading()` is true,
call `RefreshRequest(nextReqId())`, update `rowByReqId`, proceed. No duplicate senders in
subsequent runs.

### Fix: Taskfile missing `shared/**/*.go` sources

`mazarin/apps/{mail,calc,versai}/Taskfile.yml` and `mazarin/mancini/Taskfile.yml` were all
missing `shared/**/*.go` in their `sources` lists. These programs import `mazzy/shared`
packages (`mailproto`, `font`, `ipc`, `wm`, `dlist`). Changes to shared would not trigger
rebuilds. Added `shared/**/*.go` to all three arch variants in each affected Taskfile.

---

## Session: 2026-04-21 (continued 2) — x86_64 TLS + IPC cross-page fixes

### Fix 1: WRMSR→RDMSR race in TLS-sync (FIXED)

- **Root cause:** `abi_stubs_amd64.s` run path and yield path both did:
  1. WRMSR (write FSBase to MSR_FS_BASE)
  2. RDMSR (re-read MSR_FS_BASE to get address for TLS write)
  If WRMSR hadn't propagated before RDMSR, RDMSR returned the previous thread's FS_BASE.
  Result: wrong g pointer written to `FS_BASE-8` → `morestack on g0` crash in badger.
- **Fix:** Replaced `MOVL $MSR_FS_BASE, CX / RDMSR / SHLQ / ORQ` with
  `MOVQ 144(R12), AX` (direct read of saved FSBase from ThreadContext) in both paths.
- **Validated:** No `morestack on g0` in subsequent 300s test run.

### Fix 2: SyscallUringSend EINVAL on cross-page message (FIXED)

- **Root cause:** `kmazarin/ksyscall/uring_ipc.go` SyscallUringSend rejected messages
  where `(msgPtr & 0xFFF) + 128 > 4096` with EINVAL. On x86_64, the 128-byte
  `ipc.UringIPCMsg` stack variable in `fontcache.SendOpenFont` landed at a page offset
  that triggered this check (platform-dependent stack layout).
- **Symptom:** `[fontcache] SendOpenFont FAILED: invalid argument` → nil font face →
  mail app panic (exit code 2).
- **Fix:** Added slow-path in `SyscallUringSend`: when message spans page boundary, copy
  both partial pages into a 128-byte local kernel stack buffer; use that buffer's address
  as msgKVA. Fast path (single page) unchanged.
- **Still needs testing:** both fixes applied, build clean, x86_64 run pending.

---

## Session: 2026-04-21 (continued) — x86_64 mail import debugging

### Context
Continuing from a context-limited session. x86_64 reaches maildb import but
stalls after parsing ~3 messages due to a kernel TLS bug.

### Fixes applied

**1. mbox_import.go: WriteBatch → db.Update() completion (FIXED)**
- Previous session had partially converted but left `wb.Flush()` in the parse
  loop, causing compilation failure and a stale binary. Removed the call.
- `storeParsedMessage` uses `db.Update(func(txn *badger.Txn) error)` for
  per-message atomic commits. The parse loop now calls `onFirstCommit` and
  `onMessage` directly after the successful commit — no `wb` anywhere.

**2. collection.go: createCollection size=0 mid-import (FIXED)**
- `readCounter` returns 0 before `initCounters` runs (end of import). Collections
  created mid-import appeared empty to the mail app.
- Added `countDateIndex()` (scan all `date:` keys) and `countUnreadDateIndex()`
  (scan + read-flag check) helpers to `collectionStore`.
- `createCollection` now uses these instead of `readCounter` for FilterAll and
  FilterUnread. Verified: `[maildb] createCollection: collId=1 filter=0 size=3`
  (was `size=0`). Mail app receives correct initial totalSize.

**3. abi_stubs_amd64.s: g==0 guard (FIXED, previous session)**
- Kernel context-restore wrote g=0 to FS_BASE-8 from supervisor mode when a new
  thread's TLS page was demand-mapped but not yet physically present → nested
  kernel page fault → crash.
- Fix: skip the TLS sync write when g==0. Applied to both run path and yield path.

### Open issue: `morestack on g0` in badger compaction goroutine

After ~3 messages parsed and badger begins SST compaction, the compaction goroutine
crashes: `runtime: morestack on g0, stack [0x...] sp=0x...` with sp NOT in g0's
stack range. Stack trace: `levelTargets → makeslice → mallocgc → mallocgcSmallNoscan`.

Root cause is TLS corruption: `morestack` reads g from TLS (FS_BASE-8) and gets g0
when it should get the compaction goroutine's G. The context-restore RDMSR re-reads
FS_BASE from hardware immediately after WRMSR — potential pipeline race where RDMSR
sees the old FS_BASE.

**Next:** Replace RDMSR with direct read from `144(R12)` (saved FSBase in ThreadContext).

### Key observation
/data is served from a ramdisk (ext2 on MemBlockDevice). The `[blk:submit]` kernel
timing instrumentation added this session will never fire for mbox reads — no real
block I/O during maildb import. The VirtIO block device is only used during initial
boot (disk image loading).

---

## Session: 2026-04-21 — Phase 5: x86_64 end-to-end COMPLETE

### Goal
Build and run the x86_64 disk image. Identify and fix whatever breaks in the
VirtIO block driver / fs shepherd / plugin pipeline so that x86_64 reaches
parity with the ARM64 HVF stable system.

### Fixes applied

**1. linux:x86_64 — converted to plugin build**
- `maz/linux/Taskfile.yml` x86_64 task was using legacy `go build -tags mazhost`
  but `maz/linux` has no `func main()` (plugin-only). Changed to mazgo+mazlink
  `-buildmode=plugin` matching the ARM64 task.

**2. disk-x86_64 — added shepherd.elf + updated to .maz binaries**
- Added `shepherd:x86_64` dep.
- Changed `rachel-amd64.elf` → `rachel-amd64.maz`, `linux-amd64.elf` → `linux-amd64.maz`.
- Added shepherd.elf staging copy and mkext2 entry.

**3. x86_64 kernel page fault at boot — linear map / stack PT conflict**
- `mapStacks` creates a 4KB PT at PD[32] (stacks at VA 0xFFFFFFFF44100000).
- `createLinearMap` was skipping PD[32] entirely (already present), leaving
  PA 0x44000000 (VA 0xFFFFFFFF44000000) unmapped. Fix: detect "present 4KB PT"
  (no PTE_PS bit) and fill in the unmapped PT slots with 4KB linear-map entries,
  preserving the existing stack pages.
- Fixed in `diplomat/main/kernelvm_amd64.go`.

**4. maildb: runtime.addmoduledata.abi0 unresolved — switch to .maz**
- `startup.amd64.toml` was launching `/maildb.elf` (legacy ET_EXEC). A legacy
  maildb binary's static symtab lacks `runtime.addmoduledata.abi0`, so
  `mazdl.RegisterHost()` couldn't resolve it when loading mail-ui.maz.
- Fix: added `maildb-amd64.maz` build to `maz/maildb/Taskfile.yml` x86_64 task;
  added `maildb.maz` to `disk-x86_64` sources/staging/mkext2;
  changed `startup.amd64.toml` `path = "/maildb.elf"` → `path = "/maildb.maz"`.
- When maildb runs as a .maz (loaded by shepherd.elf with -dlopen-host-exports),
  RegisterHost reads from shepherd's full dynsym which includes the symbol.

### Outcome

x86_64 reaches full parity with ARM64 HVF:
- Linux console window appears ✅
- MailDB window appears ✅
- mail-ui.maz loads inside maildb ✅
- Mail import (mbox→badger parse) running ✅
- All plugin chains resolve without missing symbols ✅

---

## Session: 2026-04-21 — mazdl amd64 parity COMPLETE

### State audit

Both items task_plan.md listed as "missing" already existed in the codebase:
- `mazarin/mazdl/reloc_amd64.go` — already complete
- `mazlink-smoke-amd64` Taskfile task — already present (Taskfile.yml lines 476–484)

### Phase A: arm64 baseline

First run failed — `smoke/host-mazdl` build error:
```
go: updates to go.mod needed; to update it: go mod tidy
```
Root cause: `smoke/host-mazdl/go.mod` had `go 1.26` but root module `mazzy`
uses `go 1.26.2`. This was missed during the Go 1.26.2 migration.
Fix: bumped `smoke/host-mazdl/go.mod` to `go 1.26.2`.
After fix: arm64 SMOKE PASS. All four exits ok.

### Phase C: amd64 smoke

`docker run --platform linux/amd64 ... mazlink-smoke:amd64 /work/smoke/run-smoke.sh`
**SMOKE PASS on first run.** No additional fixes required.
```
mazlink smoke: exit1 ok
mazlink smoke: exit2 ok
mazlink smoke: exit3 ok (TotalAlloc delta=67560 bytes)
mazlink smoke: exit4 ok
==> SMOKE PASS (Phase 4 exits 1-4 ok)
```

### Phase G: cleanup

- `smoke/host-mazdl/go.mod`: `go 1.26` → `go 1.26.2`
- `mazarin/mazdl/doc.go`: removed "arm64 only" limitation; now says "arm64 and amd64"
- `task_plan.md` issue #4: closed out with outcome and known-minor-issue note
- `task_plan.md` STATUS: updated to "Phases 0–4 COMPLETE on both arm64 AND amd64"

### Known minor issue logged

Three `runtime.AddCleanup[go.shape.struct...]` generic stencils leak as DEFINED T
in the plugin (both arches). Non-blocking for current smoke test. See task_plan.md
issue #4 for description and deferred fix approach.

---

## Session: 2026-04-20

### Phase 0: Protocol Design — COMPLETE

- Researched existing maildb protocol (old GetHeaders/GetBody/BodyConfirm — to be deleted)
- Researched uring IPC infrastructure (UringIPCMsg 128 bytes, 108-byte payload, Dispatcher)
- Researched mail app (main.go, testRow, requestInitialHeaders)
- Researched GridRow interface and GridTable row rendering
- Researched TransferPages vs SharePagesWithTarget; decided TransferPages for new protocol
- Drafted wire format: 8 request types (10–17), 8 response types (50–57),
  2 unsolicited notification types (60–61)
- Added RequestId [16]byte to every message per user requirement
- Designed HOT collections: CollectionAdd/CollectionRemove unsolicited push notifications
- Defined multi-client semantics (RequestId may come from any client)
- Designed MessageStore (lazy-fetch map keyed by messageId)
- Designed Collection struct (eager msgIds[] index, LRU 16-slot store, subscribers)
- Defined DeletionNotice fan-out for MarkDeleted notifications
- Added read:/deleted: badger key schema for persistence
- User approved all protocol decisions (2026-04-20)

**Next:** Phase 3 — maildb uring handlers

## Session: 2026-04-20 (continued)

### Phase 1: Wire protocol packages — COMPLETE

- Created `shared/mailproto/protocol.go` — full v2 mail protocol package
  - Error codes, filter types, sort order constants
  - 8 request structs (MessageCountReq, KeyHeadersReq, AllHeadersReq, LatestUnreadReq,
    BodyReq, MarkReadReq, MarkDeletedReq, CreateCollectionReq) + Encode* functions
  - 8 response structs (RespMessageCount … RespCreateCollection) + Encode* functions
  - 2 notification structs (CollectionAdd, CollectionRemove) + Encode* functions
  - DecodeMailReq / DecodeMailResp dispatch functions
  - KeyHeaderEntry (240 bytes), AllHeaderEntry (1232 bytes) page layout structs
  - Pack*/Unpack* helpers (PackCreateCollection, PackKeyHeaderEntry, PackAllHeaderEntry, etc.)
- Extended `shared/fti/protocol.go` with search protocol:
  - MsgTypeSearchMail=2, MsgTypeSearchResult=20, MsgTypeSearchError=21
  - SearchMail, SearchResult, SearchError, SearchResultEntry structs
  - Encode*/Pack*/Unpack* helpers
  - DecodeFTIReq and DecodeFTIResp extended to handle new types
- Build check: both packages compile clean

**Next:** Phase 3 — maildb uring handlers (see second session entry above)

### Phase 3: Maildb uring handlers — COMPLETE (committed 3a95a79)

- `maz/maildb/mail_handler.go`: completely replaced with v2 handlers
  - handleMessageCount, handleCreateCollection, handleKeyHeaders, handleAllHeaders,
    handleLatestUnread, handleBody, handleMarkRead, handleMarkDeleted
  - MarkDeleted fan-out: calls cs.removeMessage → sends CollectionRemove per subscriber SID
  - Page transfer pattern: AllocPagesSlice → write → TransferAndUnmap → send VA in response
- `maz/maildb/main.go`: decoder uses mailproto.DecodeMailReq; mh.setStores wired after import
- `maz/fti/search_handler.go`: new searchHandler for SearchMail (bleve MatchQuery,
  count-only + paginated, SearchResultEntry pages, TransferAndUnmap)
- `maz/fti/main.go`: taggedFTIReq dispatches IndexDocument and SearchMail
- `shared/fti/protocol.go`: added SortAsc/SortDesc constants
- Build verified: task fti:arm64 and task maildb:arm64 both pass

### Phase 4: Mail Row Interactor — COMPLETE

- Created `mazarin/apps/mail/mail_row.go`:
  - rowState enum: rowLoading → rowLoaded | rowCollExpired | rowError
  - MailRow struct: collId, msgNum, requestId, maildbSID, state, headers (KeyHeaderEntry),
    onCollectionExpired, onRowSelected callbacks
  - NewMailRow: fires KeyHeaders(collId, msgNum, msgNum) immediately on construction
  - HandleKeyHeadersResp: unpacks first KeyHeaderEntry from transferred pages, frees pages,
    transitions to rowLoaded; handles ErrCollectionExpired by calling onCollectionExpired
  - Implements std.GridRow: Sender/Subject/Date return "…"/"" placeholders while loading
  - Select(): fires onRowSelected callback
  - Accessor methods: RequestId(), CollId(), MsgNum()
- Build check: `go build ./mazarin/apps/mail/` passes clean

**Next:** Phase 5 — Mail App Integration

### Phase 5: Mail App Integration — COMPLETE (build verified)

- Rewrote `mazarin/apps/mail/main.go`:
  - Replaced `shared/mail` import with `shared/mailproto`
  - `startUringDispatcher`: uses `mailproto.DecodeMailResp` (covers responses + notifications)
  - Added package-level state: `gridFrame`, `activeCollId`, `mailRows`, `rowByReqId`, `reqCounter`
  - `nextReqId()`: generates unique [16]byte IDs via UnixNano + counter
  - `requestCreateCollection()`: sends CreateCollectionReq(FilterAll, SortDesc) to maildb
  - `handleCreateCollectionResp`: stores collId, creates MailRows 0..min(size-1,49), AddRow to grid
  - `handleKeyHeadersResp`: routes by RequestId to matching MailRow.HandleKeyHeadersResp
  - `handleCollectionAdd`: creates new MailRow if < 50 shown, adds to grid
  - `handleCollectionRemove`: removes row from tracking list (grid visual removal TODO)
  - `onCollectionExpired`: clears rows, re-requests collection
  - `onRowSelected`: logs selected row (collId, msgNum)
  - Removed: `requestInitialHeaders()`, `testRow`, `testMailRows()`, old `handleMailResponse`
- Build verified: `task mail-app:arm64` passes clean

**Next:** QEMU end-to-end verification (ARM64 HVF)

---

## Session: 2026-04-17 to 2026-04-18 — mazdl / mazlink Plugin-Shape

### Phase 0: mazlink init-task NOP — COMPLETE (2026-04-18)

- `mazlinkNopHostInitTasks` added to `mazlink-patches/cmd/link/internal/ld/go.go`
- Flips `runtime..inittask.state=2` at link time so `runtime.doInit1` skips all
  init functions — prevents duplicate runtime singleton goroutines in plugins
  (forcegchelper, sysmon, bgsweep, bgscavenge, runfinq, gcBgMarkWorker, templateThread)
- Default-on for `BuildModePlugin + LinkInternal`; no flag required
- Exit criterion met: `smoke/host` passes on arm64 and amd64; no `forcegc: phase error`

### Phase 1: Design sign-off — COMPLETE (2026-04-18)

- Policy list (`mazlink-patches/policy/dlopen-host-packages.txt`) with Phase-2 starting set:
  `runtime`, `internal/runtime/...`, `internal/abi`, `internal/cpu`, `internal/bytealg`,
  `internal/goarch`, `internal/goos`, `internal/goexperiment`
- ABI contract confirmed: `ET_DYN`, UNDEF dynsym for imports, DEFINED dynsym for exports,
  `DT_NEEDED="mazarin-host"`, eager binding, no `R_*_COPY`, no symbol versioning in MVP
- One shepherd binary; everything else a plugin (see `memory/shepherd_plugin_model.md`)

### Phase 2: UNDEF dynsym + PLT emission — COMPLETE (2026-04-18)

- New `ld/mazdl.go`: `loadHostPolicy`, `isHostSymbol`, `rewriteHostSymsAsDynimport`
- `ld/elf.go`: `.plt`, `.got.plt`, `.rela.plt`, `DT_JMPREL`, `DT_NEEDED=mazarin-host`
- `ld/data.go`: sizes `.plt`/`.got.plt`
- `amd64/asm.go`, `arm64/asm.go`: emit `PLT32`/`CALL26` for SDYNIMPORT calls
- Exit criteria met: plugin has 200+ UNDEF `runtime.*` symbols; zero `T runtime.*`; < 1 MB

### Phase 3: Host exports runtime dynsym — COMPLETE (2026-04-18)

- `ld/mazdl.go` extended: `emitHostExportsDynsym`; `-dlopen-host-exports` flag
- Filter closures (`.func*` suffixes) to avoid pclntab aux-sym crashes
- Force `havedynamic=1` so stock linksetup doesn't suppress `.dynsym` on exe
- `smoke/host-probe`: validates 3292 `runtime.*`, 418 `internal/runtime/*`,
  423 `internal/abi.*` entries as `GLOBAL DEFAULT FUNC` on arm64
- `mangleTypeSym` patched: runs for exe with `-dlopen-host-exports` so hashed
  `type:.<hash>` dynsym names match between host (exe) and plugin (`BuildModePlugin`)

### Phase 4 arm64: mazdl.Open end-to-end — COMPLETE (2026-04-18)

- `kmazarin/ksyscall/`: new `SysMapELFSegment` kernel primitive
- `mazarin/mazdl/`: full `Open`/`Sym`/`Close` library per §6 of design doc
- `mazarin/mazdl/elfread/`: ELF parser (extended from maz-reloc)
- Funcval dead-reloc fix (Option A): `adddynrel` emits `GLOB_DAT` for host-policy
  funcval objects (`·f` suffix + `DynimpLib=="mazarin-host"`) instead of `RELATIVE`
  — prevents SIGILL from calls through funcvals that point into stripped .text padding
- `rewriteHostFuncvals` loader-side workaround removed from `mazdl/open.go`
- Exit criteria met under `$GO tool task mazlink-smoke`:
  1. `mazdl.Open` + `h.Sym("Hello")` succeeds, returns "hello from mazlink plugin"
  2. `runtime.Stack` shows ≤1 each singleton goroutine
  3. Plugin allocs visible in host `memstats`
  4. 1000-iteration `Stress()` clean

### Phase 4 amd64: OPEN

- mazlink Option A present in `amd64/asm.go`; plugin cross-compiles
- Still needed: `mazarin/mazdl/reloc_amd64.go` + container arch toggle in
  `mazlink-smoke` task
- See `task_plan.md` open issue #4 for exit criteria

---

## Session: 2026-04-17 — CFF Write-Barrier Investigation (PAUSED)

- Investigating SIGSEGV/growslice panic in fontsvc.maz during Italic CFF rendering
- Added `mazWriteBarrierLastVal` + `mazWriteBarrierSyncCount` instrumentation to
  `mazarin/overlay/userspace/runtime/maz_moduledata.go`
- Confirmed: RegisterMazWriteBarrier called, syncMazWriteBarriers fires (2 transitions/GC),
  compiled code reads correct writeBarrier VA, P-struct wbBuf offsets match
- Paused: plugin-shape mazdl (Phase 2+) eliminates the root class of write-barrier bugs
  by removing runtime from plugins entirely; investigation deferred until then
- **Revert before next boot:** `config/kernel.arm64.toml` `go_mem_limit=256` → `24`
- See `task_plan.md` open issue #5 for full details and next diagnostic steps

---

## Session: 2026-04-24 — linux/fs delegate consistency investigation

### Triggered by
maildb's emlx walker enumerating only 209/317 files; fti's bleve persister/merger
hitting ENOENT on segments it should have just written.

### What was investigated
- Mapped the syscall path: userspace → linux shepherd → fs.maz IPC → shared/fs/ext2.
- Read `maz/linux/syscalls.go` (sysOpenat, sysGetdents64, sysWrite, sysClose, sysFsync,
  sysRenameat, flushWriteBuf), `mazarin/fsclient/client.go` (Open, Read, Write,
  ReadDir wrappers), `maz/fs/fsipc.go` (ipcOpen, ipcReadDir, ipcRename, marshalDirents),
  `maz/fs/main.go` (single-goroutine select loop confirming serial access),
  `shared/fs/ext2/reader.go` (LookupDir, ReadDir walks all blocks),
  `shared/fs/ext2/writer.go` (Rename: add-then-remove, not atomic).

### What was found
- **Bug A root cause confirmed**: `sysGetdents64` advances directory offset by the
  fs-side marshalled count rather than the user-delivered count. Detailed in findings.md.
- **Bug B partially diagnosed**: ruled out concurrency and ext2 dir-block enumeration;
  suspects narrowed to linux write-buffer best-effort-flush-on-close OR ext2
  non-atomic Rename. Need fs.maz IPC tracing to confirm.

### Files touched
- `task_plan.md` — added "NEW INVESTIGATION (2026-04-24)" section + status update.
- `findings.md` — added "Linux/fs delegate consistency investigation (2026-04-24)" section.
- `progress.md` — this entry.
- (No code changes yet — investigation is read-only per phase plan.)

### Next steps (when resumed)
1. Implement Bug A fix in `maz/linux/syscalls.go::sysGetdents64`. Two options:
   (a) re-walk the truncated buffer counting record headers, advance offset by that.
   (b) plumb user buf size into FSIPCReqPayload so fs.maz packs accordingly.
   Option (a) is the smaller surgical fix.
2. Add Criticalf instrumentation to fs.maz's `ipcOpen`, `ipcWrite`, `ipcRename` to
   capture the operation sequence preceding a SCORCH ENOENT. Reproduce in a 90s run.
3. Once instrumented data is in, propose and implement Bug B fix.

### Session: 2026-04-24 (continued) — Bug A fix + Bug B instrumentation

**Bug A fix shipped** in `maz/linux/syscalls.go::sysGetdents64`:
- Added `deliveredDirents(src, maxBytes)` helper that walks the linux_dirent64
  records (reclen at byte offset 16 of each record) and counts how many full
  records fit inside `maxBytes` (the user's buffer). Stops on the first
  record that would overflow.
- Replaced `e.offset += int64(entryCount)` (the marshalled count) with
  `e.offset += int64(delivered.count)` (the actually-delivered count).
- Added a one-line `[linux] getdents64:` log when delivered.count !=
  entryCount, so any future truncation is visible. Will be noisy initially
  while large directories are walked — that's the point.

**Bug B instrumentation shipped** in `maz/fs/fsipc.go`:
- `fsHandle` gained a `path` field so write/close traces can identify the
  file by name.
- New helpers `tmpTrace(format, ...)` and `isTmpPath(p)` that emit
  `[fs:tmp] ...` log lines for any operation on a path under `/tmp/`.
- Wired into `ipcOpen` (OPEN, OPEN+CREAT, both ok and fail variants),
  `ipcWrite` (WRITE, WRITE FAIL), `ipcRemove`, `ipcRename`. Builds clean.

**Builds:** `linux:arm64` and `fs:arm64` both clean.

**Next:** 90s ARM64 HVF run. Expectations:
1. maildb's emlx walker should now enumerate **all 317** files (not 209).
2. `[linux] getdents64:` may print during early walks of the 231-entry
   Messages dir; that's diagnostic, not error.
3. Whenever bleve hits SCORCH ENOENT, the preceding `[fs:tmp]` lines
   will show the exact OPEN+CREAT / WRITE / RENAME sequence on that file
   path — which should let us identify Bug B's root cause from the log.

### Session: 2026-04-24 (continued) — Bug A confirmed fixed; Bug B traces too chatty

**60s ARM64 HVF run results:**

Bug A fix VALIDATED:
- emlx walked **309/317** files (was 209 before fix). +100 files now visible.
- 306 parsed cleanly (was 204).
- linux's getdents64 truncation reports fired 83 times — that's the diagnostic
  helper saying "fs marshalled more than user buf could hold; advanced offset
  by delivered count". Working as designed.
- 0 panics, 0 deadlocks, fti indexed 306/306 docs.

Bug B partial data:
- 5 OPEN FAIL events captured. 3 of them are BENIGN (bleve/badger probing for
  optional files like MANIFEST, KEYREGISTRY, "blev" typo'd parent — standard
  "open then create-if-missing" pattern).
- 2 are real bugs: SCORCH errors on `0000000000a2.zap` and `0000000000b4.zap`
  — segments bleve created during this run but later can't open for merge.
- 8 walk errors / 3 skips persist on the emlx side (similar pattern to bleve:
  files appear in dirent listing but lookup fails). Confirms Bug B is broader
  than bleve.

CRITICAL ISSUE FOUND: tmpTrace deadlock-fix #2.
- The original `fmt.Printf` deadlocked (linux IPC loop). Fixed → sys.UartWriteString.
- BUT: 86,804 successful WRITE traces × ~150 chars = ~13 MB to slow polled UART.
- User report: HUGE multi-second pause after clicking a message. Cause:
  fs.maz blocked on UART drain when bleve flushes a batch of writes.
- Fix: removed the successful-WRITE trace; kept WRITE FAIL (rare, valuable).
- All other op traces (OPEN, OPEN+CREAT, RENAME, REMOVE) retained — total
  ~1.3K traces per run, manageable.

Files touched:
- `maz/fs/fsipc.go` — removed successful-WRITE tmpTrace; comment explains why.

Next:
- Re-run with reduced tracing. Verify pauses gone.
- For Bug B, look at the OPEN+CREAT for `0000000000a2.zap` and the subsequent
  ops on it; trace forward to the failed merger lookup.

### Session: 2026-04-25 — Diversion #6 instrumentation landed

Added three short-lived diagnostic hooks for the bleve scorch ENOENT
verification, per task_plan.md "Next step (planned)".

Files touched:
- `shared/fs/ext2/pin.go` — `PinHook` (fired in `PinInode` only on the
  0→1 transition) and `UnpinDeferReclaimHook` (fired in the
  defer-reclaim branch of `UnpinInode`).
- `shared/fs/ext2/writer.go` — `DeferFreeHook` (fired when the pinned
  branch trips inside `Remove` / `WriteFile`-overwrite / `Rename`-
  overwrite). op string identifies the call site; path is the full
  user path (newPath for rename-overwrite).
- `maz/fs/main.go` — wired all three to `fmt.Printf` next to the
  existing `DirVerifyHook` block. Comment notes these are
  diagnostic and must be stripped before closing diversion #6.

Builds: `fs:arm64` and `fs:x86_64` clean. Default `task` build of
diplomat+kmazarin succeeds; `rachel:arm64` failure (font interface
mismatch in `maz/rachel/main.go:1738`) is pre-existing on master,
unrelated to this work. Verified by stash-and-rebuild.

Expectations for the next 90s ARM64 HVF run:
- `[ext2:pin] inum=N` lines per file open into ext2 (every `ipcOpen`
  on a regular file under /tmp/). Will be chatty.
- `[ext2:unpin defer-reclaim] inum=N` should fire only on the actual
  unlink-while-open path. If silent throughout the bleve scorch
  failure window, the unlink-while-open hypothesis is wrong.
- `[ext2:defer-free] op=... inum=N path=...` reveals which write-
  side call is hitting a pinned inode. If `path` matches a `.zap`
  file, fix shape is right and ENOENT comes from elsewhere
  (fsync/ordering). If never fires for `.zap`, hypothesis is wrong.

Next: 90s `run-arm64-hvf` and triage per the three-outcome table in
task_plan.md.

### Session: 2026-04-25 (cont'd) — instrumentation runs, hypothesis disproven, fontcache fix

Two diagnostic runs and a side fix.

**Side fix shipped first:** `mazarin/fontcache/internal_provider.go`
gained `RegisterBuffer`, parallel `regMu`/`registered` map, plus an
`openRegistered` path that allocates a local fontID, computes
metrics via `textshape.ComputeFontMetricsWithData`, and serves
`GlyphByGID` through `textshape.RenderGlyph` for `registered=true`
slots. Mirrors `FontSvcGlyphProvider.openRegistered` and
`DirectGlyphProvider.RegisterBuffer` so the buffer-registered path
stays in-process — fontsvc never sees these fonts, no shared cache
pages. Compile-time `var _ textshape.GlyphProvider =
(*InternalGlyphProvider)(nil)` added. Builds: `rachel:arm64`,
`rachel:x86_64`, `fontsvc:arm64`, `fontsvc:amd64`, default `task`
all clean; `cd louis14 && go build ./...` clean.

**Diagnostic run 1 — 90s ARM64 HVF:** 162 `[ext2:pin]` events,
0 `[ext2:defer-free]`, 0 `[ext2:unpin defer-reclaim]`, 0 SCORCH.
Click triggered a successful body fetch (138219 bytes). System
went idle after — no errors but no throbbing animation either.
Inconclusive on Pin/Unpin because SCORCH didn't reproduce.

**Diagnostic run 2 — 180s ARM64 HVF:** 276 `[ext2:pin]`,
**0 defer-free**, **0 defer-reclaim**, **1 SCORCH**:
`error opening new segment at /tmp/fti-17/bleve/store/00000000002b.zap`.
Defer never fired despite SCORCH firing — second outcome on the
triage table. **Hypothesis disproven.**

**Adjacent signal in run 2** worth noting:
- Single click → next `Index()` took **13.7 seconds**.
- 20-second gap in `[status]` lines (uptime 92s → 112s).
- Later isolated `Index()` of a 1069-byte doc took **41.5 seconds**.
- Suggests scheduling/IPC starvation under click load, not a
  filesystem-data bug.

**Cleanup landed:** stripped `PinHook`, `UnpinDeferReclaimHook`,
`DeferFreeHook` and their three callsites + the `fmt.Printf`
wirings in `maz/fs/main.go`. Pin/Unpin/pendingFreeSet code itself
retained — it's defensive, zero cost on the cold path. `fs:arm64`
+ `fs:x86_64` rebuild clean.

**Updated tracking:** `task_plan.md` TOP-OF-STACK title flipped to
"DISPROVEN, pivoting", findings.md gained a "Pin/Unpin hypothesis
disproven" section with three new candidate hypotheses.

**Open:** await user direction on which next-step hypothesis to
instrument:
1. Linux shepherd write-buffer drop on close (probably ruled out
   on its own — would manifest as empty file, not ENOENT).
2. Path-resolution divergence between create and reopen.
3. Same-PID scheduling stall during persister run, where the
   13.7s/41.5s `Index()` stalls and the SCORCH ENOENT may share
   a root cause.

### Session: 2026-04-25 (cont'd, late) — diagnostics chase, three real bugs surface

User picked option 2 (path-resolution). Several iterations ensued
with progressively heavier traces. Each new run revealed *new*
non-deterministic symptoms (stalled boot, dropped clicks, missing
status snapshots, fontsvc errors) that didn't reproduce reliably.

**Iterations:**
1. Added `[lin:openat .zap]` ENTER/OK/FAIL traces and `[fs:open .zap]`
   CREATE OK / RESOLVE FAIL / READINODE FAIL traces. Run reproduced
   SCORCH ENOENT once but every ENTER had a matching success on both
   sides — **path-resolution-divergence DISPROVEN** (every CREATE OK
   inum matched the corresponding REOPEN OK inum).
2. Pivoted to scheduling-stall hypothesis. Added kernel-side
   `Thread.DelegateBlockSinceTick`/`DelegateBlockSysID` and
   `delegate stuck:` line in `printEpochStatus`. Added
   `sys.DumpKernelStatus` syscall-marker (0xDB7) so .maz can request
   a status dump. Wired `[fti] SLOW Index()` and `SCORCH ASYNC ERROR`
   to call it. Run produced clean delegate-stuck data:
   `tid=687/sid=26/sysid=44/for=11273ms` showed fti+maildb threads
   stuck on Fstatat for 70+ seconds, with mail-app's Readlinkat
   joining at click. **The data was real.** Concluded the freeze
   was a linux-shepherd dispatch-loop wedge.
3. Added per-call `[lin:fstatat -> fs.Stat]` / `[fs:rpc ENTER/REPLY]`
   traces to identify where in the linux→fs.maz chain the dispatch
   wedged. Volumes ran 100s–1500s of lines per run.
4. **Heisenbug surfaced.** Each run produced a different shape:
   sometimes click-freeze, sometimes boot hang at fti load, sometimes
   clean. Kernel `[status]` was suppressed in every run. `[DLG:W]`
   prefixes appeared doubled in some runs (concurrent UART writers
   stomping on each other).
5. **Trim pass — disabled per-message and boot-noise prints in
   maildb, fti, mazhost, fs/main.go LoadFile.** Saved ~600 lines/run.
   Run still froze post-click; `[status]` still 0. Trim wasn't enough.
6. **Restricted `[fs:rpc]` to path-bearing ops only** (Open/Stat/etc;
   dropped Read/Write/Close/Sync/Fstat — the bulk). 1500 → 1290 lines.
   Still froze; user clicked but no `[rachel:click]` event ever
   logged — input pipeline was starved.
7. **Disabled `[fs:rpc]` and `[lin:fstatat]` traces entirely.**
   Improved `[fontsvc] uring.Send OpenFontReply FAILED` to log
   `senderSID` + `err.Error()`. Run was clean: user reported clicks
   worked, body fetch worked, inbox rendered normally. **The
   click-freeze was Heisenbug-induced by the traces themselves.**

**Three real bugs visible against the stable baseline:**

1. **Diversion #6 EISDIR (the original task #7 — back in scope).**
   286 SCORCH errors per 90s run, all of the form
   `write /tmp/fti-N/bleve/store/XXXXXXXXXXXX.zap: is a directory`.
   The `[fs:read EISDIR]` diagnostic shows the path resolved to
   inum=12 (the bleve-store *directory*) with `ftype=2 isDir=true`.
   Bleve's `OpenFile(.zap, O_RDWR|O_CREAT, 0600)` returns a handle
   whose backing inode is a directory. `ipcOpen` doesn't enforce
   Linux's `open(dir, O_RDWR) → EISDIR` rule.

2. **emlxImport stale dirent (Bug B).** maildb's `filepath.Walk`
   over `/data/mail/mbox/...` enumerates files whose subsequent
   `lstat`/`open` returns ENOENT. ~6 walk errors per run. Same
   shape as the bleve ENOENT we incorrectly chased early in the
   session. Pin/Unpin didn't fix it.

3. **linux-ui boot wedge (fontsvc uring.Send fail).** Seen
   intermittently. linux-ui asks fontsvc for a font during
   Bootstrap; fontsvc's `uring.Send(senderSID, &OpenFontReply)`
   returns an error; linux-ui blocks forever. Improved error
   message now in place to capture senderSID + error type.

**Files touched (kept):**
- `maz/maildb/mbox_import.go` — disabled `parse:`, `badger: stored`,
  `fti: indexed N/N` per-message traces.
- `maz/maildb/collection.go` — disabled `CollectionAdd:`.
- `maz/fti/index_handler.go` — disabled `indexDocument:`,
  per-doc `indexed`. Kept `SLOW Index() + DumpKernelStatus`.
- `mazarin/mazhost/load.go` — disabled `LaunchMaz`/`loaded`/
  `MazarinShepherd` boot traces.
- `maz/fs/main.go` — disabled `[fs] %s: %d bytes` LoadFile trace.
- `maz/fs/fsipc.go` — `[fs:rpc ENTER/REPLY]` instrumentation removed
  (helper functions `fsRPCShouldTrace`/`fsOpName` left in place for
  future reuse). `[fs:open .zap *FAIL]` retained.
- `maz/linux/syscalls.go` — `[lin:fstatat -> / <-]` traces removed.
  `[lin:openat .zap *FAIL]` retained. `fstatatSeq` left declared.
- `maz/fontsvc/main.go` — `uring.Send OpenFontReply` failure now
  logs senderSID + err.

**Files touched (kept as fix candidates):**
- `kmazarin/kmazarin/threads.go` — `Thread.DelegateBlockSinceTick`/
  `DelegateBlockSysID` fields. `printEpochStatus` shows
  `delegate stuck:` line. `RequestEpochStatusDump` exported.
- `kmazarin/kmazarin/ipc_bridge.go` — `RecordDelegateBlock` setter,
  delegate-block fields cleared on wake.
- `kmazarin/kmazarin/main.go` — `ksyscall.EpochStatusDumpFn` wired.
- `kmazarin/kmazarin/linkname_impl.go` — `recordDelegateBlock`
  bridge.
- `kmazarin/ksyscall/mazzy.go` — `DebugMarkerStatusDump = 0xDB7`,
  marker handling in `SyscallDebugPrint`.
- `kmazarin/ksyscall/delegate.go` — `RecordDelegateBlock` linkname
  declaration; called before `blockForDelegatedSyscall`.
- `mazarin/sys/syscall.go` — `DumpKernelStatus()` helper.

**Just-landed targeted fix for bug #1 (Diversion #6 EISDIR):**
`maz/fs/fsipc.go::ipcOpen` now rejects opens of directory inodes
with `O_RDWR|O_WRONLY`, returning EISDIR immediately and logging
`[fs:open EISDIR-on-write] path=... inum=N flags=0x... mode=0x...`.
Linux semantics restored, EISDIR surfaces at open time, bleve gets
the standard error shape it knows.

**Next:** run with the EISDIR enforcement; expect either
`[fs:open EISDIR-on-write]` traces for `.zap` paths (confirms
path-resolves-to-dir is the upstream defect, drop into
`Create`/`allocInode`) or no such traces (open succeeds, EISDIR
arrives later — different bug, different angle).

### Session: 2026-04-25 (very late) — EISDIR enforcement run + 4th bug discovered

EISDIR-on-write check landed in `maz/fs/fsipc.go::ipcOpen` with
trace `[fs:open EISDIR-on-write] path=... inum=N flags=0x... mode=0x...`.
Built clean and ran 90s ARM64 HVF.

**Run result:**
- **0 `[fs:open EISDIR-on-write]` traces** fired.
- **255 SCORCH errors**, but this time **all ENOENT** (not EISDIR).
  Both merger reopens of existing segments and persister reopens of
  just-created segments hit `no such file or directory`.
- Kernel `[status]` line **reappeared** at uptime=26s (only one
  snapshot — the system OOM-crashed shortly after).
- `delegate stuck: tid=398/sid=20/sysid=63/for=47ms` — transient
  Fdatasync, not a real freeze.
- **`fatal error: runtime: out of memory`** at line 376, from a
  shepherd's Go runtime trying to grow heap.

**Per-SID memory at OOM point:**
- SID=28 (mail-app): **371 MB**
- SID=20 (fti): ~117 MB
- SID=4 (maildb): ~200 MB
- SID=2 (linux): ~197 MB
- SID=0 (kernel/rachel): ~232 MB
- Other shepherds: 100-240 MB each
- Total user pages: > 1.5 GB

**Findings:**
1. **The EISDIR ↔ ENOENT variants are two faces of one bug.** Same
   `shared/fs/ext2/writer.go` dirent/inode bookkeeping defect — sometimes
   the `.zap` dirent points at a directory inum (EISDIR), sometimes
   it doesn't exist at all (ENOENT). Pin/Unpin didn't fix it. Need
   to look at `Create` / `allocInode` / `freeInode` /
   `removeDirEntry` for the inconsistency.
2. **Bug #4: mail-app memory leak.** 371 MB at 26s uptime, OOMs the
   system. Independent of bugs #1-#3. Likely body buffers, glyph
   caches, or backing-store accumulation.
3. The OOM may be **upstream of the SCORCH variants** — under
   memory pressure, ext2 metadata writes can fail in ways that
   leave dirent and inode out of sync. So #4 may be triggering
   #1/#2 indirectly. Worth checking by capping mail-app memory or
   by running a shorter window before OOM hits.

**Updated tracking:** task_plan.md TOP-OF-STACK rewritten (3-bugs
section + Heisenbug retraction); findings.md gained "Three real
bugs surfaced after silencing traces" and "Bug #4 mail-app memory
blowup" sections.

**Stopping point.** This is a clean stopping place: the click-freeze
red herring is fully retracted with documented cause, the original
Diversion #6 bug shape is back in scope with two-variant clarity, a
new bug (#4 OOM) was found that may be feeding into the others, and
all instrumentation is either tied to a real bug or zero-cost when
quiet.

**Resume points (any order):**
- **Bug #1 (Diversion #6):** trace one `Create` call for a `.zap`
  path end-to-end (parentInum, allocInode result, addDirEntry
  outcome, post-condition `LookupDir(parent, name) == inum`).
  Compare against a later failing reopen of the same path.
- **Bug #4 (mail-app OOM):** dump mail-app's heap profile after
  click+body-fetch; look for retained body buffers / glyph
  caches / backing-store frames. Set GOMEMLIMIT to constrain.
- **Bug #2 (emlxImport stale dirent):** likely same root cause as
  #1 — defer until after #1 closes.
- **Bug #3 (linux-ui font wedge):** improved error message is in
  place; will surface the actual uring.Send error next time it
  fires. Independent of #1/#4.

### Session: 2026-04-25 (evening) — Diversion #6 CLOSED

After kernel string-copy + fsclient mutex + GOGC=100 + uring retry,
the system runs cleanly. Verified with a 180s ARM64 HVF run, 5 user
clicks, 12 body fetches, 0 SCORCH, 0 OOM, 0 dead shepherds. linux's
heap settled at ~5 MB (was 178 MB), fti at ~3 MB (was 87 MB), mail-app
10–49 MB (was 15 MB stable), maildb 140 MB (badger working set,
bounded by GC).

**Files committed (this branch, feature/mail-dumb):**

Kernel side:
- `kmazarin/ksyscall/delegate.go` — `allocAndCopyCallerString` two-phase
  copy across page boundaries.
- `kmazarin/ksyscall/mazzy.go` — `DebugMarkerStatusDump = 0xDB7` +
  `EpochStatusDumpFn` hook.
- `kmazarin/kmazarin/threads.go` — `Thread.DelegateBlockSinceTick`/
  `DelegateBlockSysID`, `RequestEpochStatusDump`,
  `delegate stuck:` line in `printEpochStatus`.
- `kmazarin/kmazarin/ipc_bridge.go` — `RecordDelegateBlock` setter,
  delegate-block fields cleared on wake.
- `kmazarin/kmazarin/main.go` — `ksyscall.EpochStatusDumpFn` wired.
- `kmazarin/kmazarin/linkname_impl.go` — `recordDelegateBlock` bridge.
- `config/kernel.{arm64,amd64}.toml` — `gc_percentage` 10000 → 100.

mazarin side:
- `mazarin/fsclient/client.go` — added `sync.Mutex`, refactored
  `Read`/`Write`/`Stat`/`Fstat`/`ReadDir` to take buffers,
  `Open` returns inum.
- `mazarin/uring/syscall.go` — `SendWithRing` retries on EAGAIN.
- `mazarin/sys/syscall.go` — `DumpKernelStatus` helper.
- `mazarin/sys/memstats.go` (new) — periodic `runtime.MemStats` logger.
- `mazarin/mazhost/load.go` — boot-noise prints removed.
- `mazarin/apps/mail/main.go` — `StartMemStatsLogger("mail", 0)`.

fs.maz side:
- `maz/fs/fsipc.go` — EISDIR-on-write check, EMFILE rollback,
  O_EXCL→EEXIST, packs inum into `Result0` upper 32 bits.
- `maz/fs/main.go` — silenced LoadFile per-file trace.

Linux shepherd side:
- `maz/linux/syscalls.go` — refactored to use locked fsclient API,
  inum-keyed page cache, orphan-handle tracking on close, O_CLOEXEC
  one-shot warning, `[lin:openat .zap *FAIL]` traces.
- `maz/linux/page_cache.go` — re-keyed from fd to inum, store
  `Handle` per page, added `HasPagesFor` / `FlushAllPagesForInum`.
- `maz/linux/fdtable.go` — `inum` field on `fdEntry`.
- `maz/linux/main.go` — `StartMemStatsLogger("linux", 0)`.

fti side:
- `maz/fti/index_handler.go` — silenced per-doc traces, kept
  SLOW Index() trigger that calls `sys.DumpKernelStatus()`.
- `maz/fti/main.go` — SCORCH async error trigger, memstats logger.

maildb side:
- `maz/maildb/mbox_import.go` — silenced per-message parse/store
  traces, kept emlx-walker error path.
- `maz/maildb/main.go` — `StartMemStatsLogger("maildb", 0)`.
- `maz/maildb/collection.go` — silenced CollectionAdd trace.

fontsvc side:
- `maz/fontsvc/main.go` — improved `uring.Send OpenFontReply FAILED`
  error message (senderSID + err string), kept silent on success.

**Open follow-ups (not blocking, can be addressed during mail-dumb
work):**

- `[fontsvc] no free font slots` repetition in late-run after many
  distinct fonts requested. fontsvc's `MaxFonts=32` table fills up.
  Either grow the table or LRU-evict.
- maildb's 140 MB heap is bounded but worth periodic monitoring.
- linux-ui transient fontsvc-boot wedge wasn't seen again after the
  uring retry fix; the improved error message is in place for next
  occurrence.

**Resuming mail-dumb:** the easy part is unblocked. Diversion stack
#1–#6 all closed (#1 RISC-V removal, #2 paper sketch only, #3 windows
fix, #4 fstatat fix, #5 linux-ui notify fix, #6 this session).

### Session: 2026-04-25 (very late) — Diversion #7 starts: temp font support

Follow-up from #6's verification run, which surfaced
`[fontsvc] no free font slots` repetition late in the run. Even at
256 slots (bumped from 32 in `cde2c29`), HTML rendering with CSS
`@font-face` will exhaust the table — the workload simply has too
many distinct fonts × sizes for a fixed-size pool.

**Constraint that ruled out simple options:** size-independent slots
(collapsing all sizes of a face into one slot) won't work. Some fonts
have size-specific glyphs designed to look different at display vs.
print sizes — different shapes, not just scaled. The size IS part of
the font's identity.

**Design — "mushy middle"** agreed with user (Ian):
- Permanent pool: 64 slots. Hard error when full, no eviction.
- Temporary pool: 32 slots. Per-render lifecycle (open/close pair).
- Temp fontIDs use `0x1000` base bit so dumps are unambiguous about
  which pool.
- Permanent-first check inside `OpenTemporaryFont` lets shared default
  fonts (sans/serif/mono) be reused without consuming a temp slot.
- Same code path as permanent after the parse step — full type-I
  glyph cache built in shared pages. Amortization bet: bulk-render
  cost lower than running tier-2 traffic of a full page.
- Per-shepherd ownership tracking so shepherd-death cleanup releases
  any open temp fonts, mirroring the `orphanHandles` pattern from #6.
- Source bytes for `@font-face` flow via existing data-page
  mechanism (caller allocates pages, SharePages to fontsvc, fontsvc
  parses Face from shared region).

**Provider implementations** (three flavors):
- `FontSvcGlyphProvider` (uring IPC to fontsvc): full machinery — 0x1000
  mask, page-sharing for bytes, tier-1 cache reception.
- `InternalGlyphProvider` (rachel internal, callbacks): two new
  callbacks added to `FontSvcInjector`. Shares fontsvc temp pool
  internally; 0x1000 bit not surfaced (no IPC boundary).
- `DirectGlyphProvider` (louis14 standalone, no fontsvc): same slot
  table as `OpenFont`, `CloseTemporaryFont` actually releases the
  slot. Gives per-font close that visualtest harness can adopt.

**Implementation order (planned):**
1. Mazzy-side: interface extension, all three provider impls, fontsvc
   temp pool + IPC + ownership tracking. Existing `OpenFont` path
   unchanged. Build clean.
2. Verify mazzy alone — no regression.
3. (Coordinated with user) louis14 changes per
   `design/louis14_temp_fonts_plan.md`. Renderer.openFont switches to
   `OpenTemporaryFont`, deferred close at end of `Render`.
4. Integration test with mail-app rendering many distinct HTML
   messages.

**louis14 plan committed** to `design/louis14_temp_fonts_plan.md` —
detailed step list with rationale, recommendations on each unsettled
question, and explicit coordination checkpoints. **Not yet applied to
louis14**; user-coordinated.

**Architectural note revisited:** rachel uses `InternalGlyphProvider`
because fontsvc.maz is loaded as a plugin inside rachel's address space
(not a separate shepherd) — `SharePages` to your own SID is rejected
by the kernel, so the IPC path is unavailable in-process. Internal
callbacks are the only option. Co-locating fontsvc inside rachel
avoids a chicken-and-egg between the WM and the font subsystem at
boot.

**Open follow-ups carried forward from #6:** maildb's 140 MB heap
(badger LSM working set, bounded — monitor); linux-ui transient
fontsvc-boot wedge (uring retry fix in place; not seen since).

---

## Session: 2026-04-26 (continued) — Diversion #8: Fstatat/sysid=44 instrumentation

### Setup
Switched back to `feature/mail-dumb` (force-deleted `feature/mail-dumb-bisect` per
bisect verdict). This branch has the correct forward baseline:
- `b9fd57f` — font: real temp-pool IPC
- `e3b7159` — fix(grid): publish scroll attrs on visibleCount transition
- `7af236c` — fix(mail): run cache.Rebalance + body fetch after every iteration

### Instrumentation added (3 files, no functional change)

**`maz/linux/syscalls.go` — `sysFstatat`:**
- Re-enabled per-call entry/exit traces behind `fstatatSeq` atomic.
- Entry: `[fstatat] seq=N enter path=P`
- Exit: `[fstatat] seq=N done fsErr=N err=<nil>` (or resolveErr/EFAULT)
- Stores `req.PathString()` in a local `path` var so it's logged AND passed to `resolveAt`.

**`mazarin/fsclient/client.go` — `callLocked`:**
- After `uring.Send` succeeds but before `<-c.RespCh`, logs:
  `[fsclient] stat id=N sent; RespCh len=N` — gated on `req.Op == ipc.FSOpStat`.
- This tells us: (a) the send to fs.maz succeeded, (b) we are now blocked on RespCh,
  (c) how many unread responses are already sitting in RespCh (normally 0).

**`maz/linux/main.go`:**
- After injection: `[linux] chan caps: wmCh=8 fontReplyCh=8` (one-time at startup)
- Periodic goroutine (every 5s after SetReady): `[linux] chan-monitor: wmCh N/8 fontReplyCh N/8`

### Run results (3 × 180s ARM64 HVF)

| # | Result | Notes |
|---|--------|-------|
| 1 | STABLE | 34 chan-monitor firings, wmCh=0/8 fontReplyCh=0/8 throughout |
| 2 | STABLE | No delegate-stuck with non-empty info |
| 3 | Short log (7.8K / 161 lines) | QEMU may have had a slow start; log cuts off mid-maildb-load |

**No sysid=44 hang yet** (expected ~1-in-5; 3 runs gives ~48% chance of at least one).

### Instrumentation confirmed working
- `[linux] chan caps: wmCh=8 fontReplyCh=8` — appears at startup
- `[fstatat] seq=1 enter ...` and `[fstatat] seq=1 done ...` — appear per call
- `[fsclient] stat id=N sent; RespCh len=0` — appears per Stat call
- `[linux] chan-monitor: wmCh 0/8 fontReplyCh 0/8` — fires every 5s

### What we know from delegate-stuck format
The `delegate stuck:` epoch dump line is followed immediately by `delegateInfo`.
When no delegates are long-stuck, `delegateInfo` is empty (line shows just `delegate stuck:`).
Full stuck-delegate info would appear inline: `delegate stuck: tid=N/sid=N/sysid=44/for=Ns`.

### Next: need a hang run
Once a sysid=44 hang fires, key things to check:
1. Does `[fstatat] seq=N enter path=...` appear without a matching `done`?
   → YES: hang is in `h.fs.Stat()` or deeper (c.mu, uring.Send, or RespCh)
   → `[fsclient] stat id=N sent` present → hang is at `<-c.RespCh`
   → `[fsclient] stat id=N sent` absent → hang is at c.mu.Lock() or uring.Send
2. What do chan-monitor lines show at the time of the hang?
   → wmCh or fontReplyCh near cap=8 → dispatcher deadlock confirmed
   → both near 0 → secondary hypothesis (fs.maz not responding)

### Stopping point
Instrumentation in place, 3 clean runs, no hang yet. Pausing per user request
to discuss a specific commit before continuing.
