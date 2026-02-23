package kmem

import (
	"mazzy/kmazarin/serial"
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

// readCR3 reads the raw CR3 register value.
// x86_64 CR3 format: [63:12]=PML4 PA, [11:0]=PCID/flags
func readCR3() uintptr

// readCurrentL0PA reads the current L0 page table PA from CR3.
// Masks out PCID/flag bits in the lower 12 bits.
//
//go:nosplit
func readCurrentL0PA() uintptr {
	return readCR3() & PTE_ADDR_MASK
}

// initProcessL0 performs arch-specific initialization of a new process PML4.
// x86_64 has a single CR3 register (no TTBR0/TTBR1 split like ARM64), so
// both kernel and user mappings share one page table hierarchy.
//
// The 48-bit canonical address space splits into:
//   - User space:   PML4[0-255]   (VA 0x0000_0000_0000_0000 - 0x0000_7FFF_FFFF_FFFF)
//   - Kernel space:  PML4[256-511] (VA 0xFFFF_8000_0000_0000 - 0xFFFF_FFFF_FFFF_FFFF)
//
// Strategy:
//   - PML4[256-511]: Copy from kernel PML4 (linear map, kernel heap, stacks).
//     These entries have no USER bit — supervisor-only, which is correct.
//   - PML4[0]: Create a per-process PDPT. Copy only PDPT[1] (kmazarin code at
//     VA 0x40000000+) from the kernel. Leave all other entries empty for user
//     demand paging (mmap at 0x80000000+, ELF at lower addresses).
//   - PML4[1-255]: Leave empty. The demand pager (mapUserPageWithL0) will create
//     entries with USER bit when userspace accesses these VAs (e.g., Go heap at
//     VA 0xC000000000 = PML4[1]).
//
// CRITICAL: Do NOT copy PML4[1-255] from the kernel! UEFI firmware and diplomat
// create supervisor-only mappings (1GB identity pages, kernel heap entries) in
// these ranges. Copying them gives the process supervisor-only entries that
// shadow user demand-paged pages, causing protection-violation page faults.
//
//go:nosplit
func initProcessL0(l0VA uintptr) {
	kernelL0VA := ttbr1L0PA + constants.KernelMMIOOffset

	// Copy PML4[256-511] from kernel (kernel VA space only).
	for i := uintptr(256); i < 512; i++ {
		*(*uint64)(unsafe.Pointer(l0VA + i*8)) = *(*uint64)(unsafe.Pointer(kernelL0VA + i*8))
	}

	// PML4[1-255] are left zeroed (the page was zeroed at allocation).
	// The demand pager will create entries with USER bit when needed.

	// For PML4[0]: create a new PDPT so each process has its own user page tables.
	kernelPML4E0 := *(*uint64)(unsafe.Pointer(kernelL0VA))
	if (kernelPML4E0 & X86_PTE_PRESENT) == 0 {
		// No PML4[0] entry in kernel — nothing to do
		return
	}

	// Allocate a new PDPT page for this process
	newPdptPA := AllocPage(PageUserPT, 0) // User PT page (owned by kernel during setup)
	if newPdptPA == 0 {
		return
	}
	newPdptVA := newPdptPA + constants.KernelMMIOOffset

	// Zero the new PDPT
	for i := uintptr(0); i < 512; i++ {
		*(*uint64)(unsafe.Pointer(newPdptVA + i*8)) = 0
	}

	// Copy ONLY PDPT[1] (kmazarin code at VA 0x40000000-0x7FFFFFFF).
	// All other PDPT entries (0, 2+) are left empty for user demand paging.
	kernelPdptPA := uintptr(kernelPML4E0 & PTE_ADDR_MASK)
	kernelPdptVA := kernelPdptPA + constants.KernelMMIOOffset
	*(*uint64)(unsafe.Pointer(newPdptVA + 1*8)) = *(*uint64)(unsafe.Pointer(kernelPdptVA + 1*8))

	// Set PML4[0] to point to our new PDPT with USER bit
	*(*uint64)(unsafe.Pointer(l0VA)) = uint64(newPdptPA) | X86_PTE_PRESENT | X86_PTE_RW | X86_PTE_USER
}

// verifyUserspacePriestL0 checks that a priest's page table is valid before switching to it.
// x86_64: single CR3, so L0[0] must contain kernel mappings (copied by initProcessL0).
//
//go:nosplit
func verifyUserspacePriestL0(l0PA uintptr, asid uint16) {
	l0VA := l0PA + constants.KernelMMIOOffset
	e0 := *(*uint64)(unsafe.Pointer(l0VA))
	if (e0 & X86_PTE_PRESENT) == 0 {
		serial.RawUARTPuts("\r\n[SWITCH_PT] L0[0] INVALID! l0PA=0x")
		serial.RawUARTHex64(uint64(l0PA))
		serial.RawUARTPuts(" ASID=")
		serial.RawUARTHex64(uint64(asid))
		serial.RawUARTPuts(" L0[0]=0x")
		serial.RawUARTHex64(e0)
		serial.RawUARTPuts("\r\n")
		for i := uintptr(0); i < 4; i++ {
			entry := *(*uint64)(unsafe.Pointer(l0VA + i*8))
			serial.RawUARTPuts("  L0[")
			serial.RawUARTHex64(uint64(i))
			serial.RawUARTPuts("]=0x")
			serial.RawUARTHex64(entry)
			serial.RawUARTPuts("\r\n")
		}
		for {
		}
	}
}

// VerifyCurrentSATPL3E0 is a no-op on x86_64 (no SATP register).
//
//go:nosplit
func VerifyCurrentSATPL3E0() {}

// constructTTBR0Value constructs the CR3 register value for x86_64.
// CR3 format: [63:12]=PML4 PA, [11:0]=PCID (using lower bits for ASID)
//
//go:nosplit
func constructTTBR0Value(l0PA uintptr, asid uint16) uint64 {
	return uint64(l0PA) | uint64(asid)
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

// =========================================================================
// Platform Abstraction Layer (Stage 0)
// =========================================================================

// platformPTEToFlags converts a native x86_64 PTE to arch-neutral PTEFlags.
//
//go:nosplit
func platformPTEToFlags(pte uint64) PTEFlags {
	var flags PTEFlags

	if pte&X86_PTE_PRESENT != 0 {
		flags |= PF_Valid
	}

	if pte&X86_PTE_RW != 0 {
		flags |= PF_Write
	}

	if pte&X86_PTE_USER != 0 {
		flags |= PF_User
	}

	// Execute: inverse of NX bit 63
	if pte&X86_PTE_NX == 0 {
		flags |= PF_Execute
	}

	// Accessed: bit 5
	if pte&(1<<5) != 0 {
		flags |= PF_Accessed
	}

	// Dirty: bit 6
	if pte&(1<<6) != 0 {
		flags |= PF_Dirty
	}

	// Device: PCD bit 4 (Page Cache Disable) indicates uncacheable
	if pte&(1<<4) != 0 {
		flags |= PF_Device
	}

	// Global: bit 8
	if pte&(1<<8) != 0 {
		flags |= PF_Global
	}

	return flags
}

// platformFlagsToUserPTE converts arch-neutral PTEFlags to a native x86_64
// leaf PTE for userspace pages.
//
//go:nosplit
func platformFlagsToUserPTE(pa uintptr, flags PTEFlags) uint64 {
	pte := uint64(pa) | X86_PTE_PRESENT | X86_PTE_USER

	if flags.Has(PF_Write) {
		pte |= X86_PTE_RW
	}

	if !flags.Has(PF_Execute) {
		pte |= X86_PTE_NX
	}

	return pte
}

// platformFlagsToKernelPTE converts arch-neutral PTEFlags to a native x86_64
// leaf PTE for kernel pages.
//
//go:nosplit
func platformFlagsToKernelPTE(pa uintptr, flags PTEFlags) uint64 {
	pte := uint64(pa) | X86_PTE_PRESENT

	if flags.Has(PF_Write) {
		pte |= X86_PTE_RW
	}

	if !flags.Has(PF_Execute) {
		pte |= X86_PTE_NX
	}

	return pte
}

// platformReadPTEAt walks the x86_64 page table for va and returns the leaf PTE.
// Returns (pte, level, ok) where level is 1 (1GB page), 2 (2MB page), or 3 (4KB page).
// x86_64 uses a single PML4 (CR3) for all addresses.
//
//go:nosplit
func platformReadPTEAt(va uintptr) (pte uint64, level int, ok bool) {
	pml4Idx := (va >> L0Shift) & 0x1FF
	pdptIdx := (va >> L1Shift) & 0x1FF
	pdIdx := (va >> L2Shift) & 0x1FF
	ptIdx := (va >> L3Shift) & 0x1FF

	pml4PA := ttbr1L0PA
	pml4VA := pml4PA + constants.KernelMMIOOffset

	// PML4 — never a huge page
	pml4Entry := *(*uint64)(unsafe.Pointer(pml4VA + pml4Idx*8))
	if pml4Entry&X86_PTE_PRESENT == 0 {
		return 0, 0, false
	}

	// PDPT
	pdptPA := uintptr(pml4Entry & PTE_ADDR_MASK)
	pdptVA := pdptPA + constants.KernelMMIOOffset
	pdptEntry := *(*uint64)(unsafe.Pointer(pdptVA + pdptIdx*8))
	if pdptEntry&X86_PTE_PRESENT == 0 {
		return 0, 0, false
	}
	// 1GB page: PS bit 7
	if pdptEntry&0x80 != 0 {
		return pdptEntry, 1, true
	}

	// PD
	pdPA := uintptr(pdptEntry & PTE_ADDR_MASK)
	pdVA := pdPA + constants.KernelMMIOOffset
	pdEntry := *(*uint64)(unsafe.Pointer(pdVA + pdIdx*8))
	if pdEntry&X86_PTE_PRESENT == 0 {
		return 0, 0, false
	}
	// 2MB page: PS bit 7
	if pdEntry&0x80 != 0 {
		return pdEntry, 2, true
	}

	// PT (4KB leaf)
	ptPA := uintptr(pdEntry & PTE_ADDR_MASK)
	ptVA := ptPA + constants.KernelMMIOOffset
	ptEntry := *(*uint64)(unsafe.Pointer(ptVA + ptIdx*8))
	if ptEntry&X86_PTE_PRESENT == 0 {
		return 0, 0, false
	}
	return ptEntry, 3, true
}

// platformWritePTEAt writes a new PTE value at the leaf entry for va.
// Returns false if va is not currently mapped at L3 (does not handle huge pages).
//
//go:nosplit
func platformWritePTEAt(va uintptr, newPTE uint64) bool {
	pml4Idx := (va >> L0Shift) & 0x1FF
	pdptIdx := (va >> L1Shift) & 0x1FF
	pdIdx := (va >> L2Shift) & 0x1FF
	ptIdx := (va >> L3Shift) & 0x1FF

	pml4PA := ttbr1L0PA
	pml4VA := pml4PA + constants.KernelMMIOOffset

	pml4Entry := *(*uint64)(unsafe.Pointer(pml4VA + pml4Idx*8))
	if pml4Entry&X86_PTE_PRESENT == 0 {
		return false
	}

	pdptPA := uintptr(pml4Entry & PTE_ADDR_MASK)
	pdptVA := pdptPA + constants.KernelMMIOOffset
	pdptEntry := *(*uint64)(unsafe.Pointer(pdptVA + pdptIdx*8))
	if pdptEntry&X86_PTE_PRESENT == 0 || pdptEntry&0x80 != 0 {
		return false // Not mapped or 1GB page
	}

	pdPA := uintptr(pdptEntry & PTE_ADDR_MASK)
	pdVA := pdPA + constants.KernelMMIOOffset
	pdEntry := *(*uint64)(unsafe.Pointer(pdVA + pdIdx*8))
	if pdEntry&X86_PTE_PRESENT == 0 || pdEntry&0x80 != 0 {
		return false // Not mapped or 2MB page
	}

	ptPA := uintptr(pdEntry & PTE_ADDR_MASK)
	ptVA := ptPA + constants.KernelMMIOOffset
	ptPtr := (*uint64)(unsafe.Pointer(ptVA + ptIdx*8))
	if *ptPtr&X86_PTE_PRESENT == 0 {
		return false
	}

	*ptPtr = newPTE
	return true
}

// platformClearAccessed clears the Accessed bit (bit 5) on the PTE for va.
// Returns whether the flag was set before clearing.
//
//go:nosplit
func platformClearAccessed(va uintptr) (wasAccessed bool) {
	pte, level, ok := platformReadPTEAt(va)
	if !ok || level != 3 {
		return false
	}

	wasAccessed = pte&(1<<5) != 0
	if wasAccessed {
		newPTE := pte &^ (1 << 5)
		platformWritePTEAt(va, newPTE)
		// INVLPG to ensure TLB picks up the change
		tlbiVAE1IS(va)
	}
	return wasAccessed
}

// platformClearDirty clears the Dirty bit (bit 6) on the PTE for va.
// Returns whether the flag was set before clearing.
//
//go:nosplit
func platformClearDirty(va uintptr) (wasDirty bool) {
	pte, level, ok := platformReadPTEAt(va)
	if !ok || level != 3 {
		return false
	}

	wasDirty = pte&(1<<6) != 0
	if wasDirty {
		newPTE := pte &^ (1 << 6)
		platformWritePTEAt(va, newPTE)
		tlbiVAE1IS(va)
	}
	return wasDirty
}

// platformFlushTLBPage invalidates TLB entries for a single VA.
// x86_64: INVLPG instruction (via tlbiVAE1ISAsm which does INVLPG).
//
//go:nosplit
func platformFlushTLBPage(va uintptr) {
	tlbiVAE1IS(va)
}

// platformFlushTLBASID invalidates all TLB entries for a given ASID.
// x86_64: No per-ASID invalidation — reload CR3 (full TLB flush).
//
//go:nosplit
func platformFlushTLBASID(asid uint16) {
	TlbiASIDE1IS(asid)
}

// platformUserL0MaxEntry returns how many L0 (PML4) entries are userspace.
// x86_64: PML4[0-255] is user space, PML4[256-511] is kernel.
//
//go:nosplit
func platformUserL0MaxEntry() int {
	return 256
}

// isBlockEntry returns true if the PTE at the given level is a huge page
// (not a table pointer). level: 1 = 1GB page, 2 = 2MB page.
// x86_64: PS bit (bit 7) indicates huge page.
//
//go:nosplit
func isBlockEntry(pte uint64, level int) bool {
	if level == 1 || level == 2 {
		return pte&0x80 != 0
	}
	return false
}
