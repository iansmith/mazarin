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
	"mazzy/kmazarin/serial"
	"sync/atomic"
	"unsafe"
)

// MaxOrder is the number of orders supported (0 through MaxOrder-1).
const MaxOrder = 13 // Orders 0-12 (4KB to 16MB)

// kmazarinKernelBudgetMB is set by archauxv() from AT_KERNEL_BUDGET_MB.
// 0 means "use default". Accessed via go:linkname from runtime package.
//
//go:linkname kmazarinKernelBudgetMB runtime.kmazarinKernelBudgetMB
var kmazarinKernelBudgetMB uintptr

// defaultKernelLimitPages is the default kernel memory warning threshold (128MB).
const defaultKernelLimitPages = 32768 // 128MB = 32768 * 4KB

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

	// Per-type allocation tracking (pages)
	kernelHeapPages uint64
	kernelPTPages   uint64
	userPages       uint64
	userPTPages     uint64

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
	// Guard against inserting kmazarin code pages into free list.
	// Uses RawUART (not PollWrite) to avoid nosplit stack overflow on AMD64.
	if pa >= 0x90000000 && pa < 0x90400000 {
		serial.RawUART('!')
		serial.RawUART('!')
		serial.RawUART('!')
		for {
		}
	}
	va := pa + buddyAlloc.kernelVAOffset
	// Write current head as next pointer.
	// Use unsafe pointer arithmetic to avoid bounds-check calls to runtime.panicIndex
	// which add 16 bytes to the nosplit chain (critical for x86_64's 792-byte limit).
	listSlot := (*uintptr)(unsafe.Pointer(uintptr(unsafe.Pointer(&buddyAlloc.freeList[0])) + uintptr(order)*unsafe.Sizeof(uintptr(0))))
	*(*uintptr)(unsafe.Pointer(va)) = *listSlot
	*listSlot = pa
	countSlot := (*uint64)(unsafe.Pointer(uintptr(unsafe.Pointer(&buddyAlloc.freeCount[0])) + uintptr(order)*unsafe.Sizeof(uint64(0))))
	*countSlot++
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
	// Validate next pointer: must be 0 (end of list) or within pool range.
	if next != 0 && (next < buddyAlloc.poolStart || next >= buddyAlloc.poolEnd) {
		buddyCorruptionHalt(next, pa, order)
	}
	buddyAlloc.freeList[order] = next
	buddyAlloc.freeCount[order]--
	return pa
}

// BuddyAlloc allocates a contiguous block of 2^order pages.
// Returns the physical address of the block, or 0 on failure.
// Counts as PageKernelHeap for accounting. Use BuddyAllocTyped for explicit type.
// Thread-safe.
//
//go:nosplit
func BuddyAlloc(order int) uintptr {
	return BuddyAllocTyped(order, PageKernelHeap, 0)
}

// BuddyAllocTyped allocates a contiguous block of 2^order pages with type tracking.
// The pageType and owner parameters control accounting and PageDescriptor population.
// owner=0 for kernel, 1-31 for priest-owned pages.
// Returns the physical address of the block, or 0 on failure.
// Thread-safe.
//
//go:nosplit
func BuddyAllocTyped(order int, pageType PageType, owner int16) uintptr {
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
		buddyWarnOOM(order)
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
	if totalUsed := buddyAlloc.bootstrapPages + buddyAlloc.allocatedPages; totalUsed > buddyAlloc.peakAllocated {
		buddyAlloc.peakAllocated = totalUsed
	}

	// Per-type accounting (bucketed into 4 categories for legacy stats)
	switch pageType {
	case PageKernelHeap, PageKernelStack, PageKernelMMIO,
		PageFramebuffer, PageVirtIOQueue, PageFileBuffer,
		PageBackingStore, PageDriver, PageSharedIPC:
		buddyAlloc.kernelHeapPages += pagesAllocated
	case PageKernelPT:
		buddyAlloc.kernelPTPages += pagesAllocated
	case PageUserText, PageUserROData, PageUserData, PageUserHeap, PageUserStack:
		buddyAlloc.userPages += pagesAllocated
	case PageUserPT:
		buddyAlloc.userPTPages += pagesAllocated
	}

	// Kernel-only total: bootstrap + kernel heap + kernel PT (excludes user pages)
	kernelTotal := buddyAlloc.bootstrapPages + buddyAlloc.kernelHeapPages + buddyAlloc.kernelPTPages

	buddyAlloc.lock.Unlock()

	// Warn when kernel memory exceeds the configured budget threshold.
	// limitPages: from AT_KERNEL_BUDGET_MB auxv entry (set by kmazarin.toml),
	// or defaultKernelLimitPages (128MB) if not configured.
	// Print full stats on first crossing, then breadcrumb every 256 pages.
	limitPages := uint64(kmazarinKernelBudgetMB) * 256 // 1MB = 256 pages of 4KB
	if limitPages == 0 {
		limitPages = defaultKernelLimitPages
	}
	if kernelTotal > limitPages {
		if kernelTotal-1 <= limitPages || (kernelTotal&0xFF) == 0 {
			buddyWarnKernelLimit(kernelTotal, pageType, pa)
		}
	}

	SetPageDescriptor(pa, pageType, owner, uint8(order))

	return pa
}

// buddyWarnKernelLimit prints the 64MB kernel memory warning via raw UART.
// buddyWarnOOM prints an OOM warning via raw UART.
// NOT nosplit — breaks the nosplit chain from exception handlers.
func buddyWarnOOM(order int) {
	serial.RawUARTPuts("[kmem] Buddy OOM for order ")
	serial.RawUARTHex64(uint64(order))
	serial.RawUARTPuts("\r\n")
}

// buddyWarnKernelLimit prints the kernel memory limit warning via raw UART.
// NOT nosplit — this breaks the nosplit chain from exception handlers so
// the rawUART calls (serial.PollWrite) don't blow the 792-byte nosplit limit.
// Uses rawUART (serial.PollWrite) so output goes to COM1/serial log,
// not through console abstraction (which on AMD64 uses MMIO, not I/O ports).
func buddyWarnKernelLimit(kernelTotal uint64, pageType PageType, pa uintptr) {
	serial.RawUARTPuts("\r\n[kmem] WARNING: kernel exceeds 128MB (kern=")
	serial.RawUARTHex64(kernelTotal)
	serial.RawUARTPuts(" kheap=")
	serial.RawUARTHex64(buddyAlloc.kernelHeapPages)
	serial.RawUARTPuts(" kpt=")
	serial.RawUARTHex64(buddyAlloc.kernelPTPages)
	serial.RawUARTPuts(" boot=")
	serial.RawUARTHex64(buddyAlloc.bootstrapPages)
	serial.RawUARTPuts(" user=")
	serial.RawUARTHex64(buddyAlloc.userPages)
	serial.RawUARTPuts(" type=")
	serial.RawUARTHex64(uint64(pageType))
	serial.RawUARTPuts(" pa=")
	serial.RawUARTHex64(uint64(pa))
	serial.RawUARTPuts(")\r\n")

	// Print per-priest breakdown from page tracker
	stats := GetMemoryStats()
	if stats.TotalTracked > 0 {
		serial.RawUARTPuts("  [tracker] total=")
		serial.RawUARTHex64(stats.TotalTracked)
		serial.RawUARTPuts(" kheap=")
		serial.RawUARTHex64(stats.KernelHeapPages)
		serial.RawUARTPuts(" kpt=")
		serial.RawUARTHex64(stats.KernelPTPages)
		serial.RawUARTPuts(" user=")
		serial.RawUARTHex64(stats.UserPages)
		serial.RawUARTPuts("\r\n")
		// Per-priest: index 0 = kernel (PID 0), 1+ = priests
		if stats.ByPriest[0] > 0 {
			serial.RawUARTPuts("  [priest] kernel(0): ")
			serial.RawUARTHex64(stats.ByPriest[0])
			serial.RawUARTPuts("\r\n")
		}
		for i := 1; i < MaxPriests; i++ {
			if stats.ByPriest[i] > 0 {
				serial.RawUARTPuts("  [priest] ")
				serial.RawUARTHex64(uint64(i))
				serial.RawUARTPuts(": ")
				serial.RawUARTHex64(stats.ByPriest[i])
				serial.RawUARTPuts("\r\n")
			}
		}
	}
}

// BuddyFree returns a block of 2^order pages starting at pa to the allocator.
// Counts as PageKernelHeap for per-type accounting. Use BuddyFreeTyped for explicit type.
// Attempts to merge with the buddy if it is also free.
// Thread-safe.
//
//go:nosplit
func BuddyFree(pa uintptr, order int) {
	BuddyFreeTyped(pa, order, PageKernelHeap)
}

// BuddyFreeTyped returns a block of 2^order pages starting at pa to the allocator,
// decrementing the correct per-type counter (mirror of BuddyAllocTyped).
// Attempts to merge with the buddy if it is also free.
// Thread-safe.
//
//go:nosplit
func BuddyFreeTyped(pa uintptr, order int, pageType PageType) {
	if order < 0 || order >= MaxOrder {
		return
	}

	pagesFreed := uint64(1) << uint(order)

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

	// Track deallocation (total)
	if buddyAlloc.allocatedPages >= pagesFreed {
		buddyAlloc.allocatedPages -= pagesFreed
	}

	// Per-type accounting (mirror of BuddyAllocTyped)
	switch pageType {
	case PageKernelHeap, PageKernelStack, PageKernelMMIO,
		PageFramebuffer, PageVirtIOQueue, PageFileBuffer,
		PageBackingStore, PageDriver, PageSharedIPC:
		if buddyAlloc.kernelHeapPages >= pagesFreed {
			buddyAlloc.kernelHeapPages -= pagesFreed
		}
	case PageKernelPT:
		if buddyAlloc.kernelPTPages >= pagesFreed {
			buddyAlloc.kernelPTPages -= pagesFreed
		}
	case PageUserText, PageUserROData, PageUserData, PageUserHeap, PageUserStack:
		if buddyAlloc.userPages >= pagesFreed {
			buddyAlloc.userPages -= pagesFreed
		}
	case PageUserPT:
		if buddyAlloc.userPTPages >= pagesFreed {
			buddyAlloc.userPTPages -= pagesFreed
		}
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
		// Validate prev is within the buddy pool range before dereferencing.
		// A corrupted next pointer outside the pool would cause a page fault
		// when we add kernelVAOffset and dereference. Halt with "B!C!" marker
		// to make corruption visible in serial output.
		if prev < buddyAlloc.poolStart || prev >= buddyAlloc.poolEnd {
			buddyCorruptionHalt(prev, pa, order)
			return false
		}
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

// buddyCorruptionHalt prints diagnostic info about a corrupted free list
// entry and halts. NOT nosplit — breaks the nosplit chain from exception
// handlers so we can print full diagnostic output.
func buddyCorruptionHalt(prev, pa uintptr, order int) {
	serial.RawUARTPuts("\r\n[BUDDY] CORRUPT free list! order=")
	serial.RawUARTHex64(uint64(order))
	serial.RawUARTPuts(" bad-next=")
	serial.RawUARTHex64(uint64(prev))
	serial.RawUARTPuts(" looking-for=")
	serial.RawUARTHex64(uint64(pa))
	serial.RawUARTPuts(" pool=[")
	serial.RawUARTHex64(uint64(buddyAlloc.poolStart))
	serial.RawUARTPuts(",")
	serial.RawUARTHex64(uint64(buddyAlloc.poolEnd))
	serial.RawUARTPuts(")\r\n")
	for {
	}
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
	serial.RawUARTPuts("[kmem] Buddy stats: ")
	serial.RawUARTHex64(stats.FreePages)
	serial.RawUARTPuts(" free / ")
	serial.RawUARTHex64(stats.TotalPages)
	serial.RawUARTPuts(" total pages\r\n")
	for i := 0; i < MaxOrder; i++ {
		if stats.FreeByOrder[i] == 0 {
			continue
		}
		serial.RawUARTPuts("  order ")
		serial.RawUARTHex64(uint64(i))
		serial.RawUARTPuts(": ")
		serial.RawUARTHex64(stats.FreeByOrder[i])
		serial.RawUARTPuts(" blocks (")
		serial.RawUARTHex64(uint64(PageSize) << uint(i) / 1024)
		serial.RawUARTPuts(" KB each)\r\n")
	}
}

// PrintKernelMemSummary prints a one-line kernel memory summary to UART.
// TEMPORARY: for diagnosing kernel memory growth.
// Format: [M] k=<kern> h=<heap> p=<pt> u=<user> a=<alloc>
// All values in pages (x4KB).
//
//go:nosplit
func PrintKernelMemSummary() {
	buddyAlloc.lock.Lock()
	kh := buddyAlloc.kernelHeapPages
	kp := buddyAlloc.kernelPTPages
	boot := buddyAlloc.bootstrapPages
	up := buddyAlloc.userPages
	alloc := buddyAlloc.bootstrapPages + buddyAlloc.allocatedPages
	buddyAlloc.lock.Unlock()

	serial.RawUARTPuts("\r\n[M] k=")
	serial.RawUARTHex64(boot + kh + kp)
	serial.RawUARTPuts(" h=")
	serial.RawUARTHex64(kh)
	serial.RawUARTPuts(" p=")
	serial.RawUARTHex64(kp)
	serial.RawUARTPuts(" b=")
	serial.RawUARTHex64(boot)
	serial.RawUARTPuts(" u=")
	serial.RawUARTHex64(up)
	serial.RawUARTPuts(" a=")
	serial.RawUARTHex64(alloc)
	serial.RawUARTPuts("\r\n")
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
	PA    uintptr  // Physical address of the block
	VA    uintptr  // Virtual address (PA + KernelVAOffset)
	Order int      // Buddy order used for allocation
	Size  uint64   // Requested size in bytes
	Type  PageType // Page type for per-type accounting on free
}

// AllocBuffer allocates a contiguous buffer of at least size bytes from the
// buddy allocator. The buffer is accessible via the linear map.
// Returns nil if allocation fails or size exceeds 8MB.
func AllocBuffer(size uint64) *BuddyBuffer {
	order := OrderForSize(size)
	if order < 0 {
		serial.RawUARTPuts("[kmem] AllocBuffer: size too large (")
		serial.RawUARTHex64(size)
		serial.RawUARTPuts(")\r\n")
		return nil
	}

	pa := BuddyAllocTyped(order, PageFileBuffer, 0)
	if pa == 0 {
		return nil
	}

	va := pa + buddyAlloc.kernelVAOffset

	return &BuddyBuffer{
		PA:    pa,
		VA:    va,
		Order: order,
		Size:  size,
		Type:  PageFileBuffer,
	}
}

// FreeBuffer returns a buddy-allocated buffer to the allocator.
func FreeBuffer(buf *BuddyBuffer) {
	if buf == nil {
		return
	}
	BuddyFreeTyped(buf.PA, buf.Order, buf.Type)
	UntrackPage(buf.PA)
}

// Bytes returns a []byte slice backed by the buddy-allocated buffer.
// The slice length is the originally requested size.
func (b *BuddyBuffer) Bytes() []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(b.VA)), b.Size)
}
