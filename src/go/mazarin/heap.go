package main

import (
	"unsafe"
)

// Memory management constants
const (
	PAGE_SIZE        = 4096        // 4KB page size
	KERNEL_HEAP_SIZE = 1024 * 1024 // 1 MB heap size
	HEAP_ALIGNMENT   = 16          // 16-byte alignment for allocations
)

// Linker symbol: end of kernel (from linker.ld)
// Moved to memory.go to centralize linker symbol access

// heapSegment represents a segment in the heap's doubly-linked list
// This structure is placed at the start of each allocated/free segment
type heapSegment struct {
	next        *heapSegment // Next segment in the list
	prev        *heapSegment // Previous segment in the list
	isAllocated uint32       // 1 if allocated, 0 if free
	segmentSize uint32       // Total size including this header
}

// heapSegmentListHead points to the first segment in the heap
var heapSegmentListHead *heapSegment

// heapInit initializes the heap starting at the given address
// heapStart should be aligned to a reasonable boundary (e.g., 16 bytes)
//
//go:nosplit
func heapInit(heapStart uintptr) {
	// Debug: Write 'I' to mark heapInit entry - use direct writes to avoid any issues
	// Use inline assembly-like direct writes
	uartBaseLocal := uintptr(0x09000000)
	uartFRLocal := uartBaseLocal + 0x18
	uartDRLocal := uartBaseLocal + 0x00
	for mmio_read(uartFRLocal)&(1<<5) != 0 {
	}
	mmio_write(uartDRLocal, uint32('I'))

	// Cast the start address to a heap segment pointer
	heapSegmentListHead = castToPointer[heapSegment](heapStart)

	// Debug: Write 'J' after setting head
	for mmio_read(uartFRLocal)&(1<<5) != 0 {
	}
	mmio_write(uartDRLocal, uint32('J'))

	// Zero out the initial segment header
	bzero(unsafe.Pointer(heapSegmentListHead), uint32(unsafe.Sizeof(heapSegment{})))

	// Debug: Write 'K' after bzero
	for mmio_read(uartFRLocal)&(1<<5) != 0 {
	}
	mmio_write(uartDRLocal, uint32('K'))

	// Initialize the first segment to represent the entire heap as free
	heapSegmentListHead.next = nil
	heapSegmentListHead.prev = nil
	heapSegmentListHead.isAllocated = 0
	heapSegmentListHead.segmentSize = KERNEL_HEAP_SIZE

	// Debug: Write 'L' after initialization
	for mmio_read(uartFRLocal)&(1<<5) != 0 {
	}
	mmio_write(uartDRLocal, uint32('L'))
}

// kmalloc allocates size bytes from the heap and returns a pointer to the memory
// Returns nil if allocation fails (out of memory)
// The returned pointer points to the data area (after the heapSegment header)
//
//go:nosplit
func kmalloc(size uint32) unsafe.Pointer {
	// Debug: Write 'K' to mark kmalloc entry
	const uartBase uintptr = 0x09000000
	const uartFR = uartBase + 0x18
	const uartDR = uartBase + 0x00
	for mmio_read(uartFR)&(1<<5) != 0 {
	}
	mmio_write(uartDR, uint32('K'))

	var curr *heapSegment
	var best *heapSegment
	bestDiff := int32(0x7FFFFFFF) // Max signed int32

	// Debug: Write '1' after variable initialization
	for mmio_read(uartFR)&(1<<5) != 0 {
	}
	mmio_write(uartDR, uint32('1'))

	// Add the header size to the requested size
	totalSize := size + uint32(unsafe.Sizeof(heapSegment{}))

	// Align to 16 bytes
	align := uintptr(HEAP_ALIGNMENT)
	remainder := uintptr(totalSize) % align
	if remainder != 0 {
		totalSize = uint32(uintptr(totalSize) + align - remainder)
	}

	// Debug: Write '2' before heap check
	for mmio_read(uartFR)&(1<<5) != 0 {
	}
	mmio_write(uartDR, uint32('2'))

	// Find the best-fit free segment
	// Safety check: if heap isn't initialized, return nil
	if heapSegmentListHead == nil {
		// Debug: Write 'N' for nil head
		for mmio_read(uartFR)&(1<<5) != 0 {
		}
		mmio_write(uartDR, uint32('N'))
		return nil
	}

	// Debug: Write '3' after nil check
	for mmio_read(uartFR)&(1<<5) != 0 {
	}
	mmio_write(uartDR, uint32('3'))

	curr = heapSegmentListHead

	// Debug: Write '4' after setting curr
	for mmio_read(uartFR)&(1<<5) != 0 {
	}
	mmio_write(uartDR, uint32('4'))

	loopCount := uint32(0)
	maxLoops := uint32(1000) // Safety limit to prevent infinite loops
	for curr != nil && loopCount < maxLoops {
		// Debug: Write 'L' each loop iteration
		for mmio_read(uartFR)&(1<<5) != 0 {
		}
		mmio_write(uartDR, uint32('L'))

		if curr.isAllocated == 0 {
			// This segment is free
			diff := int32(curr.segmentSize) - int32(totalSize)
			if diff >= 0 && diff < bestDiff {
				best = curr
				bestDiff = diff
			}
		}
		curr = curr.next // This might be causing the hang if curr.next is invalid
		loopCount++
	}

	// Debug: Write '5' after loop
	for mmio_read(uartFR)&(1<<5) != 0 {
	}
	mmio_write(uartDR, uint32('5'))

	// If we hit the loop limit, something is wrong with the heap structure
	if loopCount >= maxLoops {
		return nil
	}

	// No suitable free segment found
	if best == nil {
		return nil
	}

	// If the segment is much larger than needed, split it
	minSplitSize := uint32(2 * unsafe.Sizeof(heapSegment{}))
	if bestDiff > int32(minSplitSize) {
		// Calculate the address of the new segment
		newSegAddr := pointerToUintptr(unsafe.Pointer(best)) + uintptr(totalSize)
		newSeg := castToPointer[heapSegment](newSegAddr)

		// Zero out the new segment
		bzero(unsafe.Pointer(newSeg), uint32(unsafe.Sizeof(heapSegment{})))

		// Update the new segment's fields
		newSeg.next = best.next
		newSeg.prev = best
		newSeg.isAllocated = 0
		newSeg.segmentSize = best.segmentSize - totalSize

		// Update links
		best.next = newSeg
		if newSeg.next != nil {
			newSeg.next.prev = newSeg
		}

		// Update the allocated segment's size
		best.segmentSize = totalSize
	}

	// Mark the segment as allocated
	best.isAllocated = 1

	// Return pointer to the data area (after the header)
	// In C: return best + 1
	// In Go: advance pointer by sizeof(heapSegment)
	// IMPORTANT: The data area must be 16-byte aligned for mailbox operations
	dataPtrAddr := pointerToUintptr(unsafe.Pointer(best)) + unsafe.Sizeof(heapSegment{})
	// Align data pointer to 16 bytes (reuse align variable from above)
	dataRemainder := dataPtrAddr % align
	if dataRemainder != 0 {
		dataPtrAddr += align - dataRemainder
	}
	dataPtr := unsafe.Pointer(dataPtrAddr)
	return dataPtr
}

// kfree frees memory previously allocated by kmalloc
// ptr must be a pointer returned by kmalloc (points to data area, not header)
//
//go:nosplit
func kfree(ptr unsafe.Pointer) {
	if ptr == nil {
		return
	}

	// Get the segment header by subtracting the header size from the pointer
	// In C: seg = ptr - sizeof(heap_segment_t)
	seg := castToPointer[heapSegment](pointerToUintptr(ptr) - unsafe.Sizeof(heapSegment{}))

	// Mark as free
	seg.isAllocated = 0

	// Coalesce with previous segment if it's free
	for seg.prev != nil && seg.prev.isAllocated == 0 {
		// Merge seg into prev
		prev := seg.prev
		prev.next = seg.next
		prev.segmentSize += seg.segmentSize
		if seg.next != nil {
			seg.next.prev = prev
		}
		seg = prev // Continue checking from the merged segment
	}

	// Coalesce with next segment if it's free
	for seg.next != nil && seg.next.isAllocated == 0 {
		// Merge next into seg
		next := seg.next
		seg.segmentSize += next.segmentSize
		seg.next = next.next
		if next.next != nil {
			next.next.prev = seg
		}
		// seg stays the same, check if the new next is also free
	}
}

// memInit initializes both page management and heap allocator
// This integrates Part 04 (page management) with Part 05 (heap allocator)
// Based on: https://jsandler18.github.io/tutorial/wrangling-mem.html
// and: https://jsandler18.github.io/tutorial/dyn-mem.html
//
//go:nosplit
func memInit(atagsPtr uintptr) {
	// Step 1: Initialize page management system (Part 04)
	// This also reserves heap pages
	pageInit(atagsPtr)

	// Step 2: Calculate heap start after page metadata array
	// Page metadata array starts at __end and has size: numPages * sizeof(Page)
	var pageArraySize uintptr
	if numPages > 0 {
		pageArraySize = uintptr(numPages) * unsafe.Sizeof(Page{})
	}

	// Heap starts after page metadata array
	// Align to 16-byte boundary for better performance
	heapStartBase := getLinkerSymbol("__end") + pageArraySize
	heapStart := (heapStartBase + HEAP_ALIGNMENT - 1) &^ (HEAP_ALIGNMENT - 1)

	// Step 3: Initialize heap allocator (Part 05)
	// Heap pages are already reserved by pageInit()
	heapInit(heapStart)
}
