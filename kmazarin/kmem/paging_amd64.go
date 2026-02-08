package kmem

import (
	"mazzy/shared/constants"
	"unsafe"
)

// x86_64 PTE bits for page table entry construction.
const (
	X86_PTE_PRESENT = 1 << 0         // Page is present
	X86_PTE_RW      = 1 << 1         // Read/Write
	X86_PTE_USER    = 1 << 2         // User accessible
	X86_PTE_NX      = uint64(1) << 63 // No Execute
)

// selectRootPageTable returns the root page table PA for the given VA.
// x86_64 uses a single PML4 (CR3) for all addresses — no TTBR0/TTBR1 split.
//
//go:nosplit
func selectRootPageTable(va uintptr) uintptr {
	return ttbr1L0PA
}

// makeTablePTE creates an intermediate page table entry pointing to a next-level table.
// x86_64: Present + Read/Write (required for all intermediate entries).
//
//go:nosplit
func makeTablePTE(nextLevelPA uintptr) uint64 {
	return uint64(nextLevelPA) | X86_PTE_PRESENT | X86_PTE_RW
}

// makeKernelPagePTE creates a leaf (4KB) page table entry for kernel heap pages.
// x86_64: Present + Read/Write + No Execute.
//
//go:nosplit
func makeKernelPagePTE(pa uintptr) uint64 {
	return uint64(pa) | X86_PTE_PRESENT | X86_PTE_RW | X86_PTE_NX
}

// MapDeviceMMIO is a no-op on x86_64.
// Diplomat's linear map already covers all physical addresses below 4GB
// (PA → VA via KernelMMIOOffset), including PCI BAR regions.
func MapDeviceMMIO(physAddr uintptr, size uint64) error {
	return nil
}

// walkPageTable translates a VA to PA by walking the x86_64 page tables.
// Uses the PML4 (stored in ttbr1L0PA, set from CR3) as the root.
// x86_64 page table format:
//   - PML4 index: bits [47:39], PDPT: [38:30], PD: [29:21], PT: [20:12]
//   - Present bit: bit 0
//   - Address: bits [51:12] (we use PTE_ADDR_MASK = [47:12])
//   - 2MB page: PDE bit 7 (PS) = 1
//
//go:nosplit
func walkPageTable(va uintptr) uintptr {
	// Extract indices (same shifts as ARM64 4KB granule)
	pml4Idx := (va >> L0Shift) & 0x1FF
	pdptIdx := (va >> L1Shift) & 0x1FF
	pdIdx := (va >> L2Shift) & 0x1FF
	ptIdx := (va >> L3Shift) & 0x1FF

	// PML4 table
	pml4PA := ttbr1L0PA
	pml4VA := pml4PA + constants.KernelMMIOOffset
	pml4Entry := *(*uint64)(unsafe.Pointer(pml4VA + pml4Idx*8))
	if (pml4Entry & PTE_VALID) == 0 {
		return 0
	}

	// PDPT table
	pdptPA := uintptr(pml4Entry & PTE_ADDR_MASK)
	pdptVA := pdptPA + constants.KernelMMIOOffset
	pdptEntry := *(*uint64)(unsafe.Pointer(pdptVA + pdptIdx*8))
	if (pdptEntry & PTE_VALID) == 0 {
		return 0
	}

	// Check for 1GB page at PDPT level (PS bit = bit 7)
	if (pdptEntry & 0x80) != 0 {
		blockPA := uintptr(pdptEntry & PTE_ADDR_MASK)
		pageOffset := va & ((1 << L1Shift) - 1) // offset within 1GB page
		return blockPA | pageOffset
	}

	// PD table
	pdPA := uintptr(pdptEntry & PTE_ADDR_MASK)
	pdVA := pdPA + constants.KernelMMIOOffset
	pdEntry := *(*uint64)(unsafe.Pointer(pdVA + pdIdx*8))
	if (pdEntry & PTE_VALID) == 0 {
		return 0
	}

	// Check for 2MB page at PD level (PS bit = bit 7)
	if (pdEntry & 0x80) != 0 {
		blockPA := uintptr(pdEntry & PTE_ADDR_MASK_2MB)
		pageOffset := va & ((1 << L2Shift) - 1) // offset within 2MB page
		return blockPA | pageOffset
	}

	// PT table (4KB pages)
	ptPA := uintptr(pdEntry & PTE_ADDR_MASK)
	ptVA := ptPA + constants.KernelMMIOOffset
	ptEntry := *(*uint64)(unsafe.Pointer(ptVA + ptIdx*8))
	if (ptEntry & PTE_VALID) == 0 {
		return 0
	}

	// Extract PA and add page offset
	pa := uintptr(ptEntry & PTE_ADDR_MASK)
	return pa | (va & (PageSize - 1))
}

// PTE_ADDR_MASK_2MB masks out the page offset bits for 2MB pages.
// x86_64 2MB PDE stores the PA in bits [51:21] (not [51:12] like 4KB PTEs).
const PTE_ADDR_MASK_2MB = 0x0000FFFFFFFE0000
