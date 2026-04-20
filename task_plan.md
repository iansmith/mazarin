# Task Plan: Mail Row Interactor + Maildb Collection Protocol
## STATUS: ALL PHASES COMPLETE — 2026-04-20

System verified: ARM64 HVF 90s stable, mail app renders 50 rows with correct column
clipping, 100 docs indexed by fti, Go 1.26.2, all builds clean.

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
history (commits `706820f` through `2a4a092`) for per-phase detail.

### Diagnostic Cleanup
- Removed all `fmt.Printf` diagnostic traces added during debugging:
  `app_window.go`, `column_percentage.go`, `grid_table.go`, `margin_parent.go`,
  `apps/mail/main.go` (redraw counter + forced-damage workaround).

---

## Known Bugs / Issues (open at close of this plan)

### 1. fti: bleve AnalysisWorker goroutine panic (intermittent)
- **Symptom:** `AnalysisWorker` goroutine in `bleve_index_api` panics; fti marks
  index as `corrupted` and drops subsequent documents with a logged error. The last
  document in the batch may not be indexed.
- **Root cause:** `recover()` in `handleIndexDocument` cannot catch panics in
  goroutines spawned internally by bleve. The analysis worker queue runs on its
  own goroutines.
- **Impact:** fti indexing degrades gracefully (documents are stored in badger;
  only full-text search is affected). The shepherd does not crash — the `corrupted`
  flag prevents further bleve calls.
- **Next step:** Wrap bleve's `NewAnalysisQueue` worker pool or switch to
  synchronous analysis (disable async worker queue) to avoid cross-goroutine panics.
  Alternatively, catch at bleve call site and recreate the index.

### 2. VirtIO block: intermittent stall on large file reads
- **Symptom:** fs shepherd logs `[fs] reading /fti.elf...` (18.5 MB) and then
  produces no further output for the remainder of the run. The block device IRQ
  apparently never fires for one or more DMA transfers.
- **Frequency:** ~1 in 3 cold runs observed.
- **Root cause:** Unknown. DMA scratch is 8 pages (32 KB); 18.5 MB requires ~592
  sequential transfers. An interrupt miss under HVF scheduling stalls the whole
  read permanently — there is no timeout or retry in the fs read path.
- **Next step:** Add a watchdog timer in the fs DMA read loop. Investigate whether
  the block IRQ edge-trigger is being lost under HVF when many back-to-back
  transfers are queued.

### 3. GridTable: no RemoveRow
- `CollectionRemove` notifications are tracked in the mail app's in-memory list
  but the visual grid is not updated. `GridTable` lacks a `RemoveRow` method.
- **Next step:** implement `GridTable.RemoveRow(idx int)` when mail needs it.

### 4. Cosmetic: kmem log still prints "Constraint pages v2"
- `logConstraintPagesInit` in `kmazarin/kmem/constraint.go:167` hardcodes `"v2"`
  but the actual version written is now 3.
- Trivial one-line fix; not worth a standalone commit.

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
