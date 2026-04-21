# Task Plan — Mazarin / Mazzy
## STATUS: 2026-04-20

Mail work: ALL PHASES COMPLETE.
mazdl/mazlink: Phases 0–4 arm64 COMPLETE; Phase 4 amd64 open.
CFF write-barrier investigation: PAUSED (different solution in progress).
ARM64 HVF 90s stable, mail app renders 50 rows with correct column
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

## Known Bugs / Open Issues

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

### 4. mazdl Phase 4: amd64 parity needed
- **What's done:** mazlink Option A is present in `amd64/asm.go` (mirrors arm64
  block). `smoke/host-mazdl` compiles for amd64.
- **What's missing:**
  - `mazarin/mazdl/reloc_amd64.go` — apply `R_X86_64_{RELATIVE,GLOB_DAT,JUMP_SLOT,64}`
  - Container arch toggle in `mazlink-smoke` Taskfile task so the x86_64 image
    runs on an arm64 host (smoke Dockerfile already cross-builds both arches;
    the task only runs the host-matching arch today).
- **Exit criterion:** exits #1–#4 pass on amd64:
  1. `mazdl.Open("plugin.maz")` succeeds, `h.Sym("Hello")` returns callable fn
  2. `runtime.Stack` shows ≤1 each of forcegchelper, bgsweep, bgscavenge, runfinq
  3. Plugin allocations visible in host `runtime.memstats`
  4. 1000-iteration `Stress()` test clean, no panics or races

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
