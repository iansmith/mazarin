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
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/serial"
	"sync/atomic"
	"unsafe"
)

// MaxOrder is the number of orders supported (0 through MaxOrder-1).
const MaxOrder = 15 // Orders 0-14 (4KB to 64MB)

// kmazarinKernelBudgetMB is set by archauxv() from AT_KERNEL_BUDGET_MB.
// 0 means "use default". Accessed via go:linkname from runtime package.
//
//go:linkname kmazarinKernelBudgetMB runtime.kmazarinKernelBudgetMB
var kmazarinKernelBudgetMB uintptr

// SetKernelBudgetMB overrides the kernel memory warning threshold.
// Called from main after parsing kmazarin.toml, taking priority over the
// auxv value set by diplomat during early boot.
func SetKernelBudgetMB(mb int) {
	if mb > 0 {
		kmazarinKernelBudgetMB = uintptr(mb)
	}
}

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
	carveoutPages  uint64 // Pages permanently excluded as linear-map carve-outs (MAZ-139)

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

// Per-type page counters (outstanding pages = allocs - frees).
// Indexed by PageType. Updated inside BuddyAllocTyped/BuddyFreeTyped
// under the buddy lock.
var buddyPagesByType [PageTypeCount]uint64

// InitBuddyAllocator initializes the buddy allocator from the unified pool range.
// bootstrapPages is the number of pages already allocated by the bump allocator
// (these are marked as used by not adding them to free lists).
//
// [carveStart, carveEnd) is a linear-map carve-out: a PA window whose linear-map
// VA (pa+KernelVAOffset) is STOLEN by a fixed kernel mapping, so those PAs are
// unusable via MapPAToKernelScratch and must never be allocated (MAZ-139 — see
// the g0/exc-stack carve-out computed in InitUnifiedPool). Pass 0,0 for none.
//
//go:nosplit
func InitBuddyAllocator(poolStart, poolEnd, kernelVAOffset uintptr, bootstrapPages uint64, carveStart, carveEnd uintptr) {
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

	// Add remaining memory to free lists, using largest possible orders, but
	// EXCLUDE the linear-map carve-out window [carveStart, carveEnd) if it
	// overlaps the free range. Those PAs have their linear VA stolen by a fixed
	// kernel mapping (the g0/exc stacks), so a frame there would alias the live
	// exception stack under MapPAToKernelScratch (MAZ-139). Page-aligned bounds
	// are assumed (stack VAs are page-aligned).
	if carveEnd > carveStart && carveStart < end && carveEnd > freeStart {
		if carveStart > freeStart {
			buddyAddRange(freeStart, carveStart) // gap below the carve-out
		}
		if carveEnd < end {
			buddyAddRange(carveEnd, end) // gap above the carve-out
		}
		lo, hi := carveStart, carveEnd
		if lo < freeStart {
			lo = freeStart
		}
		if hi > end {
			hi = end
		}
		buddyAlloc.carveoutPages = uint64(hi-lo) / PageSize
	} else {
		buddyAddRange(freeStart, end)
	}

	atomic.StoreUint32(&buddyAlloc.initialized, 1)

}

// BuddyCarveoutPages returns the number of pages permanently excluded from the
// free lists as linear-map carve-outs (MAZ-139). Used by InitUnifiedPool to
// assert the carve-out actually took effect.
//
//go:nosplit
func BuddyCarveoutPages() uint64 {
	return buddyAlloc.carveoutPages
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
	// Write current head as next pointer.
	// Use unsafe pointer arithmetic to avoid bounds-check calls to runtime.panicIndex
	// which add 16 bytes to the nosplit chain (critical for x86_64's 792-byte limit).
	listSlot := (*uintptr)(unsafe.Pointer(uintptr(unsafe.Pointer(&buddyAlloc.freeList[0])) + uintptr(order)*unsafe.Sizeof(uintptr(0))))
	*(*uintptr)(unsafe.Pointer(va)) = *listSlot
	*listSlot = pa
	countSlot := (*uint64)(unsafe.Pointer(uintptr(unsafe.Pointer(&buddyAlloc.freeCount[0])) + uintptr(order)*unsafe.Sizeof(uint64(0))))
	*countSlot++
}

// buddyContainsPA walks the free list for the given order and returns true if
// pa is already present. Used as a double-free guard from BuddyFreeTyped:
// inserting a PA that's already on the list would either no-op (if pa happens
// to be the current head's existing successor) or create a self-cycle (if pa
// becomes the head whose stored next pointer was the previous head, which
// then transitively points back at pa). Either way the next walker (an
// allocator or a future free) can spin forever or corrupt accounting.
//
// Caller must hold buddyAlloc.lock. Walk is bounded to maxWalk to ensure we
// terminate even on a pre-existing cycle (so this helper itself doesn't hang
// when invoked from a corrupt state).
//
//go:nosplit
func buddyContainsPA(pa uintptr, order int) bool {
	if order < 0 || order >= MaxOrder {
		return false
	}
	idx := uint(order) % uint(MaxOrder)
	const maxWalk = 8192
	cur := buddyAlloc.freeList[idx]
	for i := 0; i < maxWalk && cur != 0; i++ {
		if cur == pa {
			return true
		}
		// Validate cur is within the buddy pool range before dereferencing
		// (mirrors buddyRemoveSpecific's safety check).
		if cur < buddyAlloc.poolStart || cur >= buddyAlloc.poolEnd {
			return false
		}
		va := cur + buddyAlloc.kernelVAOffset
		cur = *(*uintptr)(unsafe.Pointer(va))
	}
	return false
}

// buddyRemoveFree removes and returns the head block from the free list.
// Returns 0 if the list is empty.
//
//go:nosplit
func buddyRemoveFree(order int) uintptr {
	if order < 0 || order >= MaxOrder {
		return 0
	}
	// Modulo is a no-op after the guard but lets the SSA prover chain the
	// fact, eliminating panicBounds64's 160-byte frame from the IRQ chain.
	idx := uint(order) % uint(MaxOrder)
	pa := buddyAlloc.freeList[idx]
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
	buddyAlloc.freeList[idx] = next
	buddyAlloc.freeCount[idx]--
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
// owner=0 for kernel, 1-31 for shepherd-owned pages.
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
	case PageUserText, PageUserROData, PageUserData, PageUserHeap, PageUserStack,
		PageFontCache, PageIPCBuffer:
		buddyAlloc.userPages += pagesAllocated
	case PageUserPT:
		buddyAlloc.userPTPages += pagesAllocated
	}

	// Per-type detail counter
	if pageType < PageTypeCount {
		buddyPagesByType[pageType] += pagesAllocated
	}

	buddyAlloc.lock.Unlock()

	SetPageDescriptor(pa, pageType, owner, uint8(order))

	return pa
}

// buddyWarnOOM prints an OOM warning with a per-type page breakdown.
// Uses direct UART output to avoid allocations when memory is exhausted.
// NOT nosplit — breaks the nosplit chain from exception handlers.
// Caller must NOT hold the buddy lock (it is re-acquired by PagesByType).
func buddyWarnOOM(order int) {
	SerialPuts("[kmem] Buddy OOM order=")
	SerialHex16(uint64(order))
	logPageBreakdownUART()
}

// LogPageBreakdown logs the current page allocation breakdown via direct
// UART. Safe to call from normal (non-IRQ) context. Uses no heap allocation.
func LogPageBreakdown() {
	logPageBreakdownUART()
}

// logPageBreakdownUART dumps pool stats and per-type page counts via UART.
// No heap allocation — safe to call when OOM.
func logPageBreakdownUART() {
	stats := GetBuddyStats()
	byType := PagesByType()

	SerialPuts(" total=")
	SerialHex16(stats.TotalPages)
	SerialPuts(" alloc=")
	SerialHex16(stats.AllocatedPages)
	SerialPuts(" free=")
	SerialHex16(stats.FreePages)
	SerialPuts(" peak=")
	SerialHex16(stats.PeakAllocated)
	serial.PollWrite('\r')
	serial.PollWrite('\n')

	for i := 0; i < int(PageTypeCount); i++ {
		if byType[i] > 0 {
			SerialPuts("[kmem]   ")
			SerialPuts(pageTypeNames[i])
			SerialPuts(": ")
			SerialHex16(byType[i])
			SerialPuts(" pages (")
			SerialHex16(byType[i] * 4)
			SerialPuts(" KB)\r\n")
		}
	}
	LogPagesByOwnerUART()
}

// SerialPuts writes a string to UART character by character.
//
//go:nosplit
func SerialPuts(s string) {
	for i := 0; i < len(s); i++ {
		serial.PollWrite(s[i])
	}
}

// SerialHex16 writes a uint64 as hex to UART, suppressing leading zeros.
//
//go:nosplit
func SerialHex16(v uint64) {
	if v == 0 {
		serial.PollWrite('0')
		return
	}
	started := false
	for i := 60; i >= 0; i -= 4 {
		d := (v >> uint(i)) & 0xF
		if d != 0 || started {
			started = true
			if d < 10 {
				serial.PollWrite(byte('0' + d))
			} else {
				serial.PollWrite(byte('a' + d - 10))
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

	// Captured before the merge loop below can reassign pa to a merged
	// buddy's (lower) address — the tracker record we need to shed was
	// filed under the ORIGINAL pa, not whatever this block merges into.
	originalPA := pa

	pagesFreed := uint64(1) << uint(order)

	buddyAlloc.lock.Lock()

	// Detect double-free BEFORE merging: check the original pa/order.
	// If we check after the merge loop, a double-free where the buddy
	// is also free silently corrupts: buddyRemoveSpecific succeeds,
	// the merged block passes buddyContainsPA (it was never inserted
	// at the higher order), and buddyInsertFree puts the merged block
	// on the free list — while the original block remains on its
	// order's list. Double-allocation follows.
	if buddyContainsPA(pa, order) {
		buddyAlloc.lock.Unlock()
		buddyDoubleFreeHalt(pa, order, pageType)
		return
	}

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
	case PageUserText, PageUserROData, PageUserData, PageUserHeap, PageUserStack,
		PageFontCache, PageIPCBuffer:
		if buddyAlloc.userPages >= pagesFreed {
			buddyAlloc.userPages -= pagesFreed
		}
	case PageUserPT:
		if buddyAlloc.userPTPages >= pagesFreed {
			buddyAlloc.userPTPages -= pagesFreed
		}
	}

	// Per-type detail counter
	if pageType < PageTypeCount {
		if buddyPagesByType[pageType] >= pagesFreed {
			buddyPagesByType[pageType] -= pagesFreed
		}
	}

	buddyAlloc.lock.Unlock()

	// MAZ-163: shed the page tracker's record for this PA. BuddyFreeTyped is
	// the common path every free eventually reaches (releasePageByPA,
	// FreeBuffer, munmap.go, mmap.go's error-unwind), so enqueuing the
	// deferred untrack here — rather than at each individual call site —
	// gives the tracker exactly one shed point. This function is
	// //go:nosplit on the SyscallMunmap -> munmapClump -> BuddyFreeTyped
	// IRQ-off chain, so it must NOT call UntrackPage directly (splittable,
	// O(n) scan under trackerLock — see page_tracker.go's doc comment).
	// QueueDeferredRecord is nosplit and lock-free; the SAME ring as every
	// TrackPage enqueue, deliberately — FIFO ordering across one ring is
	// what keeps a PA's track/untrack/re-track sequence correctly ordered
	// (see deferred.go's header comment for why a separate ring broke this).
	QueueDeferredRecord(DeferredPageRecord{
		Op: DeferredOpUntrack,
		PA: originalPA,
	})
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
//
//go:noinline
func buddyCorruptionHalt(prev, pa uintptr, order int) {
	klog.Criticalf("B!C!",
		"[BUDDY] CORRUPT free list! order=%d bad-next=0x%x looking-for=0x%x pool=[0x%x,0x%x)\n",
		order, prev, pa, buddyAlloc.poolStart, buddyAlloc.poolEnd)
	for {
	}
}

// buddyDoubleFreeHalt is invoked when BuddyFreeTyped detects that pa is
// already on the free list for this order — a double-free. Halts with a
// loud marker so the serial log shows the offending pa/order/type.
//
//go:noinline
func buddyDoubleFreeHalt(pa uintptr, order int, pageType PageType) {
	klog.Criticalf("D!F!",
		"[BUDDY] DOUBLE FREE! pa=0x%x order=%d type=%d pool=[0x%x,0x%x)\n",
		pa, order, int(pageType), buddyAlloc.poolStart, buddyAlloc.poolEnd)
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
	stats.AllocatedPages = buddyAlloc.bootstrapPages + buddyAlloc.allocatedPages + buddyAlloc.carveoutPages
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

// KernelHeapPageCount returns the current kernel heap page count.
// Uses atomic load for correctness. Safe to call from timer IRQ context
// (no lock — acquiring the buddy spinlock from IRQ would deadlock if
// the lock is already held by the interrupted thread).
//
//go:nosplit
func KernelHeapPageCount() uint64 {
	return atomic.LoadUint64(&buddyAlloc.kernelHeapPages)
}

// PagesByType returns a copy of the per-type page counters under the
// buddy lock for a consistent snapshot.
func PagesByType() [PageTypeCount]uint64 {
	buddyAlloc.lock.Lock()
	result := buddyPagesByType
	buddyAlloc.lock.Unlock()
	return result
}

// LogMemStats prints a full memory breakdown to the serial console.
// Includes pool totals, per-type counts, and per-SID counts.
// Safe to call from normal (non-IRQ) context. No heap allocation.
func LogMemStats() {
	SerialPuts("[kmem:stats]")
	logPageBreakdownUART()
}

// KernelPageFaultCount returns the number of successful kernel page faults.
// Safe to call from timer IRQ context.
//
//go:nosplit
func KernelPageFaultCount() uint64 {
	return pfSuccessCount
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
		klog.Errf("[kmem] AllocBuffer: size too large (0x%x)\n", size)
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
//
// MAZ-163: BuddyFreeTyped now enqueues the untrack itself; do not add a
// second call here.
func FreeBuffer(buf *BuddyBuffer) {
	if buf == nil {
		return
	}
	BuddyFreeTyped(buf.PA, buf.Order, buf.Type)
}

// Bytes returns a []byte slice backed by the buddy-allocated buffer.
// The slice length is the originally requested size.
func (b *BuddyBuffer) Bytes() []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(b.VA)), b.Size)
}
