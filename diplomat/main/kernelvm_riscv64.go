//go:build riscv64

// diplomat/main/kernelvm_riscv64.go - Kernel virtual memory setup for RISC-V
//
// Creates the kernel virtual memory environment:
//   - Linear map of physical RAM in high virtual addresses
//   - g0 + exception stacks at canonical VAs
//   - Page pool for demand paging
//   - MMIO device mappings

package main

import (
	"unsafe"
)

// linearMapMaxPA is the maximum physical address that can be linearly mapped.
// This is the upper limit for physical addresses accessible through the linear map.
// Set to 4GB to match ARM64/AMD64 behavior.
const linearMapMaxPA = uint64(0x100000000)

// KernelVM constants matching cardinal-style layout
const (
	KernelVAOffset       = 0xFFFFFFFF00000000
	KernelStacksVirtBase = KernelVAOffset + 0x43E00000 // 0xFFFFFFFF43E00000
	KernelG0StackSize    = 0x8000                      // 32KB
	KernelExcStackSize   = 0x4000                      // 16KB
	KernelG0StackBottom  = KernelStacksVirtBase
	KernelG0StackTop     = KernelG0StackBottom + KernelG0StackSize
	KernelExcStackBottom = KernelG0StackTop
	KernelExcStackTop    = KernelExcStackBottom + KernelExcStackSize

	// Heap VA range
	KernelHeapStart = 0xFFFF000100000000
	KernelHeapEnd   = 0xFFFF100000000000

	// MMIO physical addresses (QEMU virt RISC-V)
	mmioUartBase  = 0x10000000
	mmioPlicBase  = 0x0C000000
	mmioClintBase = 0x02000000

	// Page table pool sizing
	ptPoolPages       = 64   // 64 pages (256KB) for page tables
	heapPagePoolPages = 256  // 256 pages (1MB) for demand paging
	ptExtraPages      = 64   // Extra PT pages
)

// KernelVM holds kernel virtual memory configuration
type KernelVM struct {
	SAPTROOTL2Phys uint64 // L2 (root) physical address for SATP

	G0StackTopVA  uint64 // Top of g0 stack (VA)
	G0StackPhys   uint64 // Physical address of g0 stack pages
	ExcStackTopVA uint64 // Top of exception stack (VA)
	ExcStackPhys  uint64 // Physical address of exception stack pages

	// Page pool for demand paging
	HeapPagePoolBase uint64 // Physical start
	HeapPagePoolEnd  uint64 // Physical end

	// Unified pool (remaining physical memory)
	UnifiedPoolStart uint64
	UnifiedPoolEnd   uint64
}

// demandPagePool is global state for the assembly fault handler
var demandPagePool struct {
	current   uint64 // Next available page
	end       uint64 // End of pool
	ptCurrent uint64 // Next available PT page
	ptEnd     uint64 // End of PT pool
	pgCount   uint64 // Page fault counter
	svcCount  uint64 // SVC counter
}

// ptPageAllocator tracks page table allocation
type ptPageAllocator struct {
	base   uint64
	offset uint64
	total  uint64
}

var ptAlloc ptPageAllocator

// allocatePTPage allocates a single page from PT pool
func allocatePTPage() uint64 {
	if ptAlloc.offset >= ptAlloc.total {
		printString("FATAL: PT pool exhausted\r\n")
		return 0
	}
	page := ptAlloc.base + ptAlloc.offset
	ptAlloc.offset += PageSize
	plat.ZeroMemory(page, PageSize)
	return page
}

// kernelPageTablePhys returns the kernel page table root (SATP root)
func kernelPageTablePhys(vm *KernelVM) uint64 {
	return vm.SAPTROOTL2Phys
}

// PrepareKernelVM creates Sv39 page tables with linear map and stacks
func PrepareKernelVM(hw *HardwareInfo, kernel *LoadedKernel) (*KernelVM, error) {
	vm := dNew[KernelVM]()
	if vm == nil {
		return nil, &errVMAllocFailed
	}

	// Allocate page table pool
	ptPages, err := allocatePhysPages(ptPoolPages)
	if err != nil {
		return nil, &errPTPoolAllocFailed
	}
	ptAlloc.base = ptPages
	ptAlloc.offset = 0
	ptAlloc.total = ptPoolPages * PageSize
	plat.ZeroMemory(ptPages, ptPoolPages*PageSize)

	printString("PT pool at ")
	printHex(ptPages)
	printString("\r\n")

	// Allocate L2 root table
	l2Root := allocatePTPage()
	if l2Root == 0 {
		return nil, &errPTAllocFailed
	}
	vm.SAPTROOTL2Phys = l2Root

	// Create linear map: PA → VA (PA + KernelVAOffset)
	// Map all physical RAM in 1GB chunks using L2 leaf PTEs
	for pa := uint64(hw.RAMBase); pa < hw.RAMBase+hw.RAMSize; pa += Page1GBSize {
		va := pa + KernelVAOffset
		if err := mapPage1GB(l2Root, va, pa, PTE_LEAF_RWX); err != nil {
			return nil, err
		}
	}

	// Allocate and map g0 stack
	g0StackPages := uint64((KernelG0StackSize + PageSize - 1) / PageSize)
	g0StackPhys, err := allocatePhysPages(g0StackPages)
	if err != nil {
		return nil, &errStackAllocFailed
	}
	vm.G0StackPhys = g0StackPhys
	vm.G0StackTopVA = KernelG0StackTop
	plat.ZeroMemory(g0StackPhys, g0StackPages*PageSize)

	if err := mapRange(l2Root, KernelG0StackBottom, g0StackPhys, KernelG0StackSize, PTE_LEAF_RW); err != nil {
		return nil, err
	}

	// Allocate and map exception stack
	excStackPages := uint64((KernelExcStackSize + PageSize - 1) / PageSize)
	excStackPhys, err := allocatePhysPages(excStackPages)
	if err != nil {
		return nil, &errStackAllocFailed
	}
	vm.ExcStackPhys = excStackPhys
	vm.ExcStackTopVA = KernelExcStackTop
	plat.ZeroMemory(excStackPhys, excStackPages*PageSize)

	if err := mapRange(l2Root, KernelExcStackBottom, excStackPhys, KernelExcStackSize, PTE_LEAF_RW); err != nil {
		return nil, err
	}

	// Allocate heap page pool
	heapPoolPhys, err := allocatePhysPages(heapPagePoolPages)
	if err != nil {
		return nil, &errHeapPoolAllocFailed
	}
	vm.HeapPagePoolBase = heapPoolPhys
	vm.HeapPagePoolEnd = heapPoolPhys + heapPagePoolPages*PageSize

	// Initialize global demand page pool for assembly handler
	demandPagePool.current = vm.HeapPagePoolBase
	demandPagePool.end = vm.HeapPagePoolEnd
	demandPagePool.ptCurrent = ptAlloc.base + ptAlloc.offset
	demandPagePool.ptEnd = ptAlloc.base + ptAlloc.total

	// Map MMIO regions as device memory
	mapMMIO := func(pa, size uint64) {
		va := pa + KernelVAOffset
		// Use PTE_LEAF_RW for MMIO (no execute)
		_ = mapRange(l2Root, va, pa, size, PTE_LEAF_RW)
	}
	mapMMIO(mmioUartBase, 0x1000)
	mapMMIO(mmioPlicBase, 0x4000000)
	mapMMIO(mmioClintBase, 0x10000)

	printString("Kernel VM prepared (Sv39 SATP root at ")
	printHex(vm.SAPTROOTL2Phys)
	printString(")\r\n")

	return vm, nil
}

// mapPage1GB creates a 1GB L2 leaf PTE
func mapPage1GB(l2Root, va, pa, flags uint64) error {
	l2 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l2Root)))
	idx := l2Index(va)
	l2[idx] = makePTE(pa, flags)
	return nil
}

// mapRange maps a range of physical pages to virtual addresses
func mapRange(l2Root, va, pa, size uint64, flags uint64) error {
	for offset := uint64(0); offset < size; offset += PageSize {
		if err := mapPage4KB(l2Root, va+offset, pa+offset, flags); err != nil {
			return err
		}
	}
	return nil
}

// mapPage4KB maps a 4KB page (requires walking to L0)
func mapPage4KB(l2Root, va, pa, flags uint64) error {
	l2 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l2Root)))
	l2Idx := l2Index(va)

	// Get or create L1 table
	var l1Phys uint64
	if l2[l2Idx]&PTE_V == 0 {
		l1Phys = allocatePTPage()
		if l1Phys == 0 {
			return &errPTAllocFailed
		}
		l2[l2Idx] = makePTE(l1Phys, PTE_BRANCH)
	} else {
		l1Phys = (l2[l2Idx] >> 10) << 12
	}

	l1 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l1Phys)))
	l1Idx := l1Index(va)

	// Get or create L0 table
	var l0Phys uint64
	if l1[l1Idx]&PTE_V == 0 {
		l0Phys = allocatePTPage()
		if l0Phys == 0 {
			return &errPTAllocFailed
		}
		l1[l1Idx] = makePTE(l0Phys, PTE_BRANCH)
	} else {
		l0Phys = (l1[l1Idx] >> 10) << 12
	}

	l0 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l0Phys)))
	l0Idx := l0Index(va)

	// Create leaf PTE
	l0[l0Idx] = makePTE(pa, flags)
	return nil
}

// InstallFaultHandler installs diplomat's exception handler (stub for compatibility)
func InstallFaultHandler(vm *KernelVM) error {
	// RISC-V installs STVEC in jumpToKmazarinWithStack
	return nil
}

// Pre-allocated errors
var (
	errVMAllocFailed       = blockDevError{"kernelvm: allocation failed"}
	errPTPoolAllocFailed   = blockDevError{"kernelvm: PT pool allocation failed"}
	errPTAllocFailed       = blockDevError{"kernelvm: PT page allocation failed"}
	errStackAllocFailed    = blockDevError{"kernelvm: stack allocation failed"}
	errHeapPoolAllocFailed = blockDevError{"kernelvm: heap pool allocation failed"}
)

// getDiplomatExceptionHandlerAddr returns exception handler address
// Implemented in exc_vectors_riscv64.s
func getDiplomatExceptionHandlerAddr() uint64

// setSTVEC sets the STVEC CSR
// Implemented in exc_vectors_riscv64.s
func setSTVEC(addr uint64)

// updateIDTWithKmazarinISRs is a no-op on RISC-V.
// RISC-V uses STVEC which is installed at jump time with kmazarin's
// exception handler address. No separate IDT update needed.
func updateIDTWithKmazarinISRs(kernel *LoadedKernel, relocDelta uint64) {}
