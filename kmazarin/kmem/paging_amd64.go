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

// initProcessL0 performs arch-specific initialization of a new process PML4.
// x86_64: The kernel PML4 has both kernel and kmazarin code mappings.
// We must preserve kernel mappings in the new PML4 while giving each process
// its own user-space page tables.
//
// Layout:
//   - PML4[0]:  Contains PDPT for VA 0-512GB. Kmazarin code is at PDPT[1] (VA 0x40000000+).
//               User code is at PDPT[0] (VA 0-0x3FFFFFFF). We create a NEW PDPT per process
//               that copies kmazarin's entries but leaves user entries empty.
//   - PML4[1-511]: Copied directly (kernel linear map, demand-paged heap, etc.)
//
//go:nosplit
func initProcessL0(l0VA uintptr) {
	kernelL0VA := ttbr1L0PA + constants.KernelMMIOOffset

	// Copy PML4 entries 1-511 directly (kernel-only: linear map, heap, etc.)
	for i := uintptr(1); i < 512; i++ {
		*(*uint64)(unsafe.Pointer(l0VA + i*8)) = *(*uint64)(unsafe.Pointer(kernelL0VA + i*8))
	}

	// For PML4 entry 0: create a new PDPT so each process has its own user page tables.
	kernelPML4E0 := *(*uint64)(unsafe.Pointer(kernelL0VA))
	if (kernelPML4E0 & X86_PTE_PRESENT) == 0 {
		// No PML4[0] entry in kernel — nothing to do
		return
	}

	// Allocate a new PDPT page for this process
	newPdptPA := AllocPage(PageKernelPT)
	if newPdptPA == 0 {
		return
	}
	newPdptVA := newPdptPA + constants.KernelMMIOOffset

	// Zero the new PDPT
	for i := uintptr(0); i < 512; i++ {
		*(*uint64)(unsafe.Pointer(newPdptVA + i*8)) = 0
	}

	// Copy kmazarin-relevant PDPT entries from the kernel's PDPT.
	// Kmazarin code is at VA 0x43800000 → PDPT index 1 (VA 0x40000000-0x7FFFFFFF).
	// Copy entries 1+ (kernel code region), leave entry 0 empty (user space).
	kernelPdptPA := uintptr(kernelPML4E0 & PTE_ADDR_MASK)
	kernelPdptVA := kernelPdptPA + constants.KernelMMIOOffset
	for i := uintptr(1); i < 512; i++ {
		*(*uint64)(unsafe.Pointer(newPdptVA + i*8)) = *(*uint64)(unsafe.Pointer(kernelPdptVA + i*8))
	}

	// Set PML4[0] to point to our new PDPT
	*(*uint64)(unsafe.Pointer(l0VA)) = uint64(newPdptPA) | X86_PTE_PRESENT | X86_PTE_RW | X86_PTE_USER
}

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

// makeUserTablePTE creates an intermediate page table entry for user-accessible pages.
// x86_64: CRITICAL — Must include USER bit at every level or user access will fault.
//
//go:nosplit
func makeUserTablePTE(nextLevelPA uintptr) uint64 {
	return uint64(nextLevelPA) | X86_PTE_PRESENT | X86_PTE_RW | X86_PTE_USER
}

// makeUserPagePTE creates a leaf PTE for a userspace code/data page.
// Permissions are derived from ELF flags (PF_R, PF_W, PF_X).
// x86_64: Present + User + RW if writable + NX if not executable.
//
//go:nosplit
func makeUserPagePTE(pa uintptr, elfFlags uint32) uint64 {
	pte := uint64(pa) | X86_PTE_PRESENT | X86_PTE_USER

	if (elfFlags & ELF_PF_W) != 0 {
		pte |= X86_PTE_RW
	}

	if (elfFlags & ELF_PF_X) == 0 {
		pte |= X86_PTE_NX
	}

	return pte
}

// makeUserDevicePTE creates a leaf PTE for a userspace MMIO page (e.g. framebuffer).
// x86_64: Present + RW + User + No Execute.
//
//go:nosplit
func makeUserDevicePTE(pa uintptr) uint64 {
	return uint64(pa) | X86_PTE_PRESENT | X86_PTE_RW | X86_PTE_USER | X86_PTE_NX
}

// makeKernelDevicePTE creates a leaf PTE for kernel-only MMIO pages.
// x86_64: Present + RW + No Execute.
//
//go:nosplit
func makeKernelDevicePTE(pa uintptr) uint64 {
	return uint64(pa) | X86_PTE_PRESENT | X86_PTE_RW | X86_PTE_NX
}

// makeFileBufferPTE creates a leaf PTE for kernel file buffer pages.
// x86_64: Same as kernel page PTE (Present + RW + NX).
//
//go:nosplit
func makeFileBufferPTE(pa uintptr) uint64 {
	return makeKernelPagePTE(pa)
}

// makeKernelScratchPTE creates a leaf PTE for kernel scratch mapping pages.
// x86_64: Same as kernel page PTE (Present + RW + NX).
//
//go:nosplit
func makeKernelScratchPTE(pa uintptr) uint64 {
	return makeKernelPagePTE(pa)
}

// pteIsValid returns true if the PTE is present.
//
//go:nosplit
func pteIsValid(entry uint64) bool {
	return (entry & X86_PTE_PRESENT) != 0
}

// pteExtractPA extracts the physical address from a PTE.
//
//go:nosplit
func pteExtractPA(entry uint64) uintptr {
	return uintptr(entry & PTE_ADDR_MASK)
}

// mapDevicePage is a no-op on x86_64.
// Diplomat's linear map already covers all physical addresses with appropriate
// cache attributes, and QEMU TCG handles coherency automatically.
//
//go:nosplit
func mapDevicePage(va, pa uintptr) bool {
	return true
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
	if (pml4Entry & X86_PTE_PRESENT) == 0 {
		return 0
	}

	// PDPT table
	pdptPA := uintptr(pml4Entry & PTE_ADDR_MASK)
	pdptVA := pdptPA + constants.KernelMMIOOffset
	pdptEntry := *(*uint64)(unsafe.Pointer(pdptVA + pdptIdx*8))
	if (pdptEntry & X86_PTE_PRESENT) == 0 {
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
	if (pdEntry & X86_PTE_PRESENT) == 0 {
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
	if (ptEntry & X86_PTE_PRESENT) == 0 {
		return 0
	}

	// Extract PA and add page offset
	pa := uintptr(ptEntry & PTE_ADDR_MASK)
	return pa | (va & (PageSize - 1))
}

// PTE_ADDR_MASK_2MB masks out the page offset bits for 2MB pages.
// x86_64 2MB PDE stores the PA in bits [51:21] (not [51:12] like 4KB PTEs).
const PTE_ADDR_MASK_2MB = 0x0000FFFFFFFE0000

// DumpInstructionPageFault is a no-op on x86_64.
// The RISC-V version walks Sv48 page tables for diagnostic output.
func DumpInstructionPageFault(va uintptr) {
}
