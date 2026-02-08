package kmem

import (
	"mazzy/shared/constants"
	"unsafe"
)

// selectRootPageTable returns the root page table PA for the given VA.
// ARM64 uses bit 63 to select between TTBR0 (user space) and TTBR1 (kernel space).
//
//go:nosplit
func selectRootPageTable(va uintptr) uintptr {
	if (va>>63)&1 == 0 {
		return ttbr0L0PA
	}
	return ttbr1L0PA
}

// makeTablePTE creates an intermediate page table entry pointing to a next-level table.
// ARM64: Valid + Table descriptor (bits [1:0] = 11).
//
//go:nosplit
func makeTablePTE(nextLevelPA uintptr) uint64 {
	return uint64(nextLevelPA) | PTE_VALID | PTE_TABLE
}

// makeKernelPagePTE creates a leaf (4KB) page table entry for kernel heap pages.
// ARM64: Valid + L3 page + Access Flag + Normal memory + EL1 RW + Inner Shareable + No Execute.
//
//go:nosplit
func makeKernelPagePTE(pa uintptr) uint64 {
	return uint64(pa) | PTE_VALID | PTE_TABLE | PTE_AF |
		PTE_ATTR_NORMAL | PTE_AP_RW_EL1 | PTE_SH_INNER | PTE_EXEC_NEVER
}

// MapDeviceMMIO maps a physical address range as device MMIO memory
// in the kernel's TTBR1 page tables with non-cacheable attributes.
func MapDeviceMMIO(physAddr uintptr, size uint64) error {
	// Lazy initialization
	if !pagingInitialized {
		InitPaging()
	}

	// Calculate number of pages needed (round up)
	if size == 0 {
		size = PageSize // Default to one page if size not specified
	}
	numPages := (size + PageSize - 1) / PageSize

	// Map all pages in the region
	for i := uint64(0); i < numPages; i++ {
		pagePhys := (physAddr &^ (PageSize - 1)) + uintptr(i*PageSize)
		pageVA := pagePhys + constants.KernelMMIOOffset

		if !mapDevicePage(pageVA, pagePhys) {
			return &MappingError{addr: physAddr + uintptr(i*PageSize), msg: "failed to map device page"}
		}
	}

	return nil
}

// walkPageTable translates a VA to PA by walking the ARM64 page tables.
// Uses the TTBR1 L0 table as the root.
// ARM64 block descriptor: bits[1:0] = 01 (block), bits[1:0] = 11 (table)
//
//go:nosplit
func walkPageTable(va uintptr) uintptr {
	// Extract indices
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	// L0 table
	l0PA := ttbr1L0PA
	l0VA := l0PA + constants.KernelMMIOOffset
	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	if (l0Entry & PTE_VALID) == 0 {
		return 0
	}

	// L1 table
	l1PA := uintptr(l0Entry & PTE_ADDR_MASK)
	l1VA := l1PA + constants.KernelMMIOOffset
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if (l1Entry & PTE_VALID) == 0 {
		return 0
	}

	// L2 table
	l2PA := uintptr(l1Entry & PTE_ADDR_MASK)
	l2VA := l2PA + constants.KernelMMIOOffset
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if (l2Entry & PTE_VALID) == 0 {
		return 0
	}

	// Check if L2 entry is a block descriptor (2MB) or table pointer
	// Block: bits[1:0] = 01, Table: bits[1:0] = 11
	if (l2Entry & 0x2) == 0 {
		// L2 block descriptor (2MB) - extract PA from block address + page offset
		blockPA := uintptr(l2Entry & PTE_ADDR_MASK)
		pageOffset := va & ((1 << L2Shift) - 1) // offset within 2MB block
		return blockPA | pageOffset
	}

	// L3 table (L2 is a table pointer)
	l3PA := uintptr(l2Entry & PTE_ADDR_MASK)
	l3VA := l3PA + constants.KernelMMIOOffset
	l3Entry := *(*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	if (l3Entry & PTE_VALID) == 0 {
		return 0
	}

	// Extract PA and add page offset
	pa := uintptr(l3Entry & PTE_ADDR_MASK)
	return pa | (va & (PageSize - 1))
}
