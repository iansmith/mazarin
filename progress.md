# Progress Log

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
