# Task Plan — Mazarin / Mazzy

## TOP OF STACK: mail-dumb easy part (resumed 2026-04-24)

Back on mail-dumb easy part after a diversion to fix the fs↔linux delegate
deadlock (see progress.md "Session 2026-04-24" and findings.md
"Fs ↔ Linux Delegate Handler Deadlock"). The deadlock fix — two-lane
delegate handler (stdout vs. file lane) in the linux shepherd — is
complete, built clean on ARM64 / x86_64, stable in a 60s HVF boot, and
`fmt.Printf` from fs handlers is now visible on the linux console.

Next: mail-dumb easy part — body display, PageUp/PageDown, mark-read,
delete, and polish. Status from before the diversion follows.

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
