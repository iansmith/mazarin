package main

import (
	"mazboot/asm"
	"unsafe"
)

// Memory management constants
const (
	PAGE_SIZE        = 4096             // 4KB page size
	KERNEL_HEAP_SIZE = 64 * 1024 * 1024 // 64 MB heap size (increased for kernel development)
	HEAP_ALIGNMENT   = 16               // 16-byte alignment for allocations
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
	uartPuts("heapInit: Starting...\r\n")

	// Cast the start address to a heap segment pointer
	heapSegmentListHead = castToPointer[heapSegment](heapStart)
	uartPuts("heapInit: Set heapSegmentListHead\r\n")

	// Zero out the initial segment header
	asm.Bzero(unsafe.Pointer(heapSegmentListHead), uint32(unsafe.Sizeof(heapSegment{})))
	uartPuts("heapInit: Zeroed segment header\r\n")

	// Initialize the first segment to represent the entire heap as free
	// But limit it to available space before g0 stack region
	// g0 stack is 8KB at 0x5EFFFE000-0x5F000000 (grows downward from 0x5F000000)
	const G0_STACK_BOTTOM = 0x5EFFFE000 // g0 stack bottom (heap must end before this)
	heapEnd := heapStart + uintptr(KERNEL_HEAP_SIZE)
	actualHeapSize := uint32(KERNEL_HEAP_SIZE)

	// Check if heap would extend into g0 stack
	if heapEnd > G0_STACK_BOTTOM {
		// Heap would extend into g0 stack region - limit it
		maxSize := uint32(G0_STACK_BOTTOM - heapStart)
		if maxSize < 4*1024*1024 { // At least 4MB for framebuffer (3.6MB needed)
			uartPuts("heapInit: ERROR - Heap too small after stack boundary check\r\n")
			uartPuts("heapInit: Available space less than 4MB\r\n")
			// Don't return - try with what we have, but it will fail
		}
		actualHeapSize = maxSize
		uartPuts("heapInit: Limited heap size to avoid g0 stack\r\n")
	}

	// Verify heap is large enough for framebuffer (3.6MB + header overhead)
	// Framebuffer needs ~3.6MB, plus heapSegment header, plus alignment
	const minNeeded uint32 = 4 * 1024 * 1024 // 4MB should be enough
	if actualHeapSize < minNeeded {
		uartPuts("heapInit: WARNING - Heap size may be too small for framebuffer\r\n")
		uartPuts("heapInit: actualHeapSize is less than 4MB\r\n")
	}

	heapSegmentListHead.next = nil
	heapSegmentListHead.prev = nil
	heapSegmentListHead.isAllocated = 0
	heapSegmentListHead.segmentSize = actualHeapSize

	// Verify heap is large enough for framebuffer (3.6MB)
	if actualHeapSize < 4*1024*1024 {
		uartPuts("heapInit: WARNING - Heap may be too small for framebuffer\r\n")
	}
	uartPuts("heapInit: Complete\r\n")
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
	remainder := uintptr(totalSize) % align
	if remainder != 0 {
		totalSize = uint32(uintptr(totalSize) + align - remainder)
	}

	// Find the best-fit free segment
	// Safety check: if heap isn't initialized, return nil
	if heapSegmentListHead == nil {
		uartPuts("kmalloc: ERROR - heap not initialized\r\n")
		return nil
	}

	curr = heapSegmentListHead

	loopCount := uint32(0)
	maxLoops := uint32(1000) // Safety limit to prevent infinite loops
	for curr != nil && loopCount < maxLoops {
		// Safety check: validate pointer is in valid heap range
		currPtr := uintptr(unsafe.Pointer(curr))
		if currPtr < uintptr(unsafe.Pointer(heapSegmentListHead)) || currPtr > uintptr(unsafe.Pointer(heapSegmentListHead))+uintptr(KERNEL_HEAP_SIZE) {
			uartPuts("kmalloc: ERROR - invalid segment pointer detected\r\n")
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
		// Safety check: validate next pointer before following it
		nextPtr := uintptr(unsafe.Pointer(curr.next))
		if curr.next != nil {
			// Validate next pointer is in valid heap range
			heapStart := uintptr(unsafe.Pointer(heapSegmentListHead))
			heapEnd := heapStart + uintptr(KERNEL_HEAP_SIZE)
			if nextPtr < heapStart || nextPtr > heapEnd {
				uartPuts("kmalloc: ERROR - invalid next pointer detected\r\n")
				uartPuts("kmalloc: curr=")
				// Can't easily print hex, but we can indicate the problem
				uartPuts("heapStart=0x401xxxxx, nextPtr out of range\r\n")
				return nil // Heap structure corrupted, cannot allocate
			}
		}
		curr = curr.next
		loopCount++
	}

	// If we hit the loop limit, something is wrong with the heap structure
	if loopCount >= maxLoops {
		uartPuts("kmalloc: ERROR - loop limit reached\r\n")
		return nil
	}

	// No suitable free segment found
	if best == nil {
		uartPuts("kmalloc: No suitable free segment found\r\n")
		// Debug: Check what we have
		if heapSegmentListHead != nil {
			uartPuts("kmalloc: head segment size=")
			// Can't print number easily, but check if it's allocated
			if heapSegmentListHead.isAllocated != 0 {
				uartPuts("allocated\r\n")
			} else {
				uartPuts("free, but too small\r\n")
			}
			// Check if size is reasonable
			if heapSegmentListHead.segmentSize < totalSize {
				uartPuts("kmalloc: Segment too small for request\r\n")
			}
		}
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
	actualRemainder := uintptr(actualTotalSize) % align
	if actualRemainder != 0 {
		actualTotalSize = uint32(uintptr(actualTotalSize) + align - actualRemainder)
	}

	// Verify the segment is still large enough with actual size
	// (It should be, since we used worst-case padding for the search)
	if best.segmentSize < actualTotalSize {
		uartPuts("kmalloc: ERROR - Segment too small after recalculating alignment\r\n")
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
	// Ensure the header pointer storage area is after the segment header
	headerPtrAddr := dataPtrAddr - 8
	extraPadding := uintptr(0)
	if headerPtrAddr < headerEndAddr {
		// Need more padding to fit the header pointer storage
		// Move data pointer forward by one alignment unit to make room
		extraPadding = align
		dataPtrAddr += align
		headerPtrAddr = dataPtrAddr - 8
		uartPuts("kmalloc: Extra padding added for header pointer\r\n")
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
			uartPuts("kmalloc: ERROR - Segment too small after extra padding for header pointer\r\n")
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
		uartPuts("kmalloc: Splitting segment\r\n")
		// Calculate the address of the new segment using the updated totalSize
		newSegAddr := bestAddr + uintptr(totalSize)
		uartPuts("  best: ")
		uartPutHex64(uint64(bestAddr))
		uartPuts(", totalSize: ")
		uartPutHex64(uint64(totalSize))
		uartPuts(", newSeg: ")
		uartPutHex64(uint64(newSegAddr))
		uartPuts("\r\n")
		newSeg := castToPointer[heapSegment](newSegAddr)

		// Zero out the new segment
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
		uartPuts("kmalloc: ERROR - Not enough space for header pointer and data\r\n")
		return nil
	}

	// Verify header pointer storage is within segment bounds
	if headerPtrAddr < bestAddr || headerPtrAddr >= segmentEnd {
		uartPuts("kmalloc: ERROR - Header pointer storage outside segment bounds\r\n")
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
	uartPuts("memInit: Starting...\r\n")
	// Step 1: Initialize page management system (Part 04)
	// This also reserves heap pages
	uartPuts("memInit: Calling pageInit...\r\n")
	pageInit(atagsPtr)
	uartPuts("memInit: pageInit complete\r\n")

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

	// Step 2.5: Verify heap fits before g0 stack region
	// g0 stack is 8KB at 0x5EFFFE000-0x5F000000 (grows downward from 0x5F000000)
	const G0_STACK_BOTTOM = 0x5EFFFE000 // g0 stack bottom (heap must end before this)
	heapEnd := heapStart + KERNEL_HEAP_SIZE
	if heapEnd > G0_STACK_BOTTOM {
		// Heap would overlap with g0 stack - reduce heap size
		maxHeapSize := G0_STACK_BOTTOM - heapStart
		if maxHeapSize < 4*1024*1024 {
			// Not enough space for framebuffer (needs 3.6MB)
			uartPuts("memInit: ERROR - Not enough space for heap (would overlap g0 stack)\r\n")
			uartPuts("memInit: Available space is less than 4MB\r\n")
			return
		}
		// Use available space (will be set in heapInit)
		uartPuts("WARNING: Reducing heap size to fit before g0 stack\r\n")
		// Note: We can't change KERNEL_HEAP_SIZE constant, but heapInit uses it
		// For now, just warn - in practice with 128MB kernel region, heap extends to g0 stack
	}

	// Step 3: Initialize heap allocator (Part 05)
	// Heap pages are already reserved by pageInit()
	uartPuts("memInit: Calling heapInit...\r\n")
	heapInit(heapStart)
	uartPuts("memInit: Complete\r\n")
}
