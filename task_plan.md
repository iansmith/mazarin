# Task Plan: Mail Row Interactor + Maildb Collection Protocol

## Goal
Build a `MailRow` interactor for the Mail app backed by a new uring-based maildb protocol.
The protocol uses **HOT collections** — named, ordered result sets that maildb pushes
unsolicited Add/Remove notifications into as the live message set changes.  Every message
reference is `(collectionId, msgNumber)`.  Data-returning requests (KeyHeaders, AllHeaders,
Body) transfer page ownership to the caller via `TransferPages`.

## Rules & Discipline
Re-read before any coding session:
- `/Users/iansmith/mazzy/CLAUDE.md` — build via Taskfile only, serial log safety, env vars
- `/Users/iansmith/.claude/projects/-Users-iansmith-mazzy/memory/MEMORY.md` — auto-memory

## Current Phase
Phase 2 — MessageStore + Collection infrastructure (complete 2026-04-20)

## Code Locations
- **maildb**:                  `maz/maildb/`
- **new protocol package**:    `shared/mailproto/` (to create)
- **mail app**:                `mazarin/apps/mail/main.go`
- **grid table**:              `mazarin/mancini/std/grid_table.go`
- **mail row interactor**:     `mazarin/apps/mail/mail_row.go` (to create)
- **page transfer userspace**: `mazarin/sys/sharedmem.go`

## Phases

### Phase 0: Protocol Design — IN PROGRESS
- [x] Drafted request/response wire format (fits in 108 bytes after MsgType)
- [x] Added RequestId [16]byte (raw UUID) to every message
- [x] Defined HOT collections: CollectionAdd / CollectionRemove unsolicited notifications
- [x] Defined multi-client semantics: RequestId may come from a different client
- [x] Defined MessageStore data structure (lazy-fetch map keyed by messageId)
- [x] Defined Collection struct (eager msgIds[] index, subscribers, msgIdToNum reverse map)
- [x] Defined notification flow for MarkDeleted (DeletionNotice → CollectionRemove fan-out)
- [x] Decided: remove old GetHeaders/GetBody/BodyConfirm; extend ProtoMailReq/ProtoMailResp
- [x] Defined read/deleted persistence: `read:<msgId>` / `deleted:<msgId>` keys in badger
- [x] User approved all decisions (2026-04-20)
- **Status:** complete

### Phase 1: Wire protocol packages — COMPLETE
Two packages: new `shared/mailproto/` and additions to existing `shared/fti/protocol.go`.

**shared/mailproto/** (new — imported by maildb and mail app):
- [x] Package skeleton (no build tags needed — follows same pattern as shared/fti, shared/mail)
- [x] Error code constants (ErrNone … ErrFilterInvalid)
- [x] Filter type constants (FilterAll, FilterUnread, FilterFrom, FilterSubject)
- [x] SortOrder constants (SortDesc, SortAsc)
- [x] Request structs + encode functions (MsgType 10–17)
- [x] Response structs + decode functions (MsgType 50–57)
- [x] Unsolicited notification structs + decode functions (MsgType 60–61)
- [x] KeyHeaderEntry (240 bytes) and AllHeaderEntry (1232 bytes) page layout structs
- [x] Pack*/Unpack* helpers for all major types

**shared/fti/protocol.go** (extend existing):
- [x] MsgTypeSearchMail=2, MsgTypeSearchResult=20, MsgTypeSearchError=21
- [x] SearchMail struct + EncodeSearchMail (maildb → fti)
- [x] SearchResult + SearchError structs + encode functions (fti → maildb)
- [x] SearchResultEntry page layout struct (88 bytes, 46 per page)
- [x] SearchMail added to DecodeFTIReq; SearchResult/SearchError added to DecodeFTIResp
- [x] Build-check: `go build mazzy/shared/mailproto` and `go build mazzy/shared/fti` both clean
- **Status:** complete

### Phase 2: Maildb — MessageStore + Collection Infrastructure — COMPLETE
Core data structures in maildb.  No uring handlers yet; unit-testable in isolation.
- [x] `maz/maildb/msgstore.go`: MessageStore, MessageRecord (pure data cache)
  - Ensure, LoadHeaders, LoadFlags, LoadBody, MarkRead, MarkDeleted, Evict
- [x] `maz/maildb/counter.go`: readCounter, setCounter, adjustCounterTxn, initCounters
- [x] `maz/maildb/collection.go`: collection struct (sparse array), collectionStore (16-slot LRU)
  - createCollection: O(1) from count:all/count:unread; from/subject return 0 (Phase 3 fti TODO)
  - loadWindowAll/loadWindowUnread: key-only badger scan; uses dateKeyPrefixLen=36 fix
  - lookupCollection: returns errCollectionExpired if collId not in live set
  - evictLRULocked: evicts MessageStore records no longer in any collection
  - removeMessage: returns []collectionNotify for caller to send (no uring calls here)
- [x] mbox_import.go: initCounters(db, count, count) after wb.Flush (all imported = unread)
- [x] main.go: newMessageStore/newCollectionStore after import, handleList uses new infra
- [x] Build check: `task maildb:arm64` — ET_EXEC and .maz both built successfully
- **Status:** complete
- **Key fix:** dateKey parsing: timestamp is exactly 30 chars → msgId starts at offset 36;
  existing SplitN(key, ":", 3) was silently broken (included timestamp colons in msgId)

### Phase 3: Maildb — Uring Handlers — PENDING
Implement all request handlers and send unsolicited notifications.
Remove old GetHeaders/GetBody/BodyConfirm handlers.
- [ ] Delete old MsgTypeGetHeaders(1), MsgTypeGetBody(2), MsgTypeBodyConfirm(3) handlers
- [ ] `handleCreateCollection` → build collection, return RespCreateCollection
- [ ] `handleMessageCount` → return total message count in DB (not collection-scoped)
- [ ] `handleKeyHeaders(collId, from, to)` → EnsureReified+LoadHeaders for range,
  pack KeyHeaderEntry[] into pages, TransferPages → RespKeyHeaders
- [ ] `handleAllHeaders(collId, msgNum)` → load full headers, pack AllHeaderEntry,
  TransferPages → RespAllHeaders
- [ ] `handleLatestUnread` → scan for first unread in reverse-chron order → RespLatestUnread
  (creates an ephemeral collection or uses existing FilterUnread collection if present)
- [ ] `handleBody(collId, msgNum)` → load body pages, TransferPages → RespBody
- [ ] `handleMarkRead(collId, msgNum)` → MarkRead in store → RespMarkRead
- [ ] `handleMarkDeleted(collId, msgNum)` → MarkDeleted in store → RespMarkDeleted +
  fan-out CollectionRemove to all affected collection subscribers
- [ ] Update uring Dispatcher registration in maildb main.go
- [ ] `maz/fti/search_handler.go`: handle SearchMail — run bleve query, TransferPages results to maildb
- [ ] Register SearchMail handler in fti Dispatcher (fti main.go)
- **Status:** pending

### Phase 4: Mail Row Interactor — PENDING
New `MailRow` type: `mazarin/apps/mail/mail_row.go`
- [ ] MailRow struct (collId, msgNum, state, cached KeyHeaderEntry)
- [ ] Constructor: fire KeyHeaders(collId, msgNum, msgNum) immediately
- [ ] Response handler: unpack page → KeyHeaderEntry[0], free pages, → Loaded state
- [ ] ErrCollectionExpired handler: call onCollectionExpired callback
- [ ] Implements std.GridRow (Sender/Subject/Date strings, placeholders while Loading)
- [ ] Render: show loading indicator / placeholder row when in Pending/Loading state
- [ ] Click handler: fire onRowSelected(collId, msgNum)
- **Status:** pending

### Phase 5: Mail App Integration — PENDING
Wire new protocol into `mazarin/apps/mail/main.go`.
- [ ] Register CollectionAdd / CollectionRemove notification handlers in uring Dispatcher
- [ ] On startup: CreateCollection(FilterAll) → collId, size
- [ ] Populate GridTable with MailRow for each of first 50 messages (0..min(size-1,49))
- [ ] Handle CollectionRemove: find row by MsgId, remove from grid, renumber
- [ ] Handle CollectionAdd: insert new MailRow at correct position
- [ ] Handle onCollectionExpired: re-create collection, rebuild all rows
- [ ] Remove old requestInitialHeaders(), testRow, and HeaderEntry/HeadersEnd/BodyResult handlers
- [ ] Verify end-to-end in QEMU (ARM64 HVF)
- **Status:** pending

## Open Questions (resolved)
- Extend ProtoMailReq/ProtoMailResp? **Yes** ✓
- Read/unread stored? **Not yet; adding read:/deleted: keys** ✓
- MarkDeleted behavior? **Removes from collection immediately; unsolicited fan-out** ✓
- MailRow granularity? **One KeyHeaders request per row; batched in handler** ✓
- Remove GetBody/BodyConfirm? **Yes, both deleted** ✓
- RequestId format? **[16]byte raw UUID in wire; display as hyphenated string** ✓
- Multi-client? **Yes; clients MUST NOT assume RequestId is their own** ✓

## Decisions Made
| Decision | Rationale |
|----------|-----------|
| TransferPages (not SharePages) for data responses | Read-once data; simpler lifetime |
| Fixed-size KeyHeaderEntry (240 bytes) | Simple decode; no offset table |
| 16-slot LRU collection store | Bounded memory in small shepherd |
| Monotonically increasing CollId | Simple staleness check |
| Sparse array + 128-entry lazy window load | Mailboxes can have 50K+ messages; eager full index would be slow and blow memory |
| Persistent `count:all` / `count:unread` counters | BadgerDB has no O(1) prefix count; counters give O(1) totalSize for common filters |
| Reverse lookup = iterate 16 collection slots | No separate reverse index needed; 16 map lookups is O(1) and correct by construction |
| MessageStore = pure data cache (no membership tracking) | Membership derived on-demand from collection.msgIdToNum; simpler ownership |
| SortOrder field in CreateCollReq | Sort direction is a first-class collection property; inbox = FilterAll+SortDesc |
| [16]byte RequestId on wire | Compact; display as UUID string when needed |
| Zero UUID = no originating request | Covers external-source unsolicited notifications |
| CollectionAdd only sends msgNum+newSize | Client issues KeyHeaders to fetch data; keeps notification message small |
| CollectionRemove includes MsgId[64] | Client can locate row without valid msgNum mapping |

## Errors Encountered
| Error | Attempt | Resolution |
|-------|---------|------------|
| (none yet) | | |
