// diplomat/main/pagetable.go
// x86_64 4-level page table implementation for mapping high virtual addresses
package main

import (
	"unsafe"
)

// Page table constants
const (
	PageSize    = 4096          // 4KB pages
	Page2MBSize = 2 * 1024 * 1024 // 2MB large pages

	// Page table entry flags
	PTE_PRESENT  = 1 << 0  // Page is present
	PTE_WRITABLE = 1 << 1  // Page is writable
	PTE_USER     = 1 << 2  // User-mode accessible
	PTE_PWT      = 1 << 3  // Page-level write-through
	PTE_PCD      = 1 << 4  // Page-level cache disable
	PTE_ACCESSED = 1 << 5  // Page has been accessed
	PTE_DIRTY    = 1 << 6  // Page has been written (leaf only)
	PTE_PS       = 1 << 7  // Page size (1 = large page in PD)
	PTE_GLOBAL   = 1 << 8  // Global page (not flushed on CR3 switch)
	PTE_NX       = 1 << 63 // No-execute (if supported)

	// Address mask for page table entries (bits 51:12)
	PTE_ADDR_MASK = 0x000FFFFFFFFFF000

	// Number of entries per table (512 = 2^9)
	ENTRIES_PER_TABLE = 512

	// Default kernel memory size (64MB)
	DefaultKernelMemSize = 64 * 1024 * 1024
)

// PageTableSet holds pointers to all page table levels
type PageTableSet struct {
	PML4     uint64 // Physical address of PML4

	// Physical memory allocated for kernel
	PhysBase uint64 // Physical base address
	PhysSize uint64 // Size of physical memory allocated

	// Virtual address mapping
	VirtBase uint64 // Virtual base address (from ELF)
}

// Virtual address breakdown for index extraction
func pml4Index(vaddr uint64) uint64 {
	return (vaddr >> 39) & 0x1FF
}

func pdptIndex(vaddr uint64) uint64 {
	return (vaddr >> 30) & 0x1FF
}

func pdIndex(vaddr uint64) uint64 {
	return (vaddr >> 21) & 0x1FF
}

func ptIndex(vaddr uint64) uint64 {
	return (vaddr >> 12) & 0x1FF
}

// BuildPageTables creates page tables mapping virtBase → physBase for the given size.
// Uses 2MB pages for efficiency (64MB = 32 pages).
// Also creates identity mapping for diplomat's own code during the transition.
func BuildPageTables(virtBase, physBase, size uint64) (*PageTableSet, error) {
	// Round size up to 2MB boundary
	size = (size + Page2MBSize - 1) &^ (Page2MBSize - 1)
	numPages := size / Page2MBSize

	// Allocate page tables from UEFI
	// We need: 1 PML4 + 1 PDPT + 1 PD (for kernel mapping)
	//        + 1 PDPT (for identity mapping using 1GB pages)
	numTablePages := uint64(4)
	tablesPhys, err := allocatePhysPages(numTablePages)
	if err != nil {
		return nil, err
	}

	// Zero all page table memory
	zeroMemory(tablesPhys, numTablePages*PageSize)

	// Layout:
	// Page 0: PML4
	// Page 1: PDPT for kernel (high addresses)
	// Page 2: PD for kernel
	// Page 3: PDPT for identity (low addresses, 1GB pages)
	pml4Phys := tablesPhys
	pdptKernelPhys := tablesPhys + PageSize
	pdKernelPhys := tablesPhys + 2*PageSize
	pdptIdentPhys := tablesPhys + 3*PageSize

	// Get pointers to tables
	pml4 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(pml4Phys)))
	pdptKernel := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(pdptKernelPhys)))
	pdKernel := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(pdKernelPhys)))
	pdptIdent := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(pdptIdentPhys)))

	// Calculate indices for kernel virtual address
	kernelPML4Idx := pml4Index(virtBase)
	kernelPDPTIdx := pdptIndex(virtBase)
	kernelPDIdx := pdIndex(virtBase)

	// Set up PML4 entry for kernel space (high canonical address)
	pml4[kernelPML4Idx] = pdptKernelPhys | PTE_PRESENT | PTE_WRITABLE

	// Set up PDPT entry for kernel
	pdptKernel[kernelPDPTIdx] = pdKernelPhys | PTE_PRESENT | PTE_WRITABLE

	// Set up PD entries for kernel - map 2MB pages
	for i := uint64(0); i < numPages; i++ {
		pdIdx := kernelPDIdx + i
		if pdIdx >= ENTRIES_PER_TABLE {
			// Would overflow into next PDPT entry - not handled for simplicity
			return nil, &blockDevError{"kernel mapping spans multiple PDPT entries"}
		}
		physAddr := physBase + i*Page2MBSize
		pdKernel[pdIdx] = physAddr | PTE_PRESENT | PTE_WRITABLE | PTE_PS
	}

	// Set up identity mapping for low memory (so diplomat code keeps working)
	// Use 1GB pages (PTE_PS in PDPT entries) to map first 8GB
	// This covers UEFI firmware (~2GB), diplomat code, and allocated memory
	pml4[0] = pdptIdentPhys | PTE_PRESENT | PTE_WRITABLE
	for i := uint64(0); i < 8; i++ {
		pdptIdent[i] = (i * 1024 * 1024 * 1024) | PTE_PRESENT | PTE_WRITABLE | PTE_PS
	}

	result := dNew[PageTableSet]()
	if result == nil {
		return nil, &blockDevError{"failed to allocate PageTableSet"}
	}
	result.PML4 = pml4Phys
	result.PhysBase = physBase
	result.PhysSize = size
	result.VirtBase = virtBase

	return result, nil
}

// allocatePhysPages allocates physical pages from UEFI boot services
//go:nosplit
func allocatePhysPages(numPages uint64) (uint64, error) {
	debugPortOut('P')
	var physAddr uint64
	debugPortOut('Q')
	status := UEFIAllocatePages(AllocateAnyPages, EfiLoaderData, numPages, &physAddr)
	debugPortOut('S')
	if status != EFI_SUCCESS {
		return 0, &blockDevError{"AllocatePages failed"}
	}
	debugPortOut('T')
	return physAddr, nil
}

// JumpToKernelWithCR3 switches to new page tables and jumps to kernel entry.
// This is the point of no return - diplomat code runs via identity mapping.
func JumpToKernelWithCR3(pml4Phys, virtEntry uint64) {
	jumpToKernelWithCR3(pml4Phys, virtEntry)
}

// jumpToKernelWithCR3 is implemented in assembly
func jumpToKernelWithCR3(pml4Phys, virtEntry uint64)
