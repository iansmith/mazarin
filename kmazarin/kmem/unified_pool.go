// unified_pool.go - Unified page allocator for all memory types
//
// This replaces the separate kernel frame pool, kernel PT pool,
// userspace frame pool, and userspace PT pool with a single bump
// allocator. Allocation type is tracked for accounting purposes.

package kmem

import "sync/atomic"

// PageType identifies the purpose of a page allocation for accounting.
type PageType uint8

const (
	PageKernelHeap PageType = iota // Kernel heap pages
	PageKernelPT                   // Kernel page table pages
	PageUser                       // Userspace data pages
	PageUserPT                     // Userspace page table pages
	PageFileBuffer                 // Streaming file buffer pages
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

	cfg := getRuntimeConfigTyped()

	// Use the new unified pool fields if set, otherwise fall back to
	// computing from the legacy fields for backward compatibility
	var poolStart, poolEnd uint64
	if cfg.UnifiedPoolStart != 0 && cfg.UnifiedPoolEnd != 0 {
		poolStart = cfg.UnifiedPoolStart
		poolEnd = cfg.UnifiedPoolEnd
	} else {
		// Fallback: use legacy pool boundaries
		// Start from whichever pool starts first
		poolStart = cfg.FramePoolStart
		if cfg.UserspaceFramePoolStart > 0 && cfg.UserspaceFramePoolStart < poolStart {
			poolStart = cfg.UserspaceFramePoolStart
		}
		if cfg.UserspacePTPoolStart > 0 && cfg.UserspacePTPoolStart < poolStart {
			poolStart = cfg.UserspacePTPoolStart
		}

		// End at the highest pool end
		poolEnd = cfg.FramePoolEnd
		if cfg.UserspaceFramePoolEnd > poolEnd {
			poolEnd = cfg.UserspaceFramePoolEnd
		}
		if cfg.UserspacePTPoolEnd > poolEnd {
			poolEnd = cfg.UserspacePTPoolEnd
		}
	}

	globalPool.next = uintptr(poolStart)
	globalPool.end = uintptr(poolEnd)
	globalPool.initialNext = uintptr(poolStart)
	globalPool.kernelSoftLimit = DefaultKernelSoftLimit

	atomic.StoreUint32(&globalPool.initialized, 1)

	// Print pool info
	poolSize := poolEnd - poolStart
	poolPages := poolSize / PageSize
	uartPuts("[kmem] Unified pool: ")
	uartPutHex64(poolStart)
	uartPuts(" - ")
	uartPutHex64(poolEnd)
	uartPuts(" (")
	uartPutHex64(poolSize / (1024 * 1024))
	uartPuts(" MB, ")
	uartPutHex64(poolPages)
	uartPuts(" pages)\r\n")
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
	if atomic.LoadUint32(&buddyAlloc.initialized) != 0 {
		return BuddyAlloc(0)
	}

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
	pa := globalPool.next
	globalPool.next += PageSize

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

	cfg := getRuntimeConfigTyped()
	bootstrapPages := GetBumpAllocatedPages()

	uartPuts("[kmem] Transitioning to buddy allocator (")
	uartPutHex64(bootstrapPages)
	uartPuts(" pages already allocated)\r\n")

	InitBuddyAllocator(
		globalPool.initialNext,
		globalPool.end,
		uintptr(cfg.KernelVAOffset),
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
//
//go:nosplit
func GetPoolStats() PoolStats {
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
