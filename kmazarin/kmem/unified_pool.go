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
)

// Default soft limit for kernel memory: 16384 pages = 64MB
const DefaultKernelSoftLimit = 16384

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
// The pageType parameter is used for accounting and soft limit checks.
// Returns the physical address of the page, or 0 if the pool is exhausted.
// The page is NOT zeroed - caller must zero if needed.
//
//go:nosplit
func AllocPage(pageType PageType) uintptr {
	// Ensure pool is initialized
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

	// Soft limit warning for kernel allocations
	if pageType == PageKernelHeap || pageType == PageKernelPT {
		totalKernel := globalPool.kernelHeapPages + globalPool.kernelPTPages
		if totalKernel >= globalPool.kernelSoftLimit {
			// Just warn, don't fail - allow debugging
			uartPuts("[kmem] WARN: Kernel soft limit (")
			uartPutHex64(globalPool.kernelSoftLimit)
			uartPuts(" pages) exceeded\r\n")
		}
	}

	// Bump allocate
	pa := globalPool.next
	globalPool.next += PageSize

	// Update accounting
	switch pageType {
	case PageKernelHeap:
		globalPool.kernelHeapPages++
	case PageKernelPT:
		globalPool.kernelPTPages++
	case PageUser:
		globalPool.userPages++
	case PageUserPT:
		globalPool.userPTPages++
	}

	globalPool.lock.Unlock()
	return pa
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
