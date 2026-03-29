//go:build arm64

// diplomat/main/kernelvm_arm64.go - TTBR1 page tables, linear map, stacks, and page pool
//
// Creates the same environment cardinal provides to kmazarin:
//   - Full linear map of physical RAM in TTBR1 (PA + KernelVAOffset → VA)
//   - g0 + exception stacks mapped at cardinal-compatible VAs
//   - Page pool for demand paging during early kmazarin init
//   - MMIO device mappings with device memory attributes

package main

import (
	"unsafe"
)

// KernelVM constants matching cardinal/constants/layout.go
const (
	KernelVAOffset       = 0xFFFFFFFF00000000
	KernelStacksVirtBase = KernelVAOffset + 0x44100000 // 0xFFFFFFFF44100000
	KernelG0StackSize    = 0x8000                      // 32KB
	KernelExcStackSize   = 0x20000                     // 128KB
	KernelG0StackBottom  = KernelStacksVirtBase
	KernelG0StackTop     = KernelG0StackBottom + KernelG0StackSize
	KernelExcStackBottom = KernelG0StackTop
	KernelExcStackTop    = KernelExcStackBottom + KernelExcStackSize

	// Heap VA range (same as cardinal)
	KernelHeapStart = 0xFFFF000100000000
	KernelHeapEnd   = 0xFFFF100000000000

	// MMIO physical addresses (QEMU virt machine)
	mmioGicBase  = 0x08000000
	mmioGicSize  = 0x00020000
	mmioUartBase = 0x09000000
	mmioUartSize = 0x00010000
	mmioRtcBase  = 0x09010000
	mmioFwcfgBase = 0x09020000
	mmioPciStart = 0x10000000
	mmioPciEnd   = 0x40000000

	// Page table pool sizing
	ptPoolPages       = 64   // 64 pages (256KB) for page table hierarchy
	heapPagePoolPages = 256  // 256 pages (1MB) for diplomat demand-paging (mostly unused with kmazarin VBAR)
	ptExtraPages      = 64   // Extra PT pages for demand paging handler
)

// KernelVM holds the kernel virtual memory configuration.
type KernelVM struct {
	TTBR1L0Phys   uint64 // L0 physical address for TTBR1
	G0StackTopVA  uint64 // Top of g0 stack (VA)
	G0StackPhys   uint64 // Physical address of g0 stack pages
	ExcStackTopVA uint64 // Top of exception stack (VA)
	ExcStackPhys  uint64 // Physical address of exception stack pages

	// Page pool for demand paging
	HeapPagePoolBase uint64 // Physical start of page pool
	HeapPagePoolEnd  uint64 // Physical end of page pool

	// Unified pool (remaining physical memory for kmazarin's allocator)
	UnifiedPoolStart uint64
	UnifiedPoolEnd   uint64
}

// demandPagePool is global state for the assembly fault handler.
// Must be in diplomat's BSS (accessible via TTBR0 identity map).
var demandPagePool struct {
	current   uint64 // Next available page (bump pointer)
	end       uint64 // End of pool
	ptCurrent uint64 // Next available PT page
	ptEnd     uint64 // End of PT pool
	pgCount   uint64 // Page fault counter (for heartbeat)
	svcCount  uint64 // SVC counter (for heartbeat)
}

// ptPageAllocator tracks page table page allocation from UEFI pool.
type ptPageAllocator struct {
	base   uint64
	offset uint64
	total  uint64
}

var ptAlloc ptPageAllocator

// allocatePTPage allocates a single page from the PT pool and zeroes it.
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

// kernelPageTablePhys returns the kernel page table root physical address.
// On ARM64, this is the TTBR1 L0 table.
func kernelPageTablePhys(vm *KernelVM) uint64 {
	return vm.TTBR1L0Phys
}

// PrepareKernelVM creates TTBR1 page tables with linear map, stacks, and page pool.
func PrepareKernelVM(hw *HardwareInfo, kernel *LoadedKernel) (*KernelVM, error) {
	vm := dNew[KernelVM]()
	if vm == nil {
		return nil, &errVMAllocFailed
	}

	// Step 1: Allocate page table page pool from UEFI
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
	printString(" (")
	printHex(ptPoolPages)
	printString(" pages)\r\n")

	// Step 2: Allocate physical pages for stacks
	g0StackPages := KernelG0StackSize / PageSize  // 8 pages
	excStackPages := KernelExcStackSize / PageSize // 4 pages
	totalStackPages := uint64(g0StackPages + excStackPages)

	stackPhys, err := allocatePhysPages(totalStackPages)
	if err != nil {
		return nil, &errStackAllocFailed
	}
	plat.ZeroMemory(stackPhys, totalStackPages*PageSize)

	vm.G0StackPhys = stackPhys
	vm.ExcStackPhys = stackPhys + uint64(g0StackPages)*PageSize
	vm.G0StackTopVA = KernelG0StackTop
	vm.ExcStackTopVA = KernelExcStackTop

	printString("Stacks: g0=")
	printHex(stackPhys)
	printString(" exc=")
	printHex(vm.ExcStackPhys)
	printString("\r\n")

	// Step 3: Allocate demand paging page pool
	heapPoolPhys, err := allocatePhysPages(heapPagePoolPages + ptExtraPages)
	if err != nil {
		return nil, &errHeapPoolAllocFailed
	}
	plat.ZeroMemory(heapPoolPhys, (heapPagePoolPages+ptExtraPages)*PageSize)

	vm.HeapPagePoolBase = heapPoolPhys
	vm.HeapPagePoolEnd = heapPoolPhys + heapPagePoolPages*PageSize

	// Set up demand page pool globals for assembly handler
	demandPagePool.current = heapPoolPhys
	demandPagePool.end = heapPoolPhys + heapPagePoolPages*PageSize
	demandPagePool.ptCurrent = heapPoolPhys + heapPagePoolPages*PageSize
	demandPagePool.ptEnd = heapPoolPhys + (heapPagePoolPages+ptExtraPages)*PageSize

	printString("Heap pool: ")
	printHex(heapPoolPhys)
	printString("-")
	printHex(vm.HeapPagePoolEnd)
	printString("\r\n")

	// Step 4: Compute unified pool (remaining RAM after all our allocations)
	// This is the memory kmazarin's allocator will manage
	// Use RAM after our highest allocation as the unified pool
	unifiedPoolPages := uint64(65536) // 65536 pages (256MB) for kmazarin's unified allocator
	unifiedPhys, err := allocatePhysPages(unifiedPoolPages)
	if err != nil {
		// If we can't allocate more, use what we have
		printString("Unified pool alloc FAILED\r\n")
		vm.UnifiedPoolStart = 0
		vm.UnifiedPoolEnd = 0
	} else {
		vm.UnifiedPoolStart = unifiedPhys
		vm.UnifiedPoolEnd = unifiedPhys + unifiedPoolPages*PageSize
		printString("Unified pool: ")
		printHex(vm.UnifiedPoolStart)
		printString("-")
		printHex(vm.UnifiedPoolEnd)
		printString("\r\n")
	}

	// Step 5: Create L0 table for TTBR1
	l0Phys := allocatePTPage()
	if l0Phys == 0 {
		return nil, &errL0AllocFailed
	}
	vm.TTBR1L0Phys = l0Phys

	// Step 6: Map stacks with 4KB pages (before linear map)
	mapStacks(l0Phys, vm)

	// Step 7: Map kmazarin code at its virtual addresses FIRST
	// Must come before linear map because the linear map would create entries
	// at the same L2 indices pointing to the wrong physical addresses
	// (the linear map maps VA=PA+offset, but kmazarin is at a different PA)
	mapKernelCode(l0Phys, kernel)

	// Step 7b: Map MMIO regions with device memory attributes (nGnRnE)
	// Must come BEFORE the linear map so that createDiplomatLinearMap skips
	// MMIO entries (it preserves existing valid entries). Without this ordering,
	// MMIO regions would be mapped as Normal Write-Back cacheable by the linear
	// map, causing MMIO writes (e.g. VirtIO notify) to be buffered instead of
	// reaching the device immediately.
	mapMMIO(l0Phys)

	// Step 7c: Create linear map of physical RAM using 2MB blocks
	// Skips entries already set by mapKernelCode and mapMMIO above
	createDiplomatLinearMap(l0Phys, hw.RAMBase, hw.RAMBase+hw.RAMSize)

	// Step 9: Configure TCR_EL1 and write TTBR1
	configureUpperTranslation()
	writeTTBR1(l0Phys)
	tlbiVmalle1()
	dsb()
	isb()

	printString("TTBR1 ready: L0=")
	printHex(l0Phys)
	printString("\r\n")

	return vm, nil
}

// mapStacks maps g0 and exception stacks using 4KB page entries in TTBR1.
func mapStacks(l0Phys uint64, vm *KernelVM) {
	// Map g0 stack pages
	g0Pages := KernelG0StackSize / PageSize
	for i := uint64(0); i < uint64(g0Pages); i++ {
		va := KernelG0StackBottom + i*PageSize
		pa := vm.G0StackPhys + i*PageSize
		mapPage4K(l0Phys, va, pa, ATTR_NORMAL_RW)
	}

	// Map exception stack pages
	excPages := KernelExcStackSize / PageSize
	for i := uint64(0); i < uint64(excPages); i++ {
		va := KernelExcStackBottom + i*PageSize
		pa := vm.ExcStackPhys + i*PageSize
		mapPage4K(l0Phys, va, pa, ATTR_NORMAL_RW)
	}
}

// mapPage4K maps a single 4KB page in the TTBR1 page tables.
// Creates L1/L2/L3 tables as needed.
func mapPage4K(l0Phys, va, pa, attrs uint64) {
	l0 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l0Phys)))

	idx0 := l0Index(va)
	idx1 := l1Index(va)
	idx2 := l2Index(va)
	idx3 := l3Index(va)

	// Ensure L1 exists
	if l0[idx0]&0x3 == 0 {
		l1Phys := allocatePTPage()
		if l1Phys == 0 {
			return
		}
		l0[idx0] = l1Phys | DESC_TABLE
	}
	l1Phys := l0[idx0] & DESC_ADDR_MASK
	l1 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l1Phys)))

	// Ensure L2 exists
	if l1[idx1]&0x3 == 0 {
		l2Phys := allocatePTPage()
		if l2Phys == 0 {
			return
		}
		l1[idx1] = l2Phys | DESC_TABLE
	}
	l2Phys := l1[idx1] & DESC_ADDR_MASK
	l2 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l2Phys)))

	// Check if L2 entry is a block descriptor - if so, we need to split it
	if l2[idx2]&0x3 == DESC_BLOCK {
		// Split 2MB block into 512 4KB pages
		splitBlock(l2, idx2)
	}

	// Ensure L3 exists
	if l2[idx2]&0x3 == 0 {
		l3Phys := allocatePTPage()
		if l3Phys == 0 {
			return
		}
		l2[idx2] = l3Phys | DESC_TABLE
	}
	l3Phys := l2[idx2] & DESC_ADDR_MASK
	l3 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l3Phys)))

	// Set L3 page entry
	l3[idx3] = pa | attrs | DESC_PAGE
}

// splitBlock splits a 2MB block descriptor at L2[idx] into 512 4KB pages.
func splitBlock(l2 *[ENTRIES_PER_TABLE]uint64, idx uint64) {
	blockAddr := l2[idx] & DESC_ADDR_MASK
	blockAttrs := l2[idx] & ^uint64(DESC_ADDR_MASK) & ^uint64(0x3) // preserve attrs, clear type

	l3Phys := allocatePTPage()
	if l3Phys == 0 {
		return
	}
	l3 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l3Phys)))

	// Fill L3 with 512 page entries matching the original block
	for i := uint64(0); i < ENTRIES_PER_TABLE; i++ {
		pa := blockAddr + i*PageSize
		l3[i] = pa | blockAttrs | DESC_PAGE
	}

	// Replace L2 block with L3 table pointer
	l2[idx] = l3Phys | DESC_TABLE
}

// linearMapMaxPA is the maximum physical address that can be linearly mapped
// without wrapping in 64-bit arithmetic. PA + KernelVAOffset must remain in
// the upper canonical range (>= 0xFFFF000000000000).
// For KernelVAOffset = 0xFFFFFFFF00000000, max PA = 0x100000000 (4GB).
const linearMapMaxPA = uint64(0x100000000)

// createDiplomatLinearMap creates a linear map of physical RAM using 2MB blocks.
// Only maps PA < linearMapMaxPA to avoid 64-bit wraparound with KernelVAOffset.
// Skips L2 entries that are already valid (e.g., stack 4KB page mappings).
func createDiplomatLinearMap(l0Phys, ramStart, ramEnd uint64) {
	const blockSize2MB = 0x200000

	// Round start down and end up to 2MB boundary
	ramStart = ramStart &^ (blockSize2MB - 1)
	ramEnd = (ramEnd + blockSize2MB - 1) &^ (blockSize2MB - 1)

	// Cap at max PA to avoid VA wraparound
	if ramEnd > linearMapMaxPA {
		ramEnd = linearMapMaxPA
	}

	l0 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l0Phys)))

	blocksCreated := uint64(0)

	for pa := ramStart; pa < ramEnd; pa += blockSize2MB {
		va := pa + KernelVAOffset

		idx0 := l0Index(va)
		idx1 := l1Index(va)
		idx2 := l2Index(va)

		// Ensure L1 exists
		if l0[idx0]&0x3 == 0 {
			l1Phys := allocatePTPage()
			if l1Phys == 0 {
				printString("LinearMap: L1 alloc failed\r\n")
				return
			}
			l0[idx0] = l1Phys | DESC_TABLE
		}
		l1Phys := l0[idx0] & DESC_ADDR_MASK
		l1 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l1Phys)))

		// Ensure L2 exists
		if l1[idx1]&0x3 == 0 {
			l2Phys := allocatePTPage()
			if l2Phys == 0 {
				printString("LinearMap: L2 alloc failed\r\n")
				return
			}
			l1[idx1] = l2Phys | DESC_TABLE
		}
		l2Phys := l1[idx1] & DESC_ADDR_MASK
		l2 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l2Phys)))

		// Skip if entry is already valid (preserves 4KB page mappings for stacks)
		if l2[idx2]&0x1 != 0 {
			continue
		}

		// Create 2MB block descriptor
		l2[idx2] = pa | ATTR_NORMAL_RW | DESC_BLOCK
		blocksCreated++
	}

	printString("LinearMap: ")
	printHex(blocksCreated)
	printString(" 2MB blocks\r\n")
}

// mapKernelCode maps kmazarin's code/data at its expected virtual addresses.
// This is needed because kmazarin may be loaded at a physical address above 4GB
// by UEFI, which cannot be covered by the linear map (VA wraparound).
func mapKernelCode(l0Phys uint64, kernel *LoadedKernel) {
	const blockSize2MB = 0x200000

	virtStart := kernel.LowestVirt &^ (blockSize2MB - 1)
	virtEnd := (kernel.HighestVirt + blockSize2MB - 1) &^ (blockSize2MB - 1)
	physStart := kernel.PhysBase - (kernel.LowestVirt - virtStart)

	l0 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l0Phys)))

	blocksCreated := uint64(0)

	for offset := uint64(0); virtStart+offset < virtEnd; offset += blockSize2MB {
		va := virtStart + offset
		pa := physStart + offset

		idx0 := l0Index(va)
		idx1 := l1Index(va)
		idx2 := l2Index(va)

		// Ensure L1 exists
		if l0[idx0]&0x3 == 0 {
			l1Phys := allocatePTPage()
			if l1Phys == 0 {
				return
			}
			l0[idx0] = l1Phys | DESC_TABLE
		}
		l1Phys := l0[idx0] & DESC_ADDR_MASK
		l1 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l1Phys)))

		// Ensure L2 exists
		if l1[idx1]&0x3 == 0 {
			l2Phys := allocatePTPage()
			if l2Phys == 0 {
				return
			}
			l1[idx1] = l2Phys | DESC_TABLE
		}
		l2Phys := l1[idx1] & DESC_ADDR_MASK
		l2 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l2Phys)))

		// Skip if already mapped
		if l2[idx2]&0x1 != 0 {
			continue
		}

		l2[idx2] = pa | ATTR_NORMAL_RW | DESC_BLOCK
		blocksCreated++
	}

	printString("KernelMap: ")
	printHex(blocksCreated)
	printString(" 2MB blocks at VA ")
	printHex(virtStart)
	printString(" -> PA ")
	printHex(physStart)
	printString("\r\n")
}

// mapMMIO maps MMIO device regions with device memory attributes.
// Uses 2MB blocks where possible, 4KB pages for smaller regions.
func mapMMIO(l0Phys uint64) {
	// Map MMIO regions from 0x00000000 to 0x40000000 using 2MB blocks.
	// This covers the full device MMIO range below RAM (GIC, UART, RTC,
	// fw_cfg, and the entire PCI MMIO window 0x10000000-0x3EFFFFFF),
	// matching RISC-V diplomat's 1GB page coverage of 0x00-0x7F.
	// With full coverage, kmazarin's MapDeviceMMIO can be a no-op.
	const blockSize2MB = 0x200000
	mmioStart := uint64(0x00000000)
	mmioEnd := uint64(0x40000000)

	l0 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l0Phys)))

	for pa := mmioStart; pa < mmioEnd; pa += blockSize2MB {
		va := pa + KernelVAOffset

		idx0 := l0Index(va)
		idx1 := l1Index(va)
		idx2 := l2Index(va)

		// Ensure L1 exists
		if l0[idx0]&0x3 == 0 {
			l1Phys := allocatePTPage()
			if l1Phys == 0 {
				return
			}
			l0[idx0] = l1Phys | DESC_TABLE
		}
		l1Phys := l0[idx0] & DESC_ADDR_MASK
		l1 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l1Phys)))

		// Ensure L2 exists
		if l1[idx1]&0x3 == 0 {
			l2Phys := allocatePTPage()
			if l2Phys == 0 {
				return
			}
			l1[idx1] = l2Phys | DESC_TABLE
		}
		l2Phys := l1[idx1] & DESC_ADDR_MASK
		l2 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(l2Phys)))

		// Skip if already mapped
		if l2[idx2]&0x1 != 0 {
			continue
		}

		// Device memory: nGnRnE (ATTR_IDX_DEVICE), no execute
		l2[idx2] = pa | ATTR_DEVICE_RW | ATTR_XN | DESC_BLOCK
	}
}

// InstallFaultHandler sets up the demand paging fault handler.
// Allocates a properly 4KB-aligned page for the exception vector table
// (VBAR_EL1 requires 2KB alignment, and Go's linker doesn't guarantee it).
func InstallFaultHandler(vm *KernelVM) error {
	// Set up global state for the assembly fault handler
	demandPagePool.current = vm.HeapPagePoolBase
	demandPagePool.end = vm.HeapPagePoolEnd

	// Allocate a 4KB page for the vector table (UEFI guarantees 4KB alignment)
	vbarPage, err := allocatePhysPages(1)
	if err != nil {
		return &errVBARAllocFailed
	}
	plat.ZeroMemory(vbarPage, PageSize)

	// Build exception vector table with branch stubs to our handlers
	buildExceptionVectors(vbarPage)

	// Set VBAR_EL1 to the aligned page
	setVBAR(vbarPage)

	printString("Fault handler: VBAR=")
	printHex(vbarPage)
	printString(" pool=")
	printHex(vm.HeapPagePoolBase)
	printString("-")
	printHex(vm.HeapPagePoolEnd)
	printString("\r\n")

	return nil
}

// updateIDTWithKmazarinISRs is a no-op on ARM64.
// ARM64 uses VBAR_EL1 which is installed at jump time with kmazarin's
// ExceptionVectorTable address. No separate IDT update needed.
func updateIDTWithKmazarinISRs(kernel *LoadedKernel, relocDelta uint64) {}

// buildExceptionVectors fills a 4KB-aligned page with exception vector stubs.
// Entry 0 (offset 0x000): B diplomatSyncEL1t
// Entry 4 (offset 0x200): B diplomatSyncEL1h
// All other entries: WFI; B .-4 (halt loop)
func buildExceptionVectors(page uint64) {
	vec := (*[1024]uint32)(unsafe.Pointer(uintptr(page)))

	// Fill all 16 entries with WFI; B .-4 (halt loop)
	for entry := 0; entry < 16; entry++ {
		base := entry * 32 // 32 instructions per entry (128 bytes)
		vec[base] = 0xD503207F   // WFI
		vec[base+1] = 0x17FFFFFF // B .-4
	}

	// Entry 0 (offset 0x000): Sync from EL1t → branch to diplomatSyncEL1t
	el1tAddr := getDiplomatSyncEL1tAddr()
	offset0 := int64(el1tAddr) - int64(page)
	vec[0] = encodeBranch(offset0)

	// Entry 1 (offset 0x080): IRQ from EL1t → ERET (ignore stray interrupts)
	// UEFI timer interrupts may fire during kmazarin init; just return.
	vec[1*32] = 0xD69F03E0 // ERET

	// Entry 4 (offset 0x200): Sync from EL1h → branch to diplomatSyncEL1h
	el1hAddr := getDiplomatSyncEL1hAddr()
	offset4 := int64(el1hAddr) - int64(page+0x200)
	vec[0x200/4] = encodeBranch(offset4)

	// Entry 5 (offset 0x280): IRQ from EL1h → ERET (ignore stray interrupts)
	vec[5*32] = 0xD69F03E0 // ERET
}

// encodeBranch encodes an ARM64 unconditional branch (B) instruction.
// offset is the signed byte offset from the instruction to the target.
func encodeBranch(offset int64) uint32 {
	imm26 := (offset >> 2) & 0x03FFFFFF
	return 0x14000000 | uint32(imm26)
}

// getDiplomatSyncEL1tAddr returns the address of the EL1t sync handler
func getDiplomatSyncEL1tAddr() uint64

// getDiplomatSyncEL1hAddr returns the address of the EL1h sync handler
func getDiplomatSyncEL1hAddr() uint64

// setVBAR sets VBAR_EL1 to the given address
func setVBAR(addr uint64)

// Pre-allocated errors
var (
	errVMAllocFailed     = blockDevError{"kernelvm: allocation failed"}
	errPTPoolAllocFailed = blockDevError{"kernelvm: PT pool allocation failed"}
	errStackAllocFailed  = blockDevError{"kernelvm: stack allocation failed"}
	errHeapPoolAllocFailed = blockDevError{"kernelvm: heap pool allocation failed"}
	errL0AllocFailed     = blockDevError{"kernelvm: L0 allocation failed"}
	errVBARAllocFailed   = blockDevError{"kernelvm: VBAR page allocation failed"}
)
