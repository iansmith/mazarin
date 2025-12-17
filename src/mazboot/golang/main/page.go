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
	var memSize, pageArrayLen, kernelPages, i uint32

	// Get total memory size (fallback to 128MB for QEMU)
	memSize = getMemSize(atagsPtr)
	if memSize == 0 {
		memSize = 128 * 1024 * 1024 // 128MB for kernel RAM region
	} else if memSize > 128*1024*1024 {
		memSize = 128 * 1024 * 1024
	}

	// Calculate number of pages and allocate page metadata array
	numPages = memSize / PAGE_SIZE
	pageArrayLen = uint32(unsafe.Sizeof(Page{})) * numPages
	allPagesArrayBase = getLinkerSymbol("__end")
	allPagesArrayPtr := unsafe.Pointer(allPagesArrayBase)
	asm.Bzero(allPagesArrayPtr, pageArrayLen)

	// Calculate kernel pages
	const RAM_START = 0x40000000
	__endAddr := getLinkerSymbol("__end")
	if __endAddr >= RAM_START {
		kernelPages = uint32((__endAddr - RAM_START) / PAGE_SIZE)
	}

	// Initialize kernel pages (limit to 1000 to speed up initialization)
	ramStartPage := uint32(RAM_START / PAGE_SIZE)
	maxKernelPages := kernelPages
	if maxKernelPages > 1000 {
		maxKernelPages = 1000
	}
	kernelPageEnd := ramStartPage + maxKernelPages
	if kernelPageEnd > numPages {
		kernelPageEnd = numPages
	}
	for i = ramStartPage; i < kernelPageEnd; i++ {
		pagePtr := (*Page)(unsafe.Pointer(allPagesArrayBase + uintptr(i)*unsafe.Sizeof(Page{})))
		pagePtr.vaddrMapped = uintptr(i * PAGE_SIZE)
		pagePtr.flags = PackPageFlags(PageFlags{Allocated: true, KernelPage: true})
	}

	// Reserve heap pages
	heapPages := uint32((KMALLOC_HEAP_SIZE + PAGE_SIZE - 1) / PAGE_SIZE)
	heapPageEnd := kernelPageEnd + heapPages
	if heapPageEnd > numPages {
		heapPageEnd = numPages
	}
	for i = kernelPageEnd; i < heapPageEnd; i++ {
		pagePtr := (*Page)(unsafe.Pointer(allPagesArrayBase + uintptr(i)*unsafe.Sizeof(Page{})))
		pagePtr.vaddrMapped = uintptr(i * PAGE_SIZE)
		pagePtr.flags = PackPageFlags(PageFlags{Allocated: true, KernelPage: true})
	}

	// Build free page list (limit to 1000 pages)
	freePages = nil
	maxFreePages := numPages
	if maxFreePages > 1000 {
		maxFreePages = 1000
	}
	for ; i < maxFreePages; i++ {
		pagePtr := (*Page)(unsafe.Pointer(allPagesArrayBase + uintptr(i)*unsafe.Sizeof(Page{})))
		pagePtr.flags = PackPageFlags(PageFlags{Allocated: false, KernelPage: false})
		pagePtr.next = freePages
		pagePtr.prev = nil
		if freePages != nil {
			freePages.prev = pagePtr
		}
		freePages = pagePtr
	}
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
