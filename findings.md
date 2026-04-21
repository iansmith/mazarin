# Findings

## Existing Protocol (to be removed)

**Protocol IDs:** ProtoMailReq=13, ProtoMailResp=14  
Old message types **MsgTypeGetHeaders(1), MsgTypeGetBody(2), MsgTypeBodyConfirm(3)** are
being deleted.  GetBody/BodyConfirm were not properly designed; KeyHeaders replaces
GetHeaders.  Remove all three handlers from maildb and remove their encode/decode helpers.

**Badger key schema (existing):**
- `<messageId>` → JSON MailMessage (From, Sender, Subject, Timestamp, BodyLen)
- `body:<messageId>` → raw body bytes
- `date:<RFC3339>:<messageId>` → empty (date index, reverse-chron iteration)

**Missing (must add):**
- `read:<messageId>` → empty key presence = IsRead
- `deleted:<messageId>` → empty key presence = IsDeleted

---

## Wire Format Constraints

- `UringIPCMsg.Payload` = 112 bytes
- First 4 bytes = MsgType (uint32); remaining 108 bytes for RequestId + fields
- **RequestId** = [16]byte (raw UUID bytes; display as hyphenated UUID string when needed)
  - All messages carry RequestId — unsolicited notifications use zero UUID when there is
    no originating client request (e.g. new mail arriving from external source)
- After MsgType(4) + RequestId(16) = 20 bytes overhead, **88 bytes remain** for fields

---

## Proposed Protocol Design (approved by user 2026-04-20)

### Protocol IDs
Extend existing **ProtoMailReq=13** (mail→maildb) / **ProtoMailResp=14** (maildb→mail).
New MsgType values added; old 1/2/3 removed.

### Error Codes
```
ErrNone              = 0
ErrCollectionExpired = 1   // collId not in live set; client must CreateCollection again
ErrInvalidMsgNumber  = 2   // msgNum out of range for collection
ErrMessageNotFound   = 3   // messageId not in badger
ErrBadgerError       = 4   // internal DB error
ErrFilterInvalid     = 5   // unknown filter type or malformed FilterArg
```

### Request Messages (mail → maildb)
All requests: MsgType(4) + RequestId[16] + fields.

```
MsgTypeMessageCount     = 10   // no extra fields                                   (20 bytes)
MsgTypeKeyHeaders       = 11   // CollId uint32, From uint32, To uint32             (32 bytes)
MsgTypeAllHeaders       = 12   // CollId uint32, MsgNum uint32                      (28 bytes)
MsgTypeLatestUnread     = 13   // no extra fields                                   (20 bytes)
MsgTypeBody             = 14   // CollId uint32, MsgNum uint32                      (28 bytes)
MsgTypeMarkRead         = 15   // CollId uint32, MsgNum uint32                      (28 bytes)
MsgTypeMarkDeleted      = 16   // CollId uint32, MsgNum uint32                      (28 bytes)
MsgTypeCreateCollection = 17   // FilterType uint32, SortOrder uint32, FilterArg [64]byte  (92 bytes ≤ 108 ✓)
```

**SortOrder values:**
```
SortDesc = 0   // newest-first (default inbox order); badger Reverse:true iterator
SortAsc  = 1   // oldest-first; forward badger iterator
```
`FilterAll + SortDesc` = the inbox. Its `totalSize` comes from `count:all` — no key scan.

### Response Messages (maildb → mail, in reply to a request)
All responses echo the RequestId of the originating request.

```
MsgTypeRespMessageCount     = 50   // Count uint64, ErrCode uint32                  (32 bytes)
MsgTypeRespKeyHeaders       = 51   // TargetVA uint64, NumBytes uint32, Count uint32, ErrCode uint32  (40 bytes)
MsgTypeRespAllHeaders       = 52   // TargetVA uint64, NumBytes uint32, ErrCode uint32               (36 bytes)
MsgTypeRespLatestUnread     = 53   // CollId uint32, MsgNum uint32, TargetVA uint64, NumBytes uint32, ErrCode uint32  (44 bytes)
MsgTypeRespBody             = 54   // TargetVA uint64, NumBytes uint32, ErrCode uint32               (36 bytes)
MsgTypeRespMarkRead         = 55   // ErrCode uint32                                (24 bytes)
MsgTypeRespMarkDeleted      = 56   // ErrCode uint32, NewSize uint32                (28 bytes)
MsgTypeRespCreateCollection = 57   // CollId uint32, Size uint32, ErrCode uint32    (32 bytes)
```

### Unsolicited Notification Messages (maildb → mail, pushed without a request)
RequestId = originating client's request UUID if the change came from a client operation;
zero UUID if the change arrived from an external source (e.g. new mail delivery).
**Clients MUST NOT assume the RequestId matches one of their own outstanding requests.**

```
MsgTypeCollectionAdd    = 60   // CollId uint32, MsgNum uint32, NewSize uint32, RequestId [16]byte  (28 bytes + type = 32)
MsgTypeCollectionRemove = 61   // CollId uint32, MsgNum uint32, NewSize uint32, MsgId [64]byte, RequestId [16]byte  (96 bytes + type = 100 ≤ 108 ✓)
```

- **CollectionAdd**: new message arrived in this collection (new mail or filter match).
  Client should issue KeyHeaders(collId, MsgNum, MsgNum) to fetch header data.
- **CollectionRemove**: message was deleted from this collection (by any client or expiry).
  Includes MsgId so client can find and remove the row even with stale msgNum mapping.
  After receiving this, client must renumber its local row-to-msgNum mapping.

### Page Layouts (for data-returning responses)

**KeyHeaderEntry** (fixed-size; one per message in [From, To]):
```go
type KeyHeaderEntry struct {
    Sender  [64]byte   // null-terminated UTF-8
    Subject [128]byte  // null-terminated UTF-8
    Date    [32]byte   // RFC3339 null-terminated
    MsgNum  uint32     // message number within collection
    Flags   uint32     // bit 0 = IsRead, bit 1 = IsDeleted
    _pad    [8]byte
}
// sizeof = 240 bytes
// 50 entries = 12,000 bytes = 3 pages
```

**AllHeaderEntry** (fixed-size; one message full RFC headers; fits in 1 page):
```go
type AllHeaderEntry struct {
    From        [128]byte
    To          [256]byte
    CC          [256]byte
    Subject     [256]byte
    Date        [64]byte
    MessageId   [128]byte
    ContentType [128]byte
    MsgNum      uint32
    Flags       uint32
    _pad        [8]byte
}
// sizeof = 1,232 bytes < 4096 → 1 page
```

**Body** — raw bytes of stored message body (UTF-8 / quoted-printable / base64 as-is).
Caller decodes MIME if needed.

### Filter Types

```
FilterAll       = 0   // all messages; FilterArg unused; count from count:all
FilterUnread    = 1   // unread messages only; FilterArg unused; count from count:unread
FilterFrom      = 2   // sender contains substring; FilterArg = query bytes; count+hits via fti
FilterSubject   = 3   // subject contains substring; FilterArg = query bytes; count+hits via fti
```
SortOrder applies to all filter types.

FilterFrom and FilterSubject route through the `fti` shepherd:
- `createCollection`: maildb sends SearchMail to fti with `Size=0` → `Total` = totalSize (O(hits) in bleve, no doc retrieval)
- `loadWindow(from, to)`: maildb sends SearchMail to fti with `From=from, Size=128, SortBy=date` → page of messageIds → populate collection.entries

---

## fti Search Protocol (additions to shared/fti/protocol.go)

The existing fti protocol only has indexing (MsgTypeIndexDocument=1).  We add search.
New message types follow the same encode/decode pattern already in place.

### New MsgType constants
```
MsgTypeSearchMail   = 2   // maildb → fti: search request
MsgTypeSearchResult = 20  // fti → maildb: results (page transfer) or count-only
MsgTypeSearchError  = 21  // fti → maildb: search failed
```

### SearchMail request (maildb → fti)
MsgType(4) + RequestId[16] + fields, all ≤ 108 bytes:
```go
type SearchMail struct {
    RequestId  [16]byte  // correlation ID; echoed in response
    QueryType  uint32    // 1=Subject, 2=From (matches FilterSubject/FilterFrom)
    SortOrder  uint32    // SortDesc=0, SortAsc=1
    From       uint32    // offset into result set (for pagination)
    Size       uint32    // hits to return; 0 = count only (no page allocation)
    QueryLen   uint16
    Query      [58]byte  // search term, null-terminated
}
// 16+4+4+4+4+2+58 = 92 bytes + 4 (MsgType) = 96 ≤ 108 ✓
```

### SearchResult response (fti → maildb)
```go
type SearchResult struct {
    RequestId [16]byte  // echoed from request
    TargetVA  uint64    // VA of transferred page in maildb's address space (0 if Size=0)
    NumBytes  uint32    // bytes in page
    Count     uint32    // hits returned in this page
    Total     uint32    // total hits in result set (always set, even for Size=0)
    ErrCode   uint32
}
// 16+8+4+4+4+4 = 40 bytes + 4 (MsgType) = 44 ≤ 108 ✓
```

### SearchResultEntry page layout
Page contains a packed array of `SearchResultEntry`:
```go
type SearchResultEntry struct {
    IdLen uint16
    _pad  [6]byte
    DocId [80]byte   // message ID, matching existing DocId field size in IndexDocument
}
// sizeof = 88 bytes; 46 entries per 4096-byte page; 128 entries = 3 pages
```
fti allocates pages, writes entries, calls `TransferPages` to maildb.
maildb reads entries, populates `collection.entries[from..from+count-1]`, frees pages.

### fti search handler
New `search_handler.go` in `maz/fti/`:
- Receives SearchMail, runs `bleve.SearchRequest{Query, From, Size, SortBy:date}` 
- `Size=0`: return SearchResult with Total set, TargetVA=0, no page allocation
- `Size>0`: allocate pages, pack SearchResultEntry array, TransferPages to requester

---

## BadgerDB Count Capability (v4.9.1) — Confirmed

BadgerDB has **no O(1) count for a key prefix**.  Counting requires iteration.

- `PrefetchValues=false` key-only iteration exists and is fast; maildb already uses it
- `DB.Tables()` has per-table `KeyCount uint32` but not prefix-scoped (partial tables skew it)
- `DB.EstimateSize(prefix)` returns byte sizes only, and misses partial tables
- No `Count()`, `Len()`, or equivalent on `DB` or `Txn`

**Consequence for collection size:** cannot compute a count without an O(n) key scan.

**Solution — persistent counters in badger:**
- `count:all` → little-endian uint64 — maintained on every message import and delete
- `count:unread` → little-endian uint64 — maintained on MarkRead and message import
- These give O(1) totalSize for `FilterAll` and `FilterUnread`
- Ad-hoc filters (`FilterFrom`, `FilterSubject`) still require a key scan at CreateCollection;
  accepted cost since those are user-initiated and infrequent

---

## Collection Design

### Semantics
- maildb holds at most **16 live collections** (fixed-size LRU pool)
- `CollId` is monotonically increasing uint32 starting at 1; 0 = invalid
- LRU eviction: creating the 17th collection evicts the least-recently-used
- `lastUsed` is updated on every request touching a collection
- Looking up a stale CollId → ErrCollectionExpired; client must CreateCollection again
- Collections are **HOT**: maildb pushes CollectionAdd / CollectionRemove unsolicited
  whenever the live set of messages changes for any entry currently loaded in that collection

### Collection is a Sparse Array
A collection is a **sparse array** of msgNum → messageId.  Only entries that have been
explicitly loaded (via a KeyHeaders or similar request) are held in memory.  The rest are
not loaded.  `totalSize` is always known (from the persistent counter or a key scan), but
the individual entries are populated on demand.

```go
type collection struct {
    id          uint32
    filterType  uint32
    sortOrder   uint32        // SortDesc=0, SortAsc=1
    filterArg   [64]byte
    totalSize   int           // from persistent counter (O(1)) or key scan (ad-hoc filters)
    lastUsed    time.Time
    subscribers []int16       // SIDs to notify on Add/Remove

    // Sparse: only loaded entries are present (≤128 entries in a typical window)
    entries    map[uint32]string   // msgNum → messageId
    msgIdToNum map[string]uint32   // reverse: messageId → msgNum (for O(1) removal/notification)
}
```

### Window Loading (lazy, 128-entry cap per load)
When `KeyHeaders(collId, from, to)` arrives and entries [from, to] are not in `entries`:

1. Open a key-only iterator (`PrefetchValues=false`) on the `date:` prefix
2. If `filter == FilterUnread`: iterate only `date:` keys for which `read:<msgId>` does NOT
   exist (requires two lookups per key — acceptable; optimize with bloom filter later)
3. Skip the first `from` matching keys (O(from) key reads, no value loads)
4. Collect the next `to-from+1` messageIds (capped at 128 per load)
5. Store in `entries[from..to]`, store reverse in `msgIdToNum`
6. EnsureReified each loaded messageId in the MessageStore (membership tracking)

**Cost for typical usage:** loading first page (from=0, to=49) costs 0 skips + 50 key reads.
Loading page 2 (from=50, to=99) costs 50 skips + 50 key reads.  For message 10,000:
~10K key reads at ~60 bytes each ≈ 600KB read, likely < 10ms on the ramdisk.

**No cursor persistence:** badger iterators are transaction-scoped and cannot be persisted
between requests.  Each window load opens a fresh read-only transaction.

### Subscription
Each collection has a `subscribers []int16` list.  Today, one subscriber per collection
(the SID that called CreateCollection).  The list supports future multi-subscriber scenarios
(e.g. two mail-ui.maz instances sharing a collection).

---

## Reverse Lookup: Which Collections Contain a MessageId?

**Answer: iterate the 16-slot collection pool and check each `coll.msgIdToNum[msgId]`.**

With a hard cap of 16 live collections, this is 16 map lookups — effectively O(1).
No separate reverse index is needed.  This is correct by construction: if a messageId
is not in `coll.msgIdToNum`, it simply isn't loaded in that collection.

This means **`MessageRecord.memberships` is dropped** from the MessageStore.
The MessageStore becomes a pure **lazy data cache** — no membership tracking.

Notification fan-out for a MarkDeleted or external add:
```
for each coll in collectionPool.slots:
    if msgNum, ok := coll.msgIdToNum[msgId]; ok:
        // this collection has the message loaded — notify its subscribers
        coll.entries[msgNum] = ""  // tombstone
        delete(coll.msgIdToNum, msgId)
        coll.totalSize--
        send CollectionRemove{coll.id, msgNum, coll.totalSize, msgId, reqId}
          to each sid in coll.subscribers
```

---

## MessageStore Data Structure

The MessageStore is a **lazy data cache** keyed by messageId.  It deduplts badger reads
when the same message is loaded across multiple collections.

```go
type MessageRecord struct {
    mu        sync.Mutex
    messageId string

    // Lazy fields — nil = not yet fetched from badger
    headers   *MailMessage
    body      []byte
    isRead    *bool    // nil = unknown
    isDeleted *bool
}

type MessageStore struct {
    mu      sync.RWMutex
    records map[string]*MessageRecord  // messageId → record
}
```

### MessageStore Operations
- `Ensure(msgId)` — create record if absent (no data loaded yet)
- `LoadHeaders(msgId) *MailMessage` — lazy fetch from badger `<msgId>` key
- `LoadFlags(msgId) (isRead, isDeleted bool)` — lazy fetch `read:<msgId>` / `deleted:<msgId>`
- `LoadBody(msgId) []byte` — lazy fetch `body:<msgId>`
- `MarkRead(msgId)` — set isRead=true, write `read:<msgId>` to badger
- `MarkDeleted(msgId)` — set isDeleted=true, write `deleted:<msgId>` to badger;
  caller is responsible for iterating collections and sending notifications
- `Evict(msgId)` — remove record from cache; called after all collections evict it

---

## Notification Flow: MarkDeleted Example

1. mail app sends MarkDeleted{CollId=3, MsgNum=7, RequestId=R1}
2. maildb handler:
   a. Look up collection 3 → confirm MsgNum=7 is loaded → get messageId "abc@example.com-1234"
   b. Call `store.MarkDeleted("abc@example.com-1234")` → persists to badger
   c. Iterate all 16 collection slots: for each coll where `coll.msgIdToNum[msgId]` exists:
      - tombstone coll.entries[msgNum], remove from coll.msgIdToNum, decrement coll.totalSize
      - send CollectionRemove{coll.id, msgNum, coll.totalSize, msgId, R1}
        to every sid in coll.subscribers
        (this includes other clients that have the same message loaded)
   d. send RespMarkDeleted{ErrCode=0, NewSize=N-1} back to the requesting client
3. Requesting client gets RespMarkDeleted first (direct response), then CollectionRemove
   (fan-out notification, may arrive in any order relative to other clients' notifications)

---

## Read/Deleted Persistence in Badger

New keys to add:
- `read:<messageId>` → empty value; key presence = message is read
- `deleted:<messageId>` → empty value; key presence = message is deleted

`LoadFlags` checks for key existence with `db.View` + `txn.Get`.
`MarkRead` / `MarkDeleted` write the key in a `db.Update` transaction.

---

## Uring Infrastructure Notes

- `mazarin/uring/reader.go`: `Dispatcher.On(protocol, decodeFn, channel)` — typed dispatch
- `mazarin/uring/syscall.go`: `Send(targetSID, msg)` — non-blocking send
- Pages for data responses: `mem.AllocPagesSlice(n, mem.PageShared)` in maildb
- After `TransferPages`, maildb MUST NOT access the transferred VA
- Caller (mail app) calls `mem.FreePages(va, numPages)` after consuming data

---

## MailRow Interactor Design

- `MailRow` wraps `ColumnPercentage` (consistent with other grid rows)
- State machine: **Pending → Loading → Loaded | CollectionExpired | Error**
- On construction: fire KeyHeaders(collId, msgNum, msgNum); transition to Loading
- On response: unpack KeyHeaderEntry[0] from transferred page, free pages, → Loaded
- On ErrCollectionExpired: invoke `onCollectionExpired(collId)` callback on parent;
  parent re-creates collection and rebuilds all rows
- On CollectionRemove notification: parent removes row from GridTable, renumbers rows
- On CollectionAdd notification: parent inserts new MailRow at correct position
- Implements `std.GridRow` (Sender/Subject/Date); returns "…" placeholder while Loading
- Click → fire `onRowSelected(collId, msgNum)` event

---

## Key File Paths

| What | Path |
|------|------|
| New protocol package | `shared/mailproto/` (to create) |
| Maildb main | `maz/maildb/main.go` |
| Maildb handler (old, to gut) | `maz/maildb/mail_handler.go` |
| Maildb collection store | `maz/maildb/collection.go` (to create) |
| Maildb message store | `maz/maildb/msgstore.go` (to create) |
| Mail app main | `mazarin/apps/mail/main.go` |
| New mail row interactor | `mazarin/apps/mail/mail_row.go` (to create) |
| Page alloc (userspace) | `mazarin/sys/sharedmem.go`, `mazarin/sys/mem/` |
| Grid table | `mazarin/mancini/std/grid_table.go` |
| Badger flags persistence | new keys in `maz/maildb/mail_handler.go` |

---

## mazdl / mazlink Architecture

### Four-piece design
| Piece | Location | Responsibility |
|---|---|---|
| **mazlink** | `mazlink-patches/cmd/link/` | Emit plugin-shape ELF: UNDEF dynsym for host imports, PLT/JUMP_SLOT for function imports, GLOB_DAT for data imports, strip unreferenced host code, NOP host `init.N` entries. Also: emit host's export dynsym when linking the host binary. |
| **mazdl** | `mazarin/mazdl/` | Real dlopen: mmap segments via kernel primitive, apply `R_*_RELATIVE`, resolve UNDEF symbols against global module table, patch GOT/PLT, run `DT_INIT_ARRAY`, return handle. `dlsym` walks export table. |
| **Kernel** | `kmazarin/ksyscall/` | Single new primitive `SysMapELFSegment(fd, offset, len, vaddr, perms)` — mmap + W^X enforcement. No ELF parsing, no relocations, no symbol names. |
| **maz-reloc** | `cmd/maz-reloc/` | Retires on arm64/amd64 once Phase 5 lands. Stays alive for riscv64 until Phase 7. |

Rule of thumb: if the work requires understanding ELF structure, it lives in `mazdl`,
not the kernel. The kernel only touches page tables.

### Plugin ELF shape (ET_DYN)
- `.dynsym`: UNDEF entries for host-imported symbols; DEFINED for exports
- `.rela.dyn`: `R_*_RELATIVE` for internal pointers; `R_*_GLOB_DAT` for data imports
- `.rela.plt` + `.plt` + `.got.plt`: function imports via JUMP_SLOT — eager binding
- `.dynamic`: `DT_NEEDED="mazarin-host"`, `DT_JMPREL`, `DT_PLTREL=DT_RELA`, etc.
- `.init_array`: `_mazdl_register_moduledata` wrapper first, then user inits
- No `R_*_COPY` — single authoritative copy of every host datum
- No lazy .plt resolver — `mazdl.Open` fills every slot before returning

### Host policy (Phase-2 starting set)
Packages whose code is stripped from plugins and resolved against the host at load time:
```
runtime
internal/runtime/...   (atomic, gc, maps, math, sys, syscall, exithook, ...)
internal/abi
internal/cpu
internal/bytealg
internal/goarch
internal/goos
internal/goexperiment
```
`internal/runtime/...` kept wholesale: atomic CAS primitives must be identical on
shared memory. Ambiguous packages (`sync`, `reflect`, `os`, `time`) deferred to Phase 6
— add only when a specific bug forces it.

### Funcval dead-reloc bug (Option A fix — 2026-04-18)
Go emits an 8-byte `.data.rel.ro` funcval object for every function taken as a value
(name ends in `·f`). When mazlink strips a host-policy package's `.text`, the funcval
stays but its `R_*_RELATIVE` addend points into the zero-padding gap between stripped
functions and `runtime.etext`. Any indirect call through the funcval (e.g. map hasher)
branches into padding → `udf #0` → SIGILL.

**Fix:** In `adddynrel`'s `R_ADDR` case, when target is `SDYNIMPORT` AND
`DynimpLib=="mazarin-host"`, emit `R_AARCH64_GLOB_DAT` / `R_X86_64_GLOB_DAT` against
the target's dynsym entry. The dynamic loader writes the host's real address at load
time. The `DynimpLib=="mazarin-host"` gate is load-bearing — prevents accidentally
promoting unrelated SDYNIMPORT entries.
Files: `mazlink-patches/cmd/link/internal/arm64/asm.go`,
       `mazlink-patches/cmd/link/internal/amd64/asm.go`.

### Name-mangling parity
`BuildModePlugin` triggers `ld.mangleTypeSym` which hashes long `type:.*` symbols
to 6-byte base64 tags as dynsym `extname` (e.g. `type:.C9kB2TSL`). Stock exe mode
skips this hashing, so host and plugin would have mismatched dynsym names → "unresolved
symbol" at load time. Mazlink patches `mangleTypeSym` to also run when
`-dlopen-host-exports` is set so names agree.

### Retirements when Phase 5 lands (arm64/amd64)
| Mechanism | Why it goes away |
|---|---|
| `maz-reloc` thin-stub trampolines | Plugin no longer has its own `morestack` — `.plt` goes directly to host |
| `maz-reloc` `.maz_imports`/`.maz_import_strtab` | Standard `.dynsym`/`.rela.plt` replace them |
| `syncMazWriteBarriers` | Plugin's `runtime.writeBarrier` is gone; GOT slot points at host's flag |
| `preGrowStack` | No plugin `morestack`; host handler runs correctly |
| Kernel symbol-name hunt for `MazarinMain` | Replaced by `mazdl.Sym` |
| `RegisterMazModuledata` host helper | `_mazdl_register_moduledata` init-array entry runs from inside the plugin |

---

## CFF Write-Barrier Investigation (paused 2026-04-17)

### Symptom
fontsvc.maz crashes during CFF glyph rendering in `go-text/typesetting` after loading
the Italic font (Regular succeeds). Two modes, non-deterministic:
- `SIGSEGV addr=0x70000000000000` at `(*CharstringReader).ensureClosePath`
  (the `append(out.Segments, ...)` inside)
- `panic: runtime error: growslice: len out of range` — cap field is garbage
  before growslice is entered

Always happens after one full GC cycle (2 writeBarrier transitions).

### Confirmed NOT the bug
- Library is correct on stock Go 1.26.2 (standalone test renders 362 glyphs each arch, 0 panics)
- `RegisterMazWriteBarrier` IS called; `syncMazWriteBarriers` IS firing at STW exit
- Compiled fontsvc.maz code reads the correct `runtime.writeBarrier` VA
- Body trampolines (`morestack.abi0`, `wbBufFlush.abi0`) are patched correctly
- Go P-struct wbBuf offsets match between host and .maz (both Go 1.26.2)

### Still suspicious
1. Timing gap: `setGCPhase(_GCmark)` flips `writeBarrier.enabled=true` during STW;
   `syncMazWriteBarriers` runs in `startTheWorldWithSema` — on paper fine, but
   not runtime-verified.
2. `[]ot.Segment` GC bitmap after `buildCompleteTypemap` type-redirect — if the
   redirected `*_type` has wrong `Size_` or GC bitmap, growslice computes wrong cap.
3. Race between growslice return and slice-header store (cap written before array
   pointer at `a5350`/`a5360`) — if write barriers don't fire, GC could miss new array.

### Architecture context
- fontsvc.maz is loaded by **rachel** (rachel is the HOST, not kmazarin)
- rachel uses the userspace overlay at `mazarin/overlay/userspace/runtime/`
- fontsvc.maz uses the thin-overlay at `build/shepherd-overlay/runtime/`
- Instrumentation still in `mazarin/overlay/userspace/runtime/maz_moduledata.go`

### Next diagnostic steps (if resuming before mazdl Phase 5)
1. Force `runtime.GC()` before every glyph render in fontsvc to isolate GC-correlation
2. Add growslice instrumentation in the userspace overlay at growslice entry
3. Verify `[]ot.Segment` type descriptor after typelinksinit + buildCompleteTypemap
   (adrp x4, 0x21c000 + #0x880 = 0x21c880 in fontsvc binary)

**Revert before next boot:** `config/kernel.arm64.toml` `go_mem_limit=256` → `24`
