package main

import (
	"unsafe"

	"mazarin/bitfield"
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
	tagSize uint32           // Size of tag in words (includes this header)
	tag     atagTag          // Tag type
	mem     atagMem          // Memory tag data (union in C, struct field here)
	_       [6]uint32        // Padding for other tag types (we only care about MEM)
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

	// Get total memory size
	memSize = getMemSize(atagsPtr)
	if memSize == 0 {
		// Fallback: use 128 MB default for page management
		// Note: This fallback is for internal use only - the display shows "unknown"
		// In QEMU, ATAGs aren't available, but we need a memory size for page management
		memSize = 1024 * 1024 * 128
	}

	// Calculate number of pages
	numPages = memSize / PAGE_SIZE
	// Debug output (we'll add uart functions here if needed, but for now just continue)

	// Allocate space for page metadata array starting at __end
	pageArrayLen = uint32(unsafe.Sizeof(Page{})) * numPages
	
	// Cast __end to Page array base pointer
	// In C: all_pages_array = (page_t *)&__end;
	allPagesArrayBase = uintptr(unsafe.Pointer(&__end))
	allPagesArrayPtr := unsafe.Pointer(allPagesArrayBase)
	
	// Zero out the page array
	// Note: This can take a while if pageArrayLen is large
	// For 128MB with 4KB pages: ~32K pages * 24 bytes = ~768KB to zero
	// This might be where it's hanging - bzero of large area
	bzero(allPagesArrayPtr, pageArrayLen)

	// Calculate kernel pages (pages up to __end)
	kernelPages = uint32(uintptr(unsafe.Pointer(&__end))) / PAGE_SIZE

	// Initialize kernel pages (mark as allocated and kernel pages)
	for i = 0; i < kernelPages; i++ {
		pagePtr := (*Page)(unsafe.Pointer(allPagesArrayBase + uintptr(i)*unsafe.Sizeof(Page{})))
		
		// Identity map kernel pages
		pagePtr.vaddrMapped = uintptr(i * PAGE_SIZE)
		
		// Mark as allocated and kernel page
		flags := bitfield.PageFlags{
			Allocated:  true,
			KernelPage: true,
			Reserved:   0,
		}
		packed, _ := bitfield.PackPageFlags(flags)
		pagePtr.flags = packed
	}

	// Reserve pages for kernel heap (1 MB)
	// Based on tutorial Part 05, heap pages are reserved but marked as kernel pages
	heapPages := uint32((KERNEL_HEAP_SIZE + PAGE_SIZE - 1) / PAGE_SIZE) // Round up
	heapPageEnd := kernelPages + heapPages
	
	// Reserve heap pages (mark as allocated and kernel pages, but don't add to free list)
	for ; i < heapPageEnd && i < numPages; i++ {
		pagePtr := (*Page)(unsafe.Pointer(allPagesArrayBase + uintptr(i)*unsafe.Sizeof(Page{})))
		
		// Identity map heap pages
		pagePtr.vaddrMapped = uintptr(i * PAGE_SIZE)
		
		// Mark as allocated and kernel page (heap is kernel memory)
		flags := bitfield.PageFlags{
			Allocated:  true,
			KernelPage: true,
			Reserved:   0,
		}
		packed, _ := bitfield.PackPageFlags(flags)
		pagePtr.flags = packed
	}

	// Initialize free pages list (empty initially)
	freePages = nil

	// Mark remaining pages as unallocated and add to free list
	for ; i < numPages; i++ {
		pagePtr := (*Page)(unsafe.Pointer(allPagesArrayBase + uintptr(i)*unsafe.Sizeof(Page{})))
		
		// Mark as unallocated
		flags := bitfield.PageFlags{
			Allocated:  false,
			KernelPage: false,
			Reserved:   0,
		}
		packed, _ := bitfield.PackPageFlags(flags)
		pagePtr.flags = packed
		
		// Add to free list (simple append to head)
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
	pageAddr := uintptr(unsafe.Pointer(page))
	if allPagesArrayBase == 0 {
		// Fallback if not initialized
		allPagesArrayBase = uintptr(unsafe.Pointer(&__end))
	}
	pageIndex := (pageAddr - allPagesArrayBase) / unsafe.Sizeof(Page{})

	// Mark page as allocated and kernel page
	flags := bitfield.UnpackPageFlags(page.flags)
	flags.Allocated = true
	flags.KernelPage = true
	packed, _ := bitfield.PackPageFlags(flags)
	page.flags = packed

	// Calculate physical address of the page memory
	// Physical address = page_index * PAGE_SIZE
	pageMem := unsafe.Pointer(uintptr(pageIndex) * PAGE_SIZE)

	// Zero out the page (security: prevent data leakage)
	bzero(pageMem, PAGE_SIZE)

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
		allPagesArrayBase = uintptr(unsafe.Pointer(&__end))
	}
	pageAddr := allPagesArrayBase + pageIndex*unsafe.Sizeof(Page{})
	page := (*Page)(unsafe.Pointer(pageAddr))

	// Mark as free
	flags := bitfield.UnpackPageFlags(page.flags)
	flags.Allocated = false
	packed, _ := bitfield.PackPageFlags(flags)
	page.flags = packed

	// Add back to free list (add to head)
	page.next = freePages
	page.prev = nil
	if freePages != nil {
		freePages.prev = page
	}
	freePages = page
}

