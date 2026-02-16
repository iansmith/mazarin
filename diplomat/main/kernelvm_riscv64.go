//go:build riscv64

// diplomat/main/kernelvm_riscv64.go - Kernel virtual memory setup for RISC-V
//
// Creates Sv48 (4-level) kernel virtual memory environment:
//   - Linear map of physical RAM in high virtual addresses
//   - Kernel code mapped at ELF VAs
//   - Identity map for SATP transition trampoline
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
	KernelVAOffset = 0xFFFFFFFF00000000
	// Stacks must be in an L2 index NOT used by any 1GB leaf entry.
	// Used L2 indices in L3[0x1FF]: 0x1FC (MMIO 0x00), 0x1FD (MMIO 0x40),
	// 0x1FE (RAM 0x80), 0x1FF (RAM 0xC0).
	// We use L2[0x1FA] = VA 0xFFFFFFFE80000000 for stacks.
	KernelStacksVirtBase = 0xFFFFFFFE80000000
	KernelG0StackSize    = 0x8000                      // 32KB
	KernelExcStackSize   = 0x4000                      // 16KB
	KernelG0StackBottom  = KernelStacksVirtBase
	KernelG0StackTop     = KernelG0StackBottom + KernelG0StackSize
	KernelExcStackBottom = KernelG0StackTop
	KernelExcStackTop    = KernelExcStackBottom + KernelExcStackSize

	// Heap VA range — must match shared/constants/addresses_riscv64.go
	// Sv48 canonical kernel space: 0xFFFFFFC000000000 - 0xFFFFFFFFFFFFFFFF
	KernelHeapStart = 0xFFFFFFC100000000
	KernelHeapEnd   = 0xFFFFFFD000000000

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
	SAPTRootPhys uint64 // L3 (root) physical address for SATP (Sv48)

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

// kernelSAPTVal holds the SATP value for the kernel page tables.
// Set by PrepareKernelVMRISCV, read by jumpToKmazarinWithStack (assembly).
// Sv48: mode=9 (bits 63:60), ASID=0 (bits 59:44), PPN (bits 43:0).
var kernelSAPTVal uint64

// ptPageAllocator tracks page table allocation
type ptPageAllocator struct {
	base   uint64
	offset uint64
	total  uint64
}

var ptAlloc ptPageAllocator

// RISC-V direct boot bump allocator state
var riscvBumpAlloc struct {
	current uint64  // Next allocation address
	end     uint64  // End of available memory
}

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

// initBumpAllocatorRISCV initializes the bump allocator for RISC-V direct boot.
// Starts allocating after the kernel image in physical memory.
// The start address is derived from the actual loaded kernel size, not hardcoded.
func initBumpAllocatorRISCV(hw *HardwareInfo, kernel *LoadedKernel) {
	// Derive kernel end from actual loaded kernel info.
	// kernel.PhysBase is where the kernel was loaded (set by allocatePhysPagesNoError).
	// kernel.HighestVirt - kernel.LowestVirt gives the VA range size.
	// Round up to 2MB boundary to avoid conflicts with temp buffer used during loading.
	kernelSize := kernel.HighestVirt - kernel.LowestVirt
	kernelSize = (kernelSize + Page2MBSize - 1) &^ (Page2MBSize - 1)
	kernelEnd := kernel.PhysBase + kernelSize

	riscvBumpAlloc.current = kernelEnd
	riscvBumpAlloc.end = hw.RAMBase + hw.RAMSize

	printString("Bump allocator: ")
	printHex(riscvBumpAlloc.current)
	printString(" - ")
	printHex(riscvBumpAlloc.end)
	printString(" (")
	printHex((riscvBumpAlloc.end - riscvBumpAlloc.current) / (1024*1024))
	printString("MB available)\r\n")
}

// allocatePhysPagesRISCV allocates physical pages using bump allocator.
// For RISC-V direct boot (non-UEFI mode).
// NOTE: initBumpAllocatorRISCV must be called first to set up the allocator.
func allocatePhysPagesRISCV(pages uint64) uint64 {
	size := pages * PageSize
	if riscvBumpAlloc.current == 0 {
		printString("FATAL: Bump allocator not initialized\r\n")
		for {}
	}

	if riscvBumpAlloc.current+size > riscvBumpAlloc.end {
		printString("FATAL: Out of physical memory (need ")
		printHex(pages)
		printString(" pages, ")
		printHex((riscvBumpAlloc.end - riscvBumpAlloc.current) / PageSize)
		printString(" available)\r\n")
		for {}
	}

	addr := riscvBumpAlloc.current
	riscvBumpAlloc.current += size
	return addr
}

// kernelPageTablePhys returns the kernel page table root (Sv48 L3 root)
func kernelPageTablePhys(vm *KernelVM) uint64 {
	return vm.SAPTRootPhys
}

// PrepareKernelVM creates Sv48 page tables with linear map and stacks (UEFI path).
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

	// Allocate L3 root table (Sv48)
	l3Root := allocatePTPage()
	if l3Root == 0 {
		return nil, &errPTAllocFailed
	}
	vm.SAPTRootPhys = l3Root

	// Create linear map: PA → VA (PA + KernelVAOffset)
	// Map all physical RAM in 1GB chunks using L2 leaf PTEs
	for pa := uint64(hw.RAMBase); pa < hw.RAMBase+hw.RAMSize; pa += Page1GBSize {
		va := pa + KernelVAOffset
		if err := mapPage1GB(l3Root, va, pa, PTE_LEAF_RWX); err != nil {
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

	if err := mapRange(l3Root, KernelG0StackBottom, g0StackPhys, KernelG0StackSize, PTE_LEAF_RW); err != nil {
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

	if err := mapRange(l3Root, KernelExcStackBottom, excStackPhys, KernelExcStackSize, PTE_LEAF_RW); err != nil {
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
		_ = mapRange(l3Root, va, pa, size, PTE_LEAF_RW)
	}
	mapMMIO(mmioUartBase, 0x1000)
	mapMMIO(mmioPlicBase, 0x4000000)
	mapMMIO(mmioClintBase, 0x10000)

	printString("Kernel VM prepared (Sv48 SATP root at ")
	printHex(vm.SAPTRootPhys)
	printString(")\r\n")

	return vm, nil
}

// mapPage1GB creates a 1GB L2 leaf PTE, walking L3→L2
func mapPage1GB(l3Root, va, pa, flags uint64) error {
	l3 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l3Root)))
	l3Idx := l3Index(va)

	// Get or create L2 table
	var l2Phys uint64
	if l3[l3Idx]&PTE_V == 0 {
		l2Phys = allocatePTPage()
		if l2Phys == 0 {
			return &errPTAllocFailed
		}
		l3[l3Idx] = makePTE(l2Phys, PTE_BRANCH)
	} else {
		l2Phys = (l3[l3Idx] >> 10) << 12
	}

	l2 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l2Phys)))
	l2Idx := l2Index(va)
	l2[l2Idx] = makePTE(pa, flags)
	return nil
}

// mapRange maps a range of physical pages to virtual addresses
func mapRange(l3Root, va, pa, size uint64, flags uint64) error {
	pages := size / PageSize
	printString("  mapRange: ")
	printHex(pages)
	printString(" pages\r\n")
	for offset := uint64(0); offset < size; offset += PageSize {
		if offset == 0 || offset == PageSize {
			printString("  page ")
			printHex(offset / PageSize)
			printString(" VA=")
			printHex(va + offset)
			printString("\r\n")
		}
		if err := mapPage4KB(l3Root, va+offset, pa+offset, flags); err != nil {
			return err
		}
	}
	printString("  mapRange done\r\n")
	return nil
}

// mapPage4KB maps a 4KB page (walks L3→L2→L1→L0)
func mapPage4KB(l3Root, va, pa, flags uint64) error {
	l3 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l3Root)))
	l3Idx := l3Index(va)

	// Get or create L2 table
	var l2Phys uint64
	if l3[l3Idx]&PTE_V == 0 {
		l2Phys = allocatePTPage()
		if l2Phys == 0 {
			return &errPTAllocFailed
		}
		l3[l3Idx] = makePTE(l2Phys, PTE_BRANCH)
	} else {
		l2Phys = (l3[l3Idx] >> 10) << 12
	}

	l2 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l2Phys)))
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

// PrepareKernelVMRISCV creates Sv48 4-level page tables without UEFI allocations.
// Uses bump allocator for RISC-V direct boot mode.
func PrepareKernelVMRISCV(hw *HardwareInfo, kernel *LoadedKernel) (*KernelVM, error) {
	vm := dNew[KernelVM]()
	if vm == nil {
		printString("ERROR: Failed to allocate KernelVM\r\n")
		for {}
	}

	// Initialize bump allocator after the loaded kernel image
	initBumpAllocatorRISCV(hw, kernel)
	verifyCodePhys(kernel.PhysBase, "after bump init")

	// Allocate page table pool
	ptPages := allocatePhysPagesRISCV(ptPoolPages)
	ptAlloc.base = ptPages
	ptAlloc.offset = 0
	ptAlloc.total = ptPoolPages * PageSize

	printString("PT pool at ")
	printHex(ptPages)
	printString(", zeroing ")
	printHex(ptPoolPages * PageSize)
	printString(" bytes\r\n")
	plat.ZeroMemory(ptPages, ptPoolPages*PageSize)
	verifyCodePhys(kernel.PhysBase, "after PT zero")

	// Allocate L3 root table (Sv48)
	l3Root := allocatePTPage()
	if l3Root == 0 {
		printString("ERROR: Failed to allocate L3 root\r\n")
		for {}
	}
	vm.SAPTRootPhys = l3Root

	// --- Linear map: PA → VA (PA + KernelVAOffset) ---
	// Map all physical RAM in 1GB chunks using L2 leaf PTEs.
	// mapPage1GB walks L3→L2 to create the L2 leaf entries.
	for pa := uint64(hw.RAMBase); pa < hw.RAMBase+hw.RAMSize; pa += Page1GBSize {
		va := pa + KernelVAOffset
		if err := mapPage1GB(l3Root, va, pa, PTE_LEAF_RWX); err != nil {
			printString("ERROR: Failed to map 1GB page\r\n")
			for {}
		}
	}

	// --- Map MMIO regions via linear map ---
	// These are below hw.RAMBase so they need separate 1GB mappings.
	// Map 0x00000000-0x3FFFFFFF (UART, PLIC, CLINT) at high VA
	if err := mapPage1GB(l3Root, 0x00000000+KernelVAOffset, 0x00000000, PTE_LEAF_RW); err != nil {
		printString("ERROR: Failed to map MMIO 0x00\r\n")
		for {}
	}
	// Map 0x40000000-0x7FFFFFFF (PCI MMIO window) at high VA
	if err := mapPage1GB(l3Root, 0x40000000+KernelVAOffset, 0x40000000, PTE_LEAF_RW); err != nil {
		printString("ERROR: Failed to map MMIO 0x40\r\n")
		for {}
	}
	verifyCodePhys(kernel.PhysBase, "after linear+MMIO map")

	// --- Map kernel code at ELF VAs ---
	// Kmazarin ELF VAs (e.g., ~0xFFFFFFFF437F0000) are below the linear map base
	// (~0xFFFFFFFF80000000). These must be explicitly mapped.
	kernelSize := kernel.HighestVirt - kernel.LowestVirt
	kernelSize = (kernelSize + PageSize - 1) &^ (PageSize - 1) // round up
	printString("Mapping kernel code: VA ")
	printHex(kernel.LowestVirt)
	printString(" → PA ")
	printHex(kernel.PhysBase)
	printString(" size ")
	printHex(kernelSize)
	printString("\r\n")
	if err := mapRange(l3Root, kernel.LowestVirt, kernel.PhysBase, kernelSize, PTE_LEAF_RWX); err != nil {
		printString("ERROR: Failed to map kernel code\r\n")
		for {}
	}
	verifyCodePhys(kernel.PhysBase, "after kernel map")

	// --- Identity map for SATP transition trampoline ---
	// After writing the new SATP, diplomat's code at VA ~0x80200000 must still
	// be mapped. Add identity maps so the trampoline can continue executing.
	// IMPORTANT: Only map ranges that don't conflict with kernel code VAs.
	// Kmazarin VAs start at 0x43800000, which uses L2 index 1 (same as 0x40000000).
	// So we skip 0x40000000 to avoid overwriting kernel code L1 branch entries.
	if err := mapPage1GB(l3Root, 0x00000000, 0x00000000, PTE_LEAF_RW); err != nil {
		printString("ERROR: Failed identity map 0x00\r\n")
		for {}
	}
	// Skip 0x40000000: L2 index 1 conflicts with kernel code VA 0x43800000
	if err := mapPage1GB(l3Root, 0x80000000, 0x80000000, PTE_LEAF_RWX); err != nil {
		printString("ERROR: Failed identity map 0x80\r\n")
		for {}
	}
	verifyCodePhys(kernel.PhysBase, "after identity map")

	// Allocate and map g0 stack
	g0StackPages := uint64((KernelG0StackSize + PageSize - 1) / PageSize)
	g0StackPhys := allocatePhysPagesRISCV(g0StackPages)
	vm.G0StackPhys = g0StackPhys
	vm.G0StackTopVA = KernelG0StackTop

	printString("g0 stack at PA ")
	printHex(g0StackPhys)
	printString(", zeroing ")
	printHex(g0StackPages * PageSize)
	printString("\r\n")
	plat.ZeroMemory(g0StackPhys, g0StackPages*PageSize)
	verifyCodePhys(kernel.PhysBase, "after g0 stack zero")

	if err := mapRange(l3Root, KernelG0StackBottom, g0StackPhys, KernelG0StackSize, PTE_LEAF_RW); err != nil {
		printString("ERROR: Failed to map g0 stack\r\n")
		for {}
	}

	// Allocate and map exception stack
	excStackPages := uint64((KernelExcStackSize + PageSize - 1) / PageSize)
	excStackPhys := allocatePhysPagesRISCV(excStackPages)
	vm.ExcStackPhys = excStackPhys
	vm.ExcStackTopVA = KernelExcStackTop

	printString("exc stack at PA ")
	printHex(excStackPhys)
	printString(", zeroing ")
	printHex(excStackPages * PageSize)
	printString("\r\n")
	plat.ZeroMemory(excStackPhys, excStackPages*PageSize)
	verifyCodePhys(kernel.PhysBase, "after exc stack zero")

	if err := mapRange(l3Root, KernelExcStackBottom, excStackPhys, KernelExcStackSize, PTE_LEAF_RW); err != nil {
		printString("ERROR: Failed to map exception stack\r\n")
		for {}
	}

	// Allocate heap page pool
	heapPoolPhys := allocatePhysPagesRISCV(heapPagePoolPages)
	vm.HeapPagePoolBase = heapPoolPhys
	vm.HeapPagePoolEnd = heapPoolPhys + heapPagePoolPages*PageSize

	// Initialize global demand page pool for assembly handler
	demandPagePool.current = vm.HeapPagePoolBase
	demandPagePool.end = vm.HeapPagePoolEnd
	demandPagePool.ptCurrent = ptAlloc.base + ptAlloc.offset
	demandPagePool.ptEnd = ptAlloc.base + ptAlloc.total

	// Set unified pool: remaining bump allocator memory for kmazarin
	vm.UnifiedPoolStart = riscvBumpAlloc.current
	vm.UnifiedPoolEnd = riscvBumpAlloc.end

	// Set the kernel SATP value for jumpToKmazarinWithStack
	// Sv48 mode = 9 (bits 63:60), ASID = 0, PPN = root >> 12
	kernelSAPTVal = (uint64(9) << 60) | (vm.SAPTRootPhys >> 12)

	printString("Kernel VM prepared (Sv48 SATP root at ")
	printHex(vm.SAPTRootPhys)
	printString(", SATP val=")
	printHex(kernelSAPTVal)
	printString(")\r\n")
	printString("Unified pool: ")
	printHex(vm.UnifiedPoolStart)
	printString(" - ")
	printHex(vm.UnifiedPoolEnd)
	printString("\r\n")

	// DEBUG: Verify code integrity before jump to kmazarin
	// Check offset 0xb5a74 from physBase (VA 0x438b5a74 = handlePageFaultInternal.abi0)
	// Text segment is ~0xb6000 bytes. Check last 1KB for corruption.
	verifyCodePhys(kernel.PhysBase, "before jump")

	return vm, nil
}

// savedTextChecksum stores the XOR checksum of the last 1KB of the text segment,
// computed right after ELF loading. Used to detect corruption later.
var savedTextChecksum uint64
var savedTextChecksumStart uint64 // offset from physBase where checksum region starts
var savedTextChecksumLen uint64

// saveTextChecksum computes and saves an XOR checksum of the last 1KB of code starting
// at physBase. Must be called right after ELF segment copy, before any other operation.
func saveTextChecksum(physBase uint64, textFilesz uint64) {
	checkLen := uint64(1024)
	if textFilesz < checkLen {
		checkLen = textFilesz
	}
	startOff := textFilesz - checkLen
	savedTextChecksumStart = startOff
	savedTextChecksumLen = checkLen
	sum := uint64(0)
	for j := uint64(0); j < checkLen; j += 8 {
		val := *(*uint64)(unsafe.Pointer(uintptr(physBase + startOff + j)))
		sum ^= val
	}
	savedTextChecksum = sum
	printString("TEXT_CHECKSUM: saved=0x")
	printHex(sum)
	printString(" range=0x")
	printHex(startOff)
	printString("-0x")
	printHex(startOff + checkLen)
	printString("\r\n")
}

// verifyCodePhys compares the current XOR checksum of the text region against
// the saved checksum from right after ELF loading.
func verifyCodePhys(physBase uint64, label string) {
	printString("VERIFY_PHYS[")
	printString(label)
	printString("]: ")

	sum := uint64(0)
	for j := uint64(0); j < savedTextChecksumLen; j += 8 {
		val := *(*uint64)(unsafe.Pointer(uintptr(physBase + savedTextChecksumStart + j)))
		sum ^= val
	}
	if sum != savedTextChecksum {
		printString("CORRUPTED! got=0x")
		printHex(sum)
		printString(" want=0x")
		printHex(savedTextChecksum)
		// Find first mismatching 8-byte word
		for j := uint64(0); j < savedTextChecksumLen; j += 8 {
			val := *(*uint64)(unsafe.Pointer(uintptr(physBase + savedTextChecksumStart + j)))
			_ = val // We'd need to store original values to compare
		}
	} else {
		printString("OK")
	}
	printString("\r\n")
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
