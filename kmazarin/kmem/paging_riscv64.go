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

// readSATP reads the raw SATP register value.
// RISC-V SATP format: [63:60]=MODE(9=Sv48), [59:44]=ASID(16-bit), [43:0]=PPN(PA>>12)
func readSATP() uintptr

// readCurrentL0PA reads the current L0 page table PA from the SATP register.
// Extracts PPN (bits 43:0) and shifts left by 12 to get the physical address.
//
//go:nosplit
func readCurrentL0PA() uintptr {
	satp := readSATP()
	ppn := uintptr(satp) & 0xFFFFFFFFFFF // bits 43:0
	return ppn << 12
}

// RISC-V Sv48 uses the same 4-level page table structure as ARM64 and x86_64.
// NAMING CONVENTION WARNING:
//   Shared constants (paging.go) use ARM64 naming: L0Shift=39 (root), L3Shift=12 (leaf).
//   RISC-V convention is opposite: L3=root, L0=leaf.
//   This file uses RISC-V naming for local vars but hardcoded shift values for clarity.

// verifyUserspacePriestL0 checks that a priest's page table is valid before switching to it.
// RISC-V: single SATP, so L0[0] must contain kernel mappings (copied by initProcessL0).
//
//go:nosplit
func verifyUserspacePriestL0(l0PA uintptr, asid uint16) {
	l0VA := l0PA + constants.KernelMMIOOffset
	e0 := *(*uint64)(unsafe.Pointer(l0VA))
	if (e0 & RV_PTE_V) == 0 {
		rawUARTPuts("\r\n[SWITCH_PT] L0[0] INVALID! l0PA=0x")
		rawUARTHex64(uint64(l0PA))
		rawUARTPuts(" ASID=")
		rawUARTHex64(uint64(asid))
		rawUARTPuts(" L0[0]=0x")
		rawUARTHex64(e0)
		rawUARTPuts("\r\n")
		for i := uintptr(0); i < 4; i++ {
			entry := *(*uint64)(unsafe.Pointer(l0VA + i*8))
			rawUARTPuts("  L0[")
			rawUARTHex64(uint64(i))
			rawUARTPuts("]=0x")
			rawUARTHex64(entry)
			rawUARTPuts("\r\n")
		}
		for {
		}
	}
}

// initProcessL0 performs arch-specific initialization of a new process root page table.
// RISC-V Sv48: like x86_64, uses single SATP register for both user and kernel space.
// Must copy kernel entries to preserve kernel mappings when SATP switches.
//
// Layout:
//   - L3[1-511]: Copied directly from kernel (Go heap at L3[1]=0xC000000000,
//                kernel upper half at L3[256-511]: linear map, heap, scratch).
//   - L3[0]: Contains both kmazarin code (L2[1]: VA 0x40000000+) and user code (L2[0]).
//            We create a NEW L2 table per process that copies kmazarin entries but
//            leaves L2[0] empty for per-process user-space page tables.
//
//go:nosplit
func initProcessL0(l0VA uintptr) {
	kernelL0VA := ttbr1L0PA + constants.KernelMMIOOffset

	// Copy L3[1-511] directly (includes Go heap at L3[1], kernel linear map at L3[256+])
	for i := uintptr(1); i < 512; i++ {
		*(*uint64)(unsafe.Pointer(l0VA + i*8)) = *(*uint64)(unsafe.Pointer(kernelL0VA + i*8))
	}

	// For L3[0]: kmazarin code is at VA 0x43800000 → L2 index 1 (0x40000000-0x7FFFFFFF).
	// User code is at L2[0] (VA 0x00000000-0x3FFFFFFF). Create a new L2 table so each
	// process has its own user page tables while sharing kmazarin code.
	kernelL3E0 := *(*uint64)(unsafe.Pointer(kernelL0VA))
	if (kernelL3E0 & RV_PTE_V) == 0 {
		return // No L3[0] in kernel — nothing to do
	}

	// Allocate a new L2 table for this process
	newL2PA := AllocPage(PageKernelPT)
	if newL2PA == 0 {
		return
	}
	newL2VA := newL2PA + constants.KernelMMIOOffset

	// Zero the new L2
	for i := uintptr(0); i < 512; i++ {
		*(*uint64)(unsafe.Pointer(newL2VA + i*8)) = 0
	}

	// Copy kmazarin-relevant L2 entries from the kernel's L2.
	// Kmazarin code is at L2[1] (VA 0x40000000-0x7FFFFFFF).
	// Copy entries 1+ (kernel code region), leave entry 0 empty (user space).
	kernelL2PA := pteExtractPA(kernelL3E0)
	kernelL2VA := kernelL2PA + constants.KernelMMIOOffset
	for i := uintptr(1); i < 512; i++ {
		*(*uint64)(unsafe.Pointer(newL2VA + i*8)) = *(*uint64)(unsafe.Pointer(kernelL2VA + i*8))
	}

	// Set L3[0] to point to the new L2 table
	pte := makeTablePTE(newL2PA)
	*(*uint64)(unsafe.Pointer(l0VA)) = pte

	// Verify L3[0] was set correctly by reading it back
	readBack := *(*uint64)(unsafe.Pointer(l0VA))
	if readBack != pte {
		rawUARTPuts("[initProcessL0] MISMATCH! wrote=0x")
		rawUARTHex64(pte)
		rawUARTPuts(" read=0x")
		rawUARTHex64(readBack)
		rawUARTPuts("\r\n")
		for {
		}
	}
	if (readBack & RV_PTE_V) == 0 {
		rawUARTPuts("[initProcessL0] L3[0] INVALID after write!\r\n")
		for {
		}
	}

	// Print diagnostic: L0 PA, L2 PA, L3[0] value
	rawUARTPuts("[initProcL0] l0PA=0x")
	rawUARTHex64(uint64(l0VA - constants.KernelMMIOOffset))
	rawUARTPuts(" newL2=0x")
	rawUARTHex64(uint64(newL2PA))
	rawUARTPuts(" L3[0]=0x")
	rawUARTHex64(readBack)
	rawUARTPuts("\r\n")
}

// VerifyCurrentSATPL3E0 checks that L3[0] of the current SATP's root page table
// is valid. Called from the timer IRQ handler to detect page table corruption.
// Only checks when ASID != 0 (i.e., in process context, not kernel-only).
//
//go:nosplit
func VerifyCurrentSATPL3E0() {
	satp := readSATP()
	// Extract ASID (bits 59:44)
	asid := (uint64(satp) >> 44) & 0xFFFF
	if asid == 0 {
		return // Kernel context, skip check
	}

	// Extract root PA
	ppn := uint64(satp) & 0xFFFFFFFFFFF // bits 43:0
	rootPA := uintptr(ppn << 12)
	rootVA := rootPA + constants.KernelMMIOOffset

	// Read L3[0]
	l3e0 := *(*uint64)(unsafe.Pointer(rootVA))
	if (l3e0 & RV_PTE_V) == 0 {
		rawUARTPuts("\r\n[PT_CORRUPT] L3[0] INVALID! SATP=0x")
		rawUARTHex64(uint64(satp))
		rawUARTPuts(" rootPA=0x")
		rawUARTHex64(uint64(rootPA))
		rawUARTPuts(" L3[0]=0x")
		rawUARTHex64(l3e0)
		rawUARTPuts(" ASID=")
		rawUARTHex64(asid)
		rawUARTPuts("\r\n")

		// Print L3[1] and L3[256] for comparison (should be valid kernel entries)
		l3e1 := *(*uint64)(unsafe.Pointer(rootVA + 8))
		l3e256 := *(*uint64)(unsafe.Pointer(rootVA + 256*8))
		rawUARTPuts(" L3[1]=0x")
		rawUARTHex64(l3e1)
		rawUARTPuts(" L3[256]=0x")
		rawUARTHex64(l3e256)
		rawUARTPuts("\r\n")

		// Dump first 8 entries of root page table to see full state
		rawUARTPuts(" L3[0..7]: ")
		for i := uintptr(0); i < 8; i++ {
			entry := *(*uint64)(unsafe.Pointer(rootVA + i*8))
			rawUARTHex64(entry)
			rawUART(' ')
		}
		rawUARTPuts("\r\n")

		for {
		} // Halt
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

// constructTTBR0Value constructs the SATP register value for RISC-V Sv48.
// SATP format: [63:60]=MODE(9=Sv48), [59:44]=ASID(16-bit), [43:0]=PPN(PA>>12)
//
//go:nosplit
func constructTTBR0Value(l0PA uintptr, asid uint16) uint64 {
	const satpModeSv48 = uint64(9) << 60
	ppn := uint64(l0PA) >> 12
	return satpModeSv48 | (uint64(asid) << 44) | ppn
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
// RISC-V Sv48 page table format (4-level):
//   - L3 (root): bits[47:39] (512GB per entry)
//   - L2: bits[38:30] (1GB per entry, can be gigapage leaf)
//   - L1: bits[29:21] (2MB per entry, can be megapage leaf)
//   - L0: bits[20:12] (4KB per entry, always leaf)
//
// IMPORTANT: Uses hardcoded shift values because the shared constants
// (L0Shift=39, L3Shift=12) use ARM64 naming which is opposite to RISC-V.
//
// Leaf detection: If R|W|X bits are set, it's a leaf PTE (not a branch).
//
//go:nosplit
func walkPageTable(va uintptr) uintptr {
	// Extract indices using hardcoded shifts (RISC-V Sv48)
	// L3=root(47:39), L2(38:30), L1(29:21), L0=leaf(20:12)
	l3Idx := (va >> 39) & 0x1FF // Root level (512GB)
	l2Idx := (va >> 30) & 0x1FF // 1GB level
	l1Idx := (va >> 21) & 0x1FF // 2MB level
	l0Idx := (va >> 12) & 0x1FF // 4KB level

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
		pageOffset := va & ((1 << 30) - 1) // offset within 1GB
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
		pageOffset := va & ((1 << 21) - 1) // offset within 2MB
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

// printHexRaw prints a 64-bit value as 16 hex digits via raw UART.
//
//go:nosplit
func printHexRaw(val uint64) {
	hexChars := "0123456789ABCDEF"
	for i := 60; i >= 0; i -= 4 {
		nibble := (val >> uint(i)) & 0xF
		rawUART(hexChars[nibble])
	}
}

// DumpInstructionPageFault walks the Sv48 page table for the given VA and
// prints each level's PTE to UART. Called from the instruction page fault
// handler in exceptions_riscv64.s to diagnose why a pre-mapped code page
// fails the page table walk after sfence.vma.
//
//go:nosplit
func DumpInstructionPageFault(va uintptr) {
	// Print SATP register value
	satp := readSATP()
	rawUART('\r')
	rawUART('\n')
	rawUART('S')
	rawUART('A')
	rawUART('T')
	rawUART('P')
	rawUART('=')
	printHexRaw(uint64(satp))

	// Print expected root PA (from auxv)
	rawUART(' ')
	rawUART('R')
	rawUART('=')
	printHexRaw(uint64(ttbr1L0PA))
	rawUART('\r')
	rawUART('\n')

	// Extract root PPN from SATP (bits 43:0 = PPN)
	ppn := uint64(satp) & 0xFFFFFFFFFFF // lower 44 bits
	rootPA := uintptr(ppn << 12)
	rootVA := rootPA + constants.KernelMMIOOffset

	// Print decoded root PA
	rawUART('P')
	rawUART('A')
	rawUART('=')
	printHexRaw(uint64(rootPA))
	rawUART('\r')
	rawUART('\n')

	// L3 (root level, bits 47:39)
	l3Idx := (va >> 39) & 0x1FF
	l3Entry := *(*uint64)(unsafe.Pointer(rootVA + l3Idx*8))
	rawUART('L')
	rawUART('3')
	rawUART('[')
	printHexRaw(uint64(l3Idx))
	rawUART(']')
	rawUART('=')
	printHexRaw(l3Entry)
	rawUART('\r')
	rawUART('\n')

	if (l3Entry & RV_PTE_V) == 0 {
		rawUART('!')
		rawUART('I')
		rawUART('N')
		rawUART('V')
		rawUART('\r')
		rawUART('\n')
		return
	}

	// L2 (bits 38:30)
	ppn2 := (l3Entry >> 10) & 0xFFFFFFFFFFF
	l2PA := uintptr(ppn2 << 12)
	l2VA := l2PA + constants.KernelMMIOOffset
	l2Idx := (va >> 30) & 0x1FF
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	rawUART('L')
	rawUART('2')
	rawUART('[')
	printHexRaw(uint64(l2Idx))
	rawUART(']')
	rawUART('=')
	printHexRaw(l2Entry)
	rawUART('\r')
	rawUART('\n')

	if (l2Entry & RV_PTE_V) == 0 {
		rawUART('!')
		rawUART('I')
		rawUART('N')
		rawUART('V')
		rawUART('\r')
		rawUART('\n')
		return
	}
	// Check if L2 is a leaf (gigapage)
	if (l2Entry & (RV_PTE_R | RV_PTE_W | RV_PTE_X)) != 0 {
		rawUART('G')
		rawUART('P')
		rawUART('\r')
		rawUART('\n')
		return
	}

	// L1 (bits 29:21)
	ppn1 := (l2Entry >> 10) & 0xFFFFFFFFFFF
	l1PA := uintptr(ppn1 << 12)
	l1VA := l1PA + constants.KernelMMIOOffset
	l1Idx := (va >> 21) & 0x1FF
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	rawUART('L')
	rawUART('1')
	rawUART('[')
	printHexRaw(uint64(l1Idx))
	rawUART(']')
	rawUART('=')
	printHexRaw(l1Entry)
	rawUART('\r')
	rawUART('\n')

	if (l1Entry & RV_PTE_V) == 0 {
		rawUART('!')
		rawUART('I')
		rawUART('N')
		rawUART('V')
		rawUART('\r')
		rawUART('\n')
		return
	}
	// Check if L1 is a leaf (megapage)
	if (l1Entry & (RV_PTE_R | RV_PTE_W | RV_PTE_X)) != 0 {
		rawUART('M')
		rawUART('P')
		if (l1Entry & RV_PTE_X) == 0 {
			rawUART('!')
			rawUART('X')
		}
		rawUART('\r')
		rawUART('\n')
		return
	}

	// L0 (leaf level, bits 20:12)
	ppn0 := (l1Entry >> 10) & 0xFFFFFFFFFFF
	l0PA := uintptr(ppn0 << 12)
	l0VA := l0PA + constants.KernelMMIOOffset
	l0Idx := (va >> 12) & 0x1FF
	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	rawUART('L')
	rawUART('0')
	rawUART('[')
	printHexRaw(uint64(l0Idx))
	rawUART(']')
	rawUART('=')
	printHexRaw(l0Entry)
	rawUART('\r')
	rawUART('\n')

	if (l0Entry & RV_PTE_V) == 0 {
		rawUART('!')
		rawUART('I')
		rawUART('N')
		rawUART('V')
		rawUART('\r')
		rawUART('\n')
		return
	}

	// Report permission bits
	if (l0Entry & RV_PTE_X) == 0 {
		rawUART('!')
		rawUART('X')
	}
	if (l0Entry & RV_PTE_R) != 0 {
		rawUART('R')
	}
	if (l0Entry & RV_PTE_W) != 0 {
		rawUART('W')
	}
	if (l0Entry & RV_PTE_X) != 0 {
		rawUART('X')
	}
	rawUART('\r')
	rawUART('\n')
}
