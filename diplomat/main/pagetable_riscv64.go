//go:build riscv64

// diplomat/main/pagetable_riscv64.go
// RISC-V Sv39 3-level page table implementation
//
// RISC-V Sv39 page table format (4KB pages, 39-bit VA):
//   Level 2 (root): 512 entries, each covers 1GB
//   Level 1: 512 entries, each covers 2MB (can be leaf with large page)
//   Level 0: 512 entries, each covers 4KB
//
// PTE format (64-bit):
//   Bits 53:10 = PPN (Physical Page Number, PA[55:12])
//   Bits 9:8 = RSW (Reserved for Software)
//   Bit 7 = D (Dirty)
//   Bit 6 = A (Accessed)
//   Bit 5 = G (Global)
//   Bit 4 = U (User)
//   Bit 3 = X (eXecute)
//   Bit 2 = W (Write)
//   Bit 1 = R (Read)
//   Bit 0 = V (Valid)
//
// Leaf PTEs (pointing to actual pages): R, W, or X bit must be set
// Non-leaf PTEs (pointing to next level): R=W=X=0

package main

import (
	"unsafe"
)

// Page table constants for RISC-V Sv39
const (
	PageSize    = 4096            // 4KB pages
	Page2MBSize = 2 * 1024 * 1024 // 2MB large pages (L1 leaf)
	Page1GBSize = 1024 * 1024 * 1024

	// PTE flags
	PTE_V = 1 << 0  // Valid
	PTE_R = 1 << 1  // Readable
	PTE_W = 1 << 2  // Writable
	PTE_X = 1 << 3  // Executable
	PTE_U = 1 << 4  // User accessible
	PTE_G = 1 << 5  // Global
	PTE_A = 1 << 6  // Accessed
	PTE_D = 1 << 7  // Dirty

	// Common PTE combinations
	PTE_LEAF_RW  = PTE_V | PTE_R | PTE_W | PTE_A | PTE_D // RW page
	PTE_LEAF_RWX = PTE_V | PTE_R | PTE_W | PTE_X | PTE_A | PTE_D // RWX page
	PTE_BRANCH   = PTE_V // Non-leaf PTE (branch to next level)

	// PPN mask (bits 53:10)
	PTE_PPN_MASK = 0x3FFFFFFFFFC00

	// Number of entries per table (512 = 2^9)
	ENTRIES_PER_TABLE = 512

	// Default kernel memory size (64MB)
	DefaultKernelMemSize = 64 * 1024 * 1024
)

// PageTableSet holds pointers to page table levels
type PageTableSet struct {
	L2 uint64 // Physical address of L2 (root) table (loaded into SATP)

	// Physical memory allocated for kernel
	PhysBase uint64 // Physical base address
	PhysSize uint64 // Size of physical memory allocated

	// Virtual address mapping
	VirtBase uint64 // Virtual base address (from ELF)
}

// Virtual address breakdown for index extraction (39-bit VA)
func l2Index(vaddr uint64) uint64 {
	return (vaddr >> 30) & 0x1FF
}

func l1Index(vaddr uint64) uint64 {
	return (vaddr >> 21) & 0x1FF
}

func l0Index(vaddr uint64) uint64 {
	return (vaddr >> 12) & 0x1FF
}

// makePTE creates a PTE from a physical address and flags
func makePTE(physAddr uint64, flags uint64) uint64 {
	ppn := (physAddr >> 12) & 0xFFFFFFFFFFF // 44-bit PPN
	return (ppn << 10) | flags
}

// BuildPageTables creates Sv39 page tables mapping virtBase → physBase.
// Uses 2MB leaf PTEs for efficiency when possible.
func BuildPageTables(virtBase, physBase, size uint64) (*PageTableSet, error) {
	// Round size up to 2MB boundary
	size = (size + Page2MBSize - 1) &^ (Page2MBSize - 1)
	numBlocks := size / Page2MBSize

	// Allocate page tables: 1 L2 + 1 L1 + identity map L2
	numTablePages := uint64(3)
	tablesPhys, err := allocatePhysPages(numTablePages)
	if err != nil {
		return nil, err
	}

	// Zero all page table memory
	plat.ZeroMemory(tablesPhys, numTablePages*PageSize)

	// Layout:
	// Page 0: L2 (root)
	// Page 1: L1 for kernel
	// Page 2: L2 for identity mapping (low addresses)
	l2Phys := tablesPhys
	l1KernelPhys := tablesPhys + PageSize
	l2IdentPhys := tablesPhys + 2*PageSize

	// Get pointers to tables
	l2 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l2Phys)))
	l1Kernel := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l1KernelPhys)))
	l2Ident := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l2IdentPhys)))

	// Calculate indices for kernel virtual address
	kernelL2Idx := l2Index(virtBase)
	kernelL1Idx := l1Index(virtBase)

	// Set up L2 entry for kernel space (points to L1)
	l2[kernelL2Idx] = makePTE(l1KernelPhys, PTE_BRANCH)

	// Map kernel using 2MB L1 leaf PTEs
	virtOffset := virtBase & (Page2MBSize - 1)
	l1PhysBase := physBase - virtOffset

	for i := uint64(0); i < numBlocks; i++ {
		l1Idx := kernelL1Idx + i
		if l1Idx >= ENTRIES_PER_TABLE {
			return nil, &errKernelMappingSpans
		}
		blockPhys := l1PhysBase + i*Page2MBSize
		l1Kernel[l1Idx] = makePTE(blockPhys, PTE_LEAF_RWX)
	}

	// Identity map first 4GB using 1GB L2 leaf PTEs (for diplomat code)
	for i := uint64(0); i < 4; i++ {
		gigAddr := i * Page1GBSize
		l2Ident[i] = makePTE(gigAddr, PTE_LEAF_RWX)
	}

	// Set up L2 entry for identity mapping (low addresses, index 0)
	l2[0] = makePTE(l2IdentPhys, PTE_BRANCH)

	return &PageTableSet{
		L2:       l2Phys,
		PhysBase: physBase,
		PhysSize: size,
		VirtBase: virtBase,
	}, nil
}

// addKernelMappingToCurrentPT is a stub for compatibility
// Diplomat uses BuildPageTables and switches wholesale on RISC-V
func addKernelMappingToCurrentPT(virtBase, physBase, size uint64) error {
	return nil // Not used on RISC-V
}

// ReadPageTables is not used on RISC-V
func ReadPageTables() (*PageTableSet, error) {
	return nil, &errNotImplemented
}

// allocatePhysPages allocates physical pages below 4GB for page tables
func allocatePhysPages(pages uint64) (uint64, error) {
	var physAddr uint64 = linearMapMaxPA - 1
	status := plat.AllocatePages(AllocateMaxAddress, EfiLoaderData, pages, &physAddr)
	if status != EFI_SUCCESS {
		return 0, &errAllocationFailed
	}
	return physAddr, nil
}

// Pre-allocated errors (note: some errors like errAllocationFailed and errKernelMappingSpans
// are also defined in uefi_blockdev.go, so we don't redefine them here)
var (
	errNotImplemented = blockDevError{"pagetable: not implemented"}
)

// readBootPageTableBase returns the current UEFI page table root address.
// On RISC-V, this is SATP register PPN field (bits 43:0) << 12.
func readBootPageTableBase() uint64 {
	satp := readSATP()
	// SATP format (Sv39 mode):
	// bits 63:60 = mode (8 for Sv39)
	// bits 59:44 = ASID
	// bits 43:0 = PPN (physical page number)
	ppn := satp & 0x00000FFFFFFFFFFF
	return ppn << 12
}
