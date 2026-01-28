# Memory Management Design

## Overview

Kmazarin uses a three-layer memory management system:

1. **Linear map** (Cardinal): Maps all physical RAM into kernel VA space using 2MB L2 block descriptors, so any physical address can be accessed via `VA = PA + KernelVAOffset` without faulting.

2. **Buddy allocator** (kmazarin): Manages physical pages in power-of-two blocks (orders 0-11, 4KB to 8MB). Supports both allocation and deallocation with buddy merging.

3. **Page tracking** (kmazarin): Top-half/bottom-half system records metadata about every page allocation for debugging and memory accounting.

A bump allocator handles the earliest boot allocations before the buddy allocator is initialized.

## Physical Memory Layout

```
0x40000000  ┌──────────────────────────────────────┐
            │  DTB (1 MB)                          │
0x40100000  ├──────────────────────────────────────┤
            │  Cardinal (15 MB)                    │
0x41000000  ├──────────────────────────────────────┤
            │  VirtIO GPU Framebuffer (32 MB)      │
0x43000000  ├──────────────────────────────────────┤
            │  Page Tables (8 MB)                  │
0x43800000  ├──────────────────────────────────────┤
            │  Kmazarin ELF (~2.2 MB)              │
~0x43A00000 ├──────────────────────────────────────┤
            │                                      │
            │  UNIFIED POOL                        │
            │  (buddy allocator, capped at 4GB)    │
            │                                      │
            │  First ~76 pages: bump-allocated     │
            │  during early boot before buddy init │
            │                                      │
            │  Remaining: managed by buddy         │
            │  allocator (orders 0-11)             │
            │                                      │
0x100000000 └──────────────────────────────────────┘
            (4GB cap due to KernelVAOffset wrap)
```

## Linear Map (Cardinal)

Cardinal creates a full linear map of physical RAM at boot using 2MB L2 block descriptors in TTBR1 page tables. This means any physical address can be accessed as:

```
VA = PA + KernelVAOffset    (KernelVAOffset = 0xFFFFFFFF00000000)
```

### Implementation

```go
// cardinal/main/mmu_mapping.go

func createLinearMap(ramStart, ramEnd uintptr) {
    // For each 2MB chunk of physical RAM:
    //   Create L2 block descriptor in TTBR1 page tables
    //   Skip regions already mapped with 4KB pages (stacks, MMIO)
}
```

Block descriptors differ from table descriptors in bit[1]:
- Block: `bits[1:0] = 01` (valid + block) - maps 2MB directly
- Table: `bits[1:0] = 11` (valid + table) - points to L3 table

### Page Table Overhead

| RAM Size | L2 Entries | L2 Tables | Total PT Pages |
|----------|------------|-----------|----------------|
| 512 MB   | 256        | 1         | 3 (12 KB)      |
| 8 GB     | 4096       | 8         | 10 (40 KB)     |

### 4GB Address Limitation

`KernelVAOffset = 0xFFFFFFFF00000000` causes wraparound for PAs >= 0x100000000 (4GB). For example:

```
PA 0x100000000 + 0xFFFFFFFF00000000 = 0x10000000000000000 → wraps to 0x0
```

Since this is kernel-only memory management, the buddy allocator caps its pool at the 4GB boundary. This provides ~3GB of managed memory, which is sufficient for the kernel's 64MB budget.

## Buddy Allocator

### Design

Orders 0-11 give allocation sizes from 4KB (1 page) to 8MB (2048 pages):

| Order | Pages | Size   |
|-------|-------|--------|
| 0     | 1     | 4 KB   |
| 1     | 2     | 8 KB   |
| 2     | 4     | 16 KB  |
| 3     | 8     | 32 KB  |
| 4     | 16    | 64 KB  |
| 5     | 32    | 128 KB |
| 6     | 64    | 256 KB |
| 7     | 128   | 512 KB |
| 8     | 256   | 1 MB   |
| 9     | 512   | 2 MB   |
| 10    | 1024  | 4 MB   |
| 11    | 2048  | 8 MB   |

Free lists are **intrusive**: the first 8 bytes of each free block store a pointer to the next free block (physical address). This works because free pages are already mapped via the linear map.

### API

```go
// kmazarin/kmem/buddy.go

func BuddyAlloc(order int) uintptr    // Allocate 2^order pages, returns PA
func BuddyFree(pa uintptr, order int) // Free block, merge with buddy if possible
func AllocBuffer(size uint64) *BuddyBuffer  // Allocate contiguous buffer for file I/O
func FreeBuffer(buf *BuddyBuffer)           // Return buffer to allocator
```

### Allocation

1. Find smallest available order >= requested
2. Remove block from free list
3. Split down to requested order (upper halves become free buddies)

### Deallocation

1. Compute buddy address: `buddyPA = pa ^ (PageSize << order)`
2. If buddy is free, remove it and merge (repeat at higher order)
3. Insert merged block into free list

### Bootstrap Transition

Early boot uses a bump allocator (`unified_pool.go`). After the system is stable, `TransitionToBuddy()` initializes the buddy allocator with all remaining pages, marking bump-allocated pages as used.

```go
// Called during kmazarin init
kmem.TransitionToBuddy()
```

After transition, `AllocPage()` delegates to `BuddyAlloc(0)`.

### 64MB Kernel Budget

The buddy allocator warns on every allocation that pushes total kernel memory (bootstrap + buddy-allocated) over 64MB (16384 pages):

```
[kmem] WARNING: kernel memory exceeds 64MB (0x4001 pages, bootstrap=0x4C buddy=0x3FB5)
```

## Page Tracking (Top-Half / Bottom-Half)

### Architecture

```
Page Fault Handler          Event Poller          Page Tracking
(nosplit, exception stack)  (goroutine)           Bottom Half
                                                   (goroutine)
        │                        │                      │
        │ QueueDeferredRecord()  │                      │
        ├───────────────────────>│                      │
        │  (lock-free ring buf)  │                      │
        │                        │ PageTrackingPending  │
        │                        ├─────────────────────>│
        │                        │  (channel signal)    │
        │                        │                      │ ProcessDeferredRecords()
        │                        │                      │ → TrackPage()
        │                        │                      │   (static array insert)
```

### Data Structures

**DeferredPageRecord** (queued by top-half, nosplit-safe):
```go
type DeferredPageRecord struct {
    PA       uintptr
    VA       uintptr
    Type     PageAllocType  // KernelHeap, KernelPT, User, UserPT, FileBuffer
    PriestID int16
    ThreadID int16
    Order    uint8
}
```

**PageAllocInfo** (stored by bottom-half in static array):
```go
type PageAllocInfo struct {
    PA       uintptr
    VA       uintptr
    Type     PageAllocType
    PriestID int16
    ThreadID int16
    Order    uint8
}

var pageTracker [32768]PageAllocInfo  // ~768KB, covers up to 128MB
```

### What Gets Tracked

- `HandlePageFault`: kernel heap pages (demand-paged)
- `HandleUserPageFault`: userspace pages (demand-paged)
- `allocPTPage`: page table pages (kernel and user)

## File Loading with Buddy Allocator

ELF files are loaded into buddy-allocated buffers instead of the Go heap:

```go
// ksyscall/launch.go

elfBuf := kmem.AllocBuffer(fileSize)  // Contiguous buddy allocation
defer kmem.FreeBuffer(elfBuf)         // Returned to buddy after ELF processing

elfData := elfBuf.Bytes()             // []byte slice via linear map
file.Read(elfData)                    // Read directly into buddy pages
```

Benefits:
- Buffer is contiguous physical memory accessible via linear map
- `FreeBuffer` returns pages to buddy allocator (merges with buddies)
- No Go heap pressure or GC involvement
- Each ~2.2MB priest ELF uses an order-10 (4MB) block, freed immediately

## walkPageTable and L2 Block Descriptors

`walkPageTable()` in `paging.go` must handle both L2 block descriptors (from the linear map) and L2 table pointers (from 4KB page mappings):

```go
// Check if L2 entry is a block descriptor (2MB) or table pointer
if (l2Entry & 0x2) == 0 {
    // Block descriptor - extract PA directly
    blockPA := uintptr(l2Entry & PTE_ADDR_MASK)
    pageOffset := va & ((1 << L2Shift) - 1)
    return blockPA | pageOffset
}

// Table pointer - walk to L3
l3PA := uintptr(l2Entry & PTE_ADDR_MASK)
// ... continue walk
```

This is critical because `allocPTPage()` allocates from the buddy pool (linear map range), then `mapPage()` calls `walkPageTable()` to translate those VAs. Without the block descriptor check, the walker would misinterpret 2MB block entries as L3 table pointers.

## Memory Overhead

| Structure                | Size     |
|--------------------------|----------|
| BuddyAllocator           | ~250 B   |
| PageAllocInfo array (32K) | ~768 KB  |
| DeferredPageRecord queue  | ~32 KB   |
| Linear map page tables    | ~40 KB   |
| **Total**                 | **~840 KB** |

## Cardinal PTE Creation Sequence

Cardinal creates TTBR1 (kernel) page table entries in two phases, both using direct high-memory mapping (no low-memory staging).

### Phase 1: MMU Init (`mmu_init.go`)

`initMMU()` creates the initial kernel address space:

1. **`initKernelPageTables()`** (line ~392): Allocates TTBR1 L0 and L1 tables, links L1 into L0[511]
2. **`setupKernelStacks()`**: Maps g0 stack and exception stack via `mapKernelPage()` (4KB pages)
3. **`setupEarlyKernelMMIO()`**: Maps UART via `mapKernelPage()`
4. **`enableMMU()`**: Writes TTBR1_EL1, enables MMU

### Phase 2: Kmazarin Loading (`kernel.go`)

`loadAndRunKmazarin()` maps kmazarin and creates the linear map:

1. **Segment mapping** (line ~1437): For each ELF LOAD segment, allocates physical frames via `allocKFrame()` and maps them directly to kmazarin's high-memory VAs via `mapKernelPage()`. No low-memory staging—ELF VAs (0xFFFFFFFF41800000+) are used as-is since bit 63 = 1 means TTBR1.

2. **Linear map** (line ~1623): Calls `createLinearMap()` which iterates physical RAM in 2MB chunks, creating L2 block descriptors (bit[1]=0) at each L2 entry. Skips regions already mapped with 4KB pages (kmazarin segments, stacks, MMIO).

3. **Jump to kmazarin**: After mapping, populates runtime config and enters the kernel.

### `createLinearMap()` (`mmu_mapping.go:72`)

```go
for pa := ramStart; pa < ramEnd; pa += BLOCK_SIZE_2MB {
    va := pa + KernelVAOffset
    // Extract L0[511] → L1[idx] → L2[idx]
    // Allocate L1/L2 tables as needed
    // Create block descriptor: PA | VALID | BLOCK | AF | NORMAL | RW | XN | ISH
    // Skip if L2 entry already valid (preserves 4KB mappings)
}
```

### `createBlockEntry()` (`mmu_mapping.go:47`)

```
Block descriptor bits:
  [1:0]  = 01  (valid + block, NOT table)
  [4:2]  = AttrIdx (normal cacheable)
  [7:6]  = AP (RW at EL1)
  [9:8]  = SH (inner shareable)
  [10]   = AF (access flag)
  [53]   = PXN (privileged execute-never)
  [54]   = UXN (unprivileged execute-never)
  [47:21] = PA[47:21] (2MB-aligned physical address)
```

## Related Files

| File | Purpose |
|------|---------|
| `cardinal/main/mmu_init.go` | TTBR1 L0/L1 allocation, `mapKernelPage()`, MMU enable |
| `cardinal/main/mmu_mapping.go` | Linear map creation with 2MB blocks |
| `cardinal/main/mmu_constants.go` | PTE flag definitions |
| `cardinal/main/kernel.go` | Kmazarin segment mapping, linear map call |
| `kmazarin/kmem/buddy.go` | Buddy allocator, buffer allocation |
| `kmazarin/kmem/unified_pool.go` | Bump allocator (early boot), transition to buddy |
| `kmazarin/kmem/page_tracker.go` | PageAllocInfo tracking, memory stats |
| `kmazarin/kmem/deferred.go` | Lock-free queue for top-half to bottom-half |
| `kmazarin/kmem/paging.go` | Page fault handlers, walkPageTable |
| `kmazarin/kmazarin/bottom_half.go` | Event poller, page tracking goroutine |
| `kmazarin/ksyscall/launch.go` | ELF loading with buddy buffers |
