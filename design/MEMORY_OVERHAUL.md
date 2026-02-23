# Kmazarin Memory Management Overhaul

## Why This Change

Kmazarin's current memory management is a patchwork:

- Pages allocated during priest lifetime are **never reclaimed** when the priest exits
- `munmap` explicitly leaks frames ("memory leak, but prevents use-after-free")
- Page types are incomplete — framebuffer, kernel stacks, MMIO, shared IPC pages are untracked
- No per-priest ownership of physical frames, so cleanup is impossible
- Hardware accessed/dirty bits are never read, making future page replacement impossible
- Memory layout addresses are a mix of hardcoded constants and derived values
- The buddy allocator exists but coexists awkwardly with a bump allocator

This overhaul establishes proper page accounting, enables priest cleanup, and lays the groundwork for page replacement (swap) and microkernel shared memory (IPC page transfer).

## Objectives

1. Every physical page is accounted for with a type and owner
2. `PrintPageStats()` shows per-type, per-priest page counts at any time
3. Architecture-independent memory management — kmazarin code never checks what arch it's on
4. Exact page cleanup when a priest exits (all frames returned to pool)
5. Stubs for swap-out/swap-in with correct parameters
6. Hardware accessed/dirty bits readable through platform abstraction
7. Buddy allocator is the sole allocation path (bump only for bootstrap)
8. Shared memory primitives for microkernel IPC (Stage 6)

## Non-Objectives (Explicitly Out of Scope)

- Actual swap-to-disk implementation (stubs only)
- Clock/LRU page replacement algorithm implementation (framework only)
- Eliminating the `relocate-kmazarin` tool (Go can't link at 64-bit addresses)
- Changing userspace load addresses (Linux-compatible addresses stay)
- Multi-core / SMP support changes

## Key Design Decision: Linux-Style Page Tracking

Linux does NOT maintain a per-process list of owned physical pages. Instead, it uses:

1. **Per-frame metadata array** (`struct page`): A flat array indexed by PFN (physical frame number). Each entry has: refcount, flags, owner/mapping pointer. This is O(1) lookup given any PA.

2. **VMA-style region tracking** (`struct vm_area_struct`): Per-process sorted list of virtual memory regions (start VA, end VA, permissions). We already have `LockedSpanGroup` which serves this role.

3. **Page table walking for cleanup**: On process exit, Linux walks the VMA list. For each VMA, it walks the page tables in that address range, finds every valid PTE, decrements the page's refcount, and frees pages whose refcount reaches 0. It does NOT walk a separate "owned pages" list.

4. **Single reference count per frame**: `_refcount` tracks all holders. When it hits 0, the page is returned to the buddy allocator. Shared pages simply have refcount > 1.

**We adopt this design.** Specifically:
- `PageDescriptor[]` array indexed by `(PA - poolStart) >> 12` — our `struct page` equivalent
- `Priest.Spans` (LockedSpanGroup) — our VMA list (already exists)
- On priest exit: walk Spans + page tables, not a flat page list
- Single `RefCount` in `PageDescriptor` — no mapcount/refcount split (that's a Linux optimization for COW detection at scale)

**Why not a per-priest page list?** Maintaining a list on every map/unmap is expensive and error-prone in nosplit code. Walking page tables is hierarchical — empty ranges have no leaf tables, so cleanup is fast. The VMA list tells us which ranges to walk (typically 20-200 regions per priest).

---

## Current State Summary

### Files That Matter

| File | Role |
|------|------|
| `kmazarin/kmem/paging.go` | Shared paging: `mapPage`, `HandlePageFault`, `HandleUserPageFault`, `WalkPageTable`, `CreateProcessPageTable` |
| `kmazarin/kmem/paging_arm64.go` | ARM64: PTE construction, `walkPageTable`, `initProcessL0` (no-op), `MapDeviceMMIO` |
| `kmazarin/kmem/paging_amd64.go` | x86_64: PTE construction, `walkPageTable`, `initProcessL0` (copy PML4[256-511]), `MapDeviceMMIO` (no-op) |
| `kmazarin/kmem/paging_riscv64.go` | RISC-V: PTE construction, `walkPageTable`, `initProcessL0` (copy L3[256-511]), `MapDeviceMMIO` (no-op) |
| `kmazarin/kmem/unified_pool.go` | Bump allocator + `PageType` enum + `AllocPage()` |
| `kmazarin/kmem/buddy.go` | Buddy allocator (orders 0-12, 4KB to 16MB) |
| `kmazarin/kmem/frame.go` | `AllocKernelFrame()`, `AllocUserFrame()` wrappers |
| `kmazarin/kmem/page_tracker.go` | Diagnostic-only page tracking (32K entries, per-priest counts) |
| `kmazarin/kmem/deferred.go` | Lock-free ring buffer for top-half → bottom-half page records |
| `kmazarin/kmem/dma.go` | `AllocDriverPage()` with device memory attributes |
| `kmazarin/proc/proc.go` | `Priest` struct: PID, BumpPointer, Spans, ThreadCount |
| `kmazarin/proc/span.go` | `LockedSpanGroup` — per-priest VA range tracking (256 spans max) |
| `kmazarin/kmazarin/threads.go` | Thread/priest lifecycle: `createUserspaceThreadImpl`, `ThreadExit`, `releasePriestSchedLockHeld` |
| `kmazarin/ksyscall/launch.go` | ELF loading, `CreateProcessPageTable()`, page table switch |
| `kmazarin/ksyscall/mmap.go` | Per-priest bump allocator for userspace VAs |
| `kmazarin/ksyscall/munmap.go` | Unmaps pages but **leaks frames** |
| `shared/constants/auxv.go` | 17+ auxv entries diplomat passes to kmazarin |
| `shared/constants/layout.go` | Physical memory layout (DTB, Cardinal, Framebuffer, PageTables, Kmazarin) |
| `shared/constants/addresses.go` | Kernel VA constants (KernelMMIOOffset, KernelVABase, stacks) |
| `shared/constants/addresses_{arm64,amd64,riscv64}.go` | Arch-specific heap start/end |
| `diplomat/main/kernelvm_{arm64,amd64,riscv64}.go` | Diplomat page table setup per arch |
| `diplomat/main/config.go` | TOML parser for `/EFI/Linux/kmazarin.toml` |

### Current Page Types (Incomplete)

```
PageKernelHeap (0), PageKernelPT (1), PageUser (2),
PageUserPT (3), PageFileBuffer (4), PageDriver (5)
```

### What's Missing from Priest Cleanup

When `releasePriestSchedLockHeld()` runs (last thread exits):
- TLB shootdown: **done** (`TlbiASIDE1IS`)
- Zero priest struct: **done** (memset)
- Release priest ID: **done** (`priestIdAllocator.Release`)
- Walk page tables and free frames: **NOT DONE**
- Free page table pages themselves: **NOT DONE**
- Free shared/IPC pages (refcount decrement): **NOT DONE**
- Return frames to buddy allocator: **NOT DONE**

### RISC-V Diplomat Constraints

RISC-V diplomat is hand-rolled (no UEFI). Key differences:
- Loaded by OpenSBI via `-kernel` flag at `0x80200000`
- Uses hardcoded bump allocator (not UEFI `AllocatePages`)
- Page tables built in assembly (`entry_riscv64.s`) with hardcoded PT pool at `0x81200000`
- VirtIO block driver embedded in diplomat (hand-rolled MMIO protocol)

**Safe to change on RISC-V:** auxv entries, pool sizes, memory layout constants, PTE flags in Go code.
**Risky to change on RISC-V:** Assembly entry point, VirtIO block driver, hardcoded addresses in `entry_riscv64.s`.

---

## Implementation Stages

Each stage ends with a checkpoint: build and run on all 3 architectures, verify same behavior as before (or known/expected differences).

---

### Stage 0: Platform Abstraction Layer

**Goal:** Define arch-independent functions for page table operations. No behavioral changes. System boots and runs identically after this stage.

**Rationale:** All subsequent stages need to manipulate PTEs without knowing the architecture. Today the code uses arch-specific PTE constants directly (e.g., `PTE_VALID` on ARM64, `X86_PTE_PRESENT` on x86_64, `RV_PTE_V` on RISC-V). We need a translation layer.

**IMPORTANT: No interfaces.** Many of these functions are called from `//go:nosplit` exception handlers. Go interfaces use dynamic dispatch which is not nosplit-safe. We continue using build-tag polymorphism (same function name, different file per arch).

#### New Files

**`kmazarin/kmem/pte_flags.go`** (shared, all arches):
```go
// PTEFlags is an architecture-neutral representation of page table entry attributes.
// Each platform's paging_*.go translates to/from native PTE format.
type PTEFlags uint64

const (
    PF_Valid    PTEFlags = 1 << 0  // Entry is present/valid
    PF_Write    PTEFlags = 1 << 1  // Writable
    PF_User     PTEFlags = 1 << 2  // User-accessible (EL0/ring 3)
    PF_Execute  PTEFlags = 1 << 3  // Executable
    PF_Accessed PTEFlags = 1 << 4  // Hardware accessed bit
    PF_Dirty    PTEFlags = 1 << 5  // Hardware dirty bit
    PF_Device   PTEFlags = 1 << 6  // Device/uncacheable memory
    PF_Global   PTEFlags = 1 << 7  // Global (not ASID-tagged)
    PF_Pinned   PTEFlags = 1 << 8  // Software: do not evict
    PF_Shared   PTEFlags = 1 << 9  // Software: shared between priests
)
```

#### New Platform Functions (one implementation per arch file)

Add to each `paging_{arm64,amd64,riscv64}.go`:

```go
// platformPTEToFlags converts a native PTE to arch-neutral PTEFlags.
// Called by page walking code to return flags the caller can inspect
// without knowing the architecture.
//go:nosplit
func platformPTEToFlags(pte uint64) PTEFlags

// platformFlagsToUserPTE converts arch-neutral PTEFlags to a native leaf PTE
// for userspace pages. pa is the physical address to encode.
//go:nosplit
func platformFlagsToUserPTE(pa uintptr, flags PTEFlags) uint64

// platformFlagsToKernelPTE converts arch-neutral PTEFlags to a native leaf PTE
// for kernel pages.
//go:nosplit
func platformFlagsToKernelPTE(pa uintptr, flags PTEFlags) uint64

// platformReadPTEAt reads the raw PTE value at a given VA by walking the
// page table. Returns (pte, level, ok). level indicates which table level
// the leaf was found at (3 = 4KB page, 2 = 2MB block, 1 = 1GB block).
//go:nosplit
func platformReadPTEAt(va uintptr) (pte uint64, level int, ok bool)

// platformWritePTEAt writes a new PTE value at the leaf entry for va.
// Used for updating flags (accessed/dirty clear, permission changes).
// Returns false if va is not currently mapped.
//go:nosplit
func platformWritePTEAt(va uintptr, newPTE uint64) bool

// platformClearAccessed clears the accessed bit on the PTE for va
// and returns whether it was set. Used by page replacement scanning.
//go:nosplit
func platformClearAccessed(va uintptr) (wasAccessed bool)

// platformClearDirty clears the dirty bit on the PTE for va
// and returns whether it was set. Used before swap-out.
//go:nosplit
func platformClearDirty(va uintptr) (wasDirty bool)

// platformFlushTLBPage invalidates TLB entries for a single VA.
//go:nosplit
func platformFlushTLBPage(va uintptr)

// platformFlushTLBASID invalidates all TLB entries for a given ASID.
//go:nosplit
func platformFlushTLBASID(asid uint16)
```

#### Implementation Notes Per Architecture

**ARM64** (`paging_arm64.go`):
- `PF_Valid` ↔ bit 0 (VALID)
- `PF_Write` ↔ AP bits [7:6] (AP_RW_ALL vs AP_RO_ALL)
- `PF_User` ↔ AP bits (EL0 access) + NG bit (non-global for ASID)
- `PF_Execute` ↔ inverse of PXN/UXN bits [53:54]
- `PF_Accessed` ↔ AF bit [10] (must be set to 1 for valid pages; HW sets it)
- `PF_Dirty` ↔ DBM (dirty bit modifier) or software bit if HW DBM not used
- `PF_Device` ↔ MAIR index (AttrIdx bits [4:2])
- `PF_Global` ↔ inverse of NG bit [11]
- `platformFlushTLBPage` → `tlbiVAE1IS(va)` + `dsbSY()` + `isbSY()`
- `platformFlushTLBASID` → `tlbiASIDE1IS(asid)` (already exists)

**NOTE on ARM64 Accessed/Dirty:** ARM64 with Access Flag (AF) management by hardware requires `TCR_EL1.HA=1` and `TCR_EL1.HD=1`. Check whether diplomat sets these. If not, AF must be set by software on mapping (it already is — `PTE_AF = 1 << 10` is always set). For dirty tracking, ARM64 can use the AP bit trick: map page as read-only initially, take a permission fault on first write, then set dirty and make writable. Alternatively, if HW dirty bit management is available (ARMv8.1+), use it. **Start with software-managed dirty (AP bit trick) for portability.**

**x86_64** (`paging_amd64.go`):
- `PF_Valid` ↔ bit 0 (PRESENT)
- `PF_Write` ↔ bit 1 (RW)
- `PF_User` ↔ bit 2 (USER)
- `PF_Execute` ↔ inverse of bit 63 (NX)
- `PF_Accessed` ↔ bit 5 (A)
- `PF_Dirty` ↔ bit 6 (D)
- `PF_Device` ↔ PCD/PWT bits or PAT
- `PF_Global` ↔ bit 8 (G)
- `platformFlushTLBPage` → `invlpg` instruction (need asm stub)
- `platformFlushTLBASID` → full TLB flush via CR3 reload (x86 has no per-ASID invalidate without INVPCID)

**RISC-V** (`paging_riscv64.go`):
- `PF_Valid` ↔ bit 0 (V)
- `PF_Write` ↔ bit 2 (W)
- `PF_User` ↔ bit 4 (U)
- `PF_Execute` ↔ bit 3 (X)
- `PF_Accessed` ↔ bit 6 (A)
- `PF_Dirty` ↔ bit 7 (D)
- `PF_Device` ↔ (no hardware bit; use PMA or just normal mapping)
- `PF_Global` ↔ bit 5 (G)
- `platformFlushTLBPage` → `sfence.vma va, zero` (need asm stub)
- `platformFlushTLBASID` → `sfence.vma zero, asid`

#### Existing Functions to Wrap

The existing `make*PTE()` functions stay as internal helpers. The new `platform*` functions call them. Existing callers are migrated gradually in later stages.

#### Verification

```bash
$GO tool task run          # ARM64 boots, same output
$GO tool task run-x86_64   # x86_64 boots, same output
$GO tool task run-riscv64  # RISC-V boots, same output
```

No behavioral change. New functions exist but aren't called from mainline paths yet.

---

### Stage 1: Expanded Page Types + PageDescriptor + Per-Priest Ownership

**Goal:** Every page allocated anywhere in kmazarin gets a `PageDescriptor` with a type, owner, and refcount. Per-priest page lists enable cleanup.

#### Expand PageType Enum

In `kmazarin/kmem/unified_pool.go`, replace current enum:

```go
type PageType uint8

const (
    // Kernel pages (owner = PriestID 0)
    PageKernelText     PageType = iota  // Kernel .text (mapped by diplomat, read-only+exec)
    PageKernelROData                     // Kernel .rodata (mapped by diplomat, read-only)
    PageKernelData                       // Kernel .data/.bss (mapped by diplomat, read-write)
    PageKernelHeap                       // Kernel heap (demand-paged)
    PageKernelStack                      // Kernel g0 + exception stacks
    PageKernelPT                         // Kernel page table pages
    PageKernelMMIO                       // MMIO device pages mapped to kernel VA
    PageFramebuffer                      // VirtIO GPU framebuffer
    PageVirtIOQueue                      // VirtIO descriptor/avail/used rings

    // Userspace pages (owner = PriestID 1-31)
    PageUserText                         // ELF .text segments
    PageUserROData                       // ELF .rodata segments
    PageUserData                         // ELF .data/.bss segments
    PageUserHeap                         // Userspace heap (mmap, demand-paged)
    PageUserStack                        // Userspace thread stacks
    PageUserPT                           // Per-process page table pages

    // Shared / IPC pages
    PageSharedIPC                        // Pages transferred between priests
    PageFileBuffer                       // File I/O streaming buffers
    PageBackingStore                     // Display backing store (dapope)

    // Driver pages
    PageDriver                           // Driver DMA pages (non-cacheable)

    // Sentinel
    PageTypeCount                        // Must be last
)
```

Add `String()` method for each type (for `PrintPageStats`).

#### New PageDescriptor

Replace the existing `PageAllocInfo` in `page_tracker.go` with a richer descriptor:

```go
// PageDescriptor tracks the ownership and state of every allocated physical page.
// Stored in a flat array indexed by (PA - poolStart) / PageSize.
type PageDescriptor struct {
    PA       uintptr    // Physical address of this page
    Type     PageType   // What the page is used for
    Owner    PriestId   // 0 = kernel, 1-31 = priest
    RefCount int16      // >1 for shared pages (IPC, framebuffer)
    Order    uint8      // Buddy order (0 = single page, etc.)
    Flags    uint8      // PD_PINNED, PD_DIRTY, PD_SWAPPED_OUT
}

const (
    PD_PINNED     = 1 << 0  // Do not evict (kernel pages, MMIO, page tables)
    PD_DIRTY      = 1 << 1  // Software dirty tracking (mirrors HW dirty)
    PD_SWAPPED    = 1 << 2  // Page is swapped out (PA invalid, swap slot stored elsewhere)
    PD_SHARED     = 1 << 3  // Page mapped in multiple address spaces
)
```

#### PageDescriptor Storage (Linux `struct page` Equivalent)

The current `pageTracker[32768]` array is diagnostic and uses linear search. Replace with a **direct-indexed array** — the Linux approach where `struct page` is indexed by PFN:

```go
// Indexed by (PA - unifiedPoolStart) >> 12 (the PFN within our pool)
// For a 512MB pool, this is 131072 entries × 16 bytes = 2MB
// Allocated from the pool itself during InitUnifiedPool()
var pageDescriptors []PageDescriptor  // slice backed by pool memory
```

**O(1) lookup**: Given any PA in the pool, `pageDescriptors[(pa - poolStart) >> 12]` gives the descriptor. No search needed. This is exactly how Linux's `mem_map[]` / `vmemmap[]` works.

**Bootstrap problem:** We need pages to store `pageDescriptors` before the buddy allocator is ready. Solution: the bump allocator allocates the descriptor array first (before any other allocation), then hands the remaining pool to the buddy allocator.

#### Per-Priest Page Tracking: VMA Walk, Not Page List

**Following the Linux model**, we do NOT maintain a per-priest list of owned physical pages. Instead:

1. **`PageDescriptor.Owner`** records which priest owns each frame (set at allocation time)
2. **`Priest.Spans` (LockedSpanGroup)** records the VA regions mapped for this priest (already exists)
3. **On priest exit**: Walk Spans to find VA ranges → walk page tables within those ranges → for each valid PTE, look up `PageDescriptor` by PA, decrement `RefCount`, free if zero
**How the pieces fit together:**
- `PageDescriptor[PFN]` — O(1) lookup of type/owner/refcount for any page given its PA. Updated at alloc and free time. This is a flat array indexed by `(PA - poolStart) >> 12` — pure arithmetic, no hash tables, no allocations.
- VMA + PT walk — used at priest exit to **discover which PAs to release**. Hierarchical walk skips empty ranges.
- `PrintPageStats()` — walks the `PageDescriptor` array (linear scan, ~131K entries for 512MB pool). This is a rare diagnostic query and the scan costs microseconds. **No per-alloc counter overhead on the hot path.**

**Critical: No Go maps anywhere in kmem.** Go's `map` type heap-allocates and can trigger GC/stack growth. It is completely unsafe in `//go:nosplit` code. All data structures in `kmazarin/kmem/` must be flat arrays, fixed-size structs, and index arithmetic. No maps, no dynamic slice growth in nosplit paths.

**Enhancement to Spans for cleanup:** Currently Spans only tracks mmap'd regions. For complete cleanup, we also need to track:
- ELF segment regions (code, data, rodata) — add spans during ELF loading
- Thread stack regions — add spans during thread creation
- The page table L0 page itself — tracked separately in `Priest.PageTableL0PA`

Add to `Priest` struct:
```go
type Priest struct {
    // ... existing fields ...
    PageTableL0PA uintptr  // L0 page table PA (needed for IPC and cleanup)
}
```

#### Update All Allocation Call Sites

Every place that calls `AllocPage()`, `AllocKernelFrame()`, `AllocUserFrame()`, `AllocContiguousPages()`, or `AllocDriverPage()` must now:
1. Pass the correct expanded `PageType`
2. Pass the `PriestId` of the owner (0 for kernel)
3. The allocator creates a `PageDescriptor` and adds the PA to the priest's page list

**Call sites to update** (non-exhaustive, search for `AllocPage\|AllocKernelFrame\|AllocUserFrame\|AllocContiguous\|AllocDriverPage`):

| Call Site | Current Type | New Type | Owner |
|-----------|-------------|----------|-------|
| `HandlePageFault` (kernel heap) | `PageKernelHeap` | `PageKernelHeap` | 0 (kernel) |
| `HandleUserPageFault` (user demand page) | `PageUser` | `PageUserHeap` | current priest PID |
| `allocPTPage` (kernel PT) | `PageKernelPT` | `PageKernelPT` | 0 |
| `allocPTPage` (user PT, in `mapUserPageWithL0`) | `PageUserPT` | `PageUserPT` | current priest PID |
| `AllocDriverPage` (DMA) | `PageDriver` | `PageDriver` or `PageVirtIOQueue` | 0 |
| `MapFramebuffer` | (untracked) | `PageFramebuffer` | 0 (kernel) |
| ELF loader `.text` pages | `PageUser` | `PageUserText` | loading priest PID |
| ELF loader `.data` pages | `PageUser` | `PageUserData` | loading priest PID |
| ELF loader `.rodata` pages | `PageUser` | `PageUserROData` | loading priest PID |
| Thread stack allocation | `PageUser` | `PageUserStack` | priest PID |
| File buffer allocation | `PageFileBuffer` | `PageFileBuffer` | 0 (kernel) |

#### PrintPageStats()

New function in `page_tracker.go` (or new `page_stats.go`):

```go
func PrintPageStats() {
    // Print header
    // For each PageType:
    //   Count total pages, total bytes
    //   Break down by priest
    // Print per-priest totals
    // Print grand total and pool utilization
}
```

Called from:
- Boot (after all init complete) — baseline stats
- Priest exit — verify cleanup
- On demand via a debug syscall or timer

#### Verification

```bash
$GO tool task run TIMEOUT=15
$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log
# Expect: page stats printed at boot showing correct counts per type
```

---

### Stage 2: Buddy Allocator as Sole Allocator

**Goal:** Remove the bump allocator from the hot path. Only the first N pages (needed to bootstrap the buddy allocator's own data structures + the PageDescriptor array) use the bump allocator. Everything else goes through buddy.

#### Changes to `unified_pool.go`

```go
func InitUnifiedPool() {
    // 1. Bump-allocate the PageDescriptor array
    //    Size = (poolEnd - poolStart) / PageSize * sizeof(PageDescriptor)
    //    Typically ~2MB for 512MB pool

    // 2. Bump-allocate bootstrap pages for buddy allocator metadata
    //    (free list heads, etc.) — typically < 1 page

    // 3. Initialize buddy allocator with remaining pool
    //    TransitionToBuddy() but done immediately, not deferred

    // 4. Mark bump-allocated pages as PageKernelHeap, owner=0, pinned

    // 5. buddyReady = true
}

func AllocPage(pageType PageType, owner PriestId) uintptr {
    // Always go through buddy (panic if called before InitUnifiedPool)
    return BuddyAllocTyped(0, pageType, owner)
}
```

#### Update BuddyAllocTyped / BuddyFreeTyped

Add `owner PriestId` parameter:

```go
func BuddyAllocTyped(order int, pageType PageType, owner PriestId) uintptr {
    pa := buddyAlloc(order)
    if pa == 0 { return 0 }

    // Create PageDescriptor (flat array write, no allocation, nosplit-safe)
    idx := (pa - poolStart) >> 12
    pageDescriptors[idx] = PageDescriptor{
        PA: pa, Type: pageType, Owner: owner,
        RefCount: 1, Order: uint8(order),
    }

    // No per-alloc counters. Stats are computed by scanning pageDescriptors
    // on demand (rare diagnostic query). This keeps the hot path minimal.

    return pa
}

func BuddyFreeTyped(pa uintptr, order int) {
    idx := (pa - poolStart) >> 12
    desc := &pageDescriptors[idx]

    if desc.RefCount > 1 {
        desc.RefCount--
        return  // Still referenced by another priest, don't free
    }

    // Clear descriptor
    *desc = PageDescriptor{}

    // Return to buddy free list
    buddyFree(pa, order)
}
```

#### Verification

```bash
$GO tool task run TIMEOUT=15
# Boot should work identically
# Page stats should show same totals as Stage 1
# Buddy free list stats should show remaining pool capacity
```

---

### Stage 3: Memory Layout Rationalization

**Goal:** Derive memory layout from RAM size and platform constants. Remove gratuitous hardcoding. Keep it conservative — make the aggressive version optional.

#### Conservative Changes (Required)

1. **Physical layout constants in `layout.go`** — currently hardcoded sizes that compute addresses:
   ```go
   DtbSize              = 0x100000   // 1MB — keep as-is, reasonable
   CardinalAllocationSize = 0xF00000 // 15MB — keep as-is
   FramebufferSize      = 0x2000000  // 32MB — keep as-is (1920×1080×4 = 8MB, room to grow)
   PageTableSize        = 0x800000   // 8MB — keep as-is
   ```
   These are fine. They compute `KmazarinLoadAddr` correctly.

2. **New: Compute pool sizes from RAM size.** Currently diplomat computes unified pool boundaries from what's left after loading kmazarin. This is already dynamic. Verify it works for different RAM sizes (test with `-m 512M`, `-m 1G`, `-m 2G`).

3. **New: Add RAM size to auxv.** `AT_RAM_BASE` (0x100D) and `AT_RAM_SIZE` (0x100E) already exist. Verify kmazarin reads them and uses them for pool sizing validation.

4. **Kernel heap bounds.** Currently different per arch:
   - ARM64: `0xFFFF000100000000` — `0xFFFF100000000000` (16TB)
   - x86_64: `0xFFFF800100000000` — `0xFFFF900000000000` (1TB)
   - RISC-V: `0xFFFFFFC100000000` — `0xFFFFFFD000000000` (60GB)

   These MUST differ because each arch has different canonical address space layouts. The key constraint is that the heap range must be in the kernel half of the address space and not overlap with the linear map or kernel code. **Keep these as-is but document why they differ.** The shared code should use `constants.KernelHeapStart` / `constants.KernelHeapEnd` (which are already build-tag selected).

5. **New: `mazarin.toml` extensions.** The TOML parser already exists in `diplomat/main/config.go`. Add optional overrides:
   ```toml
   # Override framebuffer size (default 32MB)
   framebuffer_mb = 64

   # Override page table pool size (default 8MB)
   page_table_mb = 16

   # Override kernel memory budget warning threshold (default 64MB)
   kernel_budget_mb = 128
   ```

   If not specified in TOML, use defaults. This is the escape hatch for future growth.

#### Aggressive Changes (Optional, do after everything else works)

6. **Evaluate whether `KmazarinLoadAddr` should be dynamic.** Currently fixed at `PageTableEnd` = `0x43800000` for ARM64. On x86_64 and RISC-V, different addresses are used. Since the relocate-kmazarin tool and diplomat ELF loader both use this constant, changing it requires coordinated updates. **Defer unless a concrete problem arises.**

7. **Evaluate unifying kernel heap start across arches.** Would require choosing a VA range that's valid in all three canonical address spaces. The current per-arch values are fine and well-motivated.

#### RISC-V Impact Assessment

- Changes 1-4: **Safe.** Only modify constants and auxv handling in Go code.
- Change 5: **Safe.** TOML parsing is shared code. RISC-V diplomat reads the same TOML file from the FAT32 image (or uses defaults if not present).
- Changes 6-7: **Would affect `entry_riscv64.s`** if load address changes. **Defer.**

#### Diplomat Responsibilities Checklist

After this stage, diplomat must do exactly these things (and nothing else beyond current duties):

| Responsibility | ARM64 | x86_64 | RISC-V |
|---|---|---|---|
| Load kmazarin ELF from disk | UEFI File | UEFI File | VirtIO MMIO block |
| Allocate physical RAM for kernel | UEFI AllocatePages | UEFI AllocatePages | Bump allocator |
| Copy ELF segments to physical RAM | Yes | Yes | Yes |
| Set up page tables (kernel + linear map) | Yes | Yes | Yes (assembly + Go) |
| Allocate kernel stacks | Yes | Yes | Yes |
| Allocate unified page pool | Yes | Yes | Yes |
| Allocate framebuffer (if GPU found) | Yes | Yes | Yes |
| Build auxv with all AT_* entries | Yes | Yes | Yes |
| Read `kmazarin.toml` for config overrides | Yes | Yes | Yes |
| Jump to kmazarin entry point | Yes | Yes | Yes |

Anything diplomat does beyond this list is a **bug** to be filed and fixed.

#### Verification

```bash
# Test with different RAM sizes
$GO tool task run TIMEOUT=10                    # Default (2GB)
$GO tool task run TIMEOUT=10 QEMU_RAM=512M     # Reduced RAM
$GO tool task run TIMEOUT=10 QEMU_RAM=4G       # Increased RAM
# Verify page stats adapt to available memory
```

---

### Stage 4: Priest Page Cleanup on Exit

**Goal:** When the last thread of a priest exits, reclaim ALL physical pages owned by that priest.

This is the most impactful stage for correctness. After this, running N priests sequentially should consume no more memory than running 1 priest (assuming no concurrent priests).

#### Implementation: `CleanupPriestPages` (Linux-Style VMA + Page Table Walk)

New file `kmazarin/kmem/cleanup.go`. This follows the Linux `exit_mmap()` pattern:
1. Walk the priest's VMA list (Spans) to find all mapped VA ranges
2. For each range, walk the page tables to find valid PTEs
3. For each valid PTE: look up PageDescriptor, decrement RefCount, free if zero
4. Free the page table pages themselves
5. Free the L0 root page

```go
// CleanupPriestPages frees all physical pages belonging to the given priest.
// Called from releasePriestSchedLockHeld() after TLB shootdown.
//
// This follows the Linux exit_mmap() pattern:
//   1. Walk Spans (our VMA list) to find all VA ranges
//   2. Walk page tables within each range
//   3. Free leaf pages via PageDescriptor refcount
//   4. Free intermediate page table pages
//   5. Free the L0 root page
func CleanupPriestPages(priestID PriestId, l0PA uintptr) {
    freed := 0
    ptFreed := 0

    // Phase 1: Walk Spans + page tables, free leaf pages
    // For each span in the priest's LockedSpanGroup:
    priest := proc.GetPriest(priestID)
    spans := priest.Spans.GetAll()  // snapshot of all spans

    for _, span := range spans {
        // Walk page tables in this VA range
        for va := span.Start; va < span.Start + span.Length; va += PageSize {
            pa := walkUserPageTableWithL0(va, l0PA)
            if pa == 0 {
                continue  // Not mapped (sparse allocation)
            }
            // Look up PageDescriptor by PFN
            released := releasePageByPA(pa, priestID)
            if released {
                freed++
            }
        }
    }

    // Phase 2: Walk full page table hierarchy, free PT pages
    // Walk L0 entries 0-255 (user half only; kernel half is shared/copied)
    // For each valid L1: walk entries, recurse to L2, L3
    // Free L3, L2, L1 table pages bottom-up
    // Finally free L0 page
    ptFreed = walkAndFreePageTablePages(l0PA, priestID)

    console.KPrintln("Priest", uint64(priestID),
        "cleanup: freed", uint64(freed), "data pages,",
        uint64(ptFreed), "PT pages")
}

// releasePageByPA decrements refcount and frees if zero. Returns true if freed.
func releasePageByPA(pa uintptr, priestID PriestId) bool {
    idx := (pa - poolStart) >> PageShift
    if idx < 0 || idx >= uint64(len(pageDescriptors)) {
        return false  // Not in our pool (diplomat-mapped)
    }
    desc := &pageDescriptors[idx]
    if desc.RefCount <= 0 {
        return false  // Already freed or untracked
    }

    desc.RefCount--
    if desc.RefCount > 0 {
        return false  // Still shared with other priests
    }

    // RefCount reached 0 — free the page
    order := desc.Order
    *desc = PageDescriptor{}  // Clear descriptor
    buddyFree(pa, int(order))
    return true
}

// walkAndFreePageTablePages walks L0→L3 hierarchy bottom-up,
// freeing intermediate table pages. Uses arch-neutral helpers:
// pteIsValid(), pteExtractPA() via build-tag polymorphism.
func walkAndFreePageTablePages(l0PA uintptr, priestID PriestId) int {
    freed := 0
    l0VA := MapPAToKernelScratch(l0PA)

    // Only walk user half (entries 0-255 for x86_64/RISC-V; all entries for ARM64)
    // ARM64: L0 is entirely userspace (TTBR0 is separate from TTBR1)
    // x86_64: L0[0-255] is userspace, [256-511] is kernel (don't free those)
    // RISC-V: L0[0-255] is userspace, [256-511] is kernel
    maxEntry := platformUserL0MaxEntry()  // 512 for ARM64, 256 for x86_64/RISC-V

    for i := 0; i < maxEntry; i++ {
        l0e := readPTEntry(l0VA, i)
        if !pteIsValid(l0e) { continue }

        l1PA := pteExtractPA(l0e)
        l1VA := MapPAToKernelScratch(l1PA)

        for j := 0; j < 512; j++ {
            l1e := readPTEntry(l1VA, j)
            if !pteIsValid(l1e) { continue }
            if isBlockEntry(l1e, 1) { continue }  // 1GB block, skip (kernel mapping)

            l2PA := pteExtractPA(l1e)
            l2VA := MapPAToKernelScratch(l2PA)

            for k := 0; k < 512; k++ {
                l2e := readPTEntry(l2VA, k)
                if !pteIsValid(l2e) { continue }
                if isBlockEntry(l2e, 2) { continue }  // 2MB block

                l3PA := pteExtractPA(l2e)
                // Free L3 table page
                releasePageByPA(l3PA, priestID)
                freed++
            }
            // Free L2 table page
            releasePageByPA(l2PA, priestID)
            freed++
        }
        // Free L1 table page
        releasePageByPA(l1PA, priestID)
        freed++
    }
    // Free L0 page itself
    releasePageByPA(l0PA, priestID)
    freed++

    return freed
}
```

#### New Arch-Specific Helpers Needed

Each `paging_*.go` needs:
```go
// platformUserL0MaxEntry returns how many L0 entries are userspace.
// ARM64: 512 (TTBR0 is entirely user). x86_64/RISC-V: 256 (upper half is kernel).
func platformUserL0MaxEntry() int

// isBlockEntry returns true if the PTE at the given level is a block/huge page
// (not a table pointer). level: 1 = 1GB block, 2 = 2MB block.
func isBlockEntry(pte uint64, level int) bool
```

#### Update `releasePriestSchedLockHeld` in `threads.go`

```go
func releasePriestSchedLockHeld(priestIdx int, pid proc.PriestId) {
    // Existing: TLB shootdown
    kmem.TlbiASIDE1IS(uint16(pid))

    // NEW: Free all pages owned by this priest (Linux-style VMA + PT walk)
    l0PA := proc.PriestListData[priestIdx].PageTableL0PA
    kmem.CleanupPriestPages(pid, l0PA)

    // Existing: release slot
    proc.PriestListInUse[priestIdx] = false
    proc.PriestListData[priestIdx] = proc.Priest{}
    priestIdAllocator.Release(pid)
}
```

#### Fix munmap to Actually Free Pages

In `kmazarin/ksyscall/munmap.go`:

```go
func SyscallMunmap(addr, length uintptr) int64 {
    // ... existing span removal ...

    for va := alignedAddr; va < alignedAddr+alignedLen; va += kmem.PageSize {
        pa := kmem.WalkUserPageTable(va)
        if pa == 0 {
            continue  // Not mapped
        }

        // Unmap from page table
        kmem.UnmapUserPage(va)

        // NEW: Free the physical frame via PageDescriptor
        kmem.ReleasePageByPA(pa, currentPriestID)
    }

    return 0
}
```

#### Ensuring Complete Span Coverage

For the VMA-walk approach to work, ALL mapped regions must appear in Spans. Update:

| Allocation Site | Currently in Spans? | Fix |
|---|---|---|
| ELF .text segments | No (MAP_FIXED path) | Add span during ELF load in `launch.go` |
| ELF .data/.rodata/.bss | No | Add span during ELF load |
| Thread stacks (MAP_FIXED) | Yes (via mmap) | Already covered |
| Heap (bump allocator) | Partially (demand-paged) | Add span for full bump range on first mmap |
| Framebuffer mapping | No | Add span in `MapFramebuffer` |

**Safety net:** After walking Spans, also walk the full page table hierarchy (Phase 2 above). Any pages found that weren't in Spans get freed too. This catches pages we missed. Log a warning when this happens so we can add the missing Span.

#### Verification

```bash
$GO tool task run TIMEOUT=30
# Launch priest A, observe page stats
# Priest A exits, observe page stats return to baseline
# Launch priest B, observe same pages reused
# No growth in page count across repeated priest launches
```

---

### Stage 5: Accessed/Dirty Bit Tracking + Page Replacement Framework

**Goal:** Use the platform abstraction from Stage 0 to read hardware A/D bits. Build the framework for page replacement without implementing actual swap I/O.

#### A/D Bit Scanning

New file `kmazarin/kmem/page_scanner.go`:

```go
// ScanAccessedBits walks all mapped pages for the given priest,
// clears the Accessed bit, and returns a count of pages that were accessed
// since the last scan. This implements the "clock" algorithm's reference check.
func ScanAccessedBits(priestID PriestId) (accessedCount int, totalCount int) {
    list := &priestPageLists[priestID]
    for _, pa := range list.pages[:list.count] {
        idx := (pa - poolStart) >> PageShift
        desc := &pageDescriptors[idx]

        if desc.Flags & PD_PINNED != 0 {
            continue  // Skip pinned pages
        }

        // Find VA for this PA (stored in page tracker or derived from mapping)
        va := findVAForPA(pa, priestID)
        if va == 0 { continue }

        wasAccessed := platformClearAccessed(va)
        if wasAccessed {
            accessedCount++
        }

        wasDirty := platformClearDirty(va)
        if wasDirty {
            desc.Flags |= PD_DIRTY
        }

        totalCount++
    }
    return
}

// FindEvictionCandidates returns up to `count` pages suitable for eviction,
// preferring: (1) clean + unaccessed, (2) clean + accessed, (3) dirty + unaccessed.
// Never returns pinned pages.
func FindEvictionCandidates(priestID PriestId, count int) []uintptr {
    // Categorize pages into preference buckets
    // Return best candidates
}
```

#### Swap Stubs

New file `kmazarin/kmem/swap.go`:

```go
import "errors"

var ErrSwapNotImplemented = errors.New("swap not implemented")

// SwapSlot identifies a location in the swap device (block offset).
type SwapSlot struct {
    DeviceID  uint8   // Which block device
    BlockNum  uint64  // Block number on device
}

// SwapOutPage writes the contents of the physical page at `pa` to the swap device
// and marks the PTE as not-present (with a swap slot reference).
// Parameters:
//   - pa: physical address of the page to swap out
//   - desc: the page's descriptor (type, owner, dirty flag)
//   - va: virtual address where the page is mapped
//   - priestID: the owning priest (for TLB invalidation)
//
// Returns the swap slot where the page was written, or error.
func SwapOutPage(pa uintptr, desc *PageDescriptor, va uintptr, priestID PriestId) (SwapSlot, error) {
    console.KPrintln("[swap] Would swap out page PA=", pa, " VA=", va,
        " type=", desc.Type.String(), " dirty=", desc.Flags & PD_DIRTY != 0)
    return SwapSlot{}, ErrSwapNotImplemented
}

// SwapInPage reads a page from the swap device and maps it at the given VA.
// Called from the page fault handler when a not-present PTE has a swap slot reference.
// Parameters:
//   - va: the faulting virtual address
//   - slot: the swap slot to read from
//   - priestID: the owning priest
//
// Returns the new physical address, or error.
func SwapInPage(va uintptr, slot SwapSlot, priestID PriestId) (uintptr, error) {
    console.KPrintln("[swap] Would swap in page VA=", va, " from slot=", slot.BlockNum)
    return 0, ErrSwapNotImplemented
}
```

#### Integration with Page Fault Handler

In `HandleUserPageFault`, before allocating a new frame, check if the PTE contains a swap slot reference:

```go
// In HandleUserPageFault, after checking that the page is not already mapped:
// (Future: check if PTE has swap reference)
// pte, _, ok := platformReadPTEAt(faultAddr)
// if ok && isSwapPTE(pte) {
//     slot := extractSwapSlot(pte)
//     pa, err := SwapInPage(faultAddr, slot, currentPriestID)
//     if err == nil { return }
// }
// Fall through to normal allocation
```

This is commented out / behind a build flag initially. The important thing is the **function signatures are correct** so that when swap I/O is implemented, the integration points are clear.

#### Timer-Driven Scanning

Hook into the existing timer interrupt bottom-half to periodically call `ScanAccessedBits`:

```go
// In timer bottom-half (every N ticks):
if tickCount % 1000 == 0 {  // Every ~1 second
    for pid := PriestId(1); pid < MaxPriests; pid++ {
        if !proc.PriestListInUse[pid] { continue }
        accessed, total := ScanAccessedBits(pid)
        // Log or use for pressure detection
    }
}
```

#### Verification

```bash
$GO tool task run TIMEOUT=30
# Observe A/D scanning messages in serial log
# Verify no crashes from PTE manipulation
# Swap stubs log "Would swap out/in" messages
```

---

### Stage 6: Shared Memory for Microkernel IPC

**Goal:** Implement `TransferPages` and `MapSharedPage` from `design/MICROKERNEL.md` using the page ownership and refcount system from Stages 1-4.

This stage builds the primitives that the microkernel IPC will use. It does NOT implement the full transaction system — just the memory sharing mechanics, with test scaffolding.

#### New Syscalls

In `kmazarin/ksyscall/`:

**`share_pages.go`:**
```go
// SyscallTransferPages transfers ownership of pages from the calling priest
// to the target priest. Pages are unmapped from the source and mapped into
// the target's address space.
//
// Args:
//   targetPID  - destination priest ID
//   sourceVA   - start of source VA range (page-aligned)
//   numPages   - number of pages to transfer
//   perm       - permissions for target mapping (PTEFlags)
//
// Returns: VA in target address space where pages were mapped, or negative error
func SyscallTransferPages(targetPID PriestId, sourceVA uintptr, numPages int, perm kmem.PTEFlags) int64 {
    currentPID := currentThread().PID

    for i := 0; i < numPages; i++ {
        va := sourceVA + uintptr(i) * kmem.PageSize
        pa := kmem.WalkUserPageTable(va)
        if pa == 0 {
            return -EFAULT
        }

        // Look up descriptor
        desc := kmem.GetPageDescriptor(pa)
        if desc == nil || desc.Owner != currentPID {
            return -EPERM
        }

        // Unmap from source
        kmem.UnmapUserPage(va)
        platformFlushTLBPage(va)

        // Change ownership
        kmem.TransferPageOwnership(pa, currentPID, targetPID)

        // Map in target's address space (at target's bump pointer)
        targetVA := allocTargetVA(targetPID, 1)
        kmem.MapPageInProcess(targetPID, targetVA, pa, perm)
    }

    return int64(targetVA)
}
```

**`map_shared.go`:**
```go
// SyscallMapSharedPage creates a shared mapping of a page owned by one priest
// into another priest's address space. Both priests can access the page.
// The page's refcount is incremented.
//
// Args:
//   ownerPID - priest that owns the page
//   pa       - physical address of the page (obtained via IPC)
//   perm     - permissions for the mapping
//
// Returns: VA where the page was mapped in the caller's space, or negative error
func SyscallMapSharedPage(ownerPID PriestId, pa uintptr, perm kmem.PTEFlags) int64 {
    currentPID := currentThread().PID

    desc := kmem.GetPageDescriptor(pa)
    if desc == nil || desc.Owner != ownerPID {
        return -EPERM
    }

    // Increment refcount — shared pages are freed only when refcount reaches 0.
    // On priest exit, CleanupPriestPages walks page tables, finds this page's PTE,
    // calls releasePageByPA which decrements refcount. If another priest still maps
    // it, refcount stays > 0 and the page survives.
    desc.RefCount++
    desc.Flags |= PD_SHARED

    // Map in caller's address space (this creates a PTE that cleanup will find)
    callerVA := allocCallerVA(currentPID, 1)
    kmem.MapPageInProcess(currentPID, callerVA, pa, perm)

    // Add a span so cleanup walks this range
    priest := proc.GetPriest(currentPID)
    priest.Spans.AddSpan(callerVA, kmem.PageSize)

    return int64(callerVA)
}
```

#### Helper: `MapPageInProcess`

```go
// MapPageInProcess maps a physical page into a specific priest's address space.
// Requires the priest's L0 PA (obtained from thread or stored per-priest).
func MapPageInProcess(priestID PriestId, va uintptr, pa uintptr, flags PTEFlags) error {
    l0PA := getProcessL0PA(priestID)
    pte := platformFlagsToUserPTE(pa, flags)
    return mapUserPageWithL0(va, pa, elfFlagsFromPTEFlags(flags), l0PA)
}
```

#### Per-Priest L0 PA Storage

Currently the L0 PA is stored only in the thread's `PageTableL0PA` field. For IPC, we need to look up a priest's page table without having one of its threads. Add to `Priest`:

```go
type Priest struct {
    // ... existing fields ...
    PageTableL0PA uintptr  // L0 page table physical address for this priest
}
```

Set during `createUserspaceThreadImpl`.

#### Test Scaffolding

Since we don't have the full IPC transaction system yet, create a **test syscall** that exercises shared memory:

```go
// SyscallTestSharedMemory (temporary, debug-only)
// Creates a shared page between two priests for testing.
// Priest A writes a magic value, priest B reads it.
func SyscallTestSharedMemory(targetPID PriestId) int64 {
    // 1. Allocate a page owned by caller
    // 2. Write magic value 0xDEADBEEF to it
    // 3. MapSharedPage into target
    // 4. Return target VA (target can verify magic value)
}
```

This scaffolding is removed when the real IPC system is implemented.

#### Verification

```bash
$GO tool task run TIMEOUT=30
# Launch two priests
# Priest A shares a page with Priest B
# Priest B reads shared data
# Priest A exits — shared page refcount decremented, page remains for B
# Priest B exits — refcount reaches 0, page freed
# Verify page stats return to baseline
```

---

## Cross-Cutting Concerns

### Nosplit Safety

All functions called from exception handlers must be `//go:nosplit`. This includes:
- `platformPTEToFlags`, `platformReadPTEAt`, `platformClearAccessed`, `platformClearDirty`
- `platformFlushTLBPage`, `platformFlushTLBASID`
- `AllocPage`, `FreePageByPA` (if called from fault handler)

The `BuddyAllocTyped` and `BuddyFreeTyped` functions are already nosplit. The PageDescriptor array access is a simple index operation (nosplit-safe).

### Locking Strategy

- **PageDescriptor array**: Accessed from both exception handlers (top-half) and normal code (bottom-half). Use the existing spinlock pattern (`kmem.SpinLock`). Individual descriptors are small enough to update atomically in most cases; the spinlock protects the refcount decrement + free sequence.
- **Priest.Spans**: Already has its own lock (`LockedSpanGroup`). Cleanup takes a snapshot of spans under the lock, then releases it before walking page tables.
- **Buddy allocator**: Already has its own spinlock.

### Memory Overhead

| Structure | Size | Notes |
|-----------|------|-------|
| PageDescriptor array | ~2MB for 512MB pool | 16 bytes × 131072 pages |
| Total overhead | ~2MB | < 0.5% of 512MB RAM |

**No per-priest page lists. No per-alloc counters.** The only per-page cost is one `PageDescriptor` entry (flat array, indexed by PFN). Stats queries walk this array on demand — rare and cheap (linear scan of contiguous memory).

### No Go Maps Rule

**Go `map` types are forbidden in `kmazarin/kmem/` and any `//go:nosplit` code path.** Go maps heap-allocate, can trigger GC, and can trigger stack growth — all fatal in exception handlers. All data structures must be:
- Fixed-size arrays (compile-time or allocated once at boot from the pool)
- Index arithmetic on flat arrays (`pageDescriptors[(pa - poolStart) >> 12]`)
- Spinlocks for synchronization (not mutexes, which can sleep)

### Testing Strategy

Each stage must pass:
1. **ARM64**: `$GO tool task run TIMEOUT=15` — boot, print stats, run priest
2. **x86_64**: `$GO tool task run-x86_64 TIMEOUT=15` — same
3. **RISC-V**: `$GO tool task run-riscv64 TIMEOUT=15` — same
4. **Page stats**: Verify counts make sense (no negative counts, no orphaned pages)
5. **Regression**: No new crashes, panics, or hangs

### Stage Dependencies

```
Stage 0 (Platform Abstraction)
    ↓
Stage 1 (Page Types + Descriptors + Ownership)
    ↓
Stage 2 (Buddy-Only Allocator)
    ↓
Stage 3 (Layout Rationalization)  ← can partially overlap with Stage 2
    ↓
Stage 4 (Priest Cleanup)          ← depends on Stages 1+2
    ↓
Stage 5 (A/D Bits + Swap Stubs)   ← depends on Stage 0+4
    ↓
Stage 6 (Shared Memory IPC)       ← depends on Stage 4
```

Stages 3 and 5 are somewhat independent and could be reordered. Stages 0→1→2→4 is the critical path.
