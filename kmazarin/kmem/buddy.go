// buddy.go - Buddy allocator for physical page management
//
// Replaces the bump allocator in unified_pool.go with a proper buddy system
// supporting allocation and deallocation of power-of-two page blocks.
// Orders 0-11: 4KB (1 page) to 8MB (2048 pages).
//
// Free lists are intrusive: the first 8 bytes of each free block store
// a pointer to the next free block (physical address), with 0 as sentinel.
// This works because free pages are already mapped via the linear map.

package kmem

import (
	"sync/atomic"
	"unsafe"
)

// MaxOrder is the number of orders supported (0 through MaxOrder-1).
const MaxOrder = 12 // Orders 0-11

// BuddyAllocator manages physical memory using a buddy system.
type BuddyAllocator struct {
	// Free list heads per order. Each entry is a PA of the first free block,
	// or 0 if the list is empty. Blocks are linked through their first 8 bytes.
	freeList [MaxOrder]uintptr

	// Count of free blocks per order
	freeCount [MaxOrder]uint64

	// Pool boundaries (physical addresses, page-aligned)
	poolStart uintptr
	poolEnd   uintptr

	// Kernel VA offset for accessing physical memory
	kernelVAOffset uintptr

	// Total pages managed
	totalPages uint64

	// Allocation tracking
	allocatedPages uint64 // Pages allocated via buddy (excludes bootstrap)
	bootstrapPages uint64 // Pages allocated by bump allocator before buddy init
	peakAllocated  uint64 // High water mark (total = bootstrap + allocated)

	// Protection
	lock        Spinlock
	initialized uint32
}

// buddyAlloc is the singleton buddy allocator instance.
var buddyAlloc BuddyAllocator

// InitBuddyAllocator initializes the buddy allocator from the unified pool range.
// bootstrapPages is the number of pages already allocated by the bump allocator
// (these are marked as used by not adding them to free lists).
//
//go:nosplit
func InitBuddyAllocator(poolStart, poolEnd, kernelVAOffset uintptr, bootstrapPages uint64) {
	if atomic.LoadUint32(&buddyAlloc.initialized) != 0 {
		return
	}

	// Align start up and end down to page boundaries
	start := (poolStart + PageSize - 1) &^ (PageSize - 1)
	end := poolEnd &^ (PageSize - 1)

	// Cap pool at 4GB boundary: VA = PA + KernelVAOffset wraps for PA >= 0x100000000
	// when KernelVAOffset = 0xFFFFFFFF00000000
	const maxPA = uintptr(0x100000000)
	if end > maxPA {
		uartPuts("[kmem] Buddy: capping pool at 4GB boundary (was ")
		uartPutHex64(uint64(end))
		uartPuts(")\r\n")
		end = maxPA
	}

	buddyAlloc.poolStart = start
	buddyAlloc.poolEnd = end
	buddyAlloc.kernelVAOffset = kernelVAOffset
	buddyAlloc.totalPages = uint64(end-start) / PageSize
	buddyAlloc.bootstrapPages = bootstrapPages

	// Initialize free lists to empty
	for i := 0; i < MaxOrder; i++ {
		buddyAlloc.freeList[i] = 0
		buddyAlloc.freeCount[i] = 0
	}

	// Skip bootstrap pages (already allocated by bump allocator)
	freeStart := start + uintptr(bootstrapPages)*PageSize
	if freeStart > end {
		freeStart = end
	}

	// Add remaining memory to free lists, using largest possible orders
	buddyAddRange(freeStart, end)

	atomic.StoreUint32(&buddyAlloc.initialized, 1)

	// Print stats
	uartPuts("[kmem] Buddy allocator: ")
	uartPutHex64(uint64(start))
	uartPuts(" - ")
	uartPutHex64(uint64(end))
	uartPuts(" (")
	uartPutHex64(buddyAlloc.totalPages)
	uartPuts(" pages, ")
	uartPutHex64(bootstrapPages)
	uartPuts(" bootstrap)\r\n")
	PrintBuddyStats()
}

// buddyAddRange adds a contiguous physical range [start, end) to the free lists.
// Tries to use the largest possible order for each chunk.
// Must be called with no lock held (used only during init).
//
//go:nosplit
func buddyAddRange(start, end uintptr) {
	pa := start
	for pa < end {
		// Find the largest order that:
		// 1. Fits within remaining space
		// 2. Is properly aligned (block must be aligned to its size)
		order := MaxOrder - 1
		for order > 0 {
			blockSize := uintptr(PageSize) << uint(order)
			if pa+blockSize <= end && (pa&(blockSize-1)) == 0 {
				break
			}
			order--
		}
		// Double-check order 0 alignment (should always be fine if page-aligned)
		blockSize := uintptr(PageSize) << uint(order)
		if pa+blockSize > end {
			break
		}

		buddyInsertFree(pa, order)
		pa += blockSize
	}
}

// buddyInsertFree inserts a block at the head of the free list for the given order.
// Writes the next pointer into the first 8 bytes of the block via linear map.
//
//go:nosplit
func buddyInsertFree(pa uintptr, order int) {
	va := pa + buddyAlloc.kernelVAOffset
	// Write current head as next pointer
	*(*uintptr)(unsafe.Pointer(va)) = buddyAlloc.freeList[order]
	buddyAlloc.freeList[order] = pa
	buddyAlloc.freeCount[order]++
}

// buddyRemoveFree removes and returns the head block from the free list.
// Returns 0 if the list is empty.
//
//go:nosplit
func buddyRemoveFree(order int) uintptr {
	pa := buddyAlloc.freeList[order]
	if pa == 0 {
		return 0
	}
	// Read next pointer from block
	va := pa + buddyAlloc.kernelVAOffset
	next := *(*uintptr)(unsafe.Pointer(va))
	buddyAlloc.freeList[order] = next
	buddyAlloc.freeCount[order]--
	return pa
}

// BuddyAlloc allocates a contiguous block of 2^order pages.
// Returns the physical address of the block, or 0 on failure.
// Thread-safe.
//
//go:nosplit
func BuddyAlloc(order int) uintptr {
	if order < 0 || order >= MaxOrder {
		return 0
	}

	buddyAlloc.lock.Lock()

	// Find the smallest available order >= requested
	availOrder := order
	for availOrder < MaxOrder && buddyAlloc.freeCount[availOrder] == 0 {
		availOrder++
	}
	if availOrder >= MaxOrder {
		buddyAlloc.lock.Unlock()
		uartPuts("[kmem] Buddy OOM for order ")
		uartPutHex64(uint64(order))
		uartPuts("\r\n")
		return 0
	}

	// Remove block from the available order
	pa := buddyRemoveFree(availOrder)

	// Split down to requested order
	for availOrder > order {
		availOrder--
		// The upper half becomes a free buddy
		buddyPA := pa + (uintptr(PageSize) << uint(availOrder))
		buddyInsertFree(buddyPA, availOrder)
	}

	// Track allocation
	pagesAllocated := uint64(1) << uint(order)
	buddyAlloc.allocatedPages += pagesAllocated
	totalUsed := buddyAlloc.bootstrapPages + buddyAlloc.allocatedPages
	if totalUsed > buddyAlloc.peakAllocated {
		buddyAlloc.peakAllocated = totalUsed
	}

	// Warn on every allocation that keeps us over 64MB total kernel memory
	const kernelLimitPages = 16384 // 64MB = 16384 * 4KB
	over := totalUsed > kernelLimitPages
	buddyAlloc.lock.Unlock()

	if over {
		uartPuts("[kmem] WARNING: kernel memory exceeds 64MB (")
		uartPutHex64(totalUsed)
		uartPuts(" pages, bootstrap=")
		uartPutHex64(buddyAlloc.bootstrapPages)
		uartPuts(" buddy=")
		uartPutHex64(buddyAlloc.allocatedPages)
		uartPuts(")\r\n")
	}

	return pa
}

// BuddyFree returns a block of 2^order pages starting at pa to the allocator.
// Attempts to merge with the buddy if it is also free.
// Thread-safe.
//
//go:nosplit
func BuddyFree(pa uintptr, order int) {
	if order < 0 || order >= MaxOrder {
		return
	}

	buddyAlloc.lock.Lock()

	// Try to merge with buddy up to max order
	for order < MaxOrder-1 {
		blockSize := uintptr(PageSize) << uint(order)
		// Buddy address: flip the bit at position (order + 12)
		buddyPA := pa ^ blockSize

		// Check buddy is within pool range
		if buddyPA < buddyAlloc.poolStart || buddyPA+blockSize > buddyAlloc.poolEnd {
			break
		}

		// Search for buddy in free list
		if !buddyRemoveSpecific(buddyPA, order) {
			break // Buddy is not free, can't merge
		}

		// Merge: take the lower address as the new block
		if buddyPA < pa {
			pa = buddyPA
		}
		order++
	}

	buddyInsertFree(pa, order)

	// Track deallocation
	pagesFreed := uint64(1) << uint(order)
	if buddyAlloc.allocatedPages >= pagesFreed {
		buddyAlloc.allocatedPages -= pagesFreed
	}

	buddyAlloc.lock.Unlock()
}

// buddyRemoveSpecific removes a specific PA from a free list.
// Returns true if found and removed, false otherwise.
//
//go:nosplit
func buddyRemoveSpecific(pa uintptr, order int) bool {
	// Check if it's the head
	if buddyAlloc.freeList[order] == pa {
		buddyRemoveFree(order)
		return true
	}

	// Walk the list
	prev := buddyAlloc.freeList[order]
	for prev != 0 {
		prevVA := prev + buddyAlloc.kernelVAOffset
		next := *(*uintptr)(unsafe.Pointer(prevVA))
		if next == pa {
			// Found it - unlink
			paVA := pa + buddyAlloc.kernelVAOffset
			nextNext := *(*uintptr)(unsafe.Pointer(paVA))
			*(*uintptr)(unsafe.Pointer(prevVA)) = nextNext
			buddyAlloc.freeCount[order]--
			return true
		}
		prev = next
	}
	return false
}

// BuddyStats contains buddy allocator statistics.
type BuddyStats struct {
	TotalPages     uint64
	FreePages      uint64
	AllocatedPages uint64
	PeakAllocated  uint64
	FreeByOrder    [MaxOrder]uint64
	PoolStart      uintptr
	PoolEnd        uintptr
}

// GetBuddyStats returns current buddy allocator statistics.
//
//go:nosplit
func GetBuddyStats() BuddyStats {
	buddyAlloc.lock.Lock()
	var stats BuddyStats
	stats.TotalPages = buddyAlloc.totalPages
	stats.AllocatedPages = buddyAlloc.bootstrapPages + buddyAlloc.allocatedPages
	stats.PeakAllocated = buddyAlloc.peakAllocated
	stats.PoolStart = buddyAlloc.poolStart
	stats.PoolEnd = buddyAlloc.poolEnd
	for i := 0; i < MaxOrder; i++ {
		stats.FreeByOrder[i] = buddyAlloc.freeCount[i]
		stats.FreePages += buddyAlloc.freeCount[i] << uint(i)
	}
	buddyAlloc.lock.Unlock()
	return stats
}

// PrintBuddyStats prints buddy allocator statistics.
//
//go:nosplit
func PrintBuddyStats() {
	stats := GetBuddyStats()
	uartPuts("[kmem] Buddy stats: ")
	uartPutHex64(stats.FreePages)
	uartPuts(" free / ")
	uartPutHex64(stats.TotalPages)
	uartPuts(" total pages\r\n")
	for i := 0; i < MaxOrder; i++ {
		if stats.FreeByOrder[i] == 0 {
			continue
		}
		uartPuts("  order ")
		uartPutHex64(uint64(i))
		uartPuts(": ")
		uartPutHex64(stats.FreeByOrder[i])
		uartPuts(" blocks (")
		uartPutHex64(uint64(PageSize) << uint(i) / 1024)
		uartPuts(" KB each)\r\n")
	}
}

// OrderForSize returns the smallest buddy order that can hold size bytes.
// Returns -1 if size exceeds the maximum order.
func OrderForSize(size uint64) int {
	pages := (size + PageSize - 1) / PageSize
	order := 0
	for (uint64(1) << uint(order)) < pages {
		order++
	}
	if order >= MaxOrder {
		return -1
	}
	return order
}

// BuddyBuffer holds metadata about a buddy-allocated buffer so it can be freed.
type BuddyBuffer struct {
	PA    uintptr // Physical address of the block
	VA    uintptr // Virtual address (PA + KernelVAOffset)
	Order int     // Buddy order used for allocation
	Size  uint64  // Requested size in bytes
}

// AllocBuffer allocates a contiguous buffer of at least size bytes from the
// buddy allocator. The buffer is accessible via the linear map.
// Returns nil if allocation fails or size exceeds 8MB.
func AllocBuffer(size uint64) *BuddyBuffer {
	order := OrderForSize(size)
	if order < 0 {
		uartPuts("[kmem] AllocBuffer: size too large (")
		uartPutHex64(size)
		uartPuts(")\r\n")
		return nil
	}

	pa := BuddyAlloc(order)
	if pa == 0 {
		return nil
	}

	va := pa + buddyAlloc.kernelVAOffset

	return &BuddyBuffer{
		PA:    pa,
		VA:    va,
		Order: order,
		Size:  size,
	}
}

// FreeBuffer returns a buddy-allocated buffer to the allocator.
func FreeBuffer(buf *BuddyBuffer) {
	if buf == nil {
		return
	}
	BuddyFree(buf.PA, buf.Order)
}

// Bytes returns a []byte slice backed by the buddy-allocated buffer.
// The slice length is the originally requested size.
func (b *BuddyBuffer) Bytes() []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(b.VA)), b.Size)
}
