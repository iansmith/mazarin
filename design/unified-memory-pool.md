# Unified Memory Pool Design

## Overview

Kmazarin uses a **unified memory pool** that serves all page allocation needs from a single bump allocator. This replaces the original design which had four separate pools (kernel frame pool, kernel PT pool, userspace frame pool, userspace PT pool).

## Architecture

### Single Pool, Multiple Accounting Categories

```
┌─────────────────────────────────────────────────────────────────────┐
│                    UNIFIED MEMORY POOL                              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│   Physical Memory Layout (8GB RAM example):                         │
│                                                                     │
│   0x40000000  ┌──────────────────────────────────────────────────┐ │
│               │  DTB (1 MB)                                      │ │
│   0x40100000  ├──────────────────────────────────────────────────┤ │
│               │  Cardinal (15 MB)                                │ │
│   0x41000000  ├──────────────────────────────────────────────────┤ │
│               │  VirtIO GPU Framebuffer (32 MB)                  │ │
│   0x43000000  ├──────────────────────────────────────────────────┤ │
│               │  Page Tables (8 MB)                              │ │
│   0x43800000  ├──────────────────────────────────────────────────┤ │
│               │  Kmazarin ELF (~2.2 MB)                          │ │
│   ~0x43A00000 ├──────────────────────────────────────────────────┤ │
│               │                                                  │ │
│               │  UNIFIED POOL                                    │ │
│               │  (bump allocator, ~8GB)                          │ │
│               │                                                  │ │
│               │  ┌─ Pre-mapped Bootstrap Region (32 MB) ───────┐ │ │
│               │  │  Pages 0-8191 mapped at boot by Cardinal    │ │ │
│               │  │  Used for kernel heap + PT allocations      │ │ │
│               │  └─────────────────────────────────────────────┘ │ │
│               │                                                  │ │
│               │  Remaining pages: demand-paged as needed         │ │
│               │                                                  │ │
│   0x240000000 └──────────────────────────────────────────────────┘ │
│               (End of 8GB RAM)                                     │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Page Type Accounting

All allocations come from the same pool but are tracked by purpose:

| PageType | Description | Example Uses |
|----------|-------------|--------------|
| `PageKernelHeap` | Kernel heap allocations | Go runtime heap, ELF file buffers |
| `PageKernelPT` | Kernel page table pages | L0/L1/L2/L3 tables for kernel mappings |
| `PageUser` | Userspace data pages | Process code, data, bss, stack |
| `PageUserPT` | Userspace page table pages | Per-process page tables |

### Why Unified?

The original four-pool design required:
1. Pre-calculating pool sizes at boot
2. Complex RuntimeConfig with multiple start/end addresses
3. Potential waste if one pool exhausted while another had space

The unified design:
1. Single bump allocator - simple and fast
2. No pool sizing decisions
3. All memory available to whoever needs it
4. Accounting tracks usage for debugging

## Implementation

### Core Data Structure

```go
// kmazarin/kmem/unified_pool.go

type UnifiedPagePool struct {
    // Bump allocator state
    next        uintptr // Next page to allocate (PA)
    end         uintptr // End of pool (exclusive, PA)
    initialNext uintptr // Initial value for stats

    // Accounting by type
    kernelHeapPages uint64
    kernelPTPages   uint64
    userPages       uint64
    userPTPages     uint64

    // Soft limit for kernel allocations
    kernelSoftLimit uint64  // Default: 16384 pages (64MB)

    // Protection
    lock        Spinlock
    initialized uint32
}
```

### Allocation API

```go
// AllocPage allocates a single page from the unified pool.
// The pageType parameter is used for accounting only.
// Returns physical address, or 0 if pool exhausted.
func AllocPage(pageType PageType) uintptr
```

Key characteristics:
- **Returns physical address** - caller must handle VA mapping
- **Page is NOT zeroed** - caller zeros if needed
- **Thread-safe** via spinlock
- **Soft limit warning** for kernel allocations (doesn't fail, just warns)

### The Bootstrap Problem

When `allocPTPage()` allocates a page table page, it needs to:
1. Get a physical page from the pool
2. Zero the page before use
3. Return the page's virtual address

But to zero the page, we need to access it via VA. And that VA might not be mapped yet!

**Solution: Pre-mapped Bootstrap Region**

Cardinal pre-maps the first N pages of the unified pool before jumping to kmazarin:

```go
// cardinal/main/kernel.go

func premapUnifiedPoolBootstrap() {
    const bootstrapPages = 8192 // 32MB pre-mapped

    for i := 0; i < bootstrapPages; i++ {
        pa := unifiedPoolStart + i*PAGE_SIZE
        va := pa + KernelVAOffset
        mapKernelPage(va, pa, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
    }
}
```

This ensures the first 8192 pages (~32MB) can be accessed immediately after allocation.

### Memory Budget Calculation

For loading N userspace processes, approximate page requirements:

| Category | Pages per Process | Notes |
|----------|-------------------|-------|
| ELF file buffer | ~565 | `ReadAll()` into kernel heap (NOT freed!) |
| User frame pages | ~423 | Process code/data/bss/stack |
| Page table pages | ~29 | L0+L1+L2+L3 per process |
| **Total per process** | **~1017** | |

Plus initial kernel overhead: ~97 pages

**Example: 4 processes**
```
Bootstrap requirement = 97 + (4 × 1017) = 4165 pages (~16 MB)
```

The 8192 page (32MB) bootstrap provides ~2x headroom.

## GC and Memory Reclamation

### Current State: GC Disabled

Go's garbage collector is currently disabled in kmazarin:

```go
// kmazarin/kmazarin/main.go
debug.SetGCPercent(-1)
```

**Reason:** Go runtime's tagged pointer code (`runtime/tagptr_64bit.go`) assumes 49-bit virtual addresses. Kmazarin uses full 64-bit TTBR1 addresses (`0xFFFFFFFF...`), causing GC to produce "bad sweepgen in refill" errors.

### Implications

Without GC:
- Kernel heap allocations are never freed
- ELF file buffers (~565 pages each) accumulate
- Memory usage only grows
- Bootstrap region must be sized for peak usage

### Future Fix

To enable GC, patch Go's `runtime/tagptr_64bit.go` to handle TTBR1 kernel addresses. This would allow:
- Automatic ELF buffer reclamation after process load
- Reduced bootstrap region requirement
- Normal Go memory management

## Virtual Address Mapping

### Kernel VA Offset

Physical addresses are converted to kernel virtual addresses by adding a fixed offset:

```
KernelVAOffset = 0xFFFFFFFF00000000

Physical 0x44242000 → Kernel VA 0xFFFFFFFF44242000
```

### Page Table Page Access

When `allocPTPage()` allocates a new page table page:

```go
func allocPTPage() uintptr {
    pa := AllocPage(PageKernelPT)
    va := pa + KernelVAOffset

    // Zero the page (requires VA to be mapped!)
    ptr := (*[512]uint64)(unsafe.Pointer(va))
    for i := 0; i < 512; i++ {
        ptr[i] = 0
    }

    return va
}
```

This works because:
1. Pool pages are allocated sequentially (bump allocator)
2. First 8192 pages are pre-mapped by Cardinal
3. As long as total PT allocations stay under 8192 pages, no fault occurs

### User Page Mapping

User pages are mapped into TTBR0 space (low addresses) with appropriate permissions:

```go
func mapUserPage(va, pa uintptr, elfFlags uint32) bool {
    // Walk TTBR0 page tables
    // Create L3 entry with EL0-accessible permissions
    // VA is in range 0x0000000000000000 - 0x0000FFFFFFFFFFFF
}
```

## RuntimeConfig Integration

Cardinal populates `RuntimeConfig` with pool information:

```go
type RuntimeConfig struct {
    // ... other fields ...

    // Frame pool (unified pool physical boundaries)
    FramePoolStart uint64
    FramePoolEnd   uint64

    // Legacy fields (still populated for compatibility)
    KernelPTPoolStart       uint64
    KernelPTPoolEnd         uint64
    UserspaceFramePoolStart uint64
    UserspaceFramePoolEnd   uint64
    UserspacePTPoolStart    uint64
    UserspacePTPoolEnd      uint64
}
```

The unified pool initializer checks for explicit unified pool fields first, then falls back to computing from legacy fields.

## Pool Statistics

Runtime statistics are available for debugging:

```go
stats := kmem.GetPoolStats()
// stats.TotalPages      - Total pages in pool
// stats.AllocatedPages  - Pages allocated so far
// stats.RemainingPages  - Pages still available
// stats.KernelHeapPages - Pages used for kernel heap
// stats.KernelPTPages   - Pages used for kernel page tables
// stats.UserPages       - Pages used for userspace
// stats.UserPTPages     - Pages used for userspace page tables
```

Example output from boot:
```
[kmem] Pool stats:
  Total:       0x00000000001FBD8E pages  (8112 MB)
  Allocated:   0x0000000000000061 pages  (388 KB)
  Remaining:   0x00000000001FBD2D pages
  Kernel heap: 0x0000000000000056 pages
  Kernel PT:   0x000000000000000B pages
  User:        0x0000000000000000 pages
  User PT:     0x0000000000000000 pages
```

## Related Files

- `kmazarin/kmem/unified_pool.go` - Pool implementation
- `kmazarin/kmem/paging.go` - Page table management, `allocPTPage()`
- `cardinal/main/kernel.go` - `premapUnifiedPoolBootstrap()`
- `cardinal/constants/layout.go` - Memory layout constants
- `kmazarin/kmazarin/runtime_config.go` - RuntimeConfig structure
