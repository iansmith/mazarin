# Task Plan — Mazarin / Mazzy

## TOP OF STACK: GC crash — `sweep increased allocation count` in mail shepherd bgsweep

**Status (2026-04-26, latest):** PD_FILE_BACKED fixes applied. `releasePageByPA` guard
added. Guard has NOT fired in any crash run → premature-free-via-releasePageByPA is ruled
out as the active corruption path. New hypothesis: live dual-mapping concurrent write.
See `findings.md` for full evidence and next steps.

### Crash signature

```
runtime: nelems=1 nalloc=43525 (or nelems=170 nalloc=44899)
fatal error: sweep increased allocation count
```

Both are mathematically impossible without mspan struct corruption. ~43K nalloc
is consistent across runs despite varying nelems — the large value is likely the true
corrupted state; the small nelems is what the external write happened to store.

### What's already fixed (KEEP ALL)

1. **MAP_FIXED unmap** — `kmazarin/ksyscall/mmap.go` `unmapFixedRange()`. Correct.
2. **Constraint-block PD_PINNED** — `kmazarin/kmem/constraint.go`. Correct.
3. **`PD_FILE_BACKED` flag** — `page_descriptor.go`. Correct.
4. **`cleanup.go` Phase 1 + `walkAndFreePageTablePages`** — skip FILE_BACKED leaves. Correct.
5. **`delegate.go:842`** — set PD_FILE_BACKED on dual-map success. Correct.
6. **`releasePageByPA` guard** — blocks + logs premature release of FILE_BACKED pages. Correct; has not fired yet (diagnostic value pending).
7. **`mmap_writeback.go` `handleFlushReply`** — clears PD_FILE_BACKED before release. Correct.

### Hypotheses

**#1–#4, TTBR0, madvise, releasePageByPA-path:** ALL RULED OUT. See `findings.md`.

**#5 (live dual-mapping write): CURRENT STRONGEST LEAD.**
Both PTEs may be live simultaneously. Linux handler writes through its live VA→P while
mail's GC also uses P at a different VA. No free required; the guard can't catch this.
Possible routes:
  - `mapOK=false` branch at delegate.go:835: PD_FILE_BACKED never set, linux PTE stays live
  - An as-yet-unknown path that maps the same PA into both shepherds without going through delegate.go:833

### Next steps

1. **Check `mapOK=false` path** (`delegate.go:835`): if `MapPageInProcess` fails but linux
   already has a PTE to the page, the page is leaked with a live linux PTE and no
   FILE_BACKED flag. What happens to that page? Does `reclaimDataPage` skip it (DataPagePA=0)?
   If so it's permanently leaked with a live PTE. Linux writes → anyone who later gets the
   page from buddy sees corruption.

2. **Instrument `MapPageInProcess` for double-map detection**: log when the same PA is
   mapped into a second shepherd. This finds any code path creating dual-mapping without
   going through the delegate.go:833 guarded path.

3. **Longer runs to confirm intermittency**: the guard not firing doesn't mean it never
   will. Run 3–5 × 60s with guard in place. If crash appears and guard never fires across
   all runs, route #5 (live dual-mapping) is confirmed.

### Secondary concern

`[localRect] NEGATIVE: lw=354 lh=-2691673` in the same log — rachel layout corruption.
Likely collateral from the GC crash. Investigate after GC crash is resolved.

### Branch: `feature/mail-dumb`

---

## PAUSED: fstatat/sysid=44 hang — instrumentation in place, need a hang run

Instrumentation added (no functional change) in `maz/linux/syscalls.go`,
`mazarin/fsclient/client.go`, `maz/linux/main.go`. Three clean 180s runs (no hang yet;
expected ~1-in-5 rate). Stopping point: waiting for a hang run.

### What to look for when a hang fires

- Does `[fstatat] seq=N enter path=...` appear without a matching `done`?
  - YES → hang is in `h.fs.Stat()` or deeper
  - `[fsclient] stat id=N sent` present → blocked at `<-c.RespCh` (fs.maz not responding)
  - `[fsclient] stat id=N sent` absent → blocked at `c.mu.Lock()` or `uring.Send`
- What do periodic `[linux] chan-monitor:` lines show at the time of hang?
  - wmCh or fontReplyCh near cap=8 → dispatcher deadlock
  - both near 0 → secondary hypothesis (fs.maz not responding)

Resume after GC crash is fixed (don't layer more changes on an unstable baseline).

---

## PAUSED: stability bisect — did b9fd57f regress boot reliability?

After landing the real temp-pool IPC (`b9fd57f`), five 180s ARM64 HVF runs produced
five distinct failure modes (fti Fstatat hang, boot panics, `attr.Init: invalid shared
page header`). None touch the code changed; they're in the .maz plugin loader and the
kernel↔shepherd attr-shared-page handshake.

Resume after GC crash is fixed. Run 3–5 × 180s on `feature/mail-dumb`:
- **Stable** → b9fd57f exonerated; gremlins are pre-existing.
- **Unstable** → bisect b9fd57f off the branch and re-test; if that stabilizes, split
  the slot-table redesign from the IPC client rewrite and re-apply selectively.

---

## Resumable: Console rewrite (paused — foundation ready)

Grid scrollbar (item 1) is DONE (commit `cc230e5`). Console rewrite (item 2) not started.

### Spec

- Same logic as the mail header grid's row machinery.
- Fixed row interactors over a **500-line ring buffer**.
- Row count determined by viewing-area height — stack full rows only, never partial.
- Switch console rows to **DynamicLabel** (drop `consoleLine` mono renderer).
- Exports same attrs as grid: line height, visible line count, total line count.
- Scrollbar, same shape as the grid scrollbar — reuse `GreaterI64Bool` /
  `ThumbFracPermille` / `NonnegSubI64`.

### Files to touch

- `mazarin/mancini/std/console.go` — rewrite to DynamicLabel, dynamic row count,
  500-line ring buffer.
- `mazarin/mancini/std/console_frame.go` — NEW. Analogous to `GridFrame`: NeuBox +
  Console + Scrollbar. Constraint-driven Visible / Max / ThumbFrac.
- Callers of `NewConsole` / `NewConsoleWithBox` — switch to `NewConsoleFrame`.

### Attrs to publish (mirror GridTable)

- `LineHeightAttr` — refreshed each Draw.
- `VisibleLineCountAttr` — full rows that fit, computed in Draw.
- `TotalLineCountAttr` — `len(content)`, capped at 500.
- `ScrollOffsetAttr` — lines from buffer start to first visible. Default tail-anchored.

### ConsoleFrame scrollbar wiring (identical pattern to GridFrame)

```
scrollNeededAttr  = ConstraintBool(GreaterI64Bool(TotalLineCountAttr, VisibleLineCountAttr))
scrollMaxAttr     = ConstraintI64(NonnegSubI64(TotalLineCountAttr, VisibleLineCountAttr))
thumbFracAttr     = ConstraintI64(ThumbFracPermille(VisibleLineCountAttr, TotalLineCountAttr))
scrollbar.Visible             = EqualBool(scrollNeededAttr)
scrollbar.ValueAttr           = console.ScrollOffsetAttr   (shared)
scrollbar.MaxAttr             = scrollMaxAttr
scrollbar.ThumbFracPermilleAttr = thumbFracAttr
```

---

## Resumable: mail-dumb easy part (blocked on stability)

Once GC crash + stability bisect are resolved:

1. **Body display** — HTML body pane in the mail app. Requires temp-font fallback chain
   (`@font-face` → registered buffer → fontsvc OpenFont → default sans) for the case
   where fontsvc IPC stub is unavailable.
2. **PageUp/PageDown** — extend `GridTable.MoveSelection` / `ScrollBy`.
3. **Mark-read** — `MsgTypeMarkRead` IPC to maildb; update `Flags.IsRead` in the row.
4. **Delete** — `MsgTypeMarkDeleted`; remove from displayed collection.
5. **Polish** — click→body fetch latency audit; prefetch tuning.

### Mail program deferred follow-ups

- **Click→body fetch latency / prefetch-ahead audit.** 5 clicks produced 12 body
  fetches; explore whether prefetch is excessive.
- **maildb working set.** ~140 MB badger LSM; add periodic `[maildb:mem]` log to
  confirm it's bounded.
- **linux-ui transient fontsvc-boot wedge.** Not seen since uring.Send retry fix; watch.

---

## Reference: Mail / maildb Protocol

### Wire format constraints

- `UringIPCMsg.Payload` = 112 bytes
- First 4 bytes = MsgType (uint32); remaining 108 bytes for RequestId + fields
- **RequestId** = [16]byte (raw UUID bytes)
- After MsgType(4) + RequestId(16) = 20 bytes overhead, **88 bytes remain**

### Protocol IDs

ProtoMailReq=13, ProtoMailResp=14. Old types 1/2/3 deleted; KeyHeaders replaces GetHeaders.

### Error codes

```
ErrNone              = 0
ErrCollectionExpired = 1
ErrInvalidMsgNumber  = 2
ErrMessageNotFound   = 3
ErrBadgerError       = 4
ErrFilterInvalid     = 5
```

### Request messages (mail → maildb)

```
MsgTypeMessageCount     = 10   // no extra fields                                    (20 bytes)
MsgTypeKeyHeaders       = 11   // CollId uint32, From uint32, To uint32              (32 bytes)
MsgTypeAllHeaders       = 12   // CollId uint32, MsgNum uint32                       (28 bytes)
MsgTypeLatestUnread     = 13   // no extra fields                                    (20 bytes)
MsgTypeBody             = 14   // CollId uint32, MsgNum uint32                       (28 bytes)
MsgTypeMarkRead         = 15   // CollId uint32, MsgNum uint32                       (28 bytes)
MsgTypeMarkDeleted      = 16   // CollId uint32, MsgNum uint32                       (28 bytes)
MsgTypeCreateCollection = 17   // FilterType uint32, SortOrder uint32, FilterArg [64]byte  (92 bytes ≤ 108 ✓)
```

SortOrder: `SortDesc=0` (newest-first), `SortAsc=1`.

### Response messages (maildb → mail)

```
MsgTypeRespMessageCount     = 50   // Count uint64, ErrCode uint32                   (32 bytes)
MsgTypeRespKeyHeaders       = 51   // TargetVA uint64, NumBytes uint32, Count uint32, ErrCode uint32  (40 bytes)
MsgTypeRespAllHeaders       = 52   // TargetVA uint64, NumBytes uint32, ErrCode uint32               (36 bytes)
MsgTypeRespLatestUnread     = 53   // CollId uint32, MsgNum uint32, TargetVA uint64, NumBytes uint32, ErrCode uint32  (44 bytes)
MsgTypeRespBody             = 54   // TargetVA uint64, NumBytes uint32, ErrCode uint32               (36 bytes)
MsgTypeRespMarkRead         = 55   // ErrCode uint32                                 (24 bytes)
MsgTypeRespMarkDeleted      = 56   // ErrCode uint32, NewSize uint32                 (28 bytes)
MsgTypeRespCreateCollection = 57   // CollId uint32, Size uint32, ErrCode uint32     (32 bytes)
```

### Unsolicited notifications (maildb → mail)

```
MsgTypeCollectionAdd    = 60   // CollId uint32, MsgNum uint32, NewSize uint32, RequestId [16]byte
MsgTypeCollectionRemove = 61   // CollId uint32, MsgNum uint32, NewSize uint32, MsgId [64]byte, RequestId [16]byte
```

Clients MUST NOT assume RequestId matches one of their own outstanding requests.

### Page layouts

**KeyHeaderEntry** (240 bytes; 50 entries = 12,000 bytes = 3 pages):
```go
type KeyHeaderEntry struct {
    Sender  [64]byte   // null-terminated UTF-8
    Subject [128]byte  // null-terminated UTF-8
    Date    [32]byte   // RFC3339
    MsgNum  uint32
    Flags   uint32     // bit 0 = IsRead, bit 1 = IsDeleted
    _pad    [8]byte
}
```

**AllHeaderEntry** (1,232 bytes < 4096 → 1 page):
```go
type AllHeaderEntry struct {
    From, To, CC, Subject  [128/256/256/256]byte
    Date                   [64]byte
    MessageId, ContentType [128]byte
    MsgNum, Flags          uint32
    _pad                   [8]byte
}
```

**Body** — raw bytes (UTF-8 / quoted-printable / base64 as-is). Caller decodes MIME.

### Filter types

```
FilterAll       = 0   // count from count:all key
FilterUnread    = 1   // count from count:unread key
FilterFrom      = 2   // routes through fti; FilterArg = query bytes
FilterSubject   = 3   // routes through fti; FilterArg = query bytes
```

---

## Reference: fti Search Protocol

New in `shared/fti/protocol.go` (existing type: `MsgTypeIndexDocument=1`):

```
MsgTypeSearchMail   = 2    // maildb → fti
MsgTypeSearchResult = 20   // fti → maildb
MsgTypeSearchError  = 21   // fti → maildb
```

### SearchMail request

```go
type SearchMail struct {
    RequestId  [16]byte
    QueryType  uint32    // 1=Subject, 2=From
    SortOrder  uint32    // 0=desc, 1=asc
    From       uint32    // pagination offset
    Size       uint32    // hits to return; 0 = count only
    QueryLen   uint16
    Query      [58]byte
}
// 96 bytes ≤ 108 ✓
```

### SearchResult response

```go
type SearchResult struct {
    RequestId [16]byte
    TargetVA  uint64   // 0 if Size=0
    NumBytes  uint32
    Count     uint32
    Total     uint32
    ErrCode   uint32
}
// 44 bytes ≤ 108 ✓
```

### SearchResultEntry page layout

```go
type SearchResultEntry struct {
    IdLen uint16
    _pad  [6]byte
    DocId [80]byte
}
// 88 bytes; 46 entries per page
```

---

## Reference: BadgerDB Count Capability

No O(1) count for a key prefix. Solution — persistent counters in badger:
- `count:all` → little-endian uint64 (maintained on import/delete)
- `count:unread` → little-endian uint64 (maintained on MarkRead/import)

Ad-hoc filters (`FilterFrom`, `FilterSubject`) require O(n) key scan at CreateCollection.

---

## Reference: Collection and MessageStore Design

### Collection

- At most **16 live collections**. LRU eviction.
- `CollId` is monotonically increasing uint32, starting at 1; 0 = invalid.
- Stale CollId → ErrCollectionExpired; client must CreateCollection again.

```go
type collection struct {
    id, filterType, sortOrder uint32
    filterArg                 [64]byte
    totalSize                 int
    lastUsed                  time.Time
    subscribers               []int16
    entries                   map[uint32]string   // msgNum → messageId (sparse)
    msgIdToNum                map[string]uint32   // reverse
}
```

### Window loading (lazy, 128-entry cap per load)

1. Open key-only iterator on `date:` prefix.
2. Skip first `from` keys; collect next `to-from+1` messageIds.
3. Store in `entries[from..to]`.

### Notification fan-out

Iterate 16-slot pool and check `coll.msgIdToNum[msgId]` — effectively O(1).
`MessageRecord.memberships` dropped; MessageStore is a pure lazy data cache.

### MessageStore

```go
type MessageRecord struct {
    mu        sync.Mutex
    messageId string
    headers   *MailMessage   // lazy
    body      []byte         // lazy
    isRead    *bool          // lazy
    isDeleted *bool          // lazy
}
type MessageStore struct {
    mu      sync.RWMutex
    records map[string]*MessageRecord
}
```

Operations: `Ensure`, `LoadHeaders`, `LoadFlags`, `LoadBody`, `MarkRead`, `MarkDeleted`, `Evict`.

### ValueCollI64 infrastructure (implemented)

- New `RegionValueColl` region: 32 slots × 256 entries × 40B = 320KB.
- `ConstraintPageVersion` bumped 3→4.
- `SysAttrWriteCollI64 = 0x102D`.
- `ValueCollI64(uri, initial)` → `*Attribute[[]int64]`.

---

## Reference: Smart Cache Architecture

### Virtual scroll

Pool sized to `visibleCount` = `floor((contentH - headerH) / rowHeight)`. No partial rows.
On font change: pool rebuilt with new epoch; old slots abandoned in attr registry
(acceptable — pool rebuilds are infrequent).

### Scroll offset

`scrollOffset` clamped to `[0, max(0, TotalRows - visibleCount)]`. Slot `i` shows
`msgNum = scrollOffset + i`. `publishScrollAttrs` writes FirstVisible, LastVisible,
VisibleRowCount attrs after each scroll operation.

### Cache window math

With `readAhead=2`, `visibleCount=9`: `prefetch=18`, max window=45 entries.
One in-flight `KeyHeadersReq` at a time; if window changes before reply arrives,
old request abandoned and new one fired.

### Collection expiry

On `RespKeyHeaders.ErrCode == ErrCollectionExpired`: clear cache, re-request collection.
`OnExpired func()` callback on MailCache.

### selectedMsgNum storage

GridTable stores selected **msgNum** (int64, init -1), not a GridRow pointer.
On pool rebuild, find which slot has `scrollOffset + i == selectedMsgNum` and set
`SelectionState=1`. `SelectedSetAttr` uses `MaxInt64` sentinel for sets > 256 entries.
