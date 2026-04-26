# Findings

---

## GC crash — partial-munmap full-flush bug + ftruncate cache leak (2026-04-26 evening)

### Audit findings (Opus session)

Re-audit of the dual-mapping handler-side flow uncovered two concrete bugs that
fit the symptoms tightly. Sonnet's "live dual-mapping write conflict" hypothesis
was on the right track but missed the actual mechanism.

**Bug B (corruption candidate) — partial munmap full-flush** (`munmap.go:55-58` +
linux `sysMmapPageFlush`):
- `flushAndCleanupPages(fm.FD, callerSID)` was fd/inum-keyed, with no VA-range
  constraint
- linux's `RemoveRangeBatch(callerSID, inum, 511)` returned **all** cached pages
  for the inum, not just the unmapped range
- kernel's `handleFlushReply` then `ReleasePageByPA`'d all of them
- **Mail's caller PTEs in the un-unmapped portion were left pointing at PAs
  that just went back to the buddy allocator** — exactly the symptom: guard
  silent (handleFlushReply legitimately clears `PD_FILE_BACKED` before release),
  ZERO_VERIFY clean (page was zero when allocated to mail's heap), corruption
  arrives via linux's writes through still-cached entries that point at recycled
  PAs.

**Bug A (leak, possibly correlated) — sysFtruncate cache discard**
(`maz/linux/syscalls.go:649`):
- `h.cache.RemoveRange(...)` returns the evicted entries; result was **discarded**
- handler-side PTEs and kernel-side `RefCount` were never decremented
- The pages stay mapped in linux's address space with `RefCount=1` until the next
  munmap of that fd or shepherd death. linux can still write into them via
  `updateCachedPages` if a future cache lookup happens to land on a stale entry.

### Fix applied

**Bug B (proper fix)**:
- `flushAndCleanupPages` signature: `(fd, callerSID, startOffset, length uint64)`
- `DelegateCallInfo` gets `FlushOffset` / `FlushLength`; passed in IPC `Args[2]`/`Args[3]`
- new `pageCache.RemoveRangeOffsetBatch(sid, inum, startOffset, length, max)`
  filters by file-offset range
- `sysMmapPageFlush` reads `Args[2]/Args[3]`; if `length > 0`, uses range-bounded
  remove. `length == 0` retains old "all" semantics for death cleanup
- `WriteBackSharedMmapOnDeath` passes `0, 0` for full-drain
- `SyscallMunmap` passes `fm.FileOffset + (alignedAddr - fm.StartVA)` and
  `alignedLength`, and warns `[munmap:PARTIAL]` when `alignedLength != fm.Length`

**Bug A (warning only — full fix deferred)**: `sysFtruncate` now logs
`[ftruncate:LEAK]` with the count of orphaned cache entries. Proper fix needs a
new "drop these handler VAs" IPC; deferred.

**Instrumentation added**:
- `[pageCache:OVERWRITE]` warning when `cache.Add` replaces an existing entry
  with a different VA (orphan path — same `(sid, inum, offset)` faulting twice)
- `[munmap:PARTIAL]` warning at `SyscallMunmap` when unmap length is less than
  the matched FileMapping length

### Verification (2 × 120s ARM64 HVF)

| # | Uptime | Result | Warnings |
|---|--------|--------|----------|
| 1 | 51s    | clean (then unrelated `attr.Set` slot=527 panic in mail body display) | none |
| 2 | 103s+  | clean | none |

No GC `sweep increased allocation count` crashes. None of the new warnings fired
in either run. Either the partial-munmap path was not exercised in these runs,
or the bugs are fixed; multiple longer runs are needed to discriminate (the GC
crash was always intermittent — pre-fix recurrence rate was ~1-in-5 at 60–120s).

### Crash signature (for reference)

```
runtime: nelems=1 nalloc=43525 previous allocCount=1 nfreed=50162
fatal error: sweep increased allocation count
```

The `~43K` nalloc + small nelems pattern means: at countAlloc time `nelems` was
≥43525 (so the loop summed ~5440 bytes of marks); by panic-print time `nelems`
had been overwritten to a small value (1 or 170). The writer is putting a small
number at the `nelems` field offset — fits a `MsgType`, a flush-response page
`count` (0..511), or another mspan's `nelems` value.

### Next steps if crash recurs

1. 5+ × 180s ARM64 HVF runs — confirm or refute the fixes.
2. If crash recurs without warnings: the partial-munmap path wasn't the source.
   Re-aim at the `cache.Add` overwrite path — add a dedicated probe that
   detects the same `(sid, inum, offset)` faulting twice (instrument
   `handleFileMappedPageFault` with a small history table).
3. If `[ftruncate:LEAK]` fires often, prioritize the proper fix (kernel-side
   "drop these VAs" IPC).

---

## GC crash — dual-mapping refcount (superseded by partial-munmap finding above)

### Crash signature

```
runtime: nelems=36 nalloc=15375 previous allocCount=1 nfreed=50162
fatal error: sweep increased allocation count
```

Crash in mail shepherd's `bgsweep` immediately after `[mail] cache ready`. The
`nelems` / `nalloc` values are contradictory — conclusive mspan struct corruption.

### Observed nalloc values across runs

| nelems | nalloc  | Interpretation |
|--------|---------|---------------|
| 170    | 44,899  | 44K impossible from 22 bytes of gcmarkBits |
| 1      | 43,525  | 43K impossible from 1 byte of gcmarkBits |

The ~43K-45K range across runs despite very different `nelems` values strongly suggests
the TRUE corrupted nelems was ~43K-45K and the small `nelems` value (1, 170) is what
the external writer happened to write over the field AFTER countAlloc() ran.

### Root cause hypothesis (#2 — strongest lead, Opus-reviewed)

`delegate.go:833` — the MmapPageFill reply path creates a dual mapping:

```
1. allocEmptyDataPage → AllocPage(PageSharedIPC, handlerSID) → RefCount=1
2. MapPageInProcess(handlerSID, va, pa)    → handler PTE set, RefCount=1
3. Handler reads file data, replies
4. delegate.go:833: MapPageInProcess(callerSID, callerBufVA, pa)
                    → caller PTE set, RefCount STILL 1, no PD_SHARED
```

The page is now in two shepherds' address spaces with RefCount=1. Any release through
a path that doesn't check `PD_FILE_BACKED` will prematurely free the page.

### What is fixed (KEEP — all correct)

- **`page_descriptor.go`**: `PD_FILE_BACKED = 1 << 4` added.
- **`cleanup.go` Phase 1**: skips FILE_BACKED leaves — defers to handleFlushReply.
- **`walkAndFreePageTablePages` (`freeLeaves=true` path)**: same skip.
- **`delegate.go:842`**: sets `PD_FILE_BACKED` when MmapPageFill dual-map succeeds.
- **`releasePageByPA` guard**: emits `[kmem] BLOCKED:` warning and returns false if
  called on a FILE_BACKED page. Prevents premature release on any path that doesn't
  clear the flag first.
- **`mmap_writeback.go` `handleFlushReply`**: clears `PD_FILE_BACKED` before calling
  `ReleasePageByPA` (the only legitimate release path).
- **`SyscallMunmap`**: `!fileBacked` check was already correct.

### Key negative finding: guard never fires

The `releasePageByPA` guard has NOT fired in any crash run. This means:

1. The corruption is **not** going through `releasePageByPA` with PD_FILE_BACKED set.
2. Either `PD_FILE_BACKED` was never set on the corrupted page (e.g., the `mapOK=false`
   branch at delegate.go:835 skips the flag), OR
3. The page was never freed prematurely — both PTEs remain live and the corruption is a
   **concurrent write through a live handler PTE to a page that Go also uses for GC**.

### Revised hypothesis: live dual-mapping write conflict

If the mail shepherd's Go heap demand-faults a page at VA=X, AND the linux handler's
file-backed mmap also uses the same **physical page** at VA=Y (dual-mapped), then linux
can write through VA=Y while mail's GC reads/writes the same page at VA=X — no free
required, purely a live write conflict.

This would happen if:
- `handleFileMappedPageFault` allocates a page that Go's demand-pager also claims, OR
- Mail's heap VA range overlaps with the file-backed mmap bump-allocator range
  (would be caught by `scanForStalePTEs`, which has NOT fired)

### Ruled-out hypotheses

- **Zero-on-alloc gaps**: Every demand-fault / IPC-page path zeroes. ZERO_VERIFY_FAIL at scratchVA never fires.
- **Cache aliasing scratch↔userVA**: ARM64 PIPT D-cache. Ruled out architecturally.
- **Bump-allocator overlap**: `scanForStalePTEs` (`mmap.go:191`) never logs `[mmap:STALE]`.
- **TTBR0 in SyscallMunmap**: TTBR0 never changed in SVC path. Misleading `stubs.go:279` comment; ignore.
- **SyscallMadvise on file-backed pages**: Go runtime only madvises its own heap VAs, not file-backed mmap VAs (different VA regions). Madvise path cannot hit a file-backed page from Go's scavenger.
- **releasePageByPA premature free of FILE_BACKED page**: guard never fires → not this path.

### Next steps for next session

1. **Check `mapOK=false` path** at delegate.go:835: if map fails but the page is still
   in linux's address space, `PD_FILE_BACKED` is never set. If linux later tries to
   release this page, the guard won't fire. Need to check if there is a path that
   releases a page that was mapped in handler but not caller.

2. **Add PA-level tracking**: when `MapPageInProcess` is called a second time for the
   same PA (from delegate.go:833), record the PA in a small fixed-size table.
   In `releasePageByPA`, check this table regardless of `PD_FILE_BACKED` flag.
   This catches dual-mapped pages where PD_FILE_BACKED wasn't set.

3. **Alternative**: add a probe that verifies the physical page given to mail's demand
   pager was NOT previously dual-mapped. In `HandleUserPageFault`, after zeroing,
   check the page descriptor's history (requires adding a "was dual mapped" bit).

4. **Alternative**: instrument `MapPageInProcess` to detect double-mapping (same PA
   being mapped into a SECOND shepherd) and log the caller site.

---

## fstatat/sysid=44 delegate hang (intermittent, ~1-in-5 runs)

Instrumentation in place (see progress.md for current state). No hang in 3 × 180s runs yet.

### Current instrumentation

- `maz/linux/syscalls.go::sysFstatat`: per-call entry (`[fstatat] seq=N enter path=P`)
  and exit (`[fstatat] seq=N done`).
- `mazarin/fsclient/client.go::callLocked`: after `uring.Send`, before `<-c.RespCh`:
  `[fsclient] stat id=N sent; RespCh len=N` (gated on FSOpStat).
- `maz/linux/main.go`: startup cap dump + periodic `[linux] chan-monitor:` every 5s.

### Decision tree when hang fires

```
[fstatat] seq=N enter  (no matching "done") → hang is in h.fs.Stat()
  [fsclient] stat id=N sent present → blocked at <-c.RespCh → fs.maz not responding
  [fsclient] stat id=N sent absent  → blocked at c.mu.Lock() or uring.Send
  chan-monitor wmCh near cap=8      → dispatcher deadlock confirmed
```

---

## x86_64 Boot OOM — diagnostic shortcut

If you see `[kmem] Buddy OOM order=0 total=ffffffff20000` at boot:
1. `grep "Unified pool:" /tmp/diplomat-serial.log` — is start address > 0x100000000?
2. If yes: `diplomat/main/pagetable_amd64.go::allocatePhysPages` must use
   `AllocateMaxAddress` with seed `physAddrResult = linearMapMaxPA - 1`.
3. If 2.5 GB contiguous alloc still fails (UEFI fragmentation): retry-down ladder
   `[2.5G, 1.5G, 1G, 512M, 256M]` in `kernelvm_amd64.go::PrepareKernelVM`.

Both fixes are already in tree (applied 2026-04-24). This note is for future regressions.

---

## Smart Cache — architecture reference

### Virtual scroll pool sizing

Pool = `visibleCount` = `floor((contentH - headerH) / rowHeight)`. On font change:
grow-only, indexed by epoch (`poolEpoch`), old slots abandoned in attr registry
(acceptable; pool rebuilds are infrequent and slot count is small).

### Scroll state

- `scrollOffset` clamped to `[0, max(0, TotalRows - visibleCount)]`
- Slot `i` shows `msgNum = scrollOffset + i`
- `publishScrollAttrs` writes FirstVisible, LastVisible, VisibleRowCount, ScrollOffset
  attrs; called after every scroll or draw transition

### Cache window

`readAhead=2`, `visibleCount=9`: `prefetch=18`, max window=45 entries.
One in-flight `KeyHeadersReq` at a time; on window-change before reply: abandon old,
fire new immediately.

### selectedMsgNum

GridTable stores selected **msgNum** (int64, init -1) not a GridRow pointer. On pool
rebuild: find slot where `scrollOffset + i == selectedMsgNum`, set `SelectionState=1`.

`SelectedSetAttr` uses `MaxInt64` sentinel for sets > 256 entries. Consumer reads
`SelectedSetCountAttr` for true count when sentinel active.

### ConsoleFrame constraint wiring (not yet implemented)

```
scrollNeededAttr  = ConstraintBool(GreaterI64Bool(TotalLineCountAttr, VisibleLineCountAttr))
scrollMaxAttr     = ConstraintI64(NonnegSubI64(TotalLineCountAttr, VisibleLineCountAttr))
thumbFracAttr     = ConstraintI64(ThumbFracPermille(VisibleLineCountAttr, TotalLineCountAttr))
scrollbar.Visible             = EqualBool(scrollNeededAttr)
scrollbar.ValueAttr           = console.ScrollOffsetAttr
scrollbar.MaxAttr             = scrollMaxAttr
scrollbar.ThumbFracPermilleAttr = thumbFracAttr
```

---

## Inode lifecycle — Pin/Unpin (implemented, proved insufficient)

`PinInode`/`UnpinInode` in `shared/fs/ext2/pin.go` + `maz/fs/fsipc.go` implement
Linux unlink-while-open semantics. Verification showed **0 `[ext2:defer-free]` events**
during a run where the bleve SCORCH ENOENT fired — the failing path is NOT
unlink-while-open. The actual root causes were:

1. Kernel string-copy truncated paths at page boundaries (`allocAndCopyCallerString`).
2. `fsclient.Client` was racy (no mutex around path/data area writes).
3. GC effectively disabled (GOGC=10000); per-shepherd memory accumulated until
   system-wide user-page budget exhausted.

All three are fixed. The Pin/Unpin code stays in place as defensive (zero cost on cold
path) but is not the critical fix.

---

## Mail / maildb Protocol — Wire Format Reference

### Constraints

- `UringIPCMsg.Payload` = 112 bytes
- MsgType(4) + RequestId[16] = 20 bytes overhead; **88 bytes remaining**

### Request messages (mail → maildb)

```
MsgTypeMessageCount     = 10   // (20 bytes)
MsgTypeKeyHeaders       = 11   // CollId, From, To (32 bytes)
MsgTypeAllHeaders       = 12   // CollId, MsgNum (28 bytes)
MsgTypeLatestUnread     = 13   // (20 bytes)
MsgTypeBody             = 14   // CollId, MsgNum (28 bytes)
MsgTypeMarkRead         = 15   // CollId, MsgNum (28 bytes)
MsgTypeMarkDeleted      = 16   // CollId, MsgNum (28 bytes)
MsgTypeCreateCollection = 17   // FilterType, SortOrder, FilterArg[64] (92 bytes ≤ 108 ✓)
```

### Response messages (maildb → mail)

```
MsgTypeRespMessageCount     = 50
MsgTypeRespKeyHeaders       = 51   // TargetVA uint64, NumBytes, Count, ErrCode
MsgTypeRespAllHeaders       = 52   // TargetVA uint64, NumBytes, ErrCode
MsgTypeRespLatestUnread     = 53
MsgTypeRespBody             = 54   // TargetVA uint64, NumBytes, ErrCode
MsgTypeRespMarkRead         = 55
MsgTypeRespMarkDeleted      = 56
MsgTypeRespCreateCollection = 57   // CollId, Size, ErrCode
```

### Unsolicited notifications

```
MsgTypeCollectionAdd    = 60   // CollId, MsgNum, NewSize, RequestId[16]
MsgTypeCollectionRemove = 61   // CollId, MsgNum, NewSize, MsgId[64], RequestId[16]
```

### Page layouts

**KeyHeaderEntry** (240 bytes; 50 per load = 3 pages):
```go
type KeyHeaderEntry struct {
    Sender  [64]byte; Subject [128]byte; Date [32]byte
    MsgNum  uint32;   Flags   uint32;   _pad [8]byte
}
```

**AllHeaderEntry** (1,232 bytes = 1 page):
```go
type AllHeaderEntry struct {
    From [128]byte; To, CC, Subject [256]byte
    Date [64]byte;  MessageId, ContentType [128]byte
    MsgNum, Flags uint32; _pad [8]byte
}
```

### Filter types

```
FilterAll=0     // count:all key (O(1))
FilterUnread=1  // count:unread key (O(1))
FilterFrom=2    // routes through fti; O(hits) scan
FilterSubject=3 // routes through fti; O(hits) scan
```

SortOrder: `SortDesc=0` (newest-first), `SortAsc=1`.

---

## fti Search Protocol

New types in `shared/fti/protocol.go`:

```
MsgTypeSearchMail   = 2
MsgTypeSearchResult = 20
MsgTypeSearchError  = 21
```

### SearchMail

```go
type SearchMail struct {
    RequestId [16]byte; QueryType, SortOrder, From, Size uint32
    QueryLen  uint16;   Query [58]byte
}  // 96 bytes ≤ 108 ✓
```

### SearchResult

```go
type SearchResult struct {
    RequestId [16]byte; TargetVA uint64
    NumBytes, Count, Total, ErrCode uint32
}  // 44 bytes ≤ 108 ✓
```

### SearchResultEntry (page layout)

```go
type SearchResultEntry struct {
    IdLen uint16; _pad [6]byte; DocId [80]byte
}  // 88 bytes; 46 entries per 4096-byte page
```

---

## BadgerDB Count Capability

No O(1) count for a key prefix. Persistent counters in badger:
- `count:all` → little-endian uint64
- `count:unread` → little-endian uint64

Ad-hoc filters require O(n) key scan at `CreateCollection` — acceptable (user-initiated).

---

## Collection Design

- Max **16 live collections**; LRU eviction; `CollId` monotonically increasing from 1.
- Stale CollId → `ErrCollectionExpired`.
- **Sparse array**: only loaded entries (via `KeyHeaders`) held in memory.

```go
type collection struct {
    id, filterType, sortOrder uint32
    filterArg                 [64]byte
    totalSize                 int
    lastUsed                  time.Time
    subscribers               []int16
    entries                   map[uint32]string  // msgNum → messageId
    msgIdToNum                map[string]uint32  // reverse
}
```

**Window loading**: key-only iterator on `date:` prefix; skip `from` keys; collect
`to-from+1` messageIds (capped 128 per load).

**Notification fan-out**: iterate 16-slot pool, check `coll.msgIdToNum[msgId]` — O(1).
`MessageRecord.memberships` dropped; MessageStore is a pure lazy data cache.

---

## MessageStore Design

```go
type MessageRecord struct {
    mu        sync.Mutex
    messageId string
    headers   *MailMessage  // lazy
    body      []byte        // lazy
    isRead    *bool         // lazy
    isDeleted *bool         // lazy
}
type MessageStore struct {
    mu      sync.RWMutex
    records map[string]*MessageRecord
}
```

Operations: `Ensure`, `LoadHeaders`, `LoadFlags`, `LoadBody`, `MarkRead`, `MarkDeleted`, `Evict`.

---

## ValueCollI64 infrastructure (implemented)

- New `RegionValueColl` in shared constraint page: 32 slots × 256 entries × 40B = 320KB.
- `ConstraintPageVersion` bumped 3→4.
- `SysAttrWriteCollI64 = 0x102D`.
- `ValueCollI64(uri, initial)` → `*Attribute[[]int64]`.
- `SelectedSetAttr` sentinel: `[MaxInt64]` when set > 256. Consumer checks
  `SelectedSetCountAttr` for true count.
