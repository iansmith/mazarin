//go:build arm64

// diplomat/main/pagetable_arm64.go
// ARM64 4-level page table implementation for mapping high virtual addresses
//
// ARM64 page table format (4KB granule, 48-bit VA):
//   Level 0 (L0/PGD): 512 entries, each covers 512GB
//   Level 1 (L1/PUD): 512 entries, each covers 1GB (can be block descriptor)
//   Level 2 (L2/PMD): 512 entries, each covers 2MB (can be block descriptor)
//   Level 3 (L3/PTE): 512 entries, each covers 4KB
//
// Descriptor format:
//   Bits 47:12 = Output address
//   Bit 1:0 = Type (00=invalid, 01=block, 11=table/page)
//   Block descriptors allowed at L1 (1GB) and L2 (2MB)

package main

import (
	"unsafe"
)

// Page table constants for ARM64
const (
	PageSize    = 4096            // 4KB pages
	Page2MBSize = 2 * 1024 * 1024 // 2MB large pages (L2 block)
	Page1GBSize = 1024 * 1024 * 1024

	// Descriptor type bits (bits 1:0)
	DESC_INVALID = 0b00 // Invalid entry
	DESC_BLOCK   = 0b01 // Block descriptor (L1 or L2)
	DESC_TABLE   = 0b11 // Table descriptor (L0, L1, L2)
	DESC_PAGE    = 0b11 // Page descriptor (L3)

	// Lower attributes (bits 11:2)
	ATTR_IDX_NORMAL = (3 << 2)  // MAIR index 3 = Normal Write-Back (EDK2: 0xFF)
	ATTR_IDX_DEVICE = (1 << 2)  // MAIR index 1 = Device-nGnRnE (cardinal-compatible)
	ATTR_NS         = (1 << 5)  // Non-secure
	ATTR_AP_RW_EL1  = (0 << 6)  // EL1 read/write, EL0 no access
	ATTR_AP_RW_ALL  = (1 << 6)  // EL1 and EL0 read/write
	ATTR_SH_INNER   = (3 << 8)  // Inner shareable
	ATTR_AF         = (1 << 10) // Access flag

	// Upper attributes (bits 63:52)
	ATTR_PXN = (1 << 53) // Privileged execute-never
	ATTR_UXN = (1 << 54) // Unprivileged execute-never
	ATTR_XN  = ATTR_PXN | ATTR_UXN

	// Standard attribute combinations
	ATTR_NORMAL_RW = ATTR_IDX_NORMAL | ATTR_AP_RW_EL1 | ATTR_SH_INNER | ATTR_AF
	ATTR_DEVICE_RW = ATTR_IDX_DEVICE | ATTR_AP_RW_EL1 | ATTR_AF

	// Address mask for descriptors (bits 47:12)
	DESC_ADDR_MASK = 0x0000FFFFFFFFF000

	// Number of entries per table (512 = 2^9)
	ENTRIES_PER_TABLE = 512

	// Default kernel memory size (64MB)
	DefaultKernelMemSize = 64 * 1024 * 1024
)

// PageTableSet holds pointers to all page table levels
type PageTableSet struct {
	L0 uint64 // Physical address of L0 table (will be loaded into TTBR0_EL1)

	// Physical memory allocated for kernel
	PhysBase uint64 // Physical base address
	PhysSize uint64 // Size of physical memory allocated

	// Virtual address mapping
	VirtBase uint64 // Virtual base address (from ELF)
}

// Virtual address breakdown for index extraction (48-bit VA, 4KB granule)
func l0Index(vaddr uint64) uint64 {
	return (vaddr >> 39) & 0x1FF
}

func l1Index(vaddr uint64) uint64 {
	return (vaddr >> 30) & 0x1FF
}

func l2Index(vaddr uint64) uint64 {
	return (vaddr >> 21) & 0x1FF
}

func l3Index(vaddr uint64) uint64 {
	return (vaddr >> 12) & 0x1FF
}

// BuildPageTables creates page tables mapping virtBase → physBase for the given size.
// Uses 2MB blocks for efficiency (64MB = 32 blocks).
// Also creates identity mapping for diplomat's own code during the transition.
func BuildPageTables(virtBase, physBase, size uint64) (*PageTableSet, error) {
	// Round size up to 2MB boundary
	size = (size + Page2MBSize - 1) &^ (Page2MBSize - 1)
	numPages := size / Page2MBSize

	// Allocate page tables from UEFI
	// We need: 1 L0 + 1 L1 (for kernel) + 1 L2 (for kernel)
	//        + 1 L1 (for identity mapping using 1GB blocks)
	numTablePages := uint64(4)
	tablesPhys, err := allocatePhysPages(numTablePages)
	if err != nil {
		return nil, err
	}

	// Zero all page table memory
	plat.ZeroMemory(tablesPhys, numTablePages*PageSize)

	// Layout:
	// Page 0: L0
	// Page 1: L1 for kernel (high addresses)
	// Page 2: L2 for kernel
	// Page 3: L1 for identity (low addresses, 1GB blocks)
	l0Phys := tablesPhys
	l1KernelPhys := tablesPhys + PageSize
	l2KernelPhys := tablesPhys + 2*PageSize
	l1IdentPhys := tablesPhys + 3*PageSize

	// Get pointers to tables
	l0 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l0Phys)))
	l1Kernel := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l1KernelPhys)))
	l2Kernel := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l2KernelPhys)))
	l1Ident := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l1IdentPhys)))

	// Calculate indices for kernel virtual address
	kernelL0Idx := l0Index(virtBase)
	kernelL1Idx := l1Index(virtBase)
	kernelL2Idx := l2Index(virtBase)

	// Set up L0 entry for kernel space (high canonical address)
	l0[kernelL0Idx] = l1KernelPhys | DESC_TABLE

	// Set up L1 entry for kernel
	l1Kernel[kernelL1Idx] = l2KernelPhys | DESC_TABLE

	// The 2MB L2 block entries map 2MB-aligned virtual regions to 2MB-aligned physical
	// regions. If virtBase is not 2MB-aligned, adjust the physical base so the
	// sub-2MB offset within each page matches correctly.
	virtOffset := virtBase & (Page2MBSize - 1)
	l2PhysBase := physBase - virtOffset

	// Set up L2 entries for kernel - map 2MB blocks
	for i := uint64(0); i < numPages; i++ {
		l2Idx := kernelL2Idx + i
		if l2Idx >= ENTRIES_PER_TABLE {
			// Would overflow into next L1 entry - not handled for simplicity
			return nil, &errKernelMappingSpans
		}
		physAddr := l2PhysBase + i*Page2MBSize
		l2Kernel[l2Idx] = physAddr | ATTR_NORMAL_RW | DESC_BLOCK
	}

	// Set up identity mapping for low memory (so diplomat code keeps working)
	// Use 1GB blocks at L1 to map first 8GB
	// This covers UEFI firmware, diplomat code, and allocated memory
	l0[0] = l1IdentPhys | DESC_TABLE
	for i := uint64(0); i < 8; i++ {
		l1Ident[i] = (i * Page1GBSize) | ATTR_NORMAL_RW | DESC_BLOCK
	}

	result := dNew[PageTableSet]()
	if result == nil {
		return nil, &errFailedAllocPageTableSet
	}
	result.L0 = l0Phys
	result.PhysBase = physBase
	result.PhysSize = size
	result.VirtBase = virtBase

	return result, nil
}

// pageTableResult is a global to avoid heap allocation after ExitBootServices.
var pageTableResult PageTableSet

// buildPageTablesManual creates page tables without calling UEFI boot services.
// It places the 4 page table pages at physBase + size - 4*PageSize (end of kernel region).
// This must be called after ExitBootServices when UEFI allocation is unavailable.
func buildPageTablesManual(virtBase, physBase, size uint64) (*PageTableSet, error) {
	size = (size + Page2MBSize - 1) &^ (Page2MBSize - 1)
	numPages := size / Page2MBSize

	// Place page tables at the end of the kernel's allocated physical region.
	numTablePages := uint64(4)
	tablesPhys := physBase + size - numTablePages*PageSize

	// Zero all page table memory
	plat.ZeroMemory(tablesPhys, numTablePages*PageSize)

	l0Phys := tablesPhys
	l1KernelPhys := tablesPhys + PageSize
	l2KernelPhys := tablesPhys + 2*PageSize
	l1IdentPhys := tablesPhys + 3*PageSize

	l0 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l0Phys)))
	l1Kernel := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l1KernelPhys)))
	l2Kernel := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l2KernelPhys)))
	l1Ident := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l1IdentPhys)))

	kernelL0Idx := l0Index(virtBase)
	kernelL1Idx := l1Index(virtBase)
	kernelL2Idx := l2Index(virtBase)

	l0[kernelL0Idx] = l1KernelPhys | DESC_TABLE
	l1Kernel[kernelL1Idx] = l2KernelPhys | DESC_TABLE

	virtOffset := virtBase & (Page2MBSize - 1)
	l2PhysBase := physBase - virtOffset

	for i := uint64(0); i < numPages; i++ {
		l2Idx := kernelL2Idx + i
		if l2Idx >= ENTRIES_PER_TABLE {
			return nil, &errKernelMappingSpans
		}
		physAddr := l2PhysBase + i*Page2MBSize
		l2Kernel[l2Idx] = physAddr | ATTR_NORMAL_RW | DESC_BLOCK
	}

	l0[0] = l1IdentPhys | DESC_TABLE
	for i := uint64(0); i < 8; i++ {
		l1Ident[i] = (i * Page1GBSize) | ATTR_NORMAL_RW | DESC_BLOCK
	}

	pageTableResult.L0 = l0Phys
	pageTableResult.PhysBase = physBase
	pageTableResult.PhysSize = size
	pageTableResult.VirtBase = virtBase
	return &pageTableResult, nil
}

// addKernelMappingToCurrentPT grafts kernel mappings into the current (UEFI) page tables.
// For lower canonical addresses (< 0xFFFF000000000000), it modifies TTBR0_EL1's L0 table.
// For upper canonical addresses (>= 0xFFFF000000000000), it creates a new TTBR1 page table
// and configures TCR_EL1 to enable TTBR1 translations.
func addKernelMappingToCurrentPT(virtBase, physBase, size uint64) error {
	if virtBase >= 0xFFFF000000000000 {
		return addUpperKernelMapping(virtBase, physBase, size)
	}

	size = (size + Page2MBSize - 1) &^ (Page2MBSize - 1)
	numPages := size / Page2MBSize

	// Read current TTBR0_EL1
	currentTTBR0 := readTTBR0()
	l0Phys := currentTTBR0 & DESC_ADDR_MASK // mask off ASID and flags

	l0 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l0Phys)))

	kernelL0Idx := l0Index(virtBase)
	kernelL1Idx := l1Index(virtBase)
	kernelL2Idx := l2Index(virtBase)

	// Allocate L1 and L2 pages within the kernel's physical memory region
	// to avoid UEFI pool corruption. Place them at the end of the kernel memory.
	l1Phys := physBase + size - 2*PageSize
	l2Phys := physBase + size - 1*PageSize

	// Zero the new pages
	plat.ZeroMemory(l1Phys, PageSize)
	plat.ZeroMemory(l2Phys, PageSize)

	l1 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l1Phys)))
	l2 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l2Phys)))

	// NOTE: UEFI on ARM64 typically doesn't write-protect page tables,
	// but if it does, we'd need to disable MMU or change AP bits.
	// For now, assume we can write to the L0 table directly.

	// Set L0 entry to point to our new L1
	l0[kernelL0Idx] = l1Phys | DESC_TABLE

	// Set L1 entry to point to our L2
	l1[kernelL1Idx] = l2Phys | DESC_TABLE

	// Map 2MB blocks in L2
	virtOffset := virtBase & (Page2MBSize - 1)
	l2PhysBase := physBase - virtOffset

	for i := uint64(0); i < numPages; i++ {
		l2Idx := kernelL2Idx + i
		if l2Idx >= ENTRIES_PER_TABLE {
			return &errKernelMappingSpans
		}
		physAddr := l2PhysBase + i*Page2MBSize
		l2[l2Idx] = physAddr | ATTR_NORMAL_RW | DESC_BLOCK
	}

	// Invalidate TLB
	tlbiVmalle1()

	// Data synchronization barrier to ensure TLB invalidation completes
	dsb()

	// Instruction synchronization barrier
	isb()

	return nil
}

// addUpperKernelMapping creates a fresh TTBR1 page table hierarchy and maps
// the kernel at an upper canonical virtual address (>= 0xFFFF000000000000).
// UEFI firmware typically has EPD1=1 (TTBR1 disabled), so we must create the
// page tables from scratch and configure TCR_EL1 to enable TTBR1 translations.
func addUpperKernelMapping(virtBase, physBase, size uint64) error {
	size = (size + Page2MBSize - 1) &^ (Page2MBSize - 1)
	numPages := size / Page2MBSize

	// Allocate 3 pages (L0, L1, L2) at the end of the kernel physical region.
	// These are placed just before where TTBR0's L1/L2 would go, so we use
	// the last 3 pages of the kernel region.
	l0Phys := physBase + size - 3*PageSize
	l1Phys := physBase + size - 2*PageSize
	l2Phys := physBase + size - 1*PageSize

	// Zero all page table memory
	plat.ZeroMemory(l0Phys, 3*PageSize)

	l0 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l0Phys)))
	l1 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l1Phys)))
	l2 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l2Phys)))

	// For TTBR1, the hardware ignores the top bits and uses the VA-space-relative
	// index. With T1SZ=16 (48-bit VA), the L0 index is extracted from bits [47:39]
	// of the virtual address. For 0xFFFFFFFF43800000:
	//   bits [47:39] = 0x1FF = 511
	kernelL0Idx := l0Index(virtBase)
	kernelL1Idx := l1Index(virtBase)
	kernelL2Idx := l2Index(virtBase)

	// Set L0 → L1 → L2 chain
	l0[kernelL0Idx] = l1Phys | DESC_TABLE
	l1[kernelL1Idx] = l2Phys | DESC_TABLE

	// Map 2MB blocks in L2
	virtOffset := virtBase & (Page2MBSize - 1)
	l2PhysBase := physBase - virtOffset

	for i := uint64(0); i < numPages; i++ {
		l2Idx := kernelL2Idx + i
		if l2Idx >= ENTRIES_PER_TABLE {
			return &errKernelMappingSpans
		}
		physAddr := l2PhysBase + i*Page2MBSize
		l2[l2Idx] = physAddr | ATTR_NORMAL_RW | DESC_BLOCK
	}

	// Configure TCR_EL1 to enable TTBR1 translations
	configureUpperTranslation()

	// Set TTBR1_EL1 to point to our new L0 table
	writeTTBR1(l0Phys)

	// Invalidate TLB and synchronize
	tlbiVmalle1()
	dsb()
	isb()

	return nil
}

// configureUpperTranslation configures TCR_EL1 for both TTBR0 and TTBR1.
// UEFI typically sets EPD1=1 (disable TTBR1 walks) and T0SZ to a value
// that may not support full 48-bit user VAs. We configure:
//
// TTBR0 (lower/user):
//   - T0SZ=16 (48-bit VA space) — required for stack at 0x7FFF...
//
// TTBR1 (upper/kernel):
//   - T1SZ=16 (48-bit VA space)
//   - Clear EPD1 (enable TTBR1 translation walks)
//   - TG1=10 (4KB granule), SH1=11, ORGN1/IRGN1=01 (write-back cacheable)
func configureUpperTranslation() {
	tcr := readTCR()

	// Clear TTBR0 T0SZ field and TTBR1-related fields:
	clearMask := uint64(0xFFFF_FFFF_FFFF_FFFF)
	clearMask &^= 0x3F          // T0SZ[5:0]
	clearMask &^= (0x3F << 16)  // T1SZ[21:16]
	clearMask &^= (1 << 23)     // EPD1[23]
	clearMask &^= (3 << 24)     // IRGN1[25:24]
	clearMask &^= (3 << 26)     // ORGN1[27:26]
	clearMask &^= (3 << 28)     // SH1[29:28]
	clearMask &^= (3 << 30)     // TG1[31:30]
	tcr &= clearMask

	// Set TTBR0 fields:
	tcr |= 16                   // T0SZ = 16 → 48-bit user VA

	// Set TTBR1 fields:
	tcr |= (16 << 16)  // T1SZ = 16 → 48-bit VA
	tcr |= (0 << 23)   // EPD1 = 0  → enable TTBR1 walks
	tcr |= (1 << 24)   // IRGN1 = 01 → inner write-back, read-allocate, write-allocate
	tcr |= (1 << 26)   // ORGN1 = 01 → outer write-back, read-allocate, write-allocate
	tcr |= (3 << 28)   // SH1 = 11  → inner shareable
	tcr |= (2 << 30)   // TG1 = 10  → 4KB granule

	writeTCR(tcr)
}

// readTTBR0 reads the TTBR0_EL1 register (page table base for EL0/EL1)
func readTTBR0() uint64

// writeTTBR0 writes to TTBR0_EL1 (flushes TLB)
func writeTTBR0(val uint64)

// readTTBR1 reads the TTBR1_EL1 register (page table base for upper VA range)
func readTTBR1() uint64

// writeTTBR1 writes to TTBR1_EL1 with DSB+ISB barriers
func writeTTBR1(val uint64)

// readTCR reads TCR_EL1 (Translation Control Register)
func readTCR() uint64

// writeTCR writes to TCR_EL1 with ISB barrier
func writeTCR(val uint64)

// tlbiVmalle1 invalidates all TLB entries for EL1 (TLBI VMALLE1)
func tlbiVmalle1()

// dsb executes a data synchronization barrier
func dsb()

// isb executes an instruction synchronization barrier
func isb()

// disableWriteProtect is a no-op on ARM64.
// ARM64 uses AP (Access Permission) bits in page table entries
// rather than a global CR0.WP bit like x86.
func disableWriteProtect() {
	// No-op on ARM64
}

// enableWriteProtect is a no-op on ARM64.
func enableWriteProtect() {
	// No-op on ARM64
}

// physAddrResult is a global to avoid heap allocation for the UEFI out-parameter.
var physAddrResult uint64

// allocatePhysPages allocates physical pages from UEFI boot services
//
//go:nosplit
func allocatePhysPages(numPages uint64) (uint64, error) {
	debugPortOut('P')
	// Use AllocateMaxAddress to constrain UEFI to addresses below 4GB,
	// which is the limit of the linear map (linearMapMaxPA).
	physAddrResult = linearMapMaxPA - 1
	debugPortOut('Q')
	status := plat.AllocatePages(AllocateMaxAddress, EfiLoaderData, numPages, &physAddrResult)
	debugPortOut('S')
	if status != EFI_SUCCESS {
		return 0, &errAllocatePagesFailed
	}
	debugPortOut('T')
	return physAddrResult, nil
}

// readBootPageTableBase returns the current UEFI page table root address.
// On ARM64, this is TTBR0_EL1 masked to the address field.
func readBootPageTableBase() uint64 {
	return readTTBR0() & DESC_ADDR_MASK
}

// JumpToKernelWithTTBR0 switches to new page tables and jumps to kernel entry.
// This is the point of no return - diplomat code runs via identity mapping.
func JumpToKernelWithTTBR0(l0Phys, virtEntry uint64) {
	jumpToKernelWithTTBR0(l0Phys, virtEntry)
}

// jumpToKernelWithTTBR0 is implemented in assembly
func jumpToKernelWithTTBR0(l0Phys, virtEntry uint64)
