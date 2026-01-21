# Interrupt-Driven Disk I/O Architecture

## Overview

This document describes the design for interrupt-driven disk I/O in Kmazarin, including how data flows from hardware to userspace buffers.

## Current State (Polling)

```go
func ReadBlock(blockNum uint64) []byte {
    // Setup DMA transfer
    device.QueueReadRequest(blockNum)

    // BUSY WAIT - polls status register
    for !device.IsReady() {
        // CPU spins, wasting cycles
    }

    return device.GetData()
}
```

**Problems:**
- CPU blocked during entire I/O operation (milliseconds)
- Cannot run other threads while waiting
- Poor scalability with multiple concurrent requests

## Interrupt-Driven Architecture

### Components

1. **Block Device Layer** - manages physical I/O
2. **IRQ Handler** - processes completion interrupts
3. **Request Queue** - tracks pending operations
4. **Thread Blocking** - suspends waiting threads
5. **Data Transfer** - copies results to userspace

### Data Flow

```
[Userspace]
    |
    | syscall read(fd, buf, size)
    v
[Kernel - Syscall Handler]
    |
    | 1. Allocate IORequest
    | 2. Start async disk read
    | 3. Block thread (add to ioWaitQueue)
    v
[Virtio-blk DMA Controller]
    |
    | ... hardware performs transfer ...
    |
    | IRQ fires when complete
    v
[IRQ Handler]
    |
    | 1. Read completion status
    | 2. Copy data to userspace via scratch mapping
    | 3. Wake blocked thread
    v
[Thread Scheduler]
    |
    | Resume userspace thread
    v
[Userspace]
    | data now in buf
```

## Implementation Details

### 1. Page-Granular I/O

**Always read in 4KB page chunks:**

```go
const PageSize = 4096  // ARM64 page size

// Block devices report sector size (512 or 4096 bytes)
// Always read full pages for efficiency

func ReadPage(pageNum uint64) ([]byte, error) {
    // Calculate block numbers
    // If sector = 512 bytes: 1 page = 8 sectors
    blocksPerPage := PageSize / dev.GetBlockSize()
    startBlock := pageNum * uint64(blocksPerPage)

    // Allocate page-aligned buffer for DMA
    page := AllocDMABuffer(PageSize)

    // Start async read
    req := StartAsyncRead(startBlock, blocksPerPage, page)

    return req, nil
}
```

**Benefits:**
- Aligned with VM page size
- Efficient DMA transfers
- Simplifies caching
- Most filesystems use 4KB clusters anyway

### 2. Request Tracking

```go
type IORequest struct {
    BlockNum   uint64        // Starting block number
    Buffer     []byte        // Kernel DMA buffer (page-aligned)
    UserBuf    uintptr       // Userspace buffer VA
    UserSize   int           // How many bytes user requested
    UserOffset int           // Offset within page
    ThreadID   int16         // Blocked thread waiting for this
    Status     IOStatus      // PENDING, COMPLETE, ERROR
}

// Global request map (static allocation for IRQ safety)
var ioRequestData [MaxIORequests]IORequest
var ioRequestInUse [MaxIORequests]bool
var ioRequestList = ds.StaticList[IORequest]{
    Data:  ioRequestData[:],
    InUse: ioRequestInUse[:],
}
```

### 3. Async Read Syscall

```go
func SyscallRead(fd int, userBuf uintptr, size int) int64 {
    // 1. Validate userspace pointer
    if !IsValidUserPtr(userBuf, size) {
        return -EFAULT
    }

    // 2. Determine which disk page(s) we need
    fileOffset := files[fd].Offset
    pageNum := fileOffset / PageSize
    offsetInPage := fileOffset % PageSize

    // 3. Check page cache first
    if cached := pageCache.Lookup(fd, pageNum); cached != nil {
        // Cache hit - copy immediately, no I/O needed
        CopyToUser(userBuf, cached[offsetInPage:], size)
        files[fd].Offset += uint64(size)
        return int64(size)
    }

    // 4. Cache miss - allocate I/O request
    slotIdx, req := ioRequestList.Allocate()
    req.BlockNum = pageNumToBlockNum(pageNum)
    req.Buffer = AllocDMABuffer(PageSize)  // Kernel buffer
    req.UserBuf = userBuf
    req.UserSize = size
    req.UserOffset = int(offsetInPage)
    req.ThreadID = GetCurrentThreadID()
    req.Status = IO_PENDING

    // 5. Start DMA transfer (non-blocking)
    virtioDevice.QueueReadRequest(req.BlockNum, req.Buffer)

    // 6. Block current thread, switch to another
    // (IRQ will wake us when complete)
    return BlockThreadOnIO(slotIdx)
}
```

### 4. IRQ Handler

```go
// Called when virtio-blk raises interrupt
//go:nosplit
func diskIRQHandler() {
    // 1. Read which requests completed
    completed := virtioDevice.GetCompletedRequests()

    for _, reqIdx := range completed {
        req := &ioRequestData[reqIdx]
        if !ioRequestInUse[reqIdx] {
            continue // Stale completion
        }

        // 2. Check for errors
        if virtioDevice.HasError(reqIdx) {
            req.Status = IO_ERROR
            WakeThread(req.ThreadID)
            continue
        }

        // 3. Copy data to userspace via scratch mapping
        // (Cannot access userspace memory directly due to PAN)
        err := CopyDMAToUser(req)
        if err != nil {
            req.Status = IO_ERROR
        } else {
            req.Status = IO_COMPLETE
        }

        // 4. Free DMA buffer
        FreeDMABuffer(req.Buffer)

        // 5. Wake the blocked thread
        WakeThread(req.ThreadID)

        // 6. Free request slot
        ioRequestInUse[reqIdx] = false
    }

    // 7. Acknowledge interrupt
    gic.AckIRQ(virtioBlkIRQNum)
}
```

## Copying Data to Userspace

### Problem: Privileged Access Never (PAN)

ARM64 kernels running at EL1 **cannot directly write to EL0 (userspace) memory** when PAN is enabled. This prevents kernel exploits from corrupting userspace.

### Solution: Kernel Scratch Mapping

```go
// Copy from kernel DMA buffer to userspace buffer
func CopyDMAToUser(req *IORequest) error {
    // 1. Walk userspace page table to get physical address
    userVA := req.UserBuf
    userPA := kmem.WalkUserPageTable(uintptr(userVA))
    if userPA == 0 {
        return errors.New("user page not mapped")
    }

    // 2. Map userspace physical page to kernel VA (scratch area)
    // This allows kernel to write to it
    kernelVA := kmem.MapPAToKernelScratch(userPA &^ 0xFFF)
    if kernelVA == 0 {
        return errors.New("scratch mapping failed")
    }

    // 3. Calculate offset within page
    pageOffset := userVA & 0xFFF

    // 4. Copy data via kernel mapping
    srcData := req.Buffer[req.UserOffset:]  // Start at offset in page
    dstPtr := unsafe.Pointer(kernelVA + uintptr(pageOffset))

    copyLen := req.UserSize
    if copyLen > len(srcData) {
        copyLen = len(srcData)
    }

    // Actual copy (kernel VA → same physical page ← userspace VA)
    for i := 0; i < copyLen; i++ {
        *(*byte)(unsafe.Pointer(uintptr(dstPtr) + uintptr(i))) = srcData[i]
    }

    // 5. Clean cache so userspace sees the data
    kmem.CleanPageCache(kernelVA)

    return nil
}
```

### Memory Layout During Copy

```
Physical Memory:
+-------------------+
| User Page (PA)    |  ← DMA writes here
| Contains user buf |  ← Kernel writes via scratch mapping
+-------------------+  ← Userspace reads via TTBR0

Kernel Address Space (TTBR1):
+-------------------+
| Scratch Mapping   | ← Temporary kernel VA
| → User Page PA    |    Points to same physical page
+-------------------+

User Address Space (TTBR0):
+-------------------+
| User Buffer VA    | ← userspace pointer
| → User Page PA    |    Points to same physical page
+-------------------+
```

**Key insight:** We write to the physical page via a kernel-accessible VA, but the data is visible to userspace because they both map the same physical page.

### Multi-Page Reads

```go
// If user request spans multiple pages
func CopyDMAToUserMultiPage(req *IORequest) error {
    remaining := req.UserSize
    srcOffset := req.UserOffset
    dstVA := req.UserBuf

    for remaining > 0 {
        // How much in this page?
        pageOffset := dstVA & 0xFFF
        copyLen := PageSize - int(pageOffset)
        if copyLen > remaining {
            copyLen = remaining
        }

        // Map and copy this page
        userPA := kmem.WalkUserPageTable(uintptr(dstVA))
        kernelVA := kmem.MapPAToKernelScratch(userPA &^ 0xFFF)

        CopyBytes(kernelVA+uintptr(pageOffset), req.Buffer[srcOffset:], copyLen)
        kmem.CleanPageCache(kernelVA)

        // Move to next page
        remaining -= copyLen
        srcOffset += copyLen
        dstVA += uintptr(copyLen)
    }

    return nil
}
```

## Page Cache Integration

To avoid repeated disk reads, maintain a page cache:

```go
type PageCache struct {
    entries map[CacheKey]*CachedPage
}

type CacheKey struct {
    DeviceID  int
    FileID    int
    PageNum   uint64
}

type CachedPage struct {
    Data      []byte  // 4096 bytes
    Dirty     bool    // Modified but not written back?
    RefCount  int     // How many users?
}

func (pc *PageCache) Lookup(fd, pageNum uint64) []byte {
    key := CacheKey{DeviceID: 0, FileID: fd, PageNum: pageNum}
    if entry := pc.entries[key]; entry != nil {
        entry.RefCount++
        return entry.Data
    }
    return nil
}

// After I/O completes, add to cache
func (pc *PageCache) Insert(fd, pageNum uint64, data []byte) {
    key := CacheKey{DeviceID: 0, FileID: fd, PageNum: pageNum}
    pc.entries[key] = &CachedPage{
        Data:     data,
        Dirty:    false,
        RefCount: 1,
    }
}
```

## Zero-Copy Optimization (Future)

For memory-mapped files (`mmap`), avoid copying entirely:

```go
func SyscallMMap(addr, length uintptr, fd int, offset uint64) int64 {
    // Map file pages directly into userspace
    for pageNum := offset / PageSize; ...; pageNum++ {
        // Get physical page from cache or disk
        physPage := GetOrLoadFilePage(fd, pageNum)

        // Map into userspace (read-only)
        MapUserPage(addr, physPage, PAGE_READ)

        addr += PageSize
    }

    // No copy! Userspace reads directly from cache
    return 0
}
```

## Thread States During I/O

```
Running Thread:
  → Issues read() syscall
  → Kernel starts DMA
  → Thread state = ThreadBlockedIO
  → Added to ioWaitQueue[requestID]
  → Context switch to next ready thread

... DMA completes, IRQ fires ...

IRQ Handler:
  → Copies data to userspace
  → Changes thread state: ThreadBlockedIO → ThreadReady
  → Moves thread: ioWaitQueue → readyQueue

Scheduler:
  → Eventually picks resumed thread
  → Returns from syscall with data ready
```

## Static Allocation for IRQ Safety

**CRITICAL:** IRQ handlers cannot allocate memory (GC might be running). Use static structures:

```go
// Static allocation - IRQ safe
var ioRequestData [MaxIORequests]IORequest       // 64 slots
var ioRequestInUse [MaxIORequests]bool
var ioWaitQueueData [MaxIORequests]int16         // TIDs waiting for I/O
var ioWaitQueueInUse [MaxIORequests]bool

var ioRequestList = ds.StaticList[IORequest]{
    Data:  ioRequestData[:],
    InUse: ioRequestInUse[:],
}

var ioWaitQueue = ds.StaticQueue[int16]{
    Data:  ioWaitQueueData[:],
    InUse: ioWaitQueueInUse[:],
}
```

## Performance Characteristics

| Operation | Polling | Interrupt-Driven |
|-----------|---------|------------------|
| CPU usage during I/O | 100% (spinning) | ~0% (blocked) |
| Context switches | 0 (blocked) | 2 (block + resume) |
| Latency (single request) | ~same | ~same |
| Throughput (concurrent) | Poor | Good |
| Power efficiency | Poor | Good |

**When to use interrupts:**
- Multiple threads doing I/O
- Long-running I/O operations (HDD)
- Power-constrained systems

**When polling is okay:**
- Single-threaded, simple systems
- Very fast devices (NVMe with µs latency)
- Real-time constraints (avoid IRQ jitter)

## Implementation Phases

**Phase 1:** Basic interrupt support
- Enable virtio-blk IRQ in GIC
- Simple IRQ handler prints message
- Verify interrupt delivery

**Phase 2:** Async I/O without blocking
- IORequest structure
- Start DMA, return immediately
- Poll for completion (not blocking threads yet)

**Phase 3:** Thread blocking
- Block thread on I/O
- IRQ wakes thread
- Scheduler integration

**Phase 4:** Userspace copy
- Implement scratch mapping
- CopyDMAToUser logic
- Handle multi-page transfers

**Phase 5:** Page cache
- Cache frequently-accessed pages
- LRU eviction
- Write-back support

## References

- ARM ARM: Privileged Access Never (PAN)
- VirtIO Specification 1.1: Block Device
- Linux `copy_to_user()` implementation
- Kmazarin memory management (`kmem` package)
