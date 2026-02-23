package kmem

import (
	"mazzy/shared/constants"
	"unsafe"
)

// ARM64 Page Table Entry bits
const (
	PTE_VALID = 1 << 0  // Entry is valid
	PTE_TABLE = 1 << 1  // Entry points to next-level table (or is L3 page)
	PTE_AF    = 1 << 10 // Access flag

	// Memory attributes (MAIR index)
	PTE_ATTR_NORMAL = 0 << 2 // Normal cacheable (MAIR[0])
	PTE_ATTR_DEVICE = 1 << 2 // Device-nGnRnE (MAIR[1])

	// Access permissions (AP[2:1] field)
	PTE_AP_RW_EL1 = 0 << 6 // Read-write at EL1, no EL0 access (AP=00)
	PTE_AP_RW_ALL = 1 << 6 // Read-write at EL1 and EL0 (AP=01)
	PTE_AP_RO_EL1 = 2 << 6 // Read-only at EL1, no EL0 access (AP=10)
	PTE_AP_RO_ALL = 3 << 6 // Read-only at EL1 and EL0 (AP=11)

	// Execute permission for user pages
	PTE_UXN = 1 << 54 // User execute never
	PTE_PXN = 1 << 53 // Privileged execute never

	// Shareability
	PTE_SH_INNER = 3 << 8 // Inner shareable

	// Non-global flag - CRITICAL for ASID-based address space separation!
	// nG=1 means this entry is process-specific and uses ASID for TLB matching.
	// nG=0 (global) means entry is shared across all ASIDs - WRONG for userspace!
	PTE_NG = 1 << 11 // Non-global (must be set for userspace pages with ASIDs)

	// Execute permission
	PTE_EXEC_NEVER = (1 << 53) | (1 << 54) // PXN + UXN
)

// readTTBR0EL1 reads the raw TTBR0_EL1 register value.
// ARM64 TTBR0 format: [63:48]=ASID, [47:1]=PA, [0]=CnP
func readTTBR0EL1() uintptr

// readTTBR1EL1 reads the raw TTBR1_EL1 register value.
func readTTBR1EL1() uintptr

// readCurrentL0PA reads the current user-space L0 page table PA from TTBR0_EL1.
// Masks out the ASID in the upper 16 bits.
//
//go:nosplit
func readCurrentL0PA() uintptr {
	return readTTBR0EL1() & 0x0000FFFFFFFFFFFF
}

// initProcessL0 performs arch-specific initialization of a new process L0 page table.
// ARM64: no-op. TTBR0 is a separate register from TTBR1, so a fresh empty L0 is fine
// for user space — kernel mappings live in TTBR1 and are unaffected.
//
//go:nosplit
func initProcessL0(l0VA uintptr) {
	// Nothing to do on ARM64
}

// verifyUserspacePriestL0 checks that a priest's page table is valid before switching to it.
// ARM64: no-op. User L0 starts empty (kernel lives in TTBR1).
//
//go:nosplit
func verifyUserspacePriestL0(l0PA uintptr, asid uint16) {
}

// VerifyCurrentSATPL3E0 is a no-op on ARM64 (TTBR0/TTBR1 are separate).
//
//go:nosplit
func VerifyCurrentSATPL3E0() {}

// constructTTBR0Value constructs the TTBR0 register value for ARM64.
// TTBR0 format: [63:48]=ASID(16-bit), [47:1]=PA, [0]=CnP(0)
//
//go:nosplit
func constructTTBR0Value(l0PA uintptr, asid uint16) uint64 {
	return (uint64(asid) << 48) | uint64(l0PA)
}

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

// makeUserTablePTE creates an intermediate page table entry for user-accessible pages.
// ARM64: Same as kernel table PTE (Valid + Table). Permission bits are on leaf entries only.
//
//go:nosplit
func makeUserTablePTE(nextLevelPA uintptr) uint64 {
	return uint64(nextLevelPA) | PTE_VALID | PTE_TABLE
}

// makeUserPagePTE creates a leaf PTE for a userspace code/data page.
// Permissions are derived from ELF flags (PF_R, PF_W, PF_X).
// ARM64: Valid + L3 page + AF + Normal memory + Inner Shareable + Non-Global + AP/UXN/PXN from flags.
//
//go:nosplit
func makeUserPagePTE(pa uintptr, elfFlags uint32) uint64 {
	pte := uint64(pa) | PTE_VALID | PTE_TABLE | PTE_AF |
		PTE_ATTR_NORMAL | PTE_SH_INNER | PTE_NG

	if (elfFlags & ELF_PF_W) != 0 {
		pte |= PTE_AP_RW_ALL
	} else {
		pte |= PTE_AP_RO_ALL
	}

	if (elfFlags & ELF_PF_X) == 0 {
		pte |= PTE_UXN
	}
	// Always set PXN — kernel should not execute user code
	pte |= PTE_PXN

	return pte
}

// makeUserDevicePTE creates a leaf PTE for a userspace memory-mapped page.
// ARM64: Normal cacheable memory, RW for EL1+EL0, no execute, non-global.
//
// Uses PTE_ATTR_NORMAL (not PTE_ATTR_DEVICE) because the VirtIO GPU
// framebuffer is host-shared RAM, not MMIO. Device-nGnRnE memory enforces
// strict natural alignment on all accesses, which Go's generated code
// violates (e.g., 8-byte stores to 4-byte-aligned addresses). The GPU
// driver uses explicit transfer/flush VirtIO commands to synchronize,
// so device ordering guarantees are unnecessary.
//
//go:nosplit
func makeUserDevicePTE(pa uintptr) uint64 {
	return uint64(pa) | PTE_VALID | PTE_TABLE | PTE_AF |
		PTE_ATTR_NORMAL | PTE_AP_RW_ALL | PTE_EXEC_NEVER | PTE_NG
}

// makeKernelDevicePTE creates a leaf PTE for kernel-only MMIO pages.
// ARM64: Device memory, RW for EL1 only, no execute.
//
//go:nosplit
func makeKernelDevicePTE(pa uintptr) uint64 {
	return uint64(pa) | PTE_VALID | PTE_TABLE | PTE_AF |
		PTE_ATTR_DEVICE | PTE_AP_RW_EL1 | PTE_EXEC_NEVER
}

// makeFileBufferPTE creates a leaf PTE for kernel file buffer pages.
// ARM64: Same as kernel page PTE (normal memory, RW EL1, no execute).
//
//go:nosplit
func makeFileBufferPTE(pa uintptr) uint64 {
	return makeKernelPagePTE(pa)
}

// makeKernelScratchPTE creates a leaf PTE for kernel scratch mapping pages.
// ARM64: Same as kernel page PTE (normal memory, RW EL1, no execute, inner shareable).
//
//go:nosplit
func makeKernelScratchPTE(pa uintptr) uint64 {
	return makeKernelPagePTE(pa)
}

// pteIsValid returns true if the PTE is valid/present.
//
//go:nosplit
func pteIsValid(entry uint64) bool {
	return (entry & PTE_VALID) != 0
}

// pteExtractPA extracts the physical address from a PTE.
//
//go:nosplit
func pteExtractPA(entry uint64) uintptr {
	return uintptr(entry & PTE_ADDR_MASK)
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

// mapDevicePage maps a VA to PA with device memory attributes.
// ARM64: walks the 4-level page table, splits 2MB blocks if needed,
// and installs an L3 entry with Device-nGnRnE attributes.
//
//go:nosplit
func mapDevicePage(va, pa uintptr) bool {
	// Extract indices
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	// Use TTBR1 for kernel high memory (bit 63 = 1)
	if (va>>63)&1 == 0 {
		return false // Device MMIO must be in kernel space
	}
	l0PA := ttbr1L0PA

	// Get L0 table VA
	l0VA := paToVA(l0PA)
	if l0VA == 0 {
		return false
	}

	// Read L0 entry
	l0Entry := (*uint64)(unsafe.Pointer(l0VA + l0Idx*8))

	var l1VA uintptr
	if (*l0Entry & PTE_VALID) == 0 {
		l1VA = allocPTPage()
		if l1VA == 0 {
			return false
		}
		l1PA := walkPageTable(l1VA)
		if l1PA == 0 {
			return false
		}
		cachePTVA(l1PA, l1VA)
		*l0Entry = uint64(l1PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l0Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		l1PA := uintptr(*l0Entry & PTE_ADDR_MASK)
		l1VA = paToVAOrCache(l1PA)
	}
	if l1VA == 0 {
		return false
	}

	// Read L1 entry
	l1Entry := (*uint64)(unsafe.Pointer(l1VA + l1Idx*8))

	var l2VA uintptr
	if (*l1Entry & PTE_VALID) == 0 {
		l2VA = allocPTPage()
		if l2VA == 0 {
			return false
		}
		l2PA := walkPageTable(l2VA)
		if l2PA == 0 {
			return false
		}
		cachePTVA(l2PA, l2VA)
		*l1Entry = uint64(l2PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l1Entry)))
		dsbSY()
	} else {
		l2PA := uintptr(*l1Entry & PTE_ADDR_MASK)
		l2VA = paToVAOrCache(l2PA)
		if l2VA == 0 {
			return false
		}
	}

	// Read L2 entry
	l2Entry := (*uint64)(unsafe.Pointer(l2VA + l2Idx*8))

	var l3VA uintptr
	if (*l2Entry & PTE_VALID) == 0 {
		l3VA = allocPTPage()
		if l3VA == 0 {
			return false
		}
		l3PA := walkPageTable(l3VA)
		if l3PA == 0 {
			return false
		}
		cachePTVA(l3PA, l3VA)
		*l2Entry = uint64(l3PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l2Entry)))
		dsbSY()
	} else if (*l2Entry & 0x2) == 0 {
		// L2 is a 2MB block descriptor — must split into 512 L3 page entries.
		blockPA := uintptr(*l2Entry & PTE_ADDR_MASK)
		blockAttrs := *l2Entry & ^uint64(PTE_ADDR_MASK)

		l3VA = allocPTPage()
		if l3VA == 0 {
			return false
		}
		l3PA := walkPageTable(l3VA)
		if l3PA == 0 {
			return false
		}
		cachePTVA(l3PA, l3VA)

		for i := uintptr(0); i < 512; i++ {
			entryPA := blockPA + i*PageSize
			l3E := (uint64(entryPA) & PTE_ADDR_MASK) | (blockAttrs | PTE_TABLE)
			*(*uint64)(unsafe.Pointer(l3VA + i*8)) = l3E
		}

		*l2Entry = uint64(l3PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l2Entry)))
		for i := uintptr(0); i < 512; i++ {
			dcCIVAC(l3VA + i*8)
		}
		dsbSY()
		tlbiVMALLE1IS()
		dsbSY()
		isbSY()
	} else {
		l3PA := uintptr(*l2Entry & PTE_ADDR_MASK)
		l3VA = paToVAOrCache(l3PA)
		if l3VA == 0 {
			return false
		}
	}

	// Write L3 entry with DEVICE attributes
	l3Entry := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))

	if (*l3Entry & PTE_VALID) != 0 {
		existingPA := uintptr(*l3Entry & PTE_ADDR_MASK)
		if existingPA != pa {
			return false
		}
	}

	pteValue := makeKernelDevicePTE(pa)
	*l3Entry = pteValue

	dcCIVAC(uintptr(unsafe.Pointer(l3Entry)))
	dsbSY()
	tlbiVAE1IS(va)
	dsbSY()
	isbSY()

	return true
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

// DumpInstructionPageFault is a no-op on ARM64.
// The RISC-V version walks Sv48 page tables for diagnostic output.
func DumpInstructionPageFault(va uintptr) {
}

// =========================================================================
// Platform Abstraction Layer (Stage 0)
// =========================================================================

// platformPTEToFlags converts a native ARM64 PTE to arch-neutral PTEFlags.
//
//go:nosplit
func platformPTEToFlags(pte uint64) PTEFlags {
	var flags PTEFlags

	if pte&PTE_VALID != 0 {
		flags |= PF_Valid
	}

	// Write: AP[7:6] = 00 (EL1 RW) or 01 (EL1+EL0 RW) means writable.
	// AP[7:6] = 10 or 11 means read-only.
	ap := (pte >> 6) & 0x3
	if ap == 0 || ap == 1 {
		flags |= PF_Write
	}

	// User: AP bit 6 = 1 means EL0 access. Also check NG (non-global) which
	// indicates per-ASID (i.e., userspace) mapping.
	if ap == 1 || ap == 3 {
		flags |= PF_User
	}

	// Execute: UXN bit 54 clear means user-executable.
	// For kernel pages, PXN bit 53 clear means privileged-executable.
	// Report PF_Execute if either user or privileged execution is allowed.
	if pte&PTE_UXN == 0 || pte&PTE_PXN == 0 {
		flags |= PF_Execute
	}

	// Accessed: AF bit 10
	if pte&PTE_AF != 0 {
		flags |= PF_Accessed
	}

	// Dirty: ARM64 without FEAT_HAFDBS has no hardware dirty bit.
	// For now, report dirty if the page is writable (conservative).
	// Stage 5 will implement software dirty tracking via the AP bit trick.
	if flags.Has(PF_Write) {
		flags |= PF_Dirty
	}

	// Device: MAIR index in bits [4:2]. Index 1 = device-nGnRnE.
	attrIdx := (pte >> 2) & 0x7
	if attrIdx == 1 {
		flags |= PF_Device
	}

	// Global: inverse of NG bit 11. NG=0 means global.
	if pte&PTE_NG == 0 {
		flags |= PF_Global
	}

	return flags
}

// platformFlagsToUserPTE converts arch-neutral PTEFlags to a native ARM64
// leaf PTE for userspace pages.
//
//go:nosplit
func platformFlagsToUserPTE(pa uintptr, flags PTEFlags) uint64 {
	pte := uint64(pa) | PTE_VALID | PTE_TABLE | PTE_AF |
		PTE_ATTR_NORMAL | PTE_SH_INNER | PTE_NG

	if flags.Has(PF_Write) {
		pte |= PTE_AP_RW_ALL
	} else {
		pte |= PTE_AP_RO_ALL
	}

	if !flags.Has(PF_Execute) {
		pte |= PTE_UXN
	}
	// Always set PXN — kernel should not execute user code
	pte |= PTE_PXN

	if flags.Has(PF_Device) {
		// Override attribute to device-nGnRnE
		pte &^= 0x7 << 2 // clear AttrIdx
		pte |= PTE_ATTR_DEVICE
	}

	return pte
}

// platformFlagsToKernelPTE converts arch-neutral PTEFlags to a native ARM64
// leaf PTE for kernel pages.
//
//go:nosplit
func platformFlagsToKernelPTE(pa uintptr, flags PTEFlags) uint64 {
	pte := uint64(pa) | PTE_VALID | PTE_TABLE | PTE_AF |
		PTE_ATTR_NORMAL | PTE_SH_INNER

	if flags.Has(PF_Write) {
		pte |= PTE_AP_RW_EL1
	} else {
		pte |= PTE_AP_RO_EL1
	}

	if !flags.Has(PF_Execute) {
		pte |= PTE_EXEC_NEVER
	}

	if flags.Has(PF_Device) {
		pte &^= 0x7 << 2
		pte |= PTE_ATTR_DEVICE
	}

	return pte
}

// platformReadPTEAt walks the ARM64 page table for va and returns the leaf PTE.
// Returns (pte, level, ok) where level is 1 (1GB block), 2 (2MB block), or 3 (4KB page).
// Selects TTBR0 or TTBR1 based on bit 63 of va.
//
//go:nosplit
func platformReadPTEAt(va uintptr) (pte uint64, level int, ok bool) {
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	l0PA := selectRootPageTable(va)
	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		return 0, 0, false
	}

	// L0 — never a block on ARM64
	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	if l0Entry&PTE_VALID == 0 {
		return 0, 0, false
	}

	// L1
	l1PA := pteExtractPA(l0Entry)
	l1VA := paToVAOrCache(l1PA)
	if l1VA == 0 {
		return 0, 0, false
	}
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if l1Entry&PTE_VALID == 0 {
		return 0, 0, false
	}
	// Block at L1: bit 1 (TABLE) not set → 1GB block
	if l1Entry&0x2 == 0 {
		return l1Entry, 1, true
	}

	// L2
	l2PA := pteExtractPA(l1Entry)
	l2VA := paToVAOrCache(l2PA)
	if l2VA == 0 {
		return 0, 0, false
	}
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if l2Entry&PTE_VALID == 0 {
		return 0, 0, false
	}
	// Block at L2: bit 1 (TABLE) not set → 2MB block
	if l2Entry&0x2 == 0 {
		return l2Entry, 2, true
	}

	// L3
	l3PA := pteExtractPA(l2Entry)
	l3VA := paToVAOrCache(l3PA)
	if l3VA == 0 {
		return 0, 0, false
	}
	l3Entry := *(*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	if l3Entry&PTE_VALID == 0 {
		return 0, 0, false
	}
	return l3Entry, 3, true
}

// platformWritePTEAt writes a new PTE value at the leaf entry for va.
// Returns false if va is not currently mapped at L3 (does not handle block entries).
//
//go:nosplit
func platformWritePTEAt(va uintptr, newPTE uint64) bool {
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	l0PA := selectRootPageTable(va)
	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		return false
	}

	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	if l0Entry&PTE_VALID == 0 {
		return false
	}

	l1PA := pteExtractPA(l0Entry)
	l1VA := paToVAOrCache(l1PA)
	if l1VA == 0 {
		return false
	}
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if l1Entry&PTE_VALID == 0 || l1Entry&0x2 == 0 {
		return false // Not mapped or block entry
	}

	l2PA := pteExtractPA(l1Entry)
	l2VA := paToVAOrCache(l2PA)
	if l2VA == 0 {
		return false
	}
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if l2Entry&PTE_VALID == 0 || l2Entry&0x2 == 0 {
		return false // Not mapped or block entry
	}

	l3PA := pteExtractPA(l2Entry)
	l3VA := paToVAOrCache(l3PA)
	if l3VA == 0 {
		return false
	}
	l3Ptr := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	if *l3Ptr&PTE_VALID == 0 {
		return false
	}

	*l3Ptr = newPTE
	dcCIVAC(uintptr(unsafe.Pointer(l3Ptr)))
	dsbSY()
	return true
}

// platformClearAccessed clears the AF (Access Flag, bit 10) on the PTE for va.
// Returns whether the flag was set before clearing.
// NOTE: Clearing AF will cause an Access Flag fault on next access. Only use
// this for page replacement scanning where the fault handler will re-set AF.
//
//go:nosplit
func platformClearAccessed(va uintptr) (wasAccessed bool) {
	pte, level, ok := platformReadPTEAt(va)
	if !ok || level != 3 {
		return false
	}

	wasAccessed = pte&PTE_AF != 0
	if wasAccessed {
		newPTE := pte &^ PTE_AF
		platformWritePTEAt(va, newPTE)
		tlbiVAE1IS(va)
		dsbSY()
	}
	return wasAccessed
}

// platformClearDirty clears the dirty state for the PTE at va.
// ARM64 without FEAT_HAFDBS has no hardware dirty bit, so this is a no-op.
// Stage 5 will implement software dirty tracking via the AP bit trick
// (change writable pages to read-only, catch permission faults).
//
//go:nosplit
func platformClearDirty(va uintptr) (wasDirty bool) {
	// ARM64 without FEAT_HAFDBS: no hardware dirty bit to clear.
	// Software dirty tracking will be added in Stage 5.
	return false
}

// platformFlushTLBPage invalidates TLB entries for a single VA.
//
//go:nosplit
func platformFlushTLBPage(va uintptr) {
	tlbiVAE1IS(va)
	dsbSY()
	isbSY()
}

// platformFlushTLBASID invalidates all TLB entries for a given ASID.
//
//go:nosplit
func platformFlushTLBASID(asid uint16) {
	TlbiASIDE1IS(asid)
}

// platformUserL0MaxEntry returns how many L0 entries are userspace.
// ARM64: 512 — TTBR0 is entirely user, TTBR1 is entirely kernel.
//
//go:nosplit
func platformUserL0MaxEntry() int {
	return 512
}

// isBlockEntry returns true if the PTE at the given level is a block descriptor
// (not a table pointer). level: 1 = 1GB block, 2 = 2MB block.
// ARM64: block descriptors have bit 1 (TABLE) clear.
//
//go:nosplit
func isBlockEntry(pte uint64, level int) bool {
	if level == 1 || level == 2 {
		return pte&0x2 == 0
	}
	return false // L0 and L3 are never blocks on ARM64
}
