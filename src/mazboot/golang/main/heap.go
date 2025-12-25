package main

import (
	"mazboot/asm"
	"unsafe"
)

// Memory management constants
const (
	PAGE_SIZE      = 4096 // 4KB page size
	HEAP_ALIGNMENT = 16   // 16-byte alignment for allocations
	// Note: KMALLOC_HEAP_BASE and KMALLOC_HEAP_SIZE are defined in mmu.go
)

// Linker symbol: end of kernel (from linker.ld)
// Moved to memory.go to centralize linker symbol access

// heapSegment represents a segment in the heap's doubly-linked list
// This structure is placed at the start of each allocated/free segment
type heapSegment struct {
	next        *heapSegment // Next segment in the list
	prev        *heapSegment // Previous segment in the list
	isAllocated uint32       // 1 if allocated, 0 if free
	isReserved  uint32       // 1 if reserved (cannot be freed), 0 if normal
	segmentSize uint32       // Total size including this header
}

// heapSegmentListHead points to the first segment in the heap
var heapSegmentListHead *heapSegment

// heapInit initializes the heap starting at the given address
// heapStart should be KMALLOC_HEAP_BASE (fixed address from mmu.go)
//
//go:nosplit
func heapInit(heapStart uintptr) {
	// Cast the start address to a heap segment pointer
	heapSegmentListHead = castToPointer[heapSegment](heapStart)

	// Zero out the initial segment header
	asm.Bzero(unsafe.Pointer(heapSegmentListHead), uint32(unsafe.Sizeof(heapSegment{})))

	// Initialize the first segment to represent the entire heap as free
	heapSegmentListHead.next = nil
	heapSegmentListHead.prev = nil
	heapSegmentListHead.isAllocated = 0
	heapSegmentListHead.segmentSize = uint32(KMALLOC_HEAP_SIZE)
}

// kmalloc allocates size bytes from the heap and returns a pointer to the memory
// Returns nil if allocation fails (out of memory)
// The returned pointer points to the data area (after the heapSegment header)
//
//go:nosplit
func kmalloc(size uint32) unsafe.Pointer {
	var curr *heapSegment
	var best *heapSegment
	bestDiff := int32(0x7FFFFFFF) // Max signed int32

	// Calculate the total size needed, accounting for:
	// 1. Header size
	// 2. Data pointer alignment (must be 16-byte aligned)
	// 3. Header pointer storage (8 bytes stored just before data pointer for kfree)
	// 4. Requested data size
	headerSize := uint32(unsafe.Sizeof(heapSegment{}))
	align := uintptr(HEAP_ALIGNMENT)
	headerPtrSize := uintptr(8) // 8 bytes to store segment header address (64-bit pointer)

	// Calculate worst-case padding needed for data pointer alignment
	// This assumes the segment base is 16-byte aligned (worst case for padding)
	// Actual padding may be less, but this ensures we allocate enough space
	headerSizeUintptr := uintptr(headerSize)
	dataPtrAfterHeader := headerSizeUintptr
	dataRemainder := dataPtrAfterHeader % align
	maxDataPadding := uintptr(0)
	if dataRemainder != 0 {
		maxDataPadding = align - dataRemainder
	}

	// Total size = header + max data padding + header pointer storage + requested size, then align to 16 bytes
	// We use max padding to ensure we always allocate enough, even if actual padding is less
	totalSize := uint32(headerSizeUintptr + maxDataPadding + headerPtrSize + uintptr(size))

	// WORKAROUND: Add guard zone for large allocations (likely stack allocations)
	// Go's compiled code may access addresses above the SP, so we add a buffer zone
	// to prevent corruption of the heap segment header that follows.
	if size >= 4096 {
		totalSize += 256 // 256-byte guard zone for stack allocations
	}

	remainder := uintptr(totalSize) % align
	if remainder != 0 {
		totalSize = uint32(uintptr(totalSize) + align - remainder)
	}

	// Find the best-fit free segment
	if heapSegmentListHead == nil {
		return nil
	}

	curr = heapSegmentListHead

	loopCount := uint32(0)
	maxLoops := uint32(1000) // Safety limit to prevent infinite loops
	for curr != nil && loopCount < maxLoops {
		// Validate pointer is in valid heap range
		currPtr := uintptr(unsafe.Pointer(curr))
		if currPtr < uintptr(unsafe.Pointer(heapSegmentListHead)) || currPtr > uintptr(unsafe.Pointer(heapSegmentListHead))+uintptr(KMALLOC_HEAP_SIZE) {
			return nil
		}

		if curr.isAllocated == 0 {
			// This segment is free
			diff := int32(curr.segmentSize) - int32(totalSize)
			if diff >= 0 && diff < bestDiff {
				best = curr
				bestDiff = diff
				// Found a suitable segment, can break early if exact match
				if diff == 0 {
					break
				}
			}
		}
		// Validate next pointer before following it
		if curr.next != nil {
			nextPtr := uintptr(unsafe.Pointer(curr.next))
			heapStart := uintptr(unsafe.Pointer(heapSegmentListHead))
			heapEnd := heapStart + uintptr(KMALLOC_HEAP_SIZE)
			if nextPtr < heapStart || nextPtr > heapEnd {
				return nil // Heap structure corrupted
			}
		}
		curr = curr.next
		loopCount++
	}

	// Loop limit or no suitable segment found
	if loopCount >= maxLoops || best == nil {
		return nil
	}

	// Now that we know 'best', recalculate totalSize based on actual address alignment
	// This ensures the segment size matches the actual data pointer layout
	bestAddr := pointerToUintptr(unsafe.Pointer(best))
	actualDataPtrAfterHeader := bestAddr + uintptr(headerSize)
	actualDataRemainder := actualDataPtrAfterHeader % align
	actualDataPadding := uintptr(0)
	if actualDataRemainder != 0 {
		actualDataPadding = align - actualDataRemainder
	}

	// Recalculate totalSize with actual padding (including header pointer storage)
	actualTotalSize := uint32(uintptr(headerSize) + actualDataPadding + headerPtrSize + uintptr(size))

	// WORKAROUND: Add guard zone for large allocations (must match initial calculation)
	if size >= 4096 {
		actualTotalSize += 256 // 256-byte guard zone for stack allocations
	}

	actualRemainder := uintptr(actualTotalSize) % align
	if actualRemainder != 0 {
		actualTotalSize = uint32(uintptr(actualTotalSize) + align - actualRemainder)
	}

	// Verify the segment is still large enough with actual size
	if best.segmentSize < actualTotalSize {
		return nil
	}

	// Use the actual totalSize for splitting and allocation
	// Note: totalSize may be updated later if extra padding is needed for header pointer
	totalSize = actualTotalSize

	// Calculate where the data pointer should be (16-byte aligned)
	// We need space for the 8-byte header pointer before the data area
	headerEndAddr := bestAddr + unsafe.Sizeof(heapSegment{})
	dataPtrAddr := headerEndAddr
	finalDataRemainder := dataPtrAddr % align
	if finalDataRemainder != 0 {
		dataPtrAddr += align - finalDataRemainder
	}

	// Reserve 8 bytes for header pointer storage before the aligned data pointer
	headerPtrAddr := dataPtrAddr - 8
	extraPadding := uintptr(0)
	if headerPtrAddr < headerEndAddr {
		// Need more padding to fit the header pointer storage
		extraPadding = align
		dataPtrAddr += align
		headerPtrAddr = dataPtrAddr - 8
	}

	// Update totalSize to account for extra padding if needed
	// This MUST be done before splitting, otherwise the split will use the wrong size
	if extraPadding > 0 {
		// Update totalSize to include the extra padding
		actualTotalSizeWithPadding := actualTotalSize + uint32(extraPadding)
		// Re-align if necessary
		paddingRemainder := uintptr(actualTotalSizeWithPadding) % align
		if paddingRemainder != 0 {
			actualTotalSizeWithPadding = uint32(uintptr(actualTotalSizeWithPadding) + align - paddingRemainder)
		}
		// Verify segment is still large enough
		if best.segmentSize < actualTotalSizeWithPadding {
			return nil
		}
		actualTotalSize = actualTotalSizeWithPadding
		totalSize = actualTotalSize
		// CRITICAL: Update bestDiff to reflect the new totalSize
		bestDiff = int32(best.segmentSize) - int32(totalSize)
	}

	// If the segment is much larger than needed, split it
	minSplitSize := uint32(2 * unsafe.Sizeof(heapSegment{}))
	if bestDiff > int32(minSplitSize) {
		newSegAddr := bestAddr + uintptr(totalSize)
		newSeg := castToPointer[heapSegment](newSegAddr)

		// Zero out the new segment
		// This ensures isReserved field is 0 (Bzero zeros the entire structure)
		asm.Bzero(unsafe.Pointer(newSeg), uint32(unsafe.Sizeof(heapSegment{})))

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
	// IMPORTANT: The data area must be 16-byte aligned for mailbox operations
	// We store the segment header address just before the data pointer so kfree can find it
	// Layout: [header][padding if needed][header pointer (8 bytes)][data area (16-byte aligned)]
	// Note: dataPtrAddr and headerPtrAddr are already calculated above (before splitting)

	// Verify we have enough space in the segment for header pointer + data
	segmentEnd := bestAddr + uintptr(best.segmentSize)
	dataEnd := dataPtrAddr + uintptr(size)
	if dataEnd > segmentEnd {
		return nil
	}

	// Verify header pointer storage is within segment bounds
	if headerPtrAddr < bestAddr || headerPtrAddr >= segmentEnd {
		return nil
	}

	// Store the segment header address just before the data pointer (8 bytes for 64-bit pointer)
	// This allows kfree to find the segment header even when data pointer is aligned
	headerPtr := (*uintptr)(unsafe.Pointer(headerPtrAddr))
	*headerPtr = bestAddr

	// Return pointer to the data area (after the stored header pointer)
	dataPtr := unsafe.Pointer(dataPtrAddr)

	return dataPtr
}

// heapDebugDump prints the state of all heap segments for debugging
//
//go:nosplit
func heapDebugDump() {
	print("=== HEAP DEBUG DUMP ===\r\n")

	// DEBUG: Check struct offsets once at start
	print("  sizeof(heapSegment)=0x")
	printHex64(uint64(unsafe.Sizeof(heapSegment{})))
	print(" next@0x")
	printHex64(uint64(unsafe.Offsetof(heapSegment{}.next)))
	print(" size@0x")
	printHex64(uint64(unsafe.Offsetof(heapSegment{}.segmentSize)))
	print(" alloc@0x")
	printHex64(uint64(unsafe.Offsetof(heapSegment{}.isAllocated)))
	print("\r\n")

	curr := heapSegmentListHead
	count := 0
	for curr != nil && count < 10 {
		currAddr := uintptr(unsafe.Pointer(curr))
		print("  seg[")
		printHex64(uint64(count))
		print("] addr=0x")
		printHex64(uint64(currAddr))
		// Compare struct access vs raw memory access for size field
		structSize := curr.segmentSize
		rawSize := readMemory32(currAddr + 0x18) // offset of segmentSize
		print(" size=0x")
		printHex64(uint64(structSize))
		if structSize != rawSize {
			print("(raw=0x")
			printHex64(uint64(rawSize))
			print("!)")
		}
		print(" alloc=")
		printHex64(uint64(curr.isAllocated))
		print(" next=0x")
		printHex64(uint64(uintptr(unsafe.Pointer(curr.next))))
		print("\r\n")
		curr = curr.next
		count++
	}
	print("=== END HEAP DUMP ===\r\n")
}

// kmallocReserved allocates size bytes from the heap and marks them as reserved
// Reserved memory cannot be freed via kfree() - it's permanent for the lifetime of the system
// Returns nil if allocation fails (out of memory)
// The returned pointer points to the data area (after the heapSegment header)
//
func kmallocReserved(size uint32) unsafe.Pointer {
	ptr := kmalloc(size)
	if ptr == nil {
		return nil
	}

	// Get the segment header and mark as reserved
	ptrAddr := pointerToUintptr(ptr)
	headerPtrAddr := ptrAddr - 8
	headerPtr := (*uintptr)(unsafe.Pointer(headerPtrAddr))
	segAddr := *headerPtr
	seg := castToPointer[heapSegment](segAddr)
	seg.isReserved = 1

	return ptr
}

// kfree frees memory previously allocated by kmalloc
// ptr must be a pointer returned by kmalloc (points to data area, not header)
//
//go:nosplit
func kfree(ptr unsafe.Pointer) {
	if ptr == nil {
		return
	}

	// Get the segment header address stored just before the data pointer
	// kmalloc stores the segment header address in the 8 bytes before the data pointer
	ptrAddr := pointerToUintptr(ptr)
	headerPtrAddr := ptrAddr - 8
	headerPtr := (*uintptr)(unsafe.Pointer(headerPtrAddr))
	segAddr := *headerPtr
	seg := castToPointer[heapSegment](segAddr)

	// Check if this allocation is reserved (cannot be freed)
	if seg.isReserved != 0 {
		print("FATAL: kfree reserved memory\r\n")
		for {
		}
	}

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

// initKmallocHeap calculates kmalloc heap boundaries from linker symbols
// This MUST be called before heapInit()
//
//go:nosplit
func initKmallocHeap() {
	// CRITICAL: Call assembly helpers directly instead of getLinkerSymbol()
	// because getLinkerSymbol() uses string comparisons that access .rodata
	bssEnd := asm.GetBssEndAddr()
	mazbootEnd := asm.GetMazbootEnd()

	// Heap starts at first page after BSS (page-aligned)
	heapStart := (bssEnd + 0xFFF) &^ 0xFFF

	// Heap ends at mazboot allocation boundary
	heapEnd := mazbootEnd

	// Calculate heap size
	heapSize := heapEnd - heapStart

	// Set global variables
	KMALLOC_HEAP_BASE = heapStart
	KMALLOC_HEAP_END = heapEnd
	KMALLOC_HEAP_SIZE = heapSize

	// Validation: ensure we have at least 1MB of heap
	const minHeapSize = 1024 * 1024
	if heapSize < minHeapSize {
		print("FATAL: Heap too small: ")
		printHex64(uint64(heapSize))
		print(" bytes (need at least ")
		printHex64(uint64(minHeapSize))
		print(")\r\n")
		for {
		}
	}
}

// memInit initializes both page management and heap allocator
//
//go:nosplit
func memInit(atagsPtr uintptr) {
	initKmallocHeap() // Calculate heap boundaries from linker symbols
	pageInit(atagsPtr)
	heapInit(KMALLOC_HEAP_BASE)
}
