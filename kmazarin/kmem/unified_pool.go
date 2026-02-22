// unified_pool.go - Unified page allocator for all memory types
//
// This replaces the separate kernel frame pool, kernel PT pool,
// userspace frame pool, and userspace PT pool with a single bump
// allocator. Allocation type is tracked for accounting purposes.

package kmem

import (
	"mazzy/shared/constants"
	"sync/atomic"
)

// PageType identifies the purpose of a page allocation for accounting.
type PageType uint8

const (
	PageKernelHeap PageType = iota // Kernel heap pages
	PageKernelPT                   // Kernel page table pages
	PageUser                       // Userspace data pages
	PageUserPT                     // Userspace page table pages
	PageFileBuffer                 // Streaming file buffer pages
	PageDriver                     // Driver DMA pages (non-cacheable)
)

// Default soft limit for kernel memory: 16384 pages = 64MB
const DefaultKernelSoftLimit = 16384

// MaxFreePages is the maximum number of pages that can be held in the free list
const MaxFreePages = 4096

// UnifiedPagePool is a single bump allocator serving all page allocations.
// Thread-safe via spinlock protection.
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

// InitUnifiedPool initializes the unified page pool from RuntimeConfig.
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

}

// AllocPage allocates a single page from the unified pool.
// If the buddy allocator is initialized, delegates to it.
// Otherwise falls back to the bump allocator (used during early boot).
// The pageType parameter is used for accounting and soft limit checks.
// Returns the physical address of the page, or 0 if the pool is exhausted.
// The page is NOT zeroed - caller must zero if needed.
//
//go:nosplit
func AllocPage(pageType PageType) uintptr {
	// Use buddy allocator if available
	var pa uintptr
	if atomic.LoadUint32(&buddyAlloc.initialized) != 0 {
		pa = BuddyAllocTyped(0, pageType)
	} else {
		// Ensure bump pool is initialized
		if atomic.LoadUint32(&globalPool.initialized) == 0 {
			InitUnifiedPool()
		}

		globalPool.lock.Lock()

		// Check for OOM
		if globalPool.next >= globalPool.end {
			globalPool.lock.Unlock()
			uartPuts("[kmem] Unified pool OOM!\r\n")
			return 0
		}

		// Bump allocate
		pa = globalPool.next
		globalPool.next += PageSize

		globalPool.lock.Unlock()
	}

	// DEBUG: Guard against allocating kmazarin code pages
	if pa >= 0x90000000 && pa < 0x90400000 {
		rawUARTPuts("[ALLOC_GUARD] ABORT: allocated kmazarin code page! PA=0x")
		rawUARTHex64(uint64(pa))
		rawUARTPuts("\r\n")
		for {
		}
	}

	return pa
}

// AllocContiguousPages allocates a contiguous block of physical pages.
// If the buddy allocator is initialized, delegates to BuddyAlloc with the
// appropriate order (smallest power-of-2 >= pages). Otherwise falls back to
// the bump allocator where contiguity is guaranteed by sequential allocation.
// Returns the physical address of the first page, or 0 on failure.
//
//go:nosplit
func AllocContiguousPages(pages uintptr) uintptr {
	// Use buddy allocator if available
	if atomic.LoadUint32(&buddyAlloc.initialized) != 0 {
		// Compute order: smallest power of 2 >= pages
		order := 0
		for (uintptr(1) << uint(order)) < pages {
			order++
		}
		return BuddyAllocTyped(order, PageKernelHeap)
	}

	// Ensure bump pool is initialized
	if atomic.LoadUint32(&globalPool.initialized) == 0 {
		InitUnifiedPool()
	}

	globalPool.lock.Lock()

	size := pages * PageSize
	if globalPool.next+size > globalPool.end {
		globalPool.lock.Unlock()
		uartPuts("[kmem] Contiguous alloc OOM!\r\n")
		return 0
	}

	pa := globalPool.next
	globalPool.next += size

	globalPool.lock.Unlock()
	return pa
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

// TransitionToBuddy initializes the buddy allocator using the unified pool's range,
// with pages already bump-allocated marked as used.
// Call this after early boot when the system is stable enough for the buddy allocator.
//
//go:nosplit
func TransitionToBuddy() {
	if atomic.LoadUint32(&buddyAlloc.initialized) != 0 {
		return
	}
	if atomic.LoadUint32(&globalPool.initialized) == 0 {
		InitUnifiedPool()
	}

	bootstrapPages := GetBumpAllocatedPages()

	InitBuddyAllocator(
		globalPool.initialNext,
		globalPool.end,
		constants.KernelMMIOOffset,
		bootstrapPages,
	)
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

// GetPoolStats returns current unified pool statistics.
// If the buddy allocator is active, per-type stats come from it.
//
//go:nosplit
func GetPoolStats() PoolStats {
	// If buddy is active, get per-type stats from it
	if atomic.LoadUint32(&buddyAlloc.initialized) != 0 {
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

	globalPool.lock.Lock()
	defer globalPool.lock.Unlock()

	totalSize := uint64(globalPool.end - globalPool.initialNext)
	allocatedSize := uint64(globalPool.next - globalPool.initialNext)

	return PoolStats{
		TotalPages:      totalSize / PageSize,
		AllocatedPages:  allocatedSize / PageSize,
		RemainingPages:  (totalSize - allocatedSize) / PageSize,
		KernelHeapPages: globalPool.kernelHeapPages,
		KernelPTPages:   globalPool.kernelPTPages,
		UserPages:       globalPool.userPages,
		UserPTPages:     globalPool.userPTPages,
		KernelSoftLimit: globalPool.kernelSoftLimit,
	}
}

// PrintPoolStats prints the current pool statistics to the console.
//
//go:nosplit
func PrintPoolStats() {
	stats := GetPoolStats()
	uartPuts("[kmem] Pool stats:\r\n")
	uartPuts("  Total:       ")
	uartPutHex64(stats.TotalPages)
	uartPuts(" pages\r\n")
	uartPuts("  Allocated:   ")
	uartPutHex64(stats.AllocatedPages)
	uartPuts(" pages\r\n")
	uartPuts("  Remaining:   ")
	uartPutHex64(stats.RemainingPages)
	uartPuts(" pages\r\n")
	uartPuts("  Kernel heap: ")
	uartPutHex64(stats.KernelHeapPages)
	uartPuts(" pages\r\n")
	uartPuts("  Kernel PT:   ")
	uartPutHex64(stats.KernelPTPages)
	uartPuts(" pages\r\n")
	uartPuts("  User:        ")
	uartPutHex64(stats.UserPages)
	uartPuts(" pages\r\n")
	uartPuts("  User PT:     ")
	uartPutHex64(stats.UserPTPages)
	uartPuts(" pages\r\n")
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
