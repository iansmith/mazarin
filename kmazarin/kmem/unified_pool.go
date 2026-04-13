// unified_pool.go - Unified page allocator for all memory types
//
// All allocations go through the buddy allocator. A minimal bump allocator
// is used only during bootstrap to allocate the PageDescriptor array before
// the buddy allocator is ready. After InitUnifiedPool(), every AllocPage
// call goes through BuddyAllocTyped.

package kmem

import (
	"mazzy/shared/constants"
	"sync/atomic"
)

// PageType identifies the purpose of a page allocation.
type PageType uint8

const (
	// Kernel pages (owner = ShepherdId 0)
	PageKernelHeap  PageType = iota // Kernel heap (demand-paged)
	PageKernelPT                    // Kernel page table pages
	PageKernelStack                 // Kernel g0 + exception stacks
	PageKernelMMIO                  // MMIO device pages mapped to kernel VA
	PageFramebuffer                 // VirtIO GPU framebuffer
	PageVirtIOQueue                 // VirtIO descriptor/avail/used rings

	// Userspace pages (owner = ShepherdId 1-31)
	PageUserText   // ELF .text segments
	PageUserROData // ELF .rodata segments
	PageUserData   // ELF .data/.bss segments
	PageUserHeap   // Userspace heap (mmap, demand-paged)
	PageUserStack  // Userspace thread stacks
	PageUserPT     // Per-process page table pages

	// Shared / IPC pages
	PageSharedIPC    // Pages transferred between shepherds
	PageFileBuffer   // File I/O streaming buffers
	PageBackingStore // Display backing store (rachel)

	// Driver pages
	PageDriver // Driver DMA pages (non-cacheable)

	// System pages (kernel-allocated, user-accessible)
	PageVDSO             // vDSO trampoline (shared across all processes)
	PageConstraintShared // Constraint VM shared pages (kernel-writable, shepherd-readable)

	// Explicitly-allocated shared pages (via SysAllocPages)
	PageFontCache // Font glyph cache pages (owned by rachel/fontsvc)
	PageIPCBuffer // IPC ring buffer pages (owned by creating shepherd)

	// DMA I/O pages
	PageUserDMA // Userspace DMA-pinned pages (owned by shepherd, borrowed by engine)

	// Ramdisk backing store (off-heap, shepherd-owned, reclaimed on death)
	PageRamdisk

	// File-backed mmap pages (read-only, populated from file on demand)
	PageFileMmap

	// Sentinel
	PageTypeCount // Must be last
)

// Backwards-compatible alias for old code that uses the generic PageUser name.
const PageUser = PageUserHeap

// pageTypeNames maps PageType values to human-readable strings.
var pageTypeNames = [PageTypeCount]string{
	PageKernelHeap:  "KernelHeap",
	PageKernelPT:    "KernelPT",
	PageKernelStack: "KernelStack",
	PageKernelMMIO:  "KernelMMIO",
	PageFramebuffer: "Framebuffer",
	PageVirtIOQueue: "VirtIOQueue",
	PageUserText:    "UserText",
	PageUserROData:  "UserROData",
	PageUserData:    "UserData",
	PageUserHeap:    "UserHeap",
	PageUserStack:   "UserStack",
	PageUserPT:      "UserPT",
	PageSharedIPC:   "SharedIPC",
	PageFileBuffer:  "FileBuffer",
	PageBackingStore: "BackingStore",
	PageDriver:      "Driver",
	PageVDSO:             "VDSO",
	PageConstraintShared: "ConstraintShared",
	PageFontCache:        "FontCache",
	PageIPCBuffer:        "IPCBuffer",
	PageUserDMA:          "UserDMA",
	PageRamdisk:          "Ramdisk",
	PageFileMmap:         "FileMmap",
}

// String returns a human-readable name for the page type.
func (pt PageType) String() string {
	if int(pt) < len(pageTypeNames) {
		return pageTypeNames[pt]
	}
	return "Unknown"
}

// IsKernelType returns true if this page type is kernel-owned.
//
//go:nosplit
func (pt PageType) IsKernelType() bool {
	return pt <= PageVirtIOQueue || pt == PageVDSO
}

// Default soft limit for kernel memory: 16384 pages = 64MB
const DefaultKernelSoftLimit = 16384

// MaxFreePages is the maximum number of pages that can be held in the free list
const MaxFreePages = 4096

// UnifiedPagePool holds pool boundaries and the bootstrap bump allocator state.
// The bump allocator is only used during InitUnifiedPool() to allocate the
// PageDescriptor array. After that, the buddy allocator handles all allocations.
type UnifiedPagePool struct {
	// Bump allocator state
	next        uintptr // Next page to allocate (PA)
	end         uintptr // End of pool (exclusive, PA)
	initialNext uintptr // Initial value of next for stats

	// Accounting by type
	kernelHeapPages uint64
	kernelPTPages   uint64
	userPages       uint64
	userPTPages     uint64

	// Soft limit for kernel allocations (pages)
	kernelSoftLimit uint64

	// Protection
	lock        Spinlock
	initialized uint32
}

// globalPool is the singleton unified page pool instance
var globalPool UnifiedPagePool

// InitUnifiedPool initializes the unified page pool and the buddy allocator.
// After this call, all allocations go through the buddy allocator.
//
// Bootstrap sequence:
//  1. Set up bump allocator boundaries from auxv
//  2. Bump-allocate the PageDescriptor array (covers entire pool)
//  3. Initialize buddy allocator with remaining pool (bump pages marked as used)
//  4. Set buddyReady — all subsequent AllocPage calls use buddy
//
// This should be called early in kmazarin startup.
//
//go:nosplit
func InitUnifiedPool() {
	// Atomic check to avoid double initialization
	if atomic.LoadUint32(&globalPool.initialized) != 0 {
		return
	}

	// Read pool boundaries from auxv-backed vars (set by archauxv during runtime init).
	unifiedStart := uint64(kmazarinUnifiedPoolStart)
	unifiedEnd := uint64(kmazarinUnifiedPoolEnd)

	var poolStart, poolEnd uint64
	if unifiedStart != 0 && unifiedEnd != 0 {
		poolStart = unifiedStart
		poolEnd = unifiedEnd
	} else {
		// Fallback: use legacy frame pool boundaries
		poolStart = uint64(kmazarinFramePoolStart)
		poolEnd = uint64(kmazarinFramePoolEnd)
	}

	globalPool.next = uintptr(poolStart)
	globalPool.end = uintptr(poolEnd)
	globalPool.initialNext = uintptr(poolStart)
	globalPool.kernelSoftLimit = DefaultKernelSoftLimit

	atomic.StoreUint32(&globalPool.initialized, 1)

	// Initialize PageDescriptor array (bump-allocates from pool).
	// Must happen before buddy init so the array pages are counted
	// as bootstrap pages and marked as used.
	InitPageDescriptors(globalPool.initialNext, globalPool.end)

	// Count how many pages the bump allocator used (PageDescriptor array)
	bootstrapPages := GetBumpAllocatedPages()

	// Initialize buddy allocator with remaining pool
	InitBuddyAllocator(
		globalPool.initialNext,
		globalPool.end,
		constants.KernelMMIOOffset,
		bootstrapPages,
	)
}

// AllocPage allocates a single page via the buddy allocator.
// The pageType and owner parameters are used for accounting and PageDescriptor.
// owner=0 for kernel pages, 1-31 for shepherd-owned pages.
// Returns the physical address of the page, or 0 if the pool is exhausted.
// The page is NOT zeroed - caller must zero if needed.
//
// PRECONDITION: InitUnifiedPool() must have been called (happens via InitPaging).
//
//go:nosplit
func AllocPage(pageType PageType, owner int16) uintptr {
	return BuddyAllocTyped(0, pageType, owner)
}

// AllocContiguousPages allocates a contiguous block of physical pages
// via the buddy allocator with the appropriate order (smallest power-of-2 >= pages).
// Returns the physical address of the first page, or 0 on failure.
//
// PRECONDITION: InitUnifiedPool() must have been called (happens via InitPaging).
//
//go:nosplit
func AllocContiguousPages(pages uintptr, pageType PageType, owner int16) uintptr {
	// Compute order: smallest power of 2 >= pages
	order := 0
	for (uintptr(1) << uint(order)) < pages {
		order++
	}
	return BuddyAllocTyped(order, pageType, owner)
}

// GetBumpAllocatedPages returns the number of pages allocated by the bump allocator.
//
//go:nosplit
func GetBumpAllocatedPages() uint64 {
	if atomic.LoadUint32(&globalPool.initialized) == 0 {
		return 0
	}
	return uint64(globalPool.next-globalPool.initialNext) / PageSize
}

// TransitionToBuddy is a no-op retained for backward compatibility.
// The buddy allocator is now initialized eagerly in InitUnifiedPool().
//
//go:nosplit
func TransitionToBuddy() {
	// Buddy is initialized in InitUnifiedPool(). If somehow called before
	// pool init, trigger it now.
	if atomic.LoadUint32(&buddyAlloc.initialized) == 0 {
		InitUnifiedPool()
	}
}

// PoolStats contains accounting information about the unified pool.
type PoolStats struct {
	TotalPages      uint64
	AllocatedPages  uint64
	RemainingPages  uint64
	KernelHeapPages uint64
	KernelPTPages   uint64
	UserPages       uint64
	UserPTPages     uint64
	KernelSoftLimit uint64
}

// GetPoolStats returns current unified pool statistics from the buddy allocator.
//
//go:nosplit
func GetPoolStats() PoolStats {
	bs := GetBuddyStats()
	return PoolStats{
		TotalPages:      bs.TotalPages,
		AllocatedPages:  bs.AllocatedPages,
		RemainingPages:  bs.FreePages,
		KernelHeapPages: buddyAlloc.kernelHeapPages,
		KernelPTPages:   buddyAlloc.kernelPTPages,
		UserPages:       buddyAlloc.userPages,
		UserPTPages:     buddyAlloc.userPTPages,
		KernelSoftLimit: globalPool.kernelSoftLimit,
	}
}

// SetKernelSoftLimit sets the soft limit for kernel allocations (in pages).
// Default is 16384 pages (64MB).
//
//go:nosplit
func SetKernelSoftLimit(pages uint64) {
	globalPool.lock.Lock()
	globalPool.kernelSoftLimit = pages
	globalPool.lock.Unlock()
}
