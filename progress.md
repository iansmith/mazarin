# Progress Log

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
