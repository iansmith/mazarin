# Progress Log

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
