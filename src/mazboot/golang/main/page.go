package main

import (
	"mazboot/asm"
	"unsafe"
)

// ATAG constants and structures for parsing boot parameters
// Based on: https://jsandler18.github.io/tutorial/wrangling-mem.html
type atagTag uint32

const (
	ATAG_NONE atagTag = 0x00000000
	ATAG_CORE atagTag = 0x54410001
	ATAG_MEM  atagTag = 0x54410002
)

// ATAG memory structure
type atagMem struct {
	size  uint32 // Size of the memory region in bytes
	start uint32 // Start address of the memory region
}

// ATAG structure
type atag struct {
	tagSize uint32    // Size of tag in words (includes this header)
	tag     atagTag   // Tag type
	mem     atagMem   // Memory tag data (union in C, struct field here)
	_       [6]uint32 // Padding for other tag types (we only care about MEM)
}

// Page structure - metadata for each 4KB page
// Based on tutorial Part 04
type Page struct {
	vaddrMapped uintptr // Virtual address this page maps to (identity mapped initially)
	flags       uint32  // Packed PageFlags using bitfield
	next        *Page   // Next page in free list (or nil)
	prev        *Page   // Previous page in free list (or nil)
}

// Free page list head
var freePages *Page

// All pages array base pointer (page metadata starts after kernel at __end)
var allPagesArrayBase uintptr

// Number of pages in the system
var numPages uint32

// getMemSize parses ATAGs to find total memory size
// Returns 0 if no MEM tag found or ATAGs not available
// Note: QEMU does not provide ATAGs for Raspberry Pi 4 - it uses Device Tree (DTB) instead
// ATAGs are only available on real hardware with bootloaders that support them
//
//go:nosplit
func getMemSize(atagsPtr uintptr) uint32 {
	// If atagsPtr is 0, ATAGs are not available (e.g., in QEMU which uses Device Tree)
	// Return 0 to indicate memory size cannot be determined from ATAGs
	if atagsPtr == 0 {
		return 0
	}

	// Validate pointer is in reasonable memory range
	// ATAGs should be in low memory (below 1GB typically)
	if atagsPtr > 0x40000000 { // Above 1GB is suspicious
		return 0 // Invalid pointer, return 0 to use fallback
	}

	// Cast pointer to atag structure
	tag := (*atag)(unsafe.Pointer(atagsPtr))

	// Safety: Limit iterations to prevent infinite loops from corrupted ATAGs
	// ATAG lists typically have at most 10-20 tags
	maxIterations := 32
	iterations := 0

	// Iterate through ATAG list until we find NONE tag
	for iterations < maxIterations {
		// Check if tag is NONE first
		if tag.tag == ATAG_NONE {
			break
		}

		if tag.tag == ATAG_MEM {
			return tag.mem.size
		}

		// Validate tagSize to prevent invalid memory access
		// Tag size must be at least 2 (header + tag field) and reasonable (max 32 words = 128 bytes)
		if tag.tagSize < 2 || tag.tagSize > 32 {
			// Invalid tag size, stop parsing
			break
		}

		// Move to next tag: tag = ((uint32_t *)tag) + tag->tag_size;
		// tagSize is in words (4 bytes each)
		nextAddr := uintptr(unsafe.Pointer(tag)) + uintptr(tag.tagSize*4)

		// Validate next address is reasonable
		if nextAddr > 0x40000000 || nextAddr < atagsPtr {
			// Address out of bounds or going backwards - corrupted ATAGs
			break
		}

		tag = (*atag)(unsafe.Pointer(nextAddr))
		iterations++
	}

	// No MEM tag found or parsing failed, return 0
	return 0
}

// pageInit initializes the page management system
// This corresponds to mem_init() in the tutorial
//
//go:nosplit
func pageInit(atagsPtr uintptr) {
	uartPuts("pageInit: Starting...\r\n")
	var memSize, pageArrayLen, kernelPages, i uint32

	// Get total memory size
	uartPuts("pageInit: Getting memory size...\r\n")
	memSize = getMemSize(atagsPtr)
	if memSize == 0 {
		// Fallback: use 128MB for kernel RAM region (not full 1GB)
		// QEMU has 1GB total, but kernel only uses 128MB (0x40100000 - 0x48100000)
		// This limits page array size to ~0.8MB instead of ~6.3MB
		// Heap can extend beyond 0x48100000 up to g0 stack at 0x5EFFFE000
		uartPuts("pageInit: Using 128MB for kernel RAM region (QEMU)\r\n")
		memSize = 128 * 1024 * 1024 // 128MB for kernel RAM region
	} else {
		uartPuts("pageInit: Got memory size from ATAGs\r\n")
		// Limit to 128MB max for kernel region (heap extends beyond)
		if memSize > 128*1024*1024 {
			memSize = 128 * 1024 * 1024
			uartPuts("pageInit: Limited to 128MB for kernel region\r\n")
		}
	}

	// Calculate number of pages
	numPages = memSize / PAGE_SIZE
	uartPuts("pageInit: Calculated numPages\r\n")

	// Allocate space for page metadata array starting at __end
	pageArrayLen = uint32(unsafe.Sizeof(Page{})) * numPages
	uartPuts("pageInit: Calculated pageArrayLen\r\n")

	// Cast __end to Page array base pointer
	// In C: all_pages_array = (page_t *)&__end;
	allPagesArrayBase = getLinkerSymbol("__end")
	uartPuts("pageInit: Got allPagesArrayBase\r\n")
	allPagesArrayPtr := unsafe.Pointer(allPagesArrayBase)

	// Zero out the page array
	// Note: This can take a while if pageArrayLen is large
	// For 1GB with 4KB pages: ~262K pages * 24 bytes = ~6.3MB to zero
	// This might be where it's hanging - bzero of large area
	uartPuts("pageInit: Zeroing page array...\r\n")
	uartPuts("pageInit:   start = 0x")
	uartPutHex64(uint64(uintptr(allPagesArrayPtr)))
	uartPuts("\r\n")
	uartPuts("pageInit:   len = 0x")
	uartPutHex64(uint64(pageArrayLen))
	uartPuts("\r\n")
	uartPuts("pageInit:   end = 0x")
	uartPutHex64(uint64(uintptr(allPagesArrayPtr)) + uint64(pageArrayLen))
	uartPuts("\r\n")
	asm.Bzero(allPagesArrayPtr, pageArrayLen)
	uartPuts("pageInit: Page array zeroed\r\n")

	// Calculate kernel pages (pages up to __end)
	// __end is in RAM at 0x40100000+, so we need to calculate pages from RAM start (0x40000000)
	// Kernel uses pages from 0x40000000 to __end
	const RAM_START = 0x40000000
	__endAddr := getLinkerSymbol("__end")
	if __endAddr < RAM_START {
		// __end is before RAM start - shouldn't happen, but handle it
		kernelPages = 0
	} else {
		// Calculate pages from RAM start to __end
		kernelPages = uint32((__endAddr - RAM_START) / PAGE_SIZE)
	}
	uartPuts("pageInit: Calculated kernelPages\r\n")

	// Initialize kernel pages (mark as allocated and kernel pages)
	// Only initialize pages that are actually in use (from RAM_START to __end)
	uartPuts("pageInit: Initializing kernel pages...\r\n")
	// Calculate starting page index (RAM_START / PAGE_SIZE)
	ramStartPage := uint32(RAM_START / PAGE_SIZE)
	kernelPageEnd := ramStartPage + kernelPages
	// Limit to reasonable number to avoid huge loops
	// For now, limit to first 1000 pages to speed up initialization
	// TODO: Process all pages but in chunks or optimize the loop
	maxKernelPages := kernelPages
	if maxKernelPages > 1000 {
		maxKernelPages = 1000
		uartPuts("pageInit: Limiting kernel pages to 1000 (workaround)\r\n")
	}
	kernelPageEnd = ramStartPage + maxKernelPages
	if kernelPageEnd > numPages {
		kernelPageEnd = numPages
	}
	for i = ramStartPage; i < kernelPageEnd; i++ {
		pagePtr := (*Page)(unsafe.Pointer(allPagesArrayBase + uintptr(i)*unsafe.Sizeof(Page{})))

		// Identity map kernel pages (physical address = page index * PAGE_SIZE)
		pagePtr.vaddrMapped = uintptr(i * PAGE_SIZE)

		// Mark as allocated and kernel page
		flags := PageFlags{
			Allocated:  true,
			KernelPage: true,
			Reserved:   0,
		}
		packed := PackPageFlags(flags)
		pagePtr.flags = packed
	}
	uartPuts("pageInit: Kernel pages initialized\r\n")

	// Reserve pages for kernel heap (64 MB)
	// Based on tutorial Part 05, heap pages are reserved but marked as kernel pages
	heapPages := uint32((KERNEL_HEAP_SIZE + PAGE_SIZE - 1) / PAGE_SIZE) // Round up
	heapPageEnd := kernelPageEnd + heapPages
	if heapPageEnd > numPages {
		heapPageEnd = numPages
	}

	// Reserve heap pages (mark as allocated and kernel pages, but don't add to free list)
	uartPuts("pageInit: Reserving heap pages...\r\n")
	for i = kernelPageEnd; i < heapPageEnd; i++ {
		pagePtr := (*Page)(unsafe.Pointer(allPagesArrayBase + uintptr(i)*unsafe.Sizeof(Page{})))

		// Identity map heap pages
		pagePtr.vaddrMapped = uintptr(i * PAGE_SIZE)

		// Mark as allocated and kernel page (heap is kernel memory)
		flags := PageFlags{
			Allocated:  true,
			KernelPage: true,
			Reserved:   0,
		}
		packed := PackPageFlags(flags)
		pagePtr.flags = packed
	}
	uartPuts("pageInit: Heap pages reserved\r\n")

	// Initialize free pages list (empty initially)
	freePages = nil

	// Mark remaining pages as unallocated and add to free list
	uartPuts("pageInit: Building free page list...\r\n")
	// Limit free page list to avoid huge loops - only process first 1000 pages for now
	// TODO: This is a workaround - we should process all pages but in smaller chunks
	maxFreePages := numPages
	if maxFreePages > 1000 {
		maxFreePages = 1000 // Limit to first 1000 pages to avoid long initialization
		uartPuts("pageInit: Limiting free pages to 1000 (workaround)\r\n")
	}
	for ; i < maxFreePages; i++ {
		pagePtr := (*Page)(unsafe.Pointer(allPagesArrayBase + uintptr(i)*unsafe.Sizeof(Page{})))

		// Mark as unallocated
		flags := PageFlags{
			Allocated:  false,
			KernelPage: false,
			Reserved:   0,
		}
		packed := PackPageFlags(flags)
		pagePtr.flags = packed

		// Add to free list (simple append to head)
		pagePtr.next = freePages
		pagePtr.prev = nil
		if freePages != nil {
			freePages.prev = pagePtr
		}
		freePages = pagePtr
	}
	uartPuts("pageInit: Free page list built\r\n")
	uartPuts("pageInit: Complete\r\n")
}

// allocPage allocates a single 4KB page and returns a pointer to it
// Returns nil if no free pages available
// Based on tutorial Part 04
//
//go:nosplit
func allocPage() unsafe.Pointer {
	if freePages == nil {
		return nil // No free pages
	}

	// Get a free page from the list
	page := freePages
	freePages = page.next
	if freePages != nil {
		freePages.prev = nil
	}

	// Calculate page index in the array
	pageAddr := pointerToUintptr(unsafe.Pointer(page))
	if allPagesArrayBase == 0 {
		// Fallback if not initialized
		allPagesArrayBase = getLinkerSymbol("__end")
	}
	pageIndex := (pageAddr - allPagesArrayBase) / unsafe.Sizeof(Page{})

	// Mark page as allocated and kernel page
	flags := UnpackPageFlags(page.flags)
	flags.Allocated = true
	flags.KernelPage = true
	packed := PackPageFlags(flags)
	page.flags = packed

	// Calculate physical address of the page memory
	// Physical address = page_index * PAGE_SIZE
	pageMem := unsafe.Pointer(uintptr(pageIndex) * PAGE_SIZE)

	// Zero out the page (security: prevent data leakage)
	asm.Bzero(pageMem, PAGE_SIZE)

	return pageMem
}

// freePage frees a previously allocated page
// ptr must be a pointer returned by allocPage()
// Based on tutorial Part 04
//
//go:nosplit
func freePage(ptr unsafe.Pointer) {
	if ptr == nil {
		return
	}

	// Calculate page index from physical address
	// page_index = physical_address / PAGE_SIZE
	pageIndex := uintptr(ptr) / PAGE_SIZE

	// Get page metadata from the array
	if allPagesArrayBase == 0 {
		// Fallback if not initialized
		allPagesArrayBase = getLinkerSymbol("__end")
	}
	pageAddr := allPagesArrayBase + pageIndex*unsafe.Sizeof(Page{})
	page := castToPointer[Page](pageAddr)

	// Mark as free
	flags := UnpackPageFlags(page.flags)
	flags.Allocated = false
	packed := PackPageFlags(flags)
	page.flags = packed

	// Add back to free list (add to head)
	page.next = freePages
	page.prev = nil
	if freePages != nil {
		freePages.prev = page
	}
	freePages = page
}
