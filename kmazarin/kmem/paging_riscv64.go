package kmem

import (
	"mazzy/shared/constants"
	"unsafe"
)

// RISC-V Sv48 PTE bits
const (
	RV_PTE_V = 1 << 0 // Valid
	RV_PTE_R = 1 << 1 // Readable
	RV_PTE_W = 1 << 2 // Writable
	RV_PTE_X = 1 << 3 // Executable
	RV_PTE_U = 1 << 4 // User accessible
	RV_PTE_G = 1 << 5 // Global
	RV_PTE_A = 1 << 6 // Accessed
	RV_PTE_D = 1 << 7 // Dirty

	// Common PTE combinations for kernel pages
	RV_PTE_LEAF_RW  = RV_PTE_V | RV_PTE_R | RV_PTE_W | RV_PTE_A | RV_PTE_D
	RV_PTE_LEAF_RWX = RV_PTE_V | RV_PTE_R | RV_PTE_W | RV_PTE_X | RV_PTE_A | RV_PTE_D

	// PPN address mask (bits 53:10, shifted to bits 43:0)
	RV_PTE_PPN_MASK = 0x3FFFFFFFFFC00
)

// RISC-V Sv48 uses the same 4-level page table structure as ARM64 and x86_64.
// Index extraction uses the shared L0Shift-L3Shift constants from paging.go:
//   L3Shift=39 (root, 512GB per entry)
//   L2Shift=30 (1GB per entry, can be gigapage leaf)
//   L1Shift=21 (2MB per entry, can be megapage leaf)
//   L0Shift=12 (4KB per entry, always leaf)

// initProcessL0 performs arch-specific initialization of a new process root page table.
// RISC-V Sv48: like x86_64, uses single SATP register for both user and kernel space.
// Must copy kernel entries (upper half of L3 root, indices 256-511) to preserve kernel mappings.
//
//go:nosplit
func initProcessL0(l0VA uintptr) {
	kernelL0VA := ttbr1L0PA + constants.KernelMMIOOffset
	src := kernelL0VA + 256*8
	dst := l0VA + 256*8
	for i := uintptr(0); i < 256; i++ {
		*(*uint64)(unsafe.Pointer(dst + i*8)) = *(*uint64)(unsafe.Pointer(src + i*8))
	}
}

// selectRootPageTable returns the root page table PA for the given VA.
// RISC-V Sv48 uses a single SATP register (no TTBR0/TTBR1 split like ARM64).
//
//go:nosplit
func selectRootPageTable(va uintptr) uintptr {
	return ttbr1L0PA // Global root page table (diplomat sets this from SATP)
}

// makeTablePTE creates an intermediate page table entry pointing to a next-level table.
// RISC-V: Valid only (R=W=X=0 indicates branch, not leaf).
//
//go:nosplit
func makeTablePTE(nextLevelPA uintptr) uint64 {
	ppn := (uint64(nextLevelPA) >> 12) & 0xFFFFFFFFFFF // 44-bit PPN
	return (ppn << 10) | RV_PTE_V
}

// makeKernelPagePTE creates a leaf (4KB) page table entry for kernel heap pages.
// RISC-V: Valid + Read + Write + Accessed + Dirty (no execute for heap).
//
//go:nosplit
func makeKernelPagePTE(pa uintptr) uint64 {
	ppn := (uint64(pa) >> 12) & 0xFFFFFFFFFFF // 44-bit PPN
	return (ppn << 10) | RV_PTE_LEAF_RW
}

// makeUserTablePTE creates an intermediate page table entry for user-accessible pages.
// RISC-V Sv48: Valid only (R=W=X=0 indicates branch, not leaf). U bit is leaf-only.
//
//go:nosplit
func makeUserTablePTE(nextLevelPA uintptr) uint64 {
	ppn := (uint64(nextLevelPA) >> 12) & 0xFFFFFFFFFFF
	return (ppn << 10) | RV_PTE_V
}

// makeUserPagePTE creates a leaf PTE for a userspace code/data page.
// Permissions are derived from ELF flags (PF_R, PF_W, PF_X).
// RISC-V: V + R + U + A + D, plus W if writable and X if executable.
//
//go:nosplit
func makeUserPagePTE(pa uintptr, elfFlags uint32) uint64 {
	ppn := (uint64(pa) >> 12) & 0xFFFFFFFFFFF
	pte := (ppn << 10) | RV_PTE_V | RV_PTE_R | RV_PTE_U | RV_PTE_A | RV_PTE_D

	if (elfFlags & ELF_PF_W) != 0 {
		pte |= RV_PTE_W
	}

	if (elfFlags & ELF_PF_X) != 0 {
		pte |= RV_PTE_X
	}

	return pte
}

// makeUserDevicePTE creates a leaf PTE for a userspace MMIO page (e.g. framebuffer).
// RISC-V: V + R + W + U + A + D (no X).
//
//go:nosplit
func makeUserDevicePTE(pa uintptr) uint64 {
	ppn := (uint64(pa) >> 12) & 0xFFFFFFFFFFF
	return (ppn << 10) | RV_PTE_V | RV_PTE_R | RV_PTE_W | RV_PTE_U | RV_PTE_A | RV_PTE_D
}

// makeKernelDevicePTE creates a leaf PTE for kernel-only MMIO pages.
// RISC-V: V + R + W + A + D (no U, no X).
//
//go:nosplit
func makeKernelDevicePTE(pa uintptr) uint64 {
	ppn := (uint64(pa) >> 12) & 0xFFFFFFFFFFF
	return (ppn << 10) | RV_PTE_V | RV_PTE_R | RV_PTE_W | RV_PTE_A | RV_PTE_D
}

// makeFileBufferPTE creates a leaf PTE for kernel file buffer pages.
// RISC-V: Same as kernel page PTE (V + R + W + A + D).
//
//go:nosplit
func makeFileBufferPTE(pa uintptr) uint64 {
	return makeKernelPagePTE(pa)
}

// makeKernelScratchPTE creates a leaf PTE for kernel scratch mapping pages.
// RISC-V: Same as kernel page PTE (V + R + W + A + D).
//
//go:nosplit
func makeKernelScratchPTE(pa uintptr) uint64 {
	return makeKernelPagePTE(pa)
}

// pteIsValid returns true if the PTE is valid.
//
//go:nosplit
func pteIsValid(entry uint64) bool {
	return (entry & RV_PTE_V) != 0
}

// pteExtractPA extracts the physical address from a PTE.
// RISC-V: PA is encoded as PPN in bits [53:10].
//
//go:nosplit
func pteExtractPA(entry uint64) uintptr {
	ppn := (entry >> 10) & 0xFFFFFFFFFFF
	return uintptr(ppn << 12)
}

// mapDevicePage is a no-op on RISC-V.
// Diplomat's linear map already covers all physical addresses.
//
//go:nosplit
func mapDevicePage(va, pa uintptr) bool {
	return true
}

// MapDeviceMMIO maps a physical address range as device MMIO memory
// in the kernel's page tables.
// On RISC-V, diplomat's linear map already covers MMIO regions, so this is a no-op
// (same as x86_64).
func MapDeviceMMIO(physAddr uintptr, size uint64) error {
	return nil
}

// walkPageTable translates a VA to PA by walking the RISC-V Sv48 page tables.
// Uses the SATP root (stored in ttbr1L0PA) as the L3 root.
//
// RISC-V Sv48 page table format (4-level, matching ARM64/x86_64):
//   - L3 (root): bits[47:39] (512GB per entry)
//   - L2: bits[38:30] (1GB per entry, can be gigapage leaf)
//   - L1: bits[29:21] (2MB per entry, can be megapage leaf)
//   - L0: bits[20:12] (4KB per entry, always leaf)
//
// Leaf detection: If R|W|X bits are set, it's a leaf PTE (not a branch).
//
//go:nosplit
func walkPageTable(va uintptr) uintptr {
	// Extract indices (same shifts as ARM64/x86_64)
	l3Idx := (va >> L3Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l0Idx := (va >> L0Shift) & 0x1FF

	// L3 table (root)
	l3PA := ttbr1L0PA
	l3VA := l3PA + constants.KernelMMIOOffset
	l3Entry := *(*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	if (l3Entry & RV_PTE_V) == 0 {
		return 0
	}

	// L3 cannot be a leaf in Sv48 (would be 512GB terapage, not supported)

	// L2 table
	ppn2 := (l3Entry >> 10) & 0xFFFFFFFFFFF
	l2PA := uintptr(ppn2 << 12)
	l2VA := l2PA + constants.KernelMMIOOffset
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if (l2Entry & RV_PTE_V) == 0 {
		return 0
	}

	// Check if L2 is a leaf (1GB gigapage) - if R|W|X bits set
	if (l2Entry & (RV_PTE_R | RV_PTE_W | RV_PTE_X)) != 0 {
		ppn := (l2Entry >> 10) & 0xFFFFFFFFFFF
		blockPA := uintptr(ppn << 12)
		pageOffset := va & ((1 << L2Shift) - 1) // offset within 1GB
		return blockPA | pageOffset
	}

	// L1 table
	ppn1 := (l2Entry >> 10) & 0xFFFFFFFFFFF
	l1PA := uintptr(ppn1 << 12)
	l1VA := l1PA + constants.KernelMMIOOffset
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if (l1Entry & RV_PTE_V) == 0 {
		return 0
	}

	// Check if L1 is a leaf (2MB megapage) - if R|W|X bits set
	if (l1Entry & (RV_PTE_R | RV_PTE_W | RV_PTE_X)) != 0 {
		ppn := (l1Entry >> 10) & 0xFFFFFFFFFFF
		blockPA := uintptr(ppn << 12)
		pageOffset := va & ((1 << L1Shift) - 1) // offset within 2MB
		return blockPA | pageOffset
	}

	// L0 table (4KB pages)
	ppn0 := (l1Entry >> 10) & 0xFFFFFFFFFFF
	l0PA := uintptr(ppn0 << 12)
	l0VA := l0PA + constants.KernelMMIOOffset
	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	if (l0Entry & RV_PTE_V) == 0 {
		return 0
	}

	// Extract PA from L0 leaf entry and add page offset
	ppn := (l0Entry >> 10) & 0xFFFFFFFFFFF
	pa := uintptr(ppn << 12)
	return pa | (va & (PageSize - 1))
}
