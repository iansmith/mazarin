# Kmazarin Microkernel Architecture

## Overview

Kmazarin follows a microkernel design where the kernel provides minimal services:
- Thread scheduling and context switching
- Memory management (page tables, ASID, page allocation)
- IPC primitives (transactions, shared memory, attributes)
- Hardware interrupt routing
- Priest lifecycle management

Everything else lives in userspace "priests":
- Syscall emulation (Linux compatibility)
- Filesystem
- Disk I/O
- Network stack
- Device drivers
- UI/window management

---

## Priest Types

| Priest | Responsibility |
|--------|----------------|
| **Syscall Priest** | Emulates Linux syscalls, manages file descriptors |
| **Filesystem Priest** | Path resolution, inode management, directory ops |
| **Disk Priest** | Block device I/O, DMA, virtio-blk |
| **Network Priest** | TCP/IP stack, sockets |
| **Dapope** | Input event handling (keyboard, mouse, timer) |
| **Stdio** | Serial console rendering |

---

## IPC Mechanisms

### 1. Transaction-Based IPC

For request/response patterns (syscalls, RPC):

```go
// Kernel syscalls
TxID = SendRequest(targetPriest, requestData)  // Blocks until answered
AnswerRequest(TxID, responseData)               // Unblocks sender

// Async variant
TxID = SendRequestAsync(targetPriest, requestData)  // Returns immediately
response, txid = WaitAnyAnswer()                     // Blocks for any answer
```

### 2. Shared Memory (Zero-Copy)

For bulk data transfer:

```go
TransferPages(toPriest PriestId, pages []PageHandle, perm Permission)
MapSharedPage(withPriest PriestId, page PageHandle, perm Permission) uintptr
UnmapPages(va uintptr, count int) []PageHandle
```

### 3. Attribute Messages (Fast Path)

For UI and small value exchange (≤128 bytes):

```go
GetAttr(ref AttrRef, buf *[128]byte) int
SetAttr(ref AttrRef, buf []byte)
WatchAttr(ref AttrRef)           // Subscribe to changes
AttrChanged(ref AttrRef)         // Notify dependents
WaitAttrChange() AttrRef         // Block until watched attr changes
```

### 4. Soft IRQ (Hardware Events)

Existing mechanism for hardware interrupts:

```go
RegisterSoftIRQ(slot int, irqNum int)
WaitSoftIRQ(slot int, buf *SoftIRQReturn) (int, error)
```

---

## Example: os.Open("/some/path")

### Request Chain

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         USER PROGRAM                                     │
│   fd, err := os.Open("/some/path")                                      │
│   // Expects: fd is small integer, can call Read()/Write()/Close()      │
└────────────────────────────────┬────────────────────────────────────────┘
                                 │ SVC (syscall 56 = openat)
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                            KERNEL                                        │
│  1. Capture syscall from user program                                   │
│  2. Allocate TxID=100                                                   │
│  3. Block caller thread (state = ThreadBlockedTx)                       │
│  4. Route to SYSCALL PRIEST via IPC                                     │
└────────────────────────────────┬────────────────────────────────────────┘
                                 │ IPC: {TxID=100, syscall=openat,
                                 │       path="/some/path", flags=O_RDONLY}
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                        SYSCALL PRIEST                                    │
│  1. Receives open request with TxID=100                                 │
│  2. Needs to resolve path → asks FILESYSTEM PRIEST                      │
│  3. Allocates TxID=101, sends request                                   │
└────────────────────────────────┬────────────────────────────────────────┘
                                 │ IPC: {TxID=101, op=resolve_path,
                                 │       path="/some/path"}
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                       FILESYSTEM PRIEST                                  │
│  1. Parses path, walks directory tree                                   │
│  2. Needs to read directory blocks → asks DISK PRIEST                   │
│  3. Allocates TxID=102                                                  │
└────────────────────────────────┬────────────────────────────────────────┘
                                 │ IPC: {TxID=102, op=read_block,
                                 │       block=1234}
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         DISK PRIEST                                      │
│  1. Issues virtio-blk command                                           │
│  2. Waits on soft IRQ for completion                                    │
│  3. Data lands in DMA buffer (kernel page)                              │
│  4. Answers TxID=102 with page handle (zero-copy!)                      │
└────────────────────────────────┬────────────────────────────────────────┘
                                 │ Answer: {TxID=102, page_handle=0x1234}
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                       FILESYSTEM PRIEST                                  │
│  1. Maps page read-only, parses directory entries                       │
│  2. Finds inode for "/some/path"                                        │
│  3. Answers TxID=101 with file metadata                                 │
└────────────────────────────────┬────────────────────────────────────────┘
                                 │ Answer: {TxID=101, inode=567,
                                 │         size=4096, blocks=[5,6,7]}
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                        SYSCALL PRIEST                                    │
│  1. Allocates file descriptor (small int) in per-process table         │
│  2. Creates file object: {inode, position=0, fs_priest_id}             │
│  3. Answers TxID=100 with fd=3                                          │
└────────────────────────────────┬────────────────────────────────────────┘
                                 │ Answer: {TxID=100, result=3}
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                            KERNEL                                        │
│  1. Receives answer for TxID=100                                        │
│  2. Unblocks original caller thread                                     │
│  3. Sets X0 = 3 (fd), X1 = 0 (no error)                                │
│  4. ERET back to user program                                           │
└────────────────────────────────┬────────────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         USER PROGRAM                                     │
│   // fd = 3, err = nil                                                  │
│   // Can now call Read(fd, buf, len)                                    │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Example: Large Read with Zero-Copy

```
User: n, err := fd.Read(buf)  // buf is 64KB
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                            KERNEL                                        │
│  TxID=200, route to SYSCALL PRIEST                                      │
└────────────────────────────────┬────────────────────────────────────────┘
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                        SYSCALL PRIEST                                    │
│  1. Looks up fd=3 → file object with inode, position                    │
│  2. Calculates which blocks needed (position/block_size)                │
│  3. Asks FILESYSTEM PRIEST for block mapping                            │
│  4. TxID=201 → FS PRIEST                                                │
└────────────────────────────────┬────────────────────────────────────────┘
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                       FILESYSTEM PRIEST                                  │
│  1. Returns physical block numbers for logical file offsets             │
│  2. Answer: blocks=[100,101,102,103] for 16KB of data                  │
└────────────────────────────────┬────────────────────────────────────────┘
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                        SYSCALL PRIEST                                    │
│  1. Asks DISK PRIEST to read blocks                                     │
│  2. TxID=202 → DISK PRIEST                                              │
└────────────────────────────────┬────────────────────────────────────────┘
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         DISK PRIEST                                      │
│  1. Issues DMA read for blocks [100,101,102,103]                        │
│  2. Hardware fills pages at PA 0x50000000-0x50003FFF                    │
│  3. Soft IRQ signals completion                                         │
│  4. ZERO-COPY: Transfer page ownership to SYSCALL PRIEST                │
│     - Unmap from DISK PRIEST address space                              │
│     - Kernel tracks: pages now owned by SYSCALL PRIEST                  │
│  5. Answer TxID=202 with page handles                                   │
└────────────────────────────────┬────────────────────────────────────────┘
                                 │ Answer: {TxID=202,
                                 │   pages=[handle1,handle2,handle3,handle4]}
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                        SYSCALL PRIEST                                    │
│  1. Maps pages read-only into its address space                         │
│  2. Calculates byte offset within first page                            │
│  3. ZERO-COPY to user: Transfer pages to USER PROGRAM                   │
│     - Or: Tell kernel to map pages into user's buffer region            │
│  4. Answer TxID=200 with byte count                                     │
└────────────────────────────────┬────────────────────────────────────────┘
                                 │ Answer: {TxID=200, count=16384,
                                 │   pages=[...] mapped at user_buf}
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                            KERNEL                                        │
│  1. Maps pages into user's address space at buf address                 │
│  2. Unblocks user thread                                                │
│  3. Returns count=16384                                                 │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Resource Lifecycle Management

### The Core Problem

When resources (pages, transactions, shared memory) span multiple priests:
1. **Ownership tracking** - Who can access what?
2. **Cleanup on death** - Reclaim resources when a priest crashes
3. **Mid-transaction recovery** - Handle crashes during multi-step operations

### Page Tracking with Reference Counting

```go
type PageInfo struct {
    PA        uintptr
    RefCount  int32               // Number of mappings
    Mappings  [MaxPriests]uintptr // VA per priest (0 = not mapped)
}

func mapPageToPriest(pageIdx int, priest PriestId, va uintptr, perm Permission) {
    p := &pageTable[pageIdx]
    if p.Mappings[priest] != 0 {
        panic("already mapped")
    }
    p.Mappings[priest] = va
    atomic.AddInt32(&p.RefCount, 1)
    // Actually map in page tables...
}

func unmapPageFromPriest(pageIdx int, priest PriestId) {
    p := &pageTable[pageIdx]
    if p.Mappings[priest] == 0 {
        return // Not mapped
    }
    // TLB shootdown for this priest's ASID
    kmem.TlbiASIDE1IS(uint16(priest))
    p.Mappings[priest] = 0
    if atomic.AddInt32(&p.RefCount, -1) == 0 {
        // Last reference - return to pool
        freePagePool.Push(pageIdx)
    }
}
```

### Transaction Tracking

```go
type Transaction struct {
    TxID          uint32
    SourcePriest  PriestId    // Who sent the request
    SourceThread  ThreadId    // Which thread is blocked
    TargetPriest  PriestId    // Who should answer
    State         TxState     // Pending, Answered, Cancelled

    // For chained transactions (nested calls)
    ParentTxID    uint32      // 0 if top-level
    ChildTxIDs    []uint32    // Transactions spawned from this one
}

type TxState int
const (
    TxPending   TxState = 0  // Waiting for answer
    TxAnswered  TxState = 1  // Got response
    TxCancelled TxState = 2  // Target died or timeout
)
```

### Transaction Tree Example

For the `open()` example:
```
TxID=100 (User→Kernel→SyscallPriest)
    └─▶ TxID=101 (SyscallPriest→FSPriest)
            └─▶ TxID=102 (FSPriest→DiskPriest)
```

### Crash Recovery: Disk Priest Dies Mid-Read

```
User calls read() → TxID=200
  SyscallPriest asks FSPriest → TxID=201
    FSPriest asks DiskPriest → TxID=202
      ** DISK PRIEST CRASHES **
```

**Recovery sequence:**
1. Kernel detects DiskPriest death
2. Find TxID=202 (target=DiskPriest, state=Pending)
3. Answer TxID=202 with `EDEADPRIEST` → FSPriest unblocks
4. FSPriest sees error, answers TxID=201 with `EIO`
5. SyscallPriest sees error, answers TxID=200 with `EIO`
6. User's `read()` returns error

---

## Per-Priest Resource Table

```go
type PriestResources struct {
    // Pages
    OwnedPages    []PageHandle    // Pages this priest owns
    MappedPages   []PageMapping   // Pages mapped (may be shared)

    // Transactions
    PendingTxIDs  []uint32        // TxIDs waiting for answers
    OwedAnswers   []uint32        // TxIDs this priest should answer

    // Shared Memory
    AttrRegions   []AttrRegionRef // Regions created or mapped

    // IRQ Slots
    SoftIRQSlots  []uint8         // Registered soft IRQ slots

    // Watchers
    WatchedPriests []PriestId     // Priests we're watching for death
    WatchedBy      []PriestId     // Priests watching us
}
```

### Complete Cleanup on Death

```go
func onPriestDeath(pid PriestId) {
    res := &priestResources[pid]

    // 1. Answer all pending transactions with error
    for _, txid := range res.OwedAnswers {
        answerWithError(txid, EDEADPRIEST)
    }

    // 2. Cancel all transactions we were waiting on
    for _, txid := range res.PendingTxIDs {
        cancelTransaction(txid)
    }

    // 3. Unmap all pages (refcount decrement)
    for _, mapping := range res.MappedPages {
        unmapPageFromPriest(mapping.PageIdx, pid)
    }

    // 4. Handle owned pages - return to kernel pool
    for _, page := range res.OwnedPages {
        returnPageToPool(page)
    }

    // 5. Handle attr regions we created
    for _, regionRef := range res.AttrRegions {
        if regionRef.IsCreator {
            handleAttrSourceDeath(regionRef.RegionID)
        } else {
            unmapAttrRegion(pid, regionRef.RegionID)
        }
    }

    // 6. Release soft IRQ slots
    for _, slot := range res.SoftIRQSlots {
        releaseSoftIRQSlot(slot)
    }

    // 7. Notify watchers
    for _, watcher := range res.WatchedBy {
        notifyPriestEvent(watcher, PRIEST_DIED, pid)
    }

    // 8. Finally, release priest ID (TLB shootdown + ID reuse)
    releasePriestWithTLBShootdown(pid)
}
```

---

## Attribute Messages (Eval/Vite Style)

Based on Scott Hudson's work on lazy incremental evaluation for UI systems.

### Design Goals

1. **Blazingly fast** - Answers from priest memory, no disk/network
2. **Small payloads** - ≤128 bytes per attribute value
3. **Cross-priest** - Attributes can reference other priests' attributes
4. **Lazy evaluation** - Only compute when requested (demand-driven)
5. **Incremental update** - Mark dirty, recompute on access

### Attribute Reference

```go
type AttrRef struct {
    PriestID  uint16
    AttrID    uint16   // Small integer, priest-local namespace
}
```

### Shared Memory for Hot Attributes

```go
// For ultra-hot attributes (mouse position, clock, etc.)
type SharedAttrPage struct {
    Lock    uint32      // Spinlock for writers
    MouseX  int32
    MouseY  int32
    Clock   uint64
    // ... other hot attributes
}

// Readers don't syscall - just read from mapped page
// Writers acquire lock, update, release, optionally notify
```

### Cross-Priest Attribute Dependencies

```
PRIEST "button" has:
  - width = get("window/content_width") - 20

PRIEST "window" has:
  - content_width = screen_width - sidebar_width

When screen_width changes:
  1. window marks content_width dirty
  2. window calls AttrChanged(window/content_width)
  3. Kernel notifies button (who called WatchAttr)
  4. button marks its width dirty
  5. Next GetAttr("button/width") triggers lazy recomputation
```

### Shared Memory Lifecycle

```
1. PRIEST A creates shared attr region
   - Kernel allocates pages
   - Maps into Priest A
   - Creator = A, Mappings = {A}

2. PRIEST B requests to share the region
   - Kernel maps same pages into Priest B
   - Mappings = {A, B}

3. PRIEST A crashes
   - Kernel removes A from mappings
   - TLB shootdown for ASID A
   - Region still exists - B can still use it
   - Optionally: poison the data or set "source dead" flag

4. PRIEST B unmaps or crashes
   - Mappings = {}
   - No more users → free pages to pool
```

---

## New Thread States

```go
const (
    ThreadBlockedTx      ThreadState = 7  // Waiting for TxID answer
    ThreadBlockedAttr    ThreadState = 8  // Waiting for attr change
    ThreadBlockedPriest  ThreadState = 9  // Waiting for priest event
)
```

---

## Kernel Primitives Summary

### Transaction IPC
```go
SendRequest(target PriestId, data []byte) TxID
AnswerRequest(txid TxID, data []byte)
SendRequestAsync(target PriestId, data []byte) TxID
WaitAnyAnswer() (TxID, []byte)
```

### Page Transfer
```go
TransferPages(to PriestId, pages []PageHandle, perm Permission)
MapSharedPage(with PriestId, page PageHandle, perm Permission) uintptr
UnmapPages(va uintptr, count int) []PageHandle
```

### Priest Lifecycle
```go
WatchPriest(pid PriestId)
WaitPriestEvent() (event, PriestId)
CancelTxForPriest(pid PriestId)
```

### Attribute Fast Path
```go
GetAttr(ref AttrRef, buf *[128]byte) int
SetAttr(ref AttrRef, buf []byte)
WatchAttr(ref AttrRef)
AttrChanged(ref AttrRef)
WaitAttrChange() AttrRef
```

---

## Resource Tracking Summary

| Resource | Tracking | On Owner Death | On Sharer Death |
|----------|----------|----------------|-----------------|
| **Pages (owned)** | Single owner | Return to pool | N/A |
| **Pages (shared)** | Refcount + mapping table | Decrement refcount | Decrement refcount |
| **Transactions** | TxID table with source/target | Answer pending with error | Cancel subtree |
| **Attr Regions** | Creator + mapping table | Poison or unmap all | Remove mapping |
| **Soft IRQ Slots** | Per-priest list | Release slots | N/A |

---

## References

- [Scott Hudson's Research](https://www.cs.cmu.edu/~hudson/research.html)
- [Ultra-Lightweight Constraints](https://sites.cc.gatech.edu/gvu/people/faculty/hudson/constraints/ulc.final.html)
- [subArctic Toolkit](http://www.cs.cmu.edu/~hudson/teaching/05-631-f00/sub_arctic/)
- Hudson, S.E. "Incremental attribute evaluation: A flexible algorithm for lazy update." ACM TOPLAS 1991; 13(3): 315-341.
