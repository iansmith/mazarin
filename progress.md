# Progress Log — Mail Row Interactor + Maildb Collection Protocol

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
