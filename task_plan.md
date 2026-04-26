# Task Plan — Mazarin / Mazzy

## TOP OF STACK: GC crash — `sweep increased allocation count` in mail shepherd bgsweep

**Status (2026-04-26 evening):** Actively investigating. MAP_FIXED fix
applied; crash persists. Sonnet's TTBR0 hypothesis (which was Sonnet's
"likely root cause" at session end) was reviewed by Opus and largely
ruled out. Refined hypothesis ranking below; full reasoning in the
evening entry of `progress.md` and the rewritten
`continuation_bisect.md`.

### Crash signature

```
runtime: nelems=36 nalloc=15375 previous allocCount=1 nfreed=50162
fatal error: sweep increased allocation count
```
A GC span with `allocCount=1` has ~15375 mark bits set — gcmark bitmap
arena either pre-populated with garbage or marked through corrupted
heap pointers. Crash at `mgcsweep.go:685`, immediately after
`[mail] cache ready` (before any body fetches).

### What's already fixed (KEEP)

1. **MAP_FIXED unmap** — `kmazarin/ksyscall/mmap.go` `unmapFixedRange()`
   helper. Real Linux semantics. Builds clean. Crash persists, so this
   isn't the only source, but the fix is correct.
2. **Constraint-block PD_PINNED** — `kmazarin/kmem/constraint.go`.
   Symptomatic of a wider issue (see hypothesis #2 below).

### Hypotheses, ranked after Opus review

**#1 (zero-on-alloc gaps):** RULED OUT. Every demand-fault and IPC-page
allocation path zeroes via scratchVA + `CleanPageCache`. The only
non-zeroing paths are DMA (hardware fills) and code-page load (ELF
overwrites). Neither touches the GC arena.

**#2 (refcount/dual-mapping):** STRONGEST LEAD. `MapPageInProcess`
(`paging.go:2336`) installs a PTE without bumping RefCount. The
**MmapPageFill reply path** (`delegate.go:833`) creates a dual mapping
(file-backed page in caller + handler) without `desc.RefCount++` and
without `PD_SHARED`. Caller-side `SyscallMadvise` lacks a fileBacked
check (`stubs.go:281-294`) and unconditionally calls
`ReleasePageByPA` → RefCount 1→0 on a still-mapped-elsewhere page →
buddy reuses it → handler writes through stale PTE → corruption
in next owner. The constraint-block PD_PINNED fix is a band-aid for
the same family of bug.

**#3 (cache aliasing scratch↔userVA):** RULED OUT. ARM64 D-cache is
PIPT-equivalent; `DC CIVAC` works by VA→PA translation; cleaning at
scratchVA covers all aliases. ZERO_VERIFY at scratchVA never fires.

**#4 (bump-allocator overlap):** UNLIKELY. Monotonic per-shepherd
BumpPointer at 0x200T, separate IPCBumpPointer at 0x500T;
`scanForStalePTEs` debug check at `mmap.go:191` would log
`[mmap:STALE]` and hasn't.

### TTBR0 / `SyscallMunmap` (Sonnet's afternoon lead) — disregard

Sonnet claimed `SyscallMunmap` reads TTBR0 incorrectly because "SVC
runs on a kernel worker goroutine." Tracing
`exceptions_arm64.s:233-360`: SVC swaps `x28` to kmazarin's g0 (so Go
thinks it's on g0) but **never changes TTBR0**. `SyscallDispatch`
runs nosplit on the same CPU thread; TTBR0 is still the calling
shepherd's L0PA. `HandleUserPageFault` reads TTBR0 every fault and
ZERO_VERIFY_FAIL never fires — proves TTBR0 is correct on
synchronous syscalls.

The misleading comment at `stubs.go:279` is what set Sonnet on the
wrong path. `SetSyscallSwitchTarget` matters for queue-then-resume
paths (uring/futex/file-delegate); it does not affect inline
mmap/munmap/madvise/mprotect.

The TTBR0 patch Sonnet proposed is harmless but won't fix the GC
crash. Don't burn a session on it.

### Next steps (full plan in continuation_bisect.md)

1. **Probe first** — add user-VA ZERO_VERIFY in `HandleUserPageFault`
   (read back first 8 bytes via the user VA after TLB invalidate). If
   non-zero, direct evidence of stale PTE writes confirming
   hypothesis #2.

2. **If confirmed, fix dual-mapping refcount:**
   - `delegate.go:833` (MmapPageFill reply): `desc.RefCount++ +
     PD_SHARED` before `MapPageInProcess`.
   - `stubs.go` (`SyscallMadvise` userspace path): add fileBacked
     check mirroring `SyscallMunmap`.

3. **Verify** — 60s, 120s, 180s runs. If crash gone: update tracking
   files, resume the morning bisect (PAUSED section below).

4. **If probe doesn't fire** — corruption is happening AFTER the
   page-fault completes (i.e., during normal Go operation), which is
   a different problem. The PA from any other failure points us at
   page-descriptor history.

### Secondary concern

`[localRect] NEGATIVE: lw=354 lh=-2691673` in the same log — rachel's
layout engine sees corrupted floats. Likely collateral from the heap
corruption, not a separate cause. Investigate after the GC crash is
resolved.

### Branch: `feature/mail-dumb`

---

## PAUSED: stability bisect — temp-pool IPC may have regressed boot reliability

**(This was TOP OF STACK this morning; superseded by the GC crash above. Resume after
GC crash is fixed.)**

After landing the real temp-pool IPC (`b9fd57f`), the publishScrollAttrs
fix (`e3b7159`), and the event-loop body-fetch fix (`7af236c`), five
180s ARM64 HVF runs produced five distinct failure modes:

1. fti hung at 14s (Fstatat stuck on linux delegate)
2. fti completed; bodies didn't render (event-loop bug — fixed)
3. fti hung at 4s (Fstatat stuck again)
4. fti completed late; scrollbar drag didn't kick cache (publishScrollAttrs
   bug — fixed but couldn't validate yet)
5. Two boot panics: `language: tag is not well-formed` in maildb's plugin
   init (`golang.org/x/text/internal/language/compact.init.0`) and
   `attr.Init: invalid shared page header` in mail-ui's bootstrap

None of the panics or hangs touch the code I changed (event loop / grid /
fontcache / textshape) — they're in the .maz plugin loader path and
the kernel↔shepherd attr-shared-page handshake. But the failure rate
went up after `b9fd57f`. The `pushBytesToFontsvc` path
(`AllocPagesSlice` + `SharePagesWithTarget` per `RegisterBuffer` call)
is the load-bearing new behavior that touches kernel page state during
shepherd activity, even though no `@font-face` was loaded in any of
these runs (no clicks → no body render → no `RegisterBuffer` from
louis14). The provider's `slots [textshape.MaxFonts]*fontsvcFont`
bump from 32 → 256 also expanded each shepherd's per-instance memory
footprint by ~1.5KB of pointer slots, which shouldn't matter but is
new.

### Bisect plan (resume when GC crash fixed)

**Bisect.** Revert `b9fd57f` on a side branch (`feature/mail-dumb-bisect`
or similar) leaving `cc230e5` (GridFrame scrollbar) + `37b4abe`
(DrawContext temp-font surface with safe fallback) as the baseline.
Run 3–5 180s sessions:

- If stable → `b9fd57f` introduced the regression. Re-apply
  selectively: separate the slot-table redesign from the IPC client
  rewrite, validate each independently. Most likely culprit is the
  client-side `pushBytesToFontsvc` path even though it's not exercised
  in these runs (e.g. an init-time cost or a kernel side-effect of
  the new MsgTypes being decoded but not handled by some shepherd).
- If still unstable → the gremlins are pre-existing intermittent
  issues (memory: `mazlink_funcval_dead_reloc_bug.md` family). My
  changes are exonerated; we keep the temp-pool work and chase the
  underlying loader/attr-page issue separately.

**Do NOT** continue layering features on top until this is resolved.
The body-fetch fix (`7af236c`) is logically independent and would
survive a bisect since it's only in `mazarin/apps/mail/main.go`.

---

## Resumed when stability question is settled: real temp-pool IPC, redesigned around RegisterBuffer

louis14 is now calling `OpenTemporaryFont` / `CloseTemporaryFont`
through the DrawContext (commit `f41f5c4d` on `fix/flexbox-fast`),
and the mazzy-side surface is plumbed (commit `37b4abe`). Today
every call still routes through the `FontSvcGlyphProvider` fallback
to `OpenFont` and lands in the permanent pool — the temp pool stays
empty. To make the temp pool active, we need to wire the IPC client.

**Key insight (user, 2026-04-25):** louis14 already has an
unambiguous "this is a temporary font and here are the bytes" signal
— `text.FontRegistry.RegisterFontFace` → `provider.RegisterBuffer`.
Every call to `RegisterBuffer` originates from `RegisterFontFace`,
which is only called for CSS `@font-face` rules. There are no other
callers in either repo. So `RegisterBuffer` IS the moment we know
(a) this font is per-page-temporary, (b) we have the bytes in hand.

**Today the bytes never reach fontsvc.** `FontSvcGlyphProvider.RegisterBuffer`
just stuffs them into a local `registered` map; `openRegistered`
parses the Face *in-process in the shepherd* and rasterizes glyphs
locally via `textshape.RenderGlyph` (the `if ff.registered` branch
in `GlyphByGID`). Fontsvc has no idea the font exists. This means
the permanent-pool exhaustion problem isn't actually solved by the
foundation as it stands — `@font-face` fonts accumulate in
the shepherd's local `p.fonts[]` table the same way as before.

### Redesigned plan

`FontSvcGlyphProvider.RegisterBuffer` should push the bytes to
fontsvc **at register time**, not lazily on first open:

1. Allocate IPC pages, copy bytes, `SharePagesWithTarget(fontsvc)`
   ONCE at register time.
2. Send a new `wm.RegisterFontBuffer` IPC: "remember (family,
   variant) → these mapped pages". Fontsvc records the mapping; no
   Face parsing yet (different sizes will each parse on first open).
3. Fontsvc holds the bytes mapped indefinitely in its own
   `registeredBytes` map keyed by (family, variant). The mapping
   survives across many `OpenTemporaryFont` / `CloseTemporaryFont`
   pairs. Bytes are unmapped only on `wm.UnregisterFontBuffer` (or
   shepherd death cleanup) — louis14 doesn't currently call that,
   but the IPC is worth defining for completeness.
4. On `OpenTemporaryFont(family, variant, size)`: fontsvc looks up
   its registered bytes, parses Face, builds tier-1 glyph cache
   for THIS size, allocates a temp slot, returns `0x1000 | idx`.
5. `CloseTemporaryFont(0x1000|idx)`: frees the temp slot + tier-1
   cache pages. Bytes stay registered.

Why this is cleaner than "pass `FontDataVA` on every OpenTemporaryFont":

- Bytes flow ONCE (at register), not per-open. No expensive page
  copy in a render hot path.
- Different sizes of the same `@font-face` font naturally share
  bytes but get separate tier-1 caches.
- Eliminates the `msg.FontDataVA != 0` ENOSYS branch entirely; the
  kernel mapping primitive happens at register time, not in render.
- Retires the in-process `openRegistered` path: shepherd no longer
  holds raw bytes or rasterizes locally for `@font-face` fonts —
  fontsvc owns it like any other font. Memory footprint drops.

### Harfbuzz 256-slot constraint — must fix in this work

`textshape` has three fixed-size arrays sized at `maxFonts = 256`:
`HarfBuzzShaper.fonts`, `HarfBuzzTextLayout.fonts`,
`DrawContextImpl.metrics`. A fontID of `0x1001` indexes past all
three. The current `registerOpenedFont` bounds check (commit
`37b4abe`) silently skips registration for out-of-range IDs — no
crash, but DrawText for that fontID silently fails to render.

**Fix: table-based translation in `FontSvcGlyphProvider`, no
arithmetic between client and server fontIDs.** An earlier draft
of this plan proposed allocating client-local fontIDs in
`[MaxFonts, MaxFonts+MaxTempFonts)` and translating via subtraction;
that bakes the values of `MaxFonts` (in `fontcache/protocol.go`)
and `MaxTempFonts` (in `maz/fontsvc/main.go`) into every IPC
boundary crossing. If anyone bumps either constant the translation
breaks silently — server-side fontID 50 (now a legitimate permanent
ID under MaxFonts=64) would collide with the client's "temp range
starts at 32" rule.

Cleaner: store the server fontID in the slot, look it up on send.
No math.

```go
type fontsvcFont struct {
    ...
    serverFontID int32  // whatever fontsvc returned (0x1001, 0x1003, 50,
                        //   anything — opaque to the client at runtime)
    kind         slotKind // permanent vs temporary, decided when populated
}

// FontSvcGlyphProvider:
slots [textshape.MaxFonts]*fontsvcFont
```

- **On open:** send IPC. Receive whatever fontsvc returns. Allocate
  the next free index in `slots[]`. Store `slot.serverFontID =
  received`. Return the slot index upstream. The slot index is a
  small int < 256 that the textshape layer can index its arrays
  with directly.
- **On send (close, glyph request):** look up
  `slots[client_id].serverFontID`, send that. Pure storage lookup.
- **Perm vs temp dispatch inside the provider** (tier-1 cache routing,
  glyph IPC routing): `slot.kind` field set when the slot is
  populated. Never derived from arithmetic on the fontID.

Properties:

- The `0x1000` debug tag is preserved at the wire and in fontsvc
  traces (debug-friendly there) but is invisible to the client at
  runtime — fontsvc could change its tagging scheme tomorrow and
  the client wouldn't notice.
- `MaxFonts` and `MaxTempFonts` are server-side bookkeeping; the
  client doesn't reference them.
- Bumping either pool size requires no client change.
- The only client-side coupling is `slots[]` size =
  `textshape.MaxFonts` (its upstream array index ceiling). Raising
  that one constant is a local change; nothing else needs to track.

### Implementation work order

1. **Wire types** (`shared/wm/uring_font.go`):
   - `wm.RegisterFontBuffer` (family/variant + FontDataVA + len + numPages)
   - `wm.RegisterFontBufferReply` (errcode)
   - `wm.UnregisterFontBuffer` (family/variant)
   - `wm.UnregisterFontBufferReply` (errcode)
   - Encode/Decode + dispatch in DecodeFontRequest/DecodeFontResponse.

2. **Fontsvc handlers** (`maz/fontsvc/main.go`):
   - `registeredBytes map[(family,variant)] → mappedPages`
   - `handleRegisterFontBuffer`: validate, store mapping.
   - `handleUnregisterFontBuffer`: unmap + remove.
   - Update `handleOpenTemporaryFont`: when family is in
     `registeredBytes`, parse Face from THOSE pages (skip filesystem
     resolution). Build tier-1 cache for the requested size.
   - Per-shepherd cleanup in `CleanupShepherdFonts(deadSID)` should
     also unmap any registeredBytes the dead shepherd registered.

3. **Client `FontSvcGlyphProvider`** (`mazarin/fontcache/provider.go`):
   - Replace the `fonts [MaxFonts]*fontsvcFont` array with a
     larger `slots [SlotTableSize]*fontsvcFont` (where
     `SlotTableSize = textshape.MaxFonts`, currently 256).
   - Add `serverFontID int32` and `kind slotKind` fields to
     `fontsvcFont`.
   - `RegisterBuffer`: allocate pages, copy bytes,
     `SharePagesWithTarget(fontsvc)`, send `wm.RegisterFontBuffer`,
     wait for reply. Drop the local in-process Face / `registered`
     map (or keep it as a backup for the rare case fontsvc dies?).
   - `OpenTemporaryFont`:
     - Permanent-first dedupe (unchanged).
     - Otherwise: send `wm.OpenTemporaryFont` (registration already
       handed bytes over), wait for reply. Allocate next free
       index in `slots[]`, populate slot with
       `serverFontID = reply.FontID, kind = kindTemp`. Return the
       slot index upstream — small int < 256, indexable into the
       textshape arrays directly.
   - `CloseTemporaryFont`:
     - For `slots[client_id].kind == kindPermanent`: no-op.
     - For `kindTemp`: send `wm.CloseTemporaryFont` with
       `slots[client_id].serverFontID` as the FontID field, then
       drop the local slot entry.
   - `Face` / `GlyphByGID`: dispatch on `slot.kind` (no fontID
     arithmetic).
   - `OpenFont` (permanent path): same flow but `kind =
     kindPermanent`. Note that fontsvc currently returns small-int
     fontIDs in [0, MaxFonts) for permanent — those *will* match
     the client slot index for now (sequential allocation aligns)
     but the client should NOT depend on that match; it stores
     `serverFontID` and looks it up on every send.

4. **FontCache helper** (`mazarin/fontcache/fontcache.go` or
   wherever FontCache lives): add `SendOpenTemporaryFont`,
   `SendCloseTemporaryFont`, `SendRegisterFontBuffer`,
   `SendUnregisterFontBuffer` mirroring `SendOpenFont` shape.

5. **`InternalGlyphProvider`** (rachel-internal, host = fontsvc):
   - In-process equivalent of the same flow, but no IPC — direct
     callbacks like the existing `internalOpenFont` /
     `internalGlyphByGID`. New: `internalRegisterFontBuffer`,
     `internalOpenTemporaryFont`, `internalCloseTemporaryFont`.
   - Two new `FontSvcInjector` interface methods for these.

6. **Hook `CleanupShepherdFonts` into rachel's death handler**
   (`maz/rachel/main.go::handleShepherdDeath`) so dead shepherds'
   temp slots AND registered bytes get released.

7. **Validate**: full ARM64 HVF run, click through several
   distinct HTML messages with `@font-face` rules, watch
   per-fontsvc memstats. Permanent pool should stay bounded; temp
   pool should fill and drain across click → render → close cycles.

### Open question / risk

- **Where does mail-app currently get `@font-face` data?** Need to
  confirm the fetcher path: HTML body → CSS parser → `@font-face`
  rule → URL → ?. In particular, are the URLs `data:` URIs
  embedded in the message, or HTTP fetches? `data:` is fine
  (synchronous decode); HTTP would need network plumbing that may
  not exist. If the email corpus rarely has `@font-face` (most
  emails use system fonts), the temp-pool work is a smaller win
  than expected and we should profile before optimizing.

After this lands:
- Smoke-test mail-app rendering many distinct HTML messages.
- Confirm `[fontsvc] no free font slots` does NOT recur.
- Resume console rewrite (item below).

---

## Resumable: Console rewrite (paused mid-flight, grid scrollbar landed)

**Grid scrollbar (item 1) is DONE — committed `cc230e5`.** Visibility,
max-scroll, and thumb fraction are all driven by constraint programs
(`GreaterI64Bool`, `NonnegSubI64`, `ThumbFracPermille`) over the
grid's published `TotalRowsAttr` / `VisibleRowCountAttr` /
`ScrollOffsetAttr`. Scrollbar.ValueAttr is shared with
`grid.ScrollOffsetAttr` so drag and arrow keys feed the same source
of truth. GridFrame.Draw shrinks the grid subtree's draw width by
the scrollbar track width when `scrollbar.Visible()` is true.

**Console rewrite (item 2) — NOT STARTED.** Resume here:

### Spec (verbatim from user 2026-04-25)

- Use the **same logic — preferably the same exact code** as the mail
  header grid's row machinery.
- Fixed set of row interactors that show text; behind them a **500-line
  ring buffer** holds the most recent lines.
- Number of rows is **determined by viewing-area height** — stack as
  many full rows as will fit, NEVER a partial row at the bottom.
- Switch console rows to **DynamicLabel** (drop the existing
  `consoleLine` mono renderer). User accepts proportional font as
  the trade-off for shared row logic.
- Console exports the **same value attributes the grid does**: line
  height, visible line count, total line count.
- Console gets a scrollbar, **same shape as the grid scrollbar** —
  reuse `mancini.GreaterI64Bool` / `mancini.ThumbFracPermille` /
  `mancini.NonnegSubI64`. The scroll-needed constraint is identical.

### Concrete plan

**Files to touch:**
- `mazarin/mancini/std/console.go` — rewrite from `consoleLine` →
  `DynamicLabel`, dynamic row count, 500-line ring buffer.
- `mazarin/mancini/std/console_frame.go` — NEW. Analogous to
  `GridFrame`: NeuBox + Console + Scrollbar at right edge.
  Constraint-driven Visible / Max / ThumbFrac off Console's
  published attrs.
- Callers of `NewConsole` / `NewConsoleWithBox` — switch to
  `NewConsoleFrame` where the scrollbar is wanted (see `grep -rn
  NewConsoleWithBox` for hit list; mancini test program is one,
  there may be others in `tools/visuals`).

**Console fields/attrs to publish (mirror GridTable shape):**
- `LineHeightAttr` (already exists as `lineHeightAttr`; rename
  capitalized, refresh each Draw — matches `RowHeightAttr`).
- `VisibleLineCountAttr` (NEW — analogue of `VisibleRowCountAttr`;
  count of full rows that fit; computed in Draw via the same
  `floor((h - headerArea) / lineH)` math, but with `headerArea = 0`).
- `TotalLineCountAttr` (NEW — analogue of `TotalRowsAttr`; `len(content)`,
  capped at 500). Updated in `AddLine` / `HandleByte`.
- `ScrollOffsetAttr` (NEW — analogue, lines from start of buffer to
  first visible). Default value: keeps display tail-anchored unless
  scrollbar is dragged.

**Display orientation:** console is **bottom-anchored** (newest line
at the bottom slot) by default. When ScrollOffsetAttr is at its max
(= TotalLineCount - VisibleLineCount), display is "live" tail.
Scrollbar drag pulls the user back in history; tail-anchoring resumes
when ScrollOffset is manually pushed back to max OR (preferably) on
any new AddLine when the user is currently at max.

**Ring buffer:** the existing `content []lineData` + `trimBuffer()`
already has the right shape. Just bump `maxBuf` to **500** unconditionally
(drop the `rows * 10, min 200` rule). That removes a coupling between
display row count and buffer depth — the user explicitly wants the
ring at 500 regardless of display height.

**Row pool resize:** mirror `GridTable.buildSlotPool` — grow-only,
indexed by epoch, never destroys slots so attribute-table doesn't
overflow on resize churn (this was a prior bug the grid pool
specifically guards against). New labels are children of the
console's layout; their constraint URIs include the epoch.

**Per-line color:** `DynamicLabel.Color` is a public field; set it
each Draw from `content[idx].color`. (Existing fd-based stderr-red
behavior continues to populate `lineData.color` in HandleByte.)

**ConsoleFrame visibility / scrollbar wiring:**
```
scrollNeededAttr  = ConstraintBool(GreaterI64Bool(TotalLineCountAttr.URI(), VisibleLineCountAttr.URI()))
scrollMaxAttr     = ConstraintI64(NonnegSubI64(TotalLineCountAttr.URI(), VisibleLineCountAttr.URI()))
thumbFracAttr     = ConstraintI64(ThumbFracPermille(VisibleLineCountAttr.URI(), TotalLineCountAttr.URI()))
scrollbar.Visible = SwapToConstraint(EqualBool(scrollNeededAttr.URI()))
scrollbar.ValueAttr             = console.ScrollOffsetAttr   (shared)
scrollbar.MaxAttr               = scrollMaxAttr
scrollbar.ThumbFracPermilleAttr = thumbFracAttr
```

(Identical pattern to `GridFrame`'s scrollbar wiring — line-by-line
parallel to the constructor block in `grid_table.go` ~line 159–198.)

**Mono vs. proportional:** open question if user has already
committed to proportional via DynamicLabel (their words: "switch to
DynamicLabel"). If they later want mono back, options are: (a) add a
`Feature.Mono` style and route DynamicLabel through it; (b) add a
`MonoDynamicLabel` variant. Don't pre-build either — wait for the
ask.

**Callers to migrate:**
```
grep -rn "NewConsole\b\|NewConsoleWithBox" /Users/iansmith/mazzy
```
(at least one in `mazarin/mancini/tools/visuals` likely.)

**Validation:** $GO tool task mancini:build clean. Build whichever
visual test program drives the console. Smoke-test in run-arm64-hvf
once mail-app/everything else builds clean.

### Mail program follow-ups (deferred — recorded so they don't drop)

Listed in priority order:

- **HTML body-pane robustness when temp fonts unavailable.** The
  fontsvc IPC stub still returns ENOSYS for caller-shared bytes;
  louis14's HTML renderer will need a graceful fallback chain
  (`@font-face` → registered buffer → fontsvc OpenFont → default
  sans). Wired up once temp-font Phase 2 lands.
- **Click→body fetch latency / prefetch-ahead audit.** 5 clicks
  produced 12 body fetches in the verification run — explore
  whether the prefetch is excessive and whether bounded LRU on
  body cache helps tail latency.
- **maildb working set instrumentation.** ~140 MB steady state
  attributed to badger LSM. Add a periodic `[maildb:mem]` log
  that breaks down LSM-on-memory vs. heap so we can confirm it's
  bounded across longer runs.
- **linux-ui transient fontsvc-boot wedge.** Hasn't reproduced
  since the uring.Send EAGAIN retry fix; keep watching. If it
  recurs the new error message will surface senderSID + actual
  errno.

---

## Diversion #7 (paused mid-flight): Font slot exhaustion — temporary font support

### Problem

fontsvc's slot table (currently 256, just bumped from 32 in `cde2c29`)
is fixed-size. Each `OpenFont(family, variant, size)` consumes a slot.
HTML rendering with CSS `@font-face` registers many distinct fonts per
page, and even at 256 slots a click-heavy session exhausts it
(observed: late-run `[fontsvc] no free font slots` repetition).

Without per-font lifecycle tracking, fontsvc has no way to know when a
font is no longer in use, so it can't recycle slots safely.

### Constraint that rules out simple options

We can't make slots size-independent (collapsing all sizes of a face
into one slot): some fonts are designed with size-specific glyphs that
look different at display vs. print sizes — different glyph shapes,
not just scaled. The size IS part of the font's identity. Permanent
slot-per-(family, variant, size) is the right grain.

### Design — "mushy middle"

Two pools inside fontsvc, with explicit lifecycle for the temp pool:

- **Permanent pool: 64 slots.** For UI fonts that get loaded once and
  stay forever (window chrome, mancini widgets, mail row, default sans/
  serif/mono). If the pool fills, `OpenFont` returns a clean error —
  no eviction, no LRU. The system has a small, bounded set of UI fonts;
  64 is plenty.
- **Temporary pool: 32 slots.** For HTML rendering where each page
  load brings its own `@font-face` set, scoped to one render. Renderers
  call `OpenTemporaryFont` and `CloseTemporaryFont` as a pair. If the
  pool fills mid-render, `OpenTemporaryFont` returns an error and the
  renderer falls back to the default font for that face (degrades
  gracefully to a less-authored look — page still renders).

### fontID encoding

| Range            | Meaning           | Internal index           |
|------------------|-------------------|--------------------------|
| `0x0000–0x003F`  | Permanent (64)    | `fontID`                 |
| `0x1000–0x101F`  | Temporary (32)    | `fontID & 0x0FFF`        |
| anything else    | Reject `-EINVAL`  |                          |

The `0x1000` base bit makes mistakes self-evident in any dump or trace
— a `fontID=0x1003` line tells you immediately which pool and index.

### Same code path as permanent (after the parse step)

- Temporary fonts get the **full type-I glyph cache built in shared
  pages**, same as permanent. The amortization bet: pre-render all
  glyphs once, share via SharePages, vs. paying per-glyph tier-2 IPC
  for every shaped character. For Latin workloads the bulk-render cost
  is much less than the running tier-2 traffic of a full page.
- Same `Face` parsing, same `buildGlyphCache`, same `SharePagesWithTarget`,
  same `GlyphByGID` tier-1 → tier-2 fallback. Only differences:
  - allocated from `tempFonts[32]` instead of `fonts[64]`
  - `0x1000` base bit on returned fontID at the IPC boundary
  - per-shepherd ownership tracking (`ownedTemps map[sid][]tempFontID`)
    drained on `CloseTemporaryFont` and on shepherd-death cleanup

### `OpenTemporaryFont` checks permanent first

If the requested `(family, variant, size)` is already loaded in the
permanent pool, `OpenTemporaryFont` returns that fontID (no temp
allocation, no `0x1000` bit). Caller is expected to call `CloseTemporaryFont`
on whatever fontID it received; close is a no-op for fontIDs in the
permanent range. This lets a system-wide preferred font (e.g., default
sans set up in shared infrastructure or louis14) be reused by HTML
without consuming a temp slot per request.

### Per-shepherd ownership + death cleanup

fontsvc tracks `(temp fontID → owner SID)` via the existing
shepherd-death subscription path. When a shepherd dies with open temp
fonts, fontsvc closes them automatically — same hygiene as linux's
`orphanHandles` from the `mmap-survives-close` work in #6. Permanent
fonts ignore shepherd death (they're shared infrastructure).

### Source bytes for `@font-face`

When `OpenTemporaryFont` is requested for a font registered via
`RegisterBuffer` (CSS `@font-face`), the bytes flow through the
existing data-page mechanism: caller allocates pages, copies bytes,
SharePages to fontsvc, fontsvc parses Face from the shared region.
fontsvc tracks the page mapping for the lifetime of the temp font and
unmaps on `CloseTemporaryFont`, after which the caller is free to
`FreePages`.

### Provider implementations

- **`FontSvcGlyphProvider`** (uring IPC to fontsvc): full machinery —
  `0x1000` mask on send/receive, page sharing for font bytes, tier-1
  cache reception via SharePages.
- **`InternalGlyphProvider`** (rachel internal, callbacks to fontsvc
  in same address space): two new callbacks added to `FontSvcInjector`
  (`internalOpenTemporaryFont`, `internalCloseTemporaryFont`). Uses the
  same fontsvc temp pool internally; the `0x1000` bit isn't surfaced
  externally because there's no IPC boundary, but is preserved
  internally for unified accounting.
- **`DirectGlyphProvider`** (louis14 standalone, no fontsvc):
  `OpenTemporaryFont` allocates from the same slot table as
  `OpenFont`, `CloseTemporaryFont` actually releases the slot. Gives
  per-font close that the visualtest harness can use instead of
  session-wide `ResetOpenedFonts`.

### Implementation order

1. **mazzy-side first** — add the two methods to `textshape.GlyphProvider`,
   implement in all three providers, add IPC messages and fontsvc
   handlers, wire the per-shepherd ownership tracking. Build clean.
   Verify nothing currently using `OpenFont` is affected (no behavioral
   change to existing path).
2. **louis14-side coordination** — see `design/louis14_temp_fonts_plan.md`
   for the call-site changes (HTML renderer's `openFont`, render scope,
   close discipline). Don't touch louis14 yet — user-coordinated.
3. **Integration test** — fontsvc temp pool exercised end-to-end via
   mail-app rendering HTML. Verify temp slots recycle, permanent pool
   stays bounded, no leaks across many clicks.

### Progress (2026-04-25 evening, this session)

**Foundation landed and compiles clean across rachel, fontsvc, fontcache,
textshape, wm:**

- IPC types (`shared/wm/uring_font.go`): `MsgTypeOpenTemporaryFont/Reply`
  + `MsgTypeCloseTemporaryFont/Reply`, struct definitions, `TempFontIDBase
  = 0x1000`, `IsTempFontID` helper, full Encode/Decode wired into
  `DecodeFontRequest` / `DecodeFontResponseFromPayload`.
- `textshape.GlyphProvider` interface gained `OpenTemporaryFont` /
  `CloseTemporaryFont`. `DirectGlyphProvider` (louis14 standalone) has
  the real implementation: permanent-first dedupe via `findCachedFont`,
  Munmap on close. `FontSvcGlyphProvider` and `InternalGlyphProvider`
  are stubs with comments pointing at the next pass.
- fontsvc temp pool added (`maz/fontsvc/main.go`): `tempFonts[32]` +
  `tempFontOwner[32]`, `allocTempFontID`, `findCachedTempFont`,
  `resolveFontID` masking off `0x1000`. Handlers
  `handleOpenTemporaryFont` / `handleCloseTemporaryFont` /
  `CleanupShepherdFonts` / `releaseTempSlot` implemented. Fresh-open
  filesystem path uses extracted helpers `resolveFamilyPath` /
  `loadAndParseFont` / `buildTempCache` / `shareCacheAndReplyTemp`.
- Cross-module callback wiring: two new methods on `FontSvcInjector`
  (`RegisterOpenTemporaryFontHandler`, `RegisterCloseTemporaryFontHandler`),
  matching `FontSvcInit` fields, fontsvc registers
  `handleOpenTemporaryFontCallback` / `handleCloseTemporaryFontCallback`,
  rachel's dispatcher fans out to them on `wm.OpenTemporaryFont` /
  `wm.CloseTemporaryFont` cases.

**Still to do:**

1. `FontSvcGlyphProvider.OpenTemporaryFont` / `CloseTemporaryFont`:
   real IPC implementation. SharePagesWithTarget for caller-supplied
   bytes path (msg.FontDataVA != 0). Tier-1 cache reception (mmap of
   shared cache pages from fontsvc). Mask `0x1000` bit on receive.
2. `InternalGlyphProvider`: extend `FontSvcInjector` with
   `RegisterInternalOpenTemporaryFont` / `RegisterInternalCloseTemporaryFont`
   so rachel's in-process callers (window chrome) can use temp pool.
   Currently stub falls through to `OpenFont` — fine for rachel because
   rachel doesn't render HTML, but worth wiring for consistency.
3. Hook `CleanupShepherdFonts(deadSID)` into rachel's
   `handleShepherdDeath` so temp slots get freed when a shepherd dies
   mid-render.
4. fontsvc's `OpenTemporaryFont` with shared bytes (`msg.FontDataVA != 0`)
   currently returns `-ENOSYS`. Needs the kernel-side mapping primitive
   to receive the caller's pages.
5. louis14 changes per `design/louis14_temp_fonts_plan.md` — user-coordinated,
   triggered when user is ready.
6. Smoke test (currently blocked: louis14 has uncommitted Phase 13e′/13f
   `LayoutUnit` migration that breaks `mail.elf` build —
   `pkg/layout/flex_layout.go` callers haven't been updated to use
   `.Float64()` on `ResolveInlineSize`/`ResolveBlockSize` returns).

### Open follow-ups carried forward from #6

- maildb's 140 MB heap (badger LSM working set, bounded — monitor)
- linux-ui transient fontsvc-boot wedge (improved error message in
  place; not seen since uring retry fix)

---

## ✅ Diversion #6 CLOSED (2026-04-25 evening) — preserved below for context

After landing four root-cause fixes the system runs stably with full
click→body fetch flow and zero SCORCH errors over a 180s run with five
user clicks. **Returning to the mail program** — see "Mail-dumb easy
part" below for resumption.

### Diversion #6 — what it actually was

Three intertwined bugs, NOT one (we initially thought it was a single
unlink-while-open issue and chased that for a long time). Each had a
clean root cause once the noise was peeled back:

1. **Kernel string-copy truncated paths at page boundaries.**
   `kmazarin/ksyscall/delegate.go::allocAndCopyCallerString` capped its
   `CopyFromUser` at the end of the caller's first page. If the path
   string crossed a 4KB boundary (Go's allocator placed the C-string
   close to a page end), the kernel returned `strLen = maxCopy` — a
   *truncated* path. Bleve's
   `os.OpenFile("/tmp/fti-N/bleve/store/XXXXXXXXXXXX.zap", ...)` arrived
   at fs.maz as `"/tmp/fti-N/bleve"` (16 chars — exactly the page-end
   prefix), which resolved to the bleve directory's inode and produced
   the EISDIR/ENOENT alternation that mimicked dirent corruption. **Fix:**
   two-phase copy that follows the string into the next page when no
   null is found in the first chunk.

2. **fsclient.Client was racy.** No mutex around `setPath` /
   `WriteData` / `nextID++` / `<-RespCh`. Linux shepherd's many
   concurrent delegate-handler goroutines all shared one client and
   would smash each other's path bytes in the shared data area, with
   PathLen pointing past a stale null. **Fix:** added `sync.Mutex` to
   `Client`, every public method holds it for its full IPC round-trip
   (path setup → send → receive → data-area copy). Refactored
   `Read`/`Write`/`Stat`/`Fstat`/`ReadDir` to take a `buf` so the
   data-area copy happens *under the lock*, not after.

3. **GC was effectively disabled.** `config/kernel.{arm64,amd64}.toml`
   had `gc_percentage = 10000` (GOGC=10000) — Go's GC only fired when
   the heap grew 100× past its previous live set. The intent was for
   `GOMEMLIMIT=256 MB` to be the governor instead. In practice,
   long-running shepherds (linux especially) drifted to 178 MB heap
   without ever GCing, and the **kernel-side user-page budget** ran out
   across all shepherds simultaneously before any individual shepherd
   hit GOMEMLIMIT. The `runtime.sysMap` ENOMEM panic that killed linux
   was a symptom — the *system* ran out of physical pages, not linux.
   **Fix:** dropped `gc_percentage` to `100` (Go default). Linux's
   steady-state heap dropped from 178 MB → 5 MB. fti went 87 MB →
   3 MB. Mail-app stable around 10–15 MB. maildb ~140 MB (badger LSM
   working set, bounded).

4. **fontsvc dropped uring replies on EAGAIN.** `uring.Send` returned
   error and fontsvc just logged "failed" — linux-ui's font request
   blocked forever waiting for a reply that was discarded. **Fix:**
   `mazarin/uring/syscall.go::SendWithRing` now retries on EAGAIN with
   `runtime.Gosched()` (256-attempt budget) — closest equivalent to
   Linux's "write to full pipe blocks until drain."

### Side fixes that landed during the same session

- **mmap-survives-close (Linux semantics).** `maz/linux/page_cache.go`
  re-keyed from `(sid, fd, offset)` to `(sid, inum, offset)` and
  stores `Handle` per page. `sysClose` no longer drops the cache —
  pages stay alive until munmap or shepherd death. Orphan `fs.maz`
  handles whose owning fd has been closed are tracked in
  `syscallHandler.orphanHandles` and flushed on the eventual
  `sysMmapPageFlush` drain.
- **`ipcOpen` Linux-compat polish.**
  - O_CREAT + EMFILE: rolls back `Create` (Remove dirent + free inum)
    so failed opens leave no on-disk trace.
  - O_CREAT|O_EXCL: returns EEXIST when the file already exists.
  - Open of dir with O_RDWR/O_WRONLY: returns EISDIR at open instead
    of letting the caller hit "is a directory" deep in `Write`.
- **O_CLOEXEC one-shot warning** in `maz/linux/syscalls.go::sysOpenat`
  — Go's runtime adds it by default; we silently ignore it because we
  don't implement exec. The warning makes the seam visible if anyone
  ever needs to add exec semantics.
- **Per-shepherd `runtime.MemStats` periodic logger**
  (`mazarin/sys/memstats.go`) — wired into linux/fti/maildb/mail. Was
  the diagnostic that finally pinned the GC issue: showed linux at
  178 MB heap with `gc=0`, while other shepherds (slightly different
  alloc patterns) had GC'd a few times.
- **Kernel-side `delegate stuck:` line** in `printEpochStatus` plus
  `RecordDelegateBlock` / `Thread.DelegateBlockSinceTick` /
  `Thread.DelegateBlockSysID`. Zero cost when nothing is stuck;
  one line per blocked thread when something is. Kept for future
  diagnoses.
- **`sys.DumpKernelStatus()`** — `.maz` programs can request an
  on-demand kernel `[status]` snapshot via `SysDebugPrint` marker
  `0xDB7`. Wired into fti's SLOW Index() and SCORCH error paths.

### Heisenbug retraction

The "click → 70-second freeze → recovery" pattern we chased for
several iterations was **observation-induced**: per-RPC `[fs:rpc
ENTER/REPLY]` and `[lin:fstatat -> / <-]` traces saturated the UART,
starved rachel's input loop, suppressed kernel `[status]` output, and
created the appearance of deadlock while the system was actually just
slow. With those traces silenced, the freeze does not reproduce. The
remaining instrumentation is gated to fire only on real failures
(`*FAIL`, `SCORCH`, `EISDIR`, `delegate stuck`) so it's quiet by
default and load-bearing when something is wrong.

### Verification (180s ARM64 HVF, 5 user clicks)

- 5 `[rachel:click]` → 5 `[mail:click]` → 5 `[click-agent]` → 12 body
  fetches (some clicks chain prefetch-ahead).
- 0 SCORCH, 0 EISDIR, 0 OOM, 0 dead shepherds.
- Heap stable: linux ~5 MB, fti ~3 MB, mail 10–49 MB, maildb ~140 MB
  (badger working set, bounded).
- 1 `[linux] WARNING: O_CLOEXEC requested ...` — fired once at boot
  per design.
- 1 cosmetic late-run `[fontsvc] no free font slots` repetition —
  fontsvc's `MaxFonts=32` filled after the user clicked many distinct
  fonts. Doesn't crash the system; tracked as a minor follow-up.

### Open follow-ups (not blocking)

These were surfaced but deliberately not closed in this session:

- **fontsvc `no free font slots`** — table is fixed at 32 entries; a
  click-heavy workload exhausts it. Either grow the table or evict
  least-recently-used. Cosmetic until the eviction path matters.
- **maildb's 140 MB heap** — looks like badger's LSM in-memory state
  (bounded, GC fires when heap doubles). Worth monitoring across
  longer runs; not a leak.
- **linux-ui transient fontsvc-boot wedge** — was happening
  intermittently before the uring.Send retry fix. Not seen in the
  recent runs but the deeper logic of "fontsvc tries to reply,
  fails, no recovery path" still warrants a look. Improved error
  message is in place; if it recurs we'll see senderSID + actual
  errno.

### Pre-existing diversions still open

| # | Title | Status |
|---|-------|--------|
| #1 | RISC-V removal | ✅ DONE (committed `a04ce0a`) |
| #2 | fs.maz concurrency design | 📝 Paper sketch; pre-empted by #6, can resume |
| #3 | Windows-not-shown | ✅ FIXED (pipe-buffered Write delegation) |
| #4 | Fstatat DataVA fault | ✅ FIXED (root cause was #3) |
| #5 | linux-ui window disappearing | ✅ FIXED (NotifyChannel) |
| #6 | bleve scorch ENOENT/EISDIR | ✅ DONE (this session) |

**Mail-dumb's "easy part" is now unblocked.** The original task list
(pre-divergence) lives below. Resume from there.

---

## Historical: Diversion #6 — bleve scorch EISDIR (real bug back in view; click-freeze was Heisenbug)

### 2026-04-25 (late) — three real bugs surfaced after silencing traces

After a multi-iteration loop where each new instrumentation made symptoms
worse, we silenced the high-volume `[fs:rpc]` and `[lin:fstatat]` traces
entirely. The system became responsive — click handling, body fetch,
inbox rendering all worked normally. Three **distinct, real** bugs are
now visible against a stable baseline:

1. **Diversion #6 EISDIR (the original task #7).** Bleve persister
   writes to `/tmp/fti-N/bleve/store/XXXXXXXXXXXX.zap` and gets
   `is a directory` (errno -21 EISDIR). 286 SCORCH errors fired in a
   single 90s run. The smoking-gun trace
   `[fs:read EISDIR] handle=228 path="/tmp/fti-4/bleve" inum=12 ftype=2 isDir=true`
   shows the path resolution lands on the bleve-store directory inode.
   `maz/fs/fsipc.go::ipcOpen` doesn't enforce Linux's
   `open(dir, O_RDWR) → EISDIR` rule, so the failure surfaces only at
   write time.

2. **emlxImport stale dirent (Bug B).** maildb's `filepath.Walk` over
   `/data/mail/mbox/...` lists `.emlx` files whose subsequent `lstat`
   returns ENOENT. ~6 files lost per run. This is the same pattern we
   saw on the bleve side back at the start of the session and tried
   (incorrectly) to fix with Pin/Unpin. The dirent enumeration and the
   subsequent lookup disagree about which inodes exist.

3. **linux-ui boot wedge (fontsvc uring.Send fail).** Seen in 2 of the
   recent runs: `[fontsvc] uring.Send OpenFontReply failed`. linux-ui
   asks fontsvc for a font during Bootstrap, fontsvc tries to reply via
   uring, send returns an error, linux-ui blocks forever waiting.
   `Bootstrap entered: Linux Console` appears but `backing store ready`
   never does. Improved trace now shows `senderSID` and `err.Error()`.

### Heisenbug retraction

The "click → 70-second freeze → recovery" pattern we chased for several
iterations was **observation-induced** by the high-volume `[fs:rpc]` /
`[lin:fstatat]` traces. Quoting per-RPC `fmt.Printf` from fs.maz and
linux saturated the UART, starved rachel's input loop, and suppressed
kernel `[status]` output — making the system appear deadlocked while it
was actually just slow. With the traces gone the freeze does not
reproduce.

The kernel-side instrumentation we added (`RecordDelegateBlock` on the
thread struct, `delegate stuck:` in `printEpochStatus`,
`sys.DumpKernelStatus` from .maz) is **kept** — it costs nothing when
no thread is stuck, and the data it produced was real and load-bearing
during the iterations where freezes did fire.

### Active hypothesis (Diversion #6 EISDIR)

Why does `/tmp/fti-N/bleve/store/XXXXXXXXXXXX.zap` resolve to a
directory inode? Two specific suspects in
`shared/fs/ext2/writer.go::Create`:

- `allocInode()` returning an inum that's still in use elsewhere
  (bitmap drift), so the new dirent points at a directory's inode.
- An upstream lifecycle bug where the `.zap` dirent already exists
  pointing at the bleve-store directory inum before bleve's persister
  ever calls Open with O_CREAT.

### Next-step action (just landed)

`maz/fs/fsipc.go::ipcOpen` now enforces Linux semantics:
`open(dir, O_RDWR|O_WRONLY) → EISDIR` with a one-line trace
`[fs:open EISDIR-on-write] path=... inum=N flags=0x... mode=0x...`.

Two outcomes from the next run:
- If the trace fires for `.zap` paths: the path *is* resolving to a
  directory, and the upstream culprit is `Create()` /
  `ResolveInode()` returning a directory inum for a freshly-CREATEd
  file. Investigate `allocInode` bitmap consistency and `addDirEntry`.
- If the trace doesn't fire but SCORCH EISDIR still does: the open
  succeeds for a non-dir inode, then a later operation flips the inode
  type. Different bug — shift to inode-mutation lifecycle.



Status of the diversion stack (#1–#6):

- **#1 RISC-V removal** — DONE. R1–R6 + x86_64 boot OOM fix
  (during R6). Committed `a04ce0a chore: remove RISC-V architecture
  support`.
- **#2 fs.maz concurrency design** — paper only, pre-empted by
  active bleve work. Section "fs.maz Concurrency — Design Sketch"
  below; will resume after #6 closes.
- **#3 Windows-not-shown** — FIXED. Pipe-buffered Write delegation
  (kernel returns byte count immediately for fd≤2; handler releases
  via `SysReleaseDelegatePage`). All three earlier workarounds
  reverted.
- **#4 Fstatat DataVA fault** — FIXED. Real root cause was kernel
  pipe-buffering ALL Writes regardless of fd; fd>2 file-lane Writes
  triggered spurious `SyscallReply` that landed on a later delegate
  and unmapped its data page. Fix: `pipeBuffered := id ==
  sysid.Write && arg0 <= 2`. `CallerDead` flag kept for an
  independent caller-death-during-IPC race.
- **#5 linux-ui window disappearing** — FIXED. `linuxio.LinuxIO`
  gained `NotifyChannel()`; linux shepherd's `lineAccumulator`
  non-blocking-pokes after every `writeCh` send.
- **#6 Bleve scorch ENOENT/EISDIR** — IMPLEMENTED, verification
  partial. Re-scoped (in findings.md) from the original "task #7
  reject mmap on dir fds" to **task #8: ext2 unlink-while-open
  semantics via Pin/Unpin**. See section below.

Mail-dumb easy part resumes once #6 closes and #2 design is written.

### Diversion #6: bleve scorch — task #8 Pin/Unpin (LANDED, partial verification)

**Implementation:** committed in `2d22496 feat: kernel/mail
plumbing for bleve-on-tmpfs`. All four steps from findings.md
"Fix plan — inode lifecycle (approved 2026-04-25)" landed exactly
as specified:

1. `shared/fs/ext2/{reader.go,pin.go}` — `pinMu`, `inodeRefs`,
   `pendingFreeSet`, `PinInode`/`UnpinInode`/`isInodePinnedLocked`
   /`markPendingFreeLocked`/`reclaimInode`. Mutex-guarded.
   No-op on read-only mounts.
2. `shared/fs/ext2/writer.go` — `Remove` / `WriteFile`-overwrite /
   `Rename`-overwrite reordered: dirent first, then if pinned mark
   `pendingFreeSet`; else free inline as before. Immediate-free
   behavior preserved verbatim per path (Remove zeroes inode,
   Rename does not — matches existing code).
3. `maz/fs/fsipc.go` — `ipcOpen` Pins after `allocHandle` in both
   Create and regular branches. `ipcClose` widened to take
   `mt *mountTable`; Unpins before `freeHandle`. Single
   `freeHandle` callsite confirmed.
4. Build verified: `fs:arm64`, `fs:x86_64`, default arm64 task all
   clean.

**Verification (180s ARM64 HVF):** boot reaches mail; fti indexes
14 docs cleanly; then `[fti] SCORCH ASYNC ERROR ... open /tmp/
fti-30/bleve/store/00000000001d.zap: no such file or directory`.
Body fetch via badger served one click successfully, subsequent
clicks froze the UI (fti `corrupted` flag).

**Open:** ENOENT-on-zap-reopen recurred *with* Pin/Unpin in
place. We have no signal yet whether the deferred-reclaim path
fired even once — Pin/Unpin/defer-free are silent in the tree.

**Verification result (2026-04-25): hypothesis DISPROVEN.**

180s ARM64 HVF run, instrumentation hooks in place:
- 276 `[ext2:pin]` events — Pin fires on every file open as designed.
- **0 `[ext2:defer-free]` events.**
- **0 `[ext2:unpin defer-reclaim]` events.**
- 1 SCORCH ASYNC ERROR fired anyway:
  `error opening new segment at /tmp/fti-17/bleve/store/00000000002b.zap`.

Per the second outcome in the triage table above ("defer never
fires"): handles are already closed by the time the SCORCH ENOENT
hits, so the failing path is **not** unlink-while-open. The fix
doesn't help and won't hurt; we leave the Pin/Unpin code in place
as defensive (zero cost on the cold path) but strip the
instrumentation hooks.

**Stripped (commit pending):**
- `shared/fs/ext2/pin.go` — `PinHook`, `UnpinDeferReclaimHook`.
- `shared/fs/ext2/writer.go` — `DeferFreeHook` + 3 callsites.
- `maz/fs/main.go` — the three `fmt.Printf` wirings (DirVerifyHook
  retained).

**New signal from the disproven run** (also worth investigating):
- Single user click → next fti `Index()` took **13.7 seconds**.
- 20s gap in `[status]` logging between uptime=92s and uptime=112s
  (kernel logging completely silent during a stretch).
- Later isolated `Index() (41.5s)` on a 1069-byte doc.
- Suggests scheduling/IPC starvation around click handling.

**Next-step hypotheses (require user direction before instrumenting):**
1. **Linux shepherd write-buffer drops bytes** on close — file
   create returns success, but data never hits ext2, so reopen
   ENOENTs. Trace: ipcOpen for OPEN+CREAT on `*.zap` paths +
   linux's `flushWriteBuf` close path.
2. **Path-resolution divergence** between create and reopen in
   linux shepherd's openat path — different inode for same string.
3. **Same-PID scheduling starvation under click load** — the
   13.7s/41.5s stalls may be the same root cause via a different
   surface (no `removeOldData` race needed; just a deadlocked
   persister goroutine that returns ENOENT spuriously when the
   syscall path times out internally).

Stop and pick one before adding more instrumentation.

### Diversion #3: Windows-not-shown — FIXED (historical)

- **Pipe-buffered Write delegation landed.** `DelegateSyscall`
  for `sysid.Write` now skips `blockForDelegatedSyscall` and
  returns byte count immediately; handler releases the shared
  data page via new `SysReleaseDelegatePage`. Matches Linux pipe
  semantics.
- **Windows appear** on ARM64 HVF (user confirmed linux-ui
  visible for 1-2 seconds).
- **All three earlier workarounds reverted** (Gosched, async
  LaunchMaz, fontsvc preload).

### Diversion #4: Fstatat DataVA fault — FIXED

Originally treated as a caller-death race; partial fix added a
`CallerDead` flag + a defensive `SysVerifyMapping` syscall.

**Real root cause** (2026-04-24): the kernel pipe-buffered ALL
`Write` delegates regardless of fd. fd>2 Writes are routed to
linux's *file lane* which calls `req.Reply(...)`. That spurious
`SyscallReply` lands on whatever delegate the caller-TID is
*currently* blocked on (often a later `Fstatat`), unmapping the
other syscall's data page and waking the caller with the wrong
return value. Manifested as bleve mkdir ENOTDIR panic.

Fix: `pipeBuffered := id == sysid.Write && arg0 <= 2`.
The defensive `SysVerifyMapping` was removed (dead weight once
the spurious-Reply root cause was fixed). `CallerDead` flag was
kept — it handles a separate genuine caller-death-during-IPC
race independently.

### Diversion #5: linux-ui window disappearing — FIXED

linux-ui window appeared for 1-2s then vanished. Linux shepherd
itself was alive; only the .maz-side runLoop was frozen.

Root cause: `linuxapp.runLoop` blocks in `select(wmCh, eagerCh,
notifyCh)`. linux-ui returned no `NotifyCh` from `BuildResult` →
framework swapped in a never-firing dummy chan. After startup
constraints settled, the only sources of wakeups went silent and
the loop slept forever even though `writeCh` was full of
fti/maildb console output.

Fix: matched the pattern mail-ui uses (`maildbio.NotifyChannel`).
`linuxio.LinuxIO` gains `NotifyChannel()`, linux-ui creates the
channel in `MazarinShepherd`, linux shepherd's lineAccumulator
non-blocking-pokes after each `writeCh` send. Verified post-fix
by `blit#500 sid=N title="Linux Console"` in 90s HVF run.

### Diversion #6: Bleve scorch EISDIR mmap-fill (ACTIVE — task #7)

With linux-ui notify fix in place, recurring fti panic surfaces
clearly. Trace just before panic:

```
[mmap-fill] sid=N fd=M offset=0 READ ERR: errno -21
[fti] SCORCH ASYNC ERROR: source: merger, async panic
[fti] PANIC in handleIndexDocument — marking index corrupted
```

Errno -21 = EISDIR. Scorch merger mmap'd what should be a `.zap`
segment file but kernel served a directory. After the panic
`h.corrupted=true` sticks and all subsequent `Index()` calls
fail. User-visible symptom is mail throbber freeze + click
unresponsive (mail's body fetch path waits on a fti that can
never reply cleanly).

The 2026-04-22 persister-panic fix was for write-side incoherence;
this is a different recurrence on the read-side mmap-fill path.

Investigation plan:
1. Read `sysMmapPageFill` in `maz/linux/syscalls.go` to see what
   path it takes to `fs.Read`/`fs.ReadFilePages`.
2. Identify which fd is being faulted (which scorch code path
   opened the file; check whether the fd actually points to a
   regular file or to a directory).
3. Add a one-shot trace in fs-side ipcRead/ipcOpen to log the
   inode + ftype when EISDIR is about to be returned.
4. Find the resolution gap — likely a path-component handling
   issue in fsclient or fs IPC.

Historical narrative preserved in progress.md "Session: 2026-04-24
(even later)" and findings.md "Windows-not-shown: goroutine
scheduling bug". The original write-up (workarounds, sync-coupling
diagnosis, fix direction) is no longer load-bearing now that
pipe-buffered Write delegation has shipped.

---

## RISC-V Removal — Phases R1–R6 (2026-04-24)

**Why:** riscv64 has been a third-tier target. The mazlink/mazdl plugin
loader work (which is replacing the legacy .maz path) explicitly carved
riscv64 out — "riscv64 stays on legacy .maz+maz-reloc path" (Decisions Made
table). Continuing to maintain that legacy path on a third arch is dead
weight. Cutting it shrinks the surface area before we design fs.maz
concurrency and resume mail.

**Survey result (see findings.md "RISC-V Footprint Survey 2026-04-24"):**
~98 `*riscv64*` files, 23 files with combined build tags needing surgical
edits, 3 TOML configs, 1 dedicated directory (`kmazarin/arch/riscv64/`),
11 Taskfile targets across root + 2 sub-Taskfiles, scattered tooling
constants (mkesp, gen-ast-stubs, fix-go-elf, sysid). CLAUDE.md has 7
references. No mazlink/mazdl entanglement.

### Phase R1: Build & run plumbing
Goal: nothing on the build/run path can target RISC-V anymore. Failures
here surface immediately as "task not found" rather than silent breakage.

- `Taskfile.yml` (root): delete `run-riscv64`, `run-riscv64-background`,
  `run-riscv64-direct`, `run-riscv64-direct-background`, `stop-riscv64`,
  `stop-riscv64-direct`, `disk-riscv64`, `boot-riscv64`,
  `stage-embedded-fs-riscv64`, `stage-kernel-config-riscv64`,
  `userspace-build-riscv64`, `shepherd-overlay-riscv64`,
  `merged-shepherd-overlay-riscv64`. Remove RISC-V variables (`OPENSBI`,
  `QEMU_RISCV64` defaulting, port 4447 helpers).
- `diplomat/Taskfile.yml`: delete `riscv64` task and any RISC-V-only deps.
- `kmazarin/Taskfile.yml`: delete `riscv64` task and any RISC-V-only deps.
- `config/`: delete `kernel.riscv64.toml`, `rachel.riscv64.toml`,
  `startup.riscv64.toml`.

### Phase R2: Pure-RISC-V source files
Goal: delete every file that exists only for RISC-V.

- All `*_riscv64.go` and `*_riscv64.s` (and any `*_riscv.go|.s`) — ~98 files
  spread across `diplomat/main/`, `kmazarin/`, `kmazarin/ksyscall/`,
  `kmazarin/kmem/`, `kmazarin/kirq/`, `kmazarin/ktimer/`,
  `kmazarin/device/virtio/`, `shared/constants/`, `shared/fs/fat32/`,
  `runtime-patches/`, `maz/linux/`, `maz/fs/`, `mazarin/`.
- Whole directory: `kmazarin/arch/riscv64/` (PLIC driver lives only here).
- Any `*_riscv64.toml` test fixtures (none expected; verify).

Mechanically safe — these are gated by a pure `//go:build riscv64` tag, so
deleting them cannot affect arm64/amd64 compilation.

### Phase R3: Combined-tag surgical edits
Goal: 23 files where `riscv64` appears alongside other arches. Remove the
RISC-V token without changing behavior on remaining arches. Examples:

- `arm64 || amd64 || riscv64` → `arm64 || amd64` (kmazarin/ds/* etc.)
- `arm64 || riscv64` → `arm64` (clone.go, soft_irq.go) — also rename file
  to `_arm64.go` if name was generic.
- `linux && (amd64 || arm64 || loong64 || riscv64)` →
  `linux && (amd64 || arm64 || loong64)` (mmap.go, cgo_mmap.go).
- `tagptr_64bit.go` (`amd64 || arm64 || ... || riscv64 || ...`) — drop
  `riscv64` from the OR list, keep the rest intact.
- `!riscv64` negation files (e.g. `maz_name_other.go`) — the negation no
  longer needs to mention riscv64. Often the negation file collapses into
  the corresponding `_riscv64.go` removal: if removing the riscv64 file
  makes the `_other.go` apply universally on the remaining arches, delete
  the build tag entirely and rename `_other.go` to the natural name.

Each edit is mechanical but needs a human eye for the rename / collapse
case.

### Phase R4: Tooling and shared constants
Goal: remove RISC-V dispatch / arch tables / sysid entries from
cross-arch tooling.

- `cmd/mkesp/main.go`: drop the riscv64 case from the arch dispatch.
- `cmd/gen-ast-stubs/main.go`: drop `_riscv64` from the suffix list.
- `cmd/fix-go-elf/inject.go`: drop the JAL-trampoline RISC-V comment
  block and any RISC-V branch (verify whether there's actual code or
  only a comment).
- `cmd/elf2pe/main.go` and `cmd/gen-overlay/main.go`: verify no RISC-V
  branches; if generic, no edit. (Survey said generic.)
- `shared/sysid/sysid.go`: drop `RiscvHWProbe` (#258).
- `shared/constants/addresses_riscv64.go`: file-level delete (Phase R2).
- Any `safe-serial-read` or other tools with hardcoded RISC-V log paths
  (verify).

### Phase R5: Docs and memory
Goal: no stale RISC-V instructions remain.

- `CLAUDE.md`: remove RISC-V from the quick-reference env-vars block
  (drop `QEMU_RISCV64`), the run-target examples, the QEMU monitor
  ports section (drop 4447), the "Current Status" RISC-V section, and
  the RISC-V binary-utility notes if any. Total 7 references per survey.
- `design/`: survey says no mentions; spot-verify and delete any stragglers.
- `MEMORY.md` + topic files: re-tag or remove the riscv-specific memory
  files (riscv_crash_investigation.md, response_test_failures.md
  riscv portion, zero_guard_false_positive.md, dma_clump_continuation.md
  riscv portion, phase5_linux_plugin_done.md riscv portion,
  go126_randomized_heap_base.md riscv portion). Memory edits happen at
  the end so the removal log itself is recorded first.

### Phase R6: Verification
Goal: prove nothing on the remaining two arches regressed.

- `$GO tool task` (default arm64 build) clean.
- `$GO tool task run-x86_64 TIMEOUT=10` clean — kernel boots, no
  arch-dispatch surprises.
- `$GO tool task run-arm64-hvf TIMEOUT=10` clean — kernel boots.
- `git grep -i 'riscv\|riscv64\|opensbi\|plic\|fw_dynamic'` should return
  only intentional residue (e.g. third-party Go runtime files we don't
  own). Document any survivors.
- One smoke-build of `mazlink-smoke-amd64` and `mazlink-smoke` (arm64) to
  confirm no shared-table edits broke the plugin path.

### Phase order rationale
R1 → R2 → R3 → R4 → R5 → R6 is the safe order:
- R1 stops anyone from building riscv64 (surfaces broken state immediately).
- R2 removes pure-arch files (cannot break other arches).
- R3 edits combined tags (now `git grep riscv64` catches every survivor).
- R4 removes tooling support (only safe after sources are gone, otherwise
  partial-removal regressions).
- R5 docs (last so we describe the actual state, not the intent).
- R6 verifies.

### Open questions before coding
- Is there any external consumer (CI, scripts in another repo, oncall
  docs) that expects `run-riscv64` to exist? Quick check before R1.
- The `runtime-patches/` riscv64 files — are any of them actually
  upstream Go runtime files we shouldn't touch directly? Confirm before
  Phase R2 (the survey grouped them under "files specific to RISC-V" but
  they live in the runtime overlay tree).

---

## fs.maz Concurrency — Design Sketch (placeholder, 2026-04-24)

Design only, no implementation. To be filled in after RISC-V removal lands.
Reference points already in tree:

- `findings.md` "Fs ↔ Linux Delegate Handler Deadlock" — explains why
  fs.maz today is single-goroutine (per-connection shared data area, no
  ReqID match on responses, ext2 not thread-safe).
- The same findings entry sketches what concurrency would require:
  thread-safe ext2 + per-caller data slots + ReqID-routed responses.

Open design questions to answer in this phase:
1. Per-request data slot allocation in `fsclient.Client` — pool size,
   ownership, leak handling.
2. ReqID matching on the response side — how does `RespCh` become a
   per-ReqID demux without a goroutine per outstanding request?
3. ext2 thread-safety scope — full mutex vs. per-inode lock vs. RW lock
   on directory entries.
4. Backpressure — when does the caller block? On data-slot exhaustion?
   On in-flight count?
5. Interaction with the existing two-lane delegate handler — does the
   stdout lane still exist as a special case, or does generalized
   concurrency subsume it?

---

## Prior status (mail-dumb easy part next, after the two diversions)

Originally on mail-dumb easy part after a diversion to fix the fs↔linux
delegate deadlock (see progress.md "Session 2026-04-24" and findings.md
"Fs ↔ Linux Delegate Handler Deadlock"). The deadlock fix — two-lane
delegate handler (stdout vs. file lane) in the linux shepherd — is
complete, built clean on ARM64 / x86_64, stable in a 60s HVF boot, and
`fmt.Printf` from fs handlers is now visible on the linux console.

Next (after R1–R6 + fs concurrency design): mail-dumb easy part — body
display, PageUp/PageDown, mark-read, delete, and polish. Status from
before the diversion follows.

---

## STATUS: 2026-04-23 (mail-dumb hard part COMPLETE — easy part next)

**Done (this session):** Smart cache Phases S1–S4 all complete. Virtual scroll GridTable, MailCache
  sliding window, MailRow, event-loop wiring, eagerCh drain-race fix, arrow-key MoveSelection.
  100 emails display in 14 visible rows; scrolling is smooth. Debug print removed.
**Next:** mail-dumb easy part — body display, PageUp/PageDown, mark-read, delete, and polish.

**Done (prior):** Smart caching prep: Phases 2 and 3 complete.
**Done:** Phase 2 — `GridRow.MsgNum()`, click routing via `RowPercentage.Click`, `GridTable.SelectedAttr`,
  `setSelected`, `SelectionState` highlight in Draw. `MailRow.onRowSelected` removed.
**Done:** Phase 3 — multi-selection set. `selectedSet map[GridRow]bool`, shift-click detection via
  `hid.Shift`. `SelectedSetAttr` (CollI64 collection), `SelectedSetCountAttr`, `SelectedSetPagesAttr`.
  `publishSelectedSet()` with sentinel rule (>256 → [MaxInt64]). New kernel region `RegionValueColl`
  (32 slots × 256 entries × 40B), `SysAttrWriteCollI64` (slot 45), `ConstraintPageVersion` bumped 3→4.
  `flat.PageRegion.ValueCollections` added; `ReadCollectionElement` dispatches by ElemType.

Rachel window decoration: resize handles visible (borders 2→14px), groove drawn,
applyDecorations called on every Blit, focused windows blit full buffer including borders.
Resize drag: CONFIRMED WORKING — ARM64 HVF 60s run shows dragEndResize firing, window
resized from 900→1029px, BackingStoreReady sent to app. All three windows visible.
Title bar drag: clamped — `moveWindowTo` now enforces `ta.y >= borderTop` so dragging
up can never push the title bar off-screen.
fsclient: 64KB shared data area (was 4KB); linux shepherd flushWriteBuf uses DataLen().
Mail work: x86_64 — all blockers fixed; ARM64 HVF stable, 35 messages loaded, correct senders.
mazdl/mazlink: Phases 0–4 COMPLETE on both arm64 AND amd64.
Go 1.26.2, all builds clean.

**Closed:** fti bleve persister panic — write/mmap coherence fix confirmed working (2026-04-22). All mmap coherence tests pass; 100 docs indexed cleanly with no persister panic or `corrupted` flag.
**Fixed (implemented):** write/mmap coherence in `sysMmapPageFill` — `sysWrite` buffer now flushed before ext2 read on page fill.
**Caution:** always rebuild `linux-ui:arm64` when linuxapp.go changes before run.

---

## Rules & Discipline
Re-read before any coding session:
- `/Users/iansmith/mazzy/CLAUDE.md` — build via Taskfile only, serial log safety, env vars
- `/Users/iansmith/.claude/projects/-Users-iansmith-mazzy/memory/MEMORY.md` — auto-memory

---

## What Was Built

### Kernel: Constraint Collection Allocator Fix
- **Root cause:** `attr.Find(pattern)` used a single bump allocator for all collection
  results. After ~a dozen `AddRow` calls the bump region exhausted; `GetChildren()`
  returned 0, making the mail grid appear empty.
- **Fix:** Per-query fixed collection slots — 64 slots × 1024 entries each.
  - `kmazarin/kmem/constraint.go`: `ConstraintPageVersion` 2→3; `RegionCollCap`
    4096→65536; `CollCapacity` field widened `uint16`→`uint32`.
  - `kmazarin/ksyscall/constraint_mgr.go`: `queryPattern.collOff` assigned at
    registration; `MaxCollPerQuery=1024`; compile-time assertion.
  - `kmazarin/ksyscall/constraint_syscall.go`: `writeQueryCollection` uses fixed
    per-query region; `SyscallAttrRegisterQuery` assigns `collOff`.
  - `mazarin/vm/flat/layout_shared.go`: userspace header parser updated for `uint32`
    collCap and `SharedPageVersion=3`.

### Go 1.26.2 Migration (complete)
- `go.mod`, `mazarin/textshape/go.mod`, `internal/gg/go.mod` → `go 1.26.2`
- `Makefile`, `CLAUDE.md`, site docs, `cmd/check-version` all updated.
- `design/GO-126-MIGRATION.md` deleted (stale; mazgo/mazlink cover 1.26.2).
- `GOEXPERIMENT=norandomizedheapbase64,nogreenteagc` in Taskfile retained.

### GridTable: Async Row Rendering
- `GridTable.AddRow` now returns a `func()` (OnLoaded callback).
- Labels are pre-positioned at expected row Y on `AddRow` so `FullDamage()` emits
  a non-empty rect immediately. `RowPercentage.Draw` refines column X on next pass.
- `GridTable.Draw` syncs `dataLabs[].Text` from live `GridRow` data each draw pass
  — async-loaded rows (MailRow) display data without a separate update step.
- `DamageAll()` marks every leaf `DynamicLabel` dirty (parent `RowPercentage` has
  constraint DamageRect; its `FullDamage` is a no-op).
- Divider `onDamage` uses `DamageAll()` instead of manual per-label walks.

### RowPercentage: Column Clipping
- **Root cause:** `WithClip`/`Flush()` pixel save-restore was not reliably clipping
  text to column boundaries — long sender/subject strings overlapped adjacent columns.
- **Fix:** Replaced with proper DrawContext clip path:
  ```go
  dc.Push()
  dc.DrawRectangle(float64(curX), float64(y), float64(childW), float64(h))
  dc.Clip()
  d.Draw(child, curX, childY, childW, childH, damage)
  dc.ResetClip()
  dc.Pop()
  ```
  Applied when `ClipChildren=true` (set by `GridTable.AddRow`).

### Maildb / fti / Mail App (Phases 1–5)
All phases of the maildb protocol and mail app integration are complete; see git
history (commits `706820f` through `2a4a092`) and `findings.md` for per-phase detail.

### Diagnostic Cleanup
- Removed all `fmt.Printf` diagnostic traces added during debugging:
  `app_window.go`, `column_percentage.go`, `grid_table.go`, `margin_parent.go`,
  `apps/mail/main.go` (redraw counter + forced-damage workaround).

### mazdl / mazlink — Plugin-shape .maz loader (Phases 0–4 arm64)
Complete redesign of the `.maz` dynamic loading infrastructure using a
Mazarin-native `dlopen`/`dlsym` API. Full design and phase specs preserved in
`findings.md` (architecture) and `progress.md` (phase log).

- **Phase 0** (2026-04-18): `mazlinkNopHostInitTasks` in `ld/go.go` — flips
  `runtime..inittask.state=2` so plugin never spawns duplicate runtime singleton
  goroutines (forcegchelper, sysmon, bgsweep, bgscavenge, etc.).
- **Phase 1** (2026-04-18): Policy list + ABI contract signed off. Policy file at
  `mazlink-patches/policy/dlopen-host-packages.txt`.
- **Phase 2** (2026-04-18): Plugin builds with no runtime code. mazlink emits UNDEF
  dynsym + PLT + `DT_NEEDED=mazarin-host`. Plugin binary < 1 MB (was ~6 MB).
- **Phase 3** (2026-04-18): Host exports runtime dynsym (3292 `runtime.*`, 418
  `internal/runtime/*`, 423 `internal/abi.*` entries on arm64). `smoke/host-probe`
  validates. `mangleTypeSym` patched so host+plugin agree on hashed `type:.<hash>`
  dynsym names.
- **Phase 4 arm64** (2026-04-18): `mazdl.Open` loads `smoke/plugin` end-to-end;
  funcval dead-reloc bug fixed via Option A (GLOB_DAT for host-policy funcvals
  in `amd64/asm.go` + `arm64/asm.go`); `rewriteHostFuncvals` removed from
  `mazdl/open.go`. All four exit criteria pass under `$GO tool task mazlink-smoke`.

---

## Smart Cache — Phases S1–S4 (2026-04-23, COMPLETE)

Context: the current mail app loads ≤50 rows once and never scrolls. Smart cache
replaces this with a virtual-scroll model that holds only a small sliding window
of `KeyHeaderEntry` records in memory, fetching ahead as the user scrolls.

### Relationships between phases

```
S1 (GridTable scroll + attrs)
  → S2 (VirtualMailRow + MailCache structs)
    → S3 (wire main.go)
      → S4 (batch unpack, already needed by S2)
```

S4 is a dependency of S2 (cache must unpack multi-entry RespKeyHeaders). Write S4
first when implementing S2.

---

### Phase S1: GridTable virtual scroll pool ✅ COMPLETE

**Goal:** GridTable draws a fixed-size pool of slot widgets, scrolls via `scrollOffset`,
and publishes three new value attrs that the cache reads.

**Design constraints confirmed by user:**
- Pool size = `visibleCount` = integer (no rounding up) rows that fully fit
- Same slot objects are reused across scrolls — msgNum updated by grid
- Font size change triggers full pool rebuild (visibleCount changes)
- `TotalRows int64` field (set by main from collSize) used for scroll clamping

**New GridTable fields:**
| Field | Type | Purpose |
|-------|------|---------|
| `TotalRows` | `int64` | Collection size for scroll clamping; set by caller |
| `scrollOffset` | `int64` | Index of row shown in slot 0 |
| `visibleCount` | `int64` | Slots that fully fit; recomputed in Draw |
| `slotPool` | `[]GridRow` | Fixed pool of slot data objects (VirtualMailRow) |
| `slotWidgets` | `[]*RowPercentage` | Widget per slot; parallel to slotPool |
| `slotLabels` | `[][]*DynamicLabel` | Labels per slot; parallel to slotPool |
| `rowFactory` | `func() GridRow` | Creates new slot data objects on pool rebuild |
| `poolEpoch` | `int` | Incremented on rebuild; ensures unique widget URIs |
| `FirstVisibleMsgNumAttr` | `*attr.Attribute[int64]` | = scrollOffset; clamped to collSize-1 |
| `LastVisibleMsgNumAttr` | `*attr.Attribute[int64]` | = scrollOffset + visibleCount - 1 |
| `VisibleRowCountAttr` | `*attr.Attribute[int64]` | = visibleCount |

**Slot interface (in `std` package):**
```go
// MsgNumSetter is implemented by virtual row objects so the grid can update
// their displayed msgNum on scroll without importing the mail package.
type MsgNumSetter interface {
    SetMsgNum(msgNum uint32)
}
```
`GridTable.ScrollBy` checks `row.(MsgNumSetter)` and calls `SetMsgNum`.

**New GridTable methods:**
```go
SetTotalRows(n int64)             // update TotalRows + reclamp scrollOffset
SetRowFactory(f func() GridRow)   // store factory; rebuild pool if visibleCount > 0
ScrollBy(delta int64)             // move scrollOffset, call SetMsgNum on all slots,
                                  //   publishScrollAttrs, DamageAll
buildSlotPool(count int64)        // epoch++; create count slots via rowFactory + widgets
publishScrollAttrs()              // write First/Last/VisibleCount attrs
headerH(rh int64) int64           // rh + 2 + 1 (header row + 2px padding + 1px separator)
computeVisibleCount(h, rh int64) int64  // (h - headerH(rh)) / rh, min 0
```

**Draw changes:**
1. Compute `rh = rowHeight()`, `newVC = computeVisibleCount(h, rh)`
2. If `newVC != visibleCount`: call `buildSlotPool(newVC)`, set `visibleCount = newVC`
3. Draw only `slotPool[0..visibleCount-1]` (not `rows`)
4. After draw: call `publishScrollAttrs()`

**Backward compatibility:** `AddRow` / `rows` / `rowWidgets` / `dataLabs` stay for
non-virtual use. `slotPool`/`slotWidgets`/`slotLabels` are NEW parallel fields used only
when `rowFactory != nil`. `Draw` checks `rowFactory != nil` to decide which path to take.

**New GridFrame accessors (forwarding to grid):**
- `FirstVisibleMsgNumAttr() *attr.Attribute[int64]`
- `LastVisibleMsgNumAttr() *attr.Attribute[int64]`
- `VisibleRowCountAttr() *attr.Attribute[int64]`
- `SetTotalRows(n int64)`
- `SetRowFactory(f func() GridRow)`
- `ScrollBy(delta int64)`

**File:** `mazarin/mancini/std/grid_table.go`

**Constraint URI summary (new):**
| Attribute | URI | Notes |
|-----------|-----|-------|
| First visible msgNum | `layout:///NAME_tbl/int64/grid/firstVisible` | -1 if no rows |
| Last visible msgNum | `layout:///NAME_tbl/int64/grid/lastVisible` | -1 if no rows |
| Visible row count | `layout:///NAME_tbl/int64/grid/visibleCount` | 0 until first draw |

---

### Phase S2: VirtualMailRow + MailCache ✅ COMPLETE

**Goal:** Define the data layer that GridTable slots consume.

**New file: `mazarin/apps/mail/virtual_row.go`**
```go
type VirtualMailRow struct {
    msgNum uint32
    cache  *MailCache
}
// Implements GridRow (Sender, Subject, Date, MsgNum)
// Implements MsgNumSetter (SetMsgNum)
func (r *VirtualMailRow) Sender() string {
    e := r.cache.Get(r.msgNum)
    if e == nil { return "…" }
    s, _, _ := mailproto.UnpackKeyHeaderEntry(e)
    return s
}
// Subject, Date analogous; Date returns "" not "…" on nil
func (r *VirtualMailRow) MsgNum() uint32 { return r.msgNum }
func (r *VirtualMailRow) SetMsgNum(n uint32) { r.msgNum = n }
```

**New file: `mazarin/apps/mail/mail_cache.go`**
```go
const readAhead = 2

type MailCache struct {
    maildbSID int

    collId   uint32
    collSize uint32

    entries  map[uint32]*mailproto.KeyHeaderEntry
    windowLo uint32
    windowHi uint32

    inFlight    bool
    inFlightId  [16]byte
    inFlightLo  uint32
    inFlightHi  uint32

    OnUpdated  func()    // called after entries change → main calls gridFrame.DamageAll
    reqCounter uint64
}
```

**MailCache.Rebalance(first, last, visCount int64):**
```
prefetch = readAhead × visCount
lo = max(0, first - prefetch)
hi = min(collSize-1, last + prefetch)

if lo == windowLo && hi == windowHi → return (no change)

evict entries with key < lo or key > hi
windowLo = lo; windowHi = hi
fetchRange(lo, hi)
```

**MailCache.fetchRange(lo, hi uint32):**
- If already in-flight for same [lo,hi]: no-op
- Generate new reqId; send `KeyHeadersReq{CollId, From=lo, To=hi}` via `uring.Send`
- Record inFlight=true, inFlightId, inFlightLo, inFlightHi

**MailCache.HandleResponse(v any):**
- `RespKeyHeaders`: if reqId matches → unpack batch (Phase S4), call OnUpdated
- `CollectionAdd`: if CollId matches → collSize++, evict shifted range [notif.MsgNum..], call OnUpdated
- `CollectionRemove`: if CollId matches → collSize-- (floor 0), evict range, call OnUpdated
- `RespCreateCollection`: NOT handled here (stays in main)

**MailCache.Get(msgNum uint32) *mailproto.KeyHeaderEntry:**
- Returns `entries[msgNum]` — nil if not yet loaded (caller shows "…")
- Does NOT trigger a fetch (Rebalance is the fetch trigger)

**File:** `mazarin/apps/mail/mail_cache.go`

---

### Phase S3: Wire main.go ✅ COMPLETE

**Goal:** Replace the existing MailRow machinery with cache + virtual rows.

**Remove from main.go:**
- `mailRows []*MailRow` — entire slice and all management code
- `rowByReqId map[[16]byte]*MailRow`
- `reqCounter uint64`
- `nextReqId()` function (moves into MailCache)
- `handleCreateCollectionResp` (replaced below)
- `handleKeyHeadersResp`
- `handleCollectionAdd` / `handleCollectionRemove`
- `onCollectionExpired`

**Add to main.go:**
```go
var cache *MailCache   // package-level; nil until CreateCollection succeeds
```

**Simplified handleCreateCollectionResp:**
```go
func handleCreateCollectionResp(resp *mailproto.RespCreateCollection) {
    if resp.ErrCode != mailproto.ErrNone { ... }
    activeCollId = resp.CollId
    cache = &MailCache{maildbSID: maildbSID, entries: make(map[uint32]*mailproto.KeyHeaderEntry)}
    cache.SetCollection(resp.CollId, resp.Size)
    cache.OnUpdated = func() { gridFrame.DamageAll() }
    gridFrame.SetTotalRows(int64(resp.Size))
    gridFrame.SetRowFactory(func() GridRow {
        return &VirtualMailRow{cache: cache}
    })
    // Trigger initial rebalance using current visible attrs
    first := gridFrame.FirstVisibleMsgNumAttr().Get()
    last := gridFrame.LastVisibleMsgNumAttr().Get()
    vis := gridFrame.VisibleRowCountAttr().Get()
    if vis == 0 { vis = 9 }  // fallback if grid not yet drawn
    cache.Rebalance(first, last, vis)
}
```

**Modified handleMailResponse:**
```go
func handleMailResponse(v any) {
    switch resp := v.(type) {
    case mailproto.RespCreateCollection:
        handleCreateCollectionResp(&resp)
    default:
        if cache != nil { cache.HandleResponse(v) }
    }
}
```

**eagerCh handler — add cache rebalance:**
```go
case <-eagerCh:
    if cache != nil {
        first := gridFrame.FirstVisibleMsgNumAttr().Get()
        last  := gridFrame.LastVisibleMsgNumAttr().Get()
        vis   := gridFrame.VisibleRowCountAttr().Get()
        cache.Rebalance(first, last, vis)
    }
    redraw("eagerCh")
```

**Keyboard scroll (in wmCh KeyboardPress handler):**
```go
case wm.KeyboardPress:
    vis := gridFrame.VisibleRowCountAttr().Get()
    switch m.Key {
    case hid.KeyDown:     gridFrame.ScrollBy(1)
    case hid.KeyUp:       gridFrame.ScrollBy(-1)
    case hid.KeyPageDown: gridFrame.ScrollBy(vis)
    case hid.KeyPageUp:   gridFrame.ScrollBy(-vis)
    }
```

**File:** `mazarin/apps/mail/main.go`

---

### Phase S4: Batch KeyHeaders unpack in MailCache ✅ COMPLETE

**Goal:** Correctly unpack multi-entry RespKeyHeaders pages.

**Key facts from protocol:**
- `RespKeyHeaders.Count` = number of entries in pages
- Pages contain a packed array: `Count × KeyHeaderEntrySize` bytes at `TargetVA`
- `KeyHeaderEntry.MsgNum` field = collection position (set by maildb)
- maildb caps at 128 entries per request; our window (readAhead=2, visibleCount≤20) ≤ 56 entries

**Unpack loop (inside MailCache.HandleResponse RespKeyHeaders branch):**
```go
pages := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(resp.TargetVA))), int(resp.NumBytes))
for i := 0; i < int(resp.Count); i++ {
    off := i * mailproto.KeyHeaderEntrySize
    e := *(*mailproto.KeyHeaderEntry)(unsafe.Pointer(&pages[off]))
    eCopy := e
    c.entries[e.MsgNum] = &eCopy
}
numPages := (int(resp.NumBytes) + 4095) / 4096
mem.FreePages(unsafe.Pointer(uintptr(resp.TargetVA)), numPages)
```

Note: `eCopy := e` is required — `e` is a local copy from the pages slice which will be
freed; the map must hold a stable pointer.

**File:** `mazarin/apps/mail/mail_cache.go` (part of S2 impl)

---

### Implementation Order

1. **S4 + S2** together (VirtualMailRow + MailCache with batch unpack)
2. **S1** (GridTable scroll pool + attrs) — can be built/tested with a stub factory
3. **S3** (wire main.go) — connects the two

---

### Open questions (resolve before coding)

- **Keyboard event routing:** does `wm.KeyboardPress` land in `wmCh`? Confirm the key
  constant names for Up/Down/PageUp/PageDown in `shared/hid/`.
- **`MailRow` file:** keep as dead code or delete? Delete is cleaner; `MailCache`
  replaces its role entirely.
- **Selection after scroll:** `SelectedAttr` currently holds a msgNum from the old row.
  After pool rebuild, the VirtualMailRow at that slot has a different msgNum. Need
  `RefreshSelected()` call after `buildSlotPool`. Add to `buildSlotPool`.

---

## Smart Caching Prep — Phases 1–3

Context: mailboxes can have 10s of thousands of messages. Collections are sparse —
only loaded windows are in memory. These three phases prepare the UI infrastructure
so smart caching (fetching only the visible window) knows what to fetch.

---

### Phase 1: KnownHeight on Face interface

**Goal:** Any `Face` can report its preferred pixel height given the current drawing
font. `GridTable` publishes `rowHeight` and `visibleRows` into the constraint network
so callers can compute the fetch window size.

**Design decisions:**
- `KnownHeight(dc DrawContext) int64` added to the `Face` interface.  Returns 0 if
  the face cannot determine height yet (nil DC, no font opened).
- `LatinTextFaceImpl` returns `int64(ceil(ascent + descent))` from `dc.GetFontMetrics`.
  Falls back to 0 when dc == nil (first call before DrawFace).
- `ClockFace` is a *separate* interface and does not embed `Face`, so it is unaffected.
  Any Face wrapping a clock (not currently present) would return its diameter.
- Other Face implementors (if any): add a `KnownHeight` returning 0.

**Files to change:**
| File | Change |
|------|--------|
| `mazarin/mancini/text_face.go` | Add `KnownHeight(dc DrawContext) int64` to `Face` interface |
| `mazarin/mancini/impl/latin_text_face.go` | Implement: `dc.GetFontMetrics(fontID)`, return `ascent+descent` in px; 0 if dc nil |
| `mazarin/mancini/std/grid_table.go` | After font is resolved, compute and publish `rowHeight` and `visibleRows` attrs |

**Constraint export from GridTable:**
```
gridURI(name, "rowHeight")   → attr.ValueI64, pixels per data row (0 until font resolved)
gridURI(name, "visibleRows") → attr.ValueI64, floor(gridHeight / rowHeight); 0 until known
```
`gridURI` already exists: `mancini.LayoutURI(gridName, mancini.DataTypeInt64, mancini.LayoutProp("grid/"+field))`.
These two new attrs are `ValueI64` fields on `GridTable`, initialized to 0.
`GridTable.Draw` updates them whenever `lastFontSize` changes or they are still 0.
The height comes from calling `KnownHeight` on the face of any label in the first data row.

**How callers use it:** Constrain to `gridURI(tblName, "visibleRows")` to know the
window size; use `gridURI(tblName, "rowHeight")` to compute scroll offsets.

**Complexity:** Low. Main edge case: KnownHeight returns 0 before first draw; callers
must treat 0 as "not yet known."

---

### Phase 2: Selected item exported to constraint network

**Goal:** Clicking a grid row updates a published `int64` attribute that any other
component can constrain against. Value is the `msgNum` from the collection (-1 = none).

**Design decisions:**
- `GridTable` owns the selection state. `selectedIdx int` (row index, -1 = none) is
  an unexported field.
- `SelectedAttr *attr.Attribute[int64]` on `GridTable` — a `ValueI64` at
  `gridURI(name, "selected")`, initial value -1.
- Click routing: `RowPercentage` gets an `OnClick func()` callback field.
  GridTable sets this during `AddRow`. `RowPercentage` implements `Clickable` by
  calling `OnClick()`.  GridTable's `AddRow` closure captures the row index.
- On click: GridTable sets `selectedIdx = rowIdx`, looks up the `GridRow` to get
  `msgNum`, calls `SelectedAttr.Set(int64(msgNum))`, damages both old and new selected
  rows so they repaint.
- Visual: `RowPercentage` gains a `SelectionState` field (int, 0 = none, 1 = selected).
  When `SelectionState != 0`, `Draw` fills the row rect with a semi-transparent
  highlight before drawing children. Color for state 1: `pal.Dark()` at ~40% alpha
  (exact value TBD from visual testing).
- `GridRow` interface extended: add `MsgNum() uint32` so GridTable can read the msgNum
  without casting to `*MailRow`. All current GridRow implementors must add this method.

**Files to change:**
| File | Change |
|------|--------|
| `mazarin/mancini/std/grid_table.go` | `selectedIdx`, `SelectedAttr` fields; update `AddRow` to wire `OnClick`; `SetSelected(idx int)` internal helper; `SelectedAttr` init in `NewGridTable` |
| `mazarin/mancini/std/row_percentage.go` | `OnClick func()` field; implement `Click(*InputEvent) bool` |
| `mazarin/mancini/std/grid_table.go` (Draw) | Pass `SelectionState` to each `RowPercentage` on draw |
| `mazarin/mancini/std/grid_table.go` (GridRow) | ✅ `MsgNum() uint32` added to interface |
| `mazarin/apps/mail/mail_row.go` | ✅ `MsgNum()` already existed at line 77; `Select()` already exists but is now called via `OnClick` |
| `mazarin/apps/mail/main.go` | Remove manual `onRowSelected` wiring; read `grid.SelectedAttr.URI()` to constrain viewer |

**Constraint export URI:** `layout:///gridframeName_tbl/int64/grid/selected`
(using `gridURI("gridName_tbl", "selected")`). Value = msgNum int64, or -1.

**Complexity:** Medium. Click wiring is new; `GridRow.MsgNum()` requires touching all
implementors (currently only `MailRow` and any test rows).

---

### Phase 3: Multi-selection set

**Goal:** Shift+click adds a row's msgNum to an exported set. The primary selected item
(Phase 2) is always in the set. Export the full set as a proper `CollI64` collection
attribute so any component can consume it directly from the constraint network.

**Design decisions:**
- `GridTable` adds `selectedSet map[uint32]bool` (msgNum → present).
- Shift detection: `ev.Mods & hid.ModShift != 0` in the `Click` callback.
  Normal click: clears the set, sets new primary (Phase 2 behavior unchanged).
  Shift+click: toggles the clicked row's msgNum in the set; does NOT change
  `selectedIdx` (primary is always the last non-shift click).
  The primary msgNum is always in `selectedSet`.
- `SelectedSetAttr *attr.Attribute[vm.Value]` — `attr.ValueCollI64` at
  `gridURI(name, "selectedSet")`. Initial value: empty collection.
  **Sentinel rule:** if `len(selectedSet) > 256`, the collection contains exactly one
  element: `math.MaxInt64`. This signals "large set — use the IPC path."
  `math.MaxInt64` is a safe sentinel: valid msgNums are uint32, so max valid value as
  int64 is `math.MaxUint32` (4,294,967,295), far below MaxInt64.
- `SelectedSetCountAttr *attr.Attribute[int64]` — `attr.ValueI64` at
  `gridURI(name, "selectedSetCount")`. Always reflects the **true** count of selected
  items, regardless of whether the sentinel is active.
- `SelectedSetPagesAttr *attr.Attribute[int64]` — `attr.ConstraintI64` at
  `gridURI(name, "selectedSetPages")`. Computed via constraint program
  `ProgComputeNeededPages` bound to `SelectedSetCountAttr`. Formula:
  ```
  entriesPerPage = pageSize / intSize = 4096 / 8 = 512
  base = count / entriesPerPage
  rem  = count % entriesPerPage
  if rem != 0: result = base + 1
  else:        result = base
  ```
  This is `ceil(count / 512)` expressed as an explicit modulo-plus-conditional
  constraint. Consumers that receive the sentinel use this attribute to know
  exactly how many pages to allocate — no arithmetic required on their side.
- **TODO (large-collection IPC path):** When a consumer sees the sentinel, it
  allocates `SelectedSetPagesAttr.Get()` shared pages and passes them to the grid
  (via a yet-to-be-designed IPC message). The grid fills the pages with the full
  selected msgNum set and signals completion. This covers bulk mail operations
  (e.g. moving all messages from a sender into a folder). Design deferred.
- Visual — three states for `RowPercentage.SelectionState`:
  - 0: no background (unselected)
  - 1: primary selection — `pal.Highlight()`
  - 2: in set but not primary — `pal.Accent()`
  `GridTable.Draw` computes each row's state from `selectedIdx` and `selectedSet` each pass.

**New infrastructure required:**

The existing collection region (`RegionCollCap = 65536`) is fully committed to query
results (64 queries × 1024 entries). Value-collection attributes need their own
dedicated region. This requires a new page-layout region and a `ConstraintPageVersion`
bump from 3 → 4.

**Design rule:** value-collection attributes are capped at `MaxValueCollEntries = 256`
per attribute. This is a deliberate constraint — the constraint network carries UI-scale
values. Larger collections (e.g. "select all 50K messages") belong in the
maildb/IPC collection protocol, expressed as a `FilterAll` descriptor, not an
enumerated set.

| Layer | What to add |
|-------|-------------|
| `kmazarin/kmem/constraint.go` | `RegionValueCollSlots = 32`; `MaxValueCollEntries = 256`; `RegionValueCollSize = RegionValueCollSlots × MaxValueCollEntries × valueSize`; add after `RegionCollSize`; bump `ConstraintPageVersion` to 4 |
| `kmazarin/kmem/page_descriptor.go` | Add `ValueCollRegionOff`, `ValueCollSlotCount` to header; update `InitConstraintHeader` |
| `shared/mazzy/mazzy.go` | `SysAttrWriteCollI64 = MazzySyscallBase + 44 // 0x102C` |
| `kmazarin/ksyscall/constraint_syscall.go` | Handler: args = slot, userVA, count; validates count ≤ 256; reads int64s from userVA via `WalkUserPageTable`; writes into value-coll region slot; sets flat.Value to CollRef; propagates dirty |
| `mazarin/sys/constraint.go` | `AttrWriteCollI64(slot uint16, values []int64, isConstraintResult bool) error` — passes user VA of slice backing array |
| `mazarin/attr/attribute.go` | Add `isCollI64 bool` field; `Set()` branch calls `sys.AttrWriteCollI64` when set |
| `mazarin/attr/attribute_value.go` | `ValueCollI64(uri string, initial []int64) *Attribute[vm.Value]` — `flat.TypeCollection`, `ElemType=TypeI64`, `isCollI64: true`; panics if `len(initial) > 256` |
| `mazarin/vm/flat/layout_shared.go` | Update userspace header parser for version 4; add `ValueCollRegionOff` + new region accessors |

**Files to change:**
| File | Change |
|------|--------|
| `mazarin/mancini/std/computeneededpages.vgo` | New `.vgo` source; `compile-constraints` generates `selected_set_pages.vbc.go` → `ProgComputeNeededPages` |
| `mazarin/mancini/std/grid_table.go` | `selectedSet map[uint32]bool`; `SelectedSetAttr`; `SelectedSetCountAttr`; `SelectedSetPagesAttr`; `setSelected` updated for shift; `publishSelectedSet()` — see sentinel rule; `NewGridTable` wires `ProgComputeNeededPages` via `BindStrings` |
| `mazarin/mancini/std/row_percentage.go` | `SelectionState int` field (was bool in Phase 2); state 2 background in `Draw` |
| `mazarin/apps/mail/main.go` | Optionally constrain to `SelectedSetAttr.URI()` for bulk-op toolbar |

**Constraint export URI:** `layout:///gridName/int64/grid/selectedSet`
(using `DataTypeInt64` since `CollI64` elements are int64; the collection type tag
is carried in the flat value itself, not the URI type segment).

**Complexity:** Medium. The new infrastructure is 4 small, mechanical additions. The
`isCollI64` flag in `Attribute[T]` is 4 lines mirroring the existing `isStr` path.

---

### Phase 1–3 Implementation Order

1. Phase 1 first — establishes `KnownHeight` contract; purely additive, no behavior change.
2. Phase 2 next — click wiring and constraint export; requires `GridRow.MsgNum()`.
3. Phase 3 last — extends Phase 2 selection model; requires `hid.ModShift` access.

### Open questions before coding

- **hid.ModShift:** confirmed in `shared/hid/`; use `ev.Mods & hid.ModShift != 0`.
- **GridRow.MsgNum():** `*MailRow` is the only implementor in the codebase. No other
  types need updating.

---

## Known Bugs / Open Issues

### 6. x86_64: `morestack on g0` in badger compaction goroutine (FIXED 2026-04-21)
- **Root cause:** TLS-sync path in `abi_stubs_amd64.s` did WRMSR then RDMSR to get FS_BASE.
  WRMSR hadn't propagated when RDMSR fired → stale FS_BASE → wrong g written to TLS.
- **Fix:** Replaced RDMSR with direct read from `144(R12)` (saved FSBase in ThreadContext).
  Both run path and yield path fixed. No `morestack on g0` in subsequent test runs.
  File: `kmazarin/kmazarin/abi_stubs_amd64.s`.

### 8. x86_64: mail app crashes (exit code 2, panic not visible) (FIXED 2026-04-21)
- **Root cause:** Two independent bugs, both fixed:
  1. WRMSR→RDMSR race in TLS sync (`abi_stubs_amd64.s`): stale FS_BASE → wrong g → `morestack on g0`.
  2. `SyscallUringSend` EINVAL for cross-page 128-byte IPC message on x86_64 stack layout → nil
     font face → mail app panic.
- **Fix 1:** Direct read from `144(R12)` instead of RDMSR in both RunFirstThread and YieldToReadyThread.
- **Fix 2:** Slow-path copy in `kmazarin/ksyscall/uring_ipc.go` for messages spanning page boundaries.
- **Confirmed FIXED:** 300s run completes with no crash; mail app loads and renders correctly.

### 9. x86_64: CollectionAdd double-counting race (FIXED 2026-04-21)
- **Symptom:** Mail grid showed duplicate sender for the last imported message.
  `totalSize` inflated to N+1 for an N-message mailbox; fourth row showed same sender as third.
- **Root cause:** `createCollection` counted messages outside `cs.mu`; then `addMessage` fired
  for a message already included in that count, incrementing `totalSize` a second time.
  Also: `CollectionAdd` shifted a still-loading MailRow's `msgNum` but left the in-flight
  `KeyHeaders` request carrying the old position → maildb returned data for the wrong message.
- **Fix 1 (collection.go):** Moved `countDateIndex()` call inside `cs.mu` lock in `createCollection`
  so the count and slot assignment are atomic with respect to `addMessage`.
- **Fix 2 (collection.go):** `addMessage` now calls `countDateIndex()` under `cs.mu` before
  processing any collection; skips collections where `currentCount <= coll.totalSize` (message
  was already counted at creation time).
- **Fix 3 (mail_row.go + main.go):** Added `IsLoading()` and `RefreshRequest(newReqId)` to
  `MailRow`; `handleCollectionAdd` calls `RefreshRequest` for any displaced loading row so the
  new in-flight request carries the post-shift `msgNum`.
- **Confirmed FIXED:** Subsequent runs show clean sequential CollectionAdds with distinct
  correct senders and no duplicates.

### 7. x86_64: collection created with size=0 mid-import (FIXED 2026-04-21)
- **Root cause:** `createCollection` called `readCounter` which returns 0 before
  `initCounters` runs. Fixed by scanning the date: index instead.
- **Fix:** `countDateIndex()` / `countUnreadDateIndex()` helpers in `collection.go`;
  `createCollection` uses them instead of `readCounter`.

### 1. fti: bleve persisterLoop panic — write/mmap coherence (CONFIRMED FIXED 2026-04-22)
- **Symptom:** Bleve scorch's `persisterLoop` (which has `defer recover()`) panics; fti
  marks index as `corrupted` and drops subsequent documents with a logged error.
- **Root cause:** `sysWrite` buffers sequential writes in `fdEntry.writeBuf` without writing
  to ext2 immediately. Bleve writes `.zap` segment data via `write()` then mmaps the same fd.
  The mmap page fault calls `sysMmapPageFill`, which read directly from ext2 — which had zeros
  because the write buffer was never flushed. Bleve reads back zeros from the segment,
  dereferences a nil pointer, and the persister panics. Scorch's `persisterLoop` `recover()`
  catches this and calls `fireAsyncError(ErrAsyncPanic)`.
- **Impact:** fti indexing degrades gracefully (documents are stored in badger;
  only full-text search is affected). The shepherd does not crash — the `corrupted`
  flag prevents further bleve calls.
- **Mitigation (2026-04-21):** `waitForOne` in `maz/maildb/mbox_import.go` deduplicates
  error notifications: first occurrence shown; subsequent identical messages suppressed
  (every 50th shown as "Index error (Nx): ...").
- **Root fix (2026-04-21, confirmed 2026-04-22):** `sysMmapPageFill` in
  `maz/linux/syscalls.go` now calls `flushWriteBuf` before reading from ext2, ensuring
  `sysWrite`-buffered data is visible to mmap page faults. ARM64 HVF 120s run: all
  mmap coherence tests pass; 100/100 docs indexed cleanly; no persister panic; no
  `[maildb] WARNING: mmap coherence test FAILED`.

### 10. Rachel: title bar off-screen after window drag (FIXED 2026-04-21)
- **Symptom:** Dragging a window upward would eventually push the title bar completely above
  y=0, making it invisible. The window appeared to have no title bar. Screenshot confirmed
  `ta.y=13 < borderTop=24` after a drag, placing `face.top = -9` off-screen.
- **Root cause:** `moveWindowTo` in `maz/rachel/main.go` clamped the LR anchor box (100×100
  at lower-right) but had no equivalent clamp preventing `ta.y < borderTop`. For a 1200px
  tall window the LR-box top clamp fires at `ta.y = borderTop - winH + boxH = 24 - 1200 + 100 = -1076`,
  leaving the title bar freely draftable off-screen.
- **Fix:** Added `if newY < bT { newY = bT }` immediately after the LR-box top clamp. Ensures
  `ta.y >= borderTop` always, so `face.top = ta.y - borderTop >= 0`.
- **Confirmed FIXED:** Subsequent screenshot showed "Mail" title bar visible at top of window.

### 2. VirtIO block: intermittent stall on large file reads
- **Symptom:** fs shepherd logs `[fs] reading /fti.elf...` (18.5 MB) and then
  produces no further output for the remainder of the run. The block device IRQ
  apparently never fires for one or more DMA transfers.
- **Frequency:** ~1 in 3 cold runs observed.
- **Root cause:** Unknown. DMA scratch is 8 pages (32 KB); 18.5 MB requires ~592
  sequential transfers. An interrupt miss under HVF scheduling stalls the whole
  read permanently — there is no timeout or retry in the fs read path.
- **Confirmed working when not stalled:** A 120s run with a fresh disk image loaded
  fti.elf in full (4642 blocks, all batches completed). fti indexed 98 emails and
  the mail app rendered all 50 rows correctly.
- **Red herring (2026-04-21):** The 300s hang that triggered investigation was caused
  by a stale disk.img (Taskfile `method: checksum` did not detect the kmazarin.elf
  dependency change). After forcing a rebuild the hang did not recur in that run.
  The true intermittent VirtIO stall is a separate bug, still open.
- **Next step:** Add a watchdog timer in the fs DMA read loop. Investigate whether
  the block IRQ edge-trigger is being lost under HVF when many back-to-back
  transfers are queued.

### 3. GridTable: no RemoveRow
- `CollectionRemove` notifications are tracked in the mail app's in-memory list
  but the visual grid is not updated. `GridTable` lacks a `RemoveRow` method.
- **Next step:** implement `GridTable.RemoveRow(idx int)` when mail needs it.

### 4. mazdl Phase 4: amd64 parity — COMPLETE (2026-04-21)

**All four exit criteria pass on amd64.** `$GO tool task mazlink-smoke-amd64`
exits 0.

**Root cause of regression:** `smoke/host-mazdl/go.mod` had `go 1.26` but the
root `mazzy` module uses `go 1.26.2` (updated in the Go 1.26.2 migration commit).
This caused mazgo to report `go: updates to go.mod needed` when building
`host-mazdl` (which imports mazzy via replace directive). Fixed by bumping
`smoke/host-mazdl/go.mod` to `go 1.26.2`.

**All loader-side and linker-side code was already correct** — `reloc_amd64.go`,
`amd64/asm.go` Option A block, R_GOTPCREL handler, and the `mazlink-smoke-amd64`
Taskfile task all existed and worked without change. Phase 4 arm64 and amd64 are
now both continuously verified by their respective smoke tasks.

**Known minor issue (non-blocking):** Three `runtime.AddCleanup[go.shape.struct...]`
generic stencils from Go 1.26.2 appear as DEFINED T in the plugin (Phase 2
metric shows "DEFINED T runtime.* symbols: 3" on both arches). These are GCshape
instantiations whose `SymPkg` in the linker is not attributed to `runtime`,
bypassing the policy filter in `rewriteHostSymsAsDynimport`. They don't affect
smoke exit criteria (AddCleanup is not called by the smoke plugin) but represent
a mild policy gap for production plugins that use generics or packages that call
`runtime.AddCleanup`. Fix when relevant: add name-based fallback matching in
`rewriteHostSymsAsDynimport` for symbols whose `SymPkg` is empty/wrong but whose
name starts with a policy-matched package prefix.

### 5. CFF write-barrier crash in fontsvc.maz (paused)
- **Symptom:** fontsvc.maz crashes during CFF glyph rendering in go-text/typesetting
  after loading the Italic font. Two modes: SIGSEGV at `ensureClosePath` (append),
  or `panic: growslice: len out of range`. Always happens after one full GC cycle.
- **Confirmed not the bug:** library is fine on stock Go 1.26.2; `RegisterMazWriteBarrier`
  IS called; `syncMazWriteBarriers` IS firing (2 transitions/GC); compiled code
  reads the correct `writeBarrier` address; body trampolines are patched correctly;
  P-struct wbBuf offsets are identical between host and .maz.
- **Still suspicious:** (a) timing gap between `setGCPhase` and `syncMazWriteBarriers`
  (on paper correct, not runtime-verified); (b) `[]ot.Segment` GC bitmap after
  `buildCompleteTypemap` type redirect; (c) race between growslice return and
  slice-header store if write barriers don't fire.
- **Paused to pursue different solution:** Plugin-shape mazdl (Phases 2–4) eliminates
  the root class of write-barrier/morestack/typemap bugs by removing runtime code
  from plugins entirely. Once Phase 4 arm64 fully stabilizes this approach can replace
  the .maz model and the CFF investigation becomes moot.
- **If resuming before that:** force `runtime.GC()` before every glyph render in
  fontsvc to isolate the GC-correlation hypothesis; add growslice instrumentation
  in the userspace overlay; verify `[]ot.Segment` type descriptor after typemap merge.
- **State at pause:** `mazarin/overlay/userspace/runtime/maz_moduledata.go` has
  `mazWriteBarrierLastVal` + `mazWriteBarrierSyncCount` instrumentation still in
  place. `config/kernel.arm64.toml` has `go_mem_limit=256` (was 24) — **revert
  to 24 before next boot**.

---

## Decisions Made
| Decision | Rationale |
|----------|-----------|
| TransferPages (not SharePages) for data responses | Read-once data; simpler lifetime |
| Fixed-size KeyHeaderEntry (240 bytes) | Simple decode; no offset table |
| 16-slot LRU collection store | Bounded memory in small shepherd |
| Monotonically increasing CollId | Simple staleness check |
| Sparse array + 128-entry lazy window load | Mailboxes can have 50K+ messages |
| Persistent `count:all` / `count:unread` counters | O(1) totalSize for common filters |
| Per-query fixed collection slots (64×1024) | Eliminates bump-allocator exhaustion |
| dc.Push/DrawRectangle/Clip/Pop for column clipping | Correct Cairo clip vs fragile pixel save |
| mazlink Option A (internal-linker patches, not post-processing) | Direct; no external toolchain dependency |
| `mazlinkNopHostInitTasks` flips inittask state=2 | 4-byte write; no instruction rewriting; cleaner than NOPing init.N bodies |
| No `R_*_COPY` relocations | Single authoritative copy of every host datum |
| No symbol versioning in MVP | Host+plugin built in lockstep; version skew is non-concern within a release |
| Eager binding (not lazy .plt resolver) | Fail at Open time on missing symbol, not at first call |
| riscv64 stays on legacy .maz+maz-reloc path | riscv64 PIE emission in mazlink is Phase 7; legacy path still works |
| One shepherd binary, everything else is a plugin | Collapsed architecture; simpler than per-app shepherd binaries |


---
## NEW INVESTIGATION (2026-04-24): linux/fs delegate consistency bugs

### Triggering observations
- maildb's emlx walker enumerated only **209/317** files in current.mbox tree (one
  Messages dir has 231 entries; many are invisible to readdir).
- 5 walked-but-open-failed files (`open … : no such file or directory` after
  filepath.Walk reported them).
- 3 `lstat … : no such file or directory` walk errors.
- fti's bleve SCORCH backend logs persistent ENOENT on segments it just wrote
  (`open /tmp/fti-N/bleve/store/000000000016.zap: no such file or directory`).

### Hypotheses
- **H1**: linux's `getdents` delegate (or the fs shepherd's ReadDir) only yields
  entries from the first ext2 directory block; large dirs whose entries span
  multiple blocks lose later entries.
- **H2**: A read-after-write inconsistency between linux's path-resolution layer
  (`openat`/`lstat`) and the fs/ext2 backing store — file is on disk but not
  visible by name immediately after creation. May be inode/dirent cache or
  rename-atomicity related.

### Phases
- [ ] **P1: Map the call graph.** Identify which shepherd handles which delegate:
  linux (`maz/linux`) for path syscalls; fs (`maz/fs`) for LoadFile/ReadFilePages.
  Document the read path from `os.Open(path)` in a userspace shepherd all the way
  down to `shared/fs/ext2/reader.go`.
- [ ] **P2: Bug A (directory enumeration).** Inspect linux's getdents/readdir
  delegate. Check whether it walks all blocks of a directory inode or stops at
  the first. Compare with the working `shared/fs/ext2/reader.go::ReadDir` which
  does walk all blocks. Find the gap.
- [ ] **P3: Bug B (write/open ENOENT).** Trace the path bleve takes to write a
  segment file to /tmp (which lives on the linux shepherd's ramdisk, not on
  ext2). Find linux's tmpfs/ramdisk implementation. Identify whether new file
  visibility lags the write, or rename atomicity is broken.
- [ ] **P4: Propose fixes** with test plan for each.

### Decisions / non-goals
- This investigation is read-only first; no patches in this phase.
- Bug A and Bug B *may* share root cause (a single dirent-cache layer that's
  inconsistent) or may be entirely separate (Bug A in fs/ext2 path, Bug B in
  linux's tmpfs). The investigation is open to either outcome.

### Status update (2026-04-24, end of P1+P2+P3-instrumentation)
- [x] **P1: Map the call graph** — done. See findings.md "Architecture (confirmed)".
- [x] **P2: Bug A (directory enumeration)** — **FIXED + VALIDATED in 60s ARM64 HVF run**:
  - Root cause: `maz/linux/syscalls.go::sysGetdents64` advanced `e.offset` by the
    number of dirents fs.maz marshalled (~1500 in 65KB) rather than the number that
    fit in the user's 4KB buffer (~80). Dropped the difference silently.
  - Fix: new `deliveredDirents(src, maxBytes)` helper walks the linux_dirent64
    records in the truncated buffer and counts how many fully fit; offset advances
    by that count instead. Diagnostic line emitted on every truncated call.
  - Empirical result: emlx walker now sees **309/317** files (was 209). 306 parsed
    cleanly. 83 truncation reports fired, all in expected dirs.
- [x] **P3-instrumentation: Bug B trace plumbing** — done in `maz/fs/fsipc.go`:
  - `fsHandle` gained a `path` field so handle-based ops know the file name.
  - `tmpTrace`/`isTmpPath` helpers emit `[fs:tmp] OP path=… …` lines for any
    operation under `/tmp/`.
  - **Two iterations to get the trace right:**
    1. First attempt used `fmt.Printf` → DEADLOCK (linux ↔ fs.maz IPC cycle).
       Fixed by switching to `sys.UartWriteString` (direct UART, no IPC).
    2. Second attempt traced every successful WRITE → **86,804 traces / ~13 MB
       to slow polled UART** → multi-second pauses on every body-fetch. Fixed
       by removing the successful-WRITE trace; kept WRITE FAIL.
- [ ] **P3: Bug B (write/open ENOENT)** — partial: ruled out concurrency and ext2-side
  dir block enumeration. Trace data so far:
  - 5 OPEN FAIL events captured. **3 are benign** (bleve/badger probing for
    optional metadata files, `MANIFEST`, `KEYREGISTRY`, etc.).
  - **2 are real Bug B**: `0000000000a2.zap` and `0000000000b4.zap` — bleve created
    these segments earlier in the same run but the merger/persister can't open
    them later. Same symptom as the 8 emlx walk-errors and 3 emlx skips, so Bug B
    is broader than just bleve.
  - Next: re-run, find the OPEN+CREAT for the failing zap files in the trace,
    walk forward through every subsequent op (RENAME, REMOVE on neighboring
    files in the same dir) until the lookup fails. Hypothesis to confirm or
    reject: ext2 `removeDirEntry` (writer.go) re-coalesces slack in a way that
    invalidates a sibling entry's reclen.
- [ ] **P4: Propose fixes** — Bug A fix landed. Bug B fix design pending more
  trace data on the create-then-fail sequence.

---

## CURRENT WORK: Diversion #8 — Fstatat/sysid=44 intermittent hang

### Status: Step 1 instrumentation complete; hunting for first hang run

### Background
Pre-existing intermittent hang (~1-in-5 × 180s ARM64 HVF runs). Confirmed
pre-existing by bisect: appears on both sides of the b9fd57f boundary.

Symptom: kernel epoch dump shows `delegate stuck: tid=N/sid=N/sysid=44/for=Ns`
with multiple threads piling up (escalating wait times: ~25s → 37s → 57s).

### Instrumentation added (2026-04-26)
Three files patched — no functional change, pure diagnostics:
1. `maz/linux/syscalls.go::sysFstatat` — entry/exit per call with seq
2. `mazarin/fsclient/client.go::callLocked` — "stat id=N sent; RespCh len=N" after
   uring.Send succeeds but before `<-c.RespCh` (gated on FSOpStat)
3. `maz/linux/main.go` — startup cap log + 5s chan-monitor goroutine for wmCh/fontReplyCh

### Runs so far (3 × 180s ARM64 HVF)
- Run 1: STABLE. wmCh=0/8 fontReplyCh=0/8 throughout (34 samples).
- Run 2: STABLE. No non-empty delegate-stuck.
- Run 3: Short log (QEMU slow start? log only 7.8K).

### What to do when a hang fires
Look for:
1. `[fstatat] seq=N enter` without matching `done` → stuck in Stat/callLocked
2. `[fsclient] stat id=N sent` present → stuck at `<-c.RespCh`
3. `chan-monitor: wmCh N/8 ...` near capacity → dispatcher deadlock confirmed
4. Both channels 0 → secondary hypothesis (fs.maz not responding)

### Hypotheses (ranked)
- **Primary**: ipcDispatcher (ring 0) blocks on `tempWMCh <- typed` or
  `tempFontReplyCh <- typed` when wmCh/fontReplyCh fill up. This prevents ring 0
  from routing ProtoFSIPCResp to `fsClient.RespCh`. callLocked blocks forever.
  Channel capacities: tempWMCh=8, wmCh=8, tempFontReplyCh=8, fontReplyCh=8.
  All sends are blocking (confirmed in `mazarin/uring/reader.go:198`).
- **Secondary**: fs.maz is not processing the FSOpStat request (stuck goroutine
  in fs.maz). Would show as "stat id=N sent" present but wmCh/fontReplyCh near 0.

### Fix (pending confirmation; architectural — needs user discussion)
If primary confirmed: separate FSIPCResp onto its own dedicated ring so WM/font
backpressure can never block the fs-response path.
