//go:build amd64

// diplomat/main/kernelvm_amd64.go - PML4 page tables, linear map, stacks, and page pool
//
// Creates the same environment for kmazarin as the ARM64 version:
//   - Full linear map of physical RAM (PA + KernelVAOffset → VA)
//   - g0 + exception stacks mapped at cardinal-compatible VAs
//   - Page pool for demand paging during early kmazarin init
//   - MMIO device mappings for LAPIC/IOAPIC
//
// x86_64 uses a single PML4 (CR3) for both low and high canonical addresses.
// UEFI provides the PML4 with identity-mapped low memory; we add high-memory
// entries for the kernel linear map, stacks, and heap.

package main

import (
	"unsafe"
)

// KernelVM constants matching shared/constants/addresses.go
const (
	KernelVAOffset       = 0xFFFFFFFF00000000
	KernelStacksVirtBase = KernelVAOffset + 0x43E00000 // 0xFFFFFFFF43E00000
	KernelG0StackSize    = 0x8000                      // 32KB
	KernelExcStackSize   = 0x4000                      // 16KB
	KernelG0StackBottom  = KernelStacksVirtBase
	KernelG0StackTop     = KernelG0StackBottom + KernelG0StackSize
	KernelExcStackBottom = KernelG0StackTop
	KernelExcStackTop    = KernelExcStackBottom + KernelExcStackSize

	// Heap VA range (same as ARM64)
	// x86_64 heap must be in canonical address range (bit 47 must match bits 48-63).
	// ARM64 uses 0xFFFF000100000000 (TTBR1 space) but that's non-canonical on x86_64
	// with 48-bit paging. Use the negative canonical half instead.
	KernelHeapStart = 0xFFFF800100000000
	KernelHeapEnd   = 0xFFFF900000000000

	// MMIO physical addresses (x86_64 QEMU q35)
	mmioLAPICBase  = 0xFEE00000
	mmioLAPICSize  = 0x00001000
	mmioIOAPICBase = 0xFEC00000
	mmioIOAPICSize = 0x00001000

	// Page table pool sizing
	ptPoolPages       = 64    // 64 pages (256KB) for page table hierarchy
	heapPagePoolPages = 4096  // 4096 pages (16MB) for demand paging
	ptExtraPages      = 256   // Extra PT pages for demand paging handler
)

// KernelVM holds the kernel virtual memory configuration.
type KernelVM struct {
	PML4Phys      uint64 // PML4 physical address (for CR3)
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

// demandPagePool is global state for the assembly page fault handler.
// Layout must match the offsets used in exc_vectors_amd64.s.
var demandPagePool struct {
	current   uint64 // +0:  Next available page (bump pointer)
	end       uint64 // +8:  End of page pool
	ptCurrent uint64 // +16: Next available PT page
	ptEnd     uint64 // +24: End of PT pool
	pml4Phys  uint64 // +32: PML4 physical address (for page table walks)
	pgCount   uint64 // +40: Page fault counter
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

// PrepareKernelVM creates page tables with linear map, stacks, and page pool.
// On x86_64, we graft kernel mappings into the existing UEFI PML4.
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

	printString("Heap pool: ")
	printHex(heapPoolPhys)
	printString("-")
	printHex(vm.HeapPagePoolEnd)
	printString("\r\n")

	// Step 4: Compute unified pool (remaining RAM for kmazarin's allocator)
	unifiedPoolPages := uint64(65536) // 256MB
	unifiedPhys, err := allocatePhysPages(unifiedPoolPages)
	if err != nil {
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

	// Step 5: Get current UEFI PML4 from CR3
	currentCR3 := plat.ReadCR3()
	pml4Phys := currentCR3 & PTE_ADDR_MASK
	vm.PML4Phys = pml4Phys

	// Disable write protection for ALL mapping operations.
	// UEFI write-protects its page table pages (PML4, PDPT, PD).
	// We need WP disabled for creating new entries, splitting 2MB pages, etc.
	plat.DisableWriteProtect()

	// Step 6: Map stacks with 4KB pages
	mapStacks(pml4Phys, vm)

	// Step 7: Map kmazarin code at its virtual addresses (4KB pages)
	mapKernelCode(pml4Phys, kernel)

	// Step 7b: Create linear map of physical RAM using 2MB pages
	createLinearMap(pml4Phys, hw.RAMBase, hw.RAMBase+hw.RAMSize)

	// Step 8: Map MMIO regions (LAPIC, IOAPIC)
	mapMMIO(pml4Phys)

	plat.EnableWriteProtect()

	// Flush TLB by reloading CR3
	plat.WriteCR3(currentCR3)

	printString("PML4 ready: ")
	printHex(pml4Phys)
	printString("\r\n")

	return vm, nil
}

// ensurePDPT ensures a PDPT exists at pml4[idx], creating one if needed.
// Returns the physical address of the PDPT.
// Caller must ensure CR0.WP is disabled (needed for UEFI-owned PML4).
func ensurePDPT(pml4 *[ENTRIES_PER_TABLE]uint64, idx uint64) uint64 {
	if pml4[idx]&PTE_PRESENT != 0 {
		return pml4[idx] & PTE_ADDR_MASK
	}
	pdptPhys := allocatePTPage()
	if pdptPhys == 0 {
		return 0
	}
	pml4[idx] = pdptPhys | PTE_PRESENT | PTE_WRITABLE
	return pdptPhys
}

// ensurePD ensures a PD exists at pdpt[idx], creating one if needed.
func ensurePD(pdpt *[ENTRIES_PER_TABLE]uint64, idx uint64) uint64 {
	if pdpt[idx]&PTE_PRESENT != 0 {
		return pdpt[idx] & PTE_ADDR_MASK
	}
	pdPhys := allocatePTPage()
	if pdPhys == 0 {
		return 0
	}
	pdpt[idx] = pdPhys | PTE_PRESENT | PTE_WRITABLE
	return pdPhys
}

// ensurePT ensures a PT exists at pd[idx], creating one if needed.
// If the PD entry is a 2MB large page, it is split into 512 4KB pages.
func ensurePT(pd *[ENTRIES_PER_TABLE]uint64, idx uint64) uint64 {
	entry := pd[idx]
	if entry&PTE_PRESENT != 0 {
		if entry&PTE_PS != 0 {
			// Split 2MB page into 512 4KB pages
			return splitLargePage(pd, idx)
		}
		return entry & PTE_ADDR_MASK
	}
	ptPhys := allocatePTPage()
	if ptPhys == 0 {
		return 0
	}
	pd[idx] = ptPhys | PTE_PRESENT | PTE_WRITABLE
	return ptPhys
}

// splitLargePage splits a 2MB PD entry into 512 4KB PT entries.
func splitLargePage(pd *[ENTRIES_PER_TABLE]uint64, idx uint64) uint64 {
	entry := pd[idx]
	blockAddr := entry & PTE_ADDR_MASK
	blockFlags := entry & ^uint64(PTE_ADDR_MASK) & ^uint64(PTE_PS) // preserve flags, clear PS

	ptPhys := allocatePTPage()
	if ptPhys == 0 {
		return 0
	}
	pt := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(ptPhys)))

	for i := uint64(0); i < ENTRIES_PER_TABLE; i++ {
		pa := blockAddr + i*PageSize
		pt[i] = pa | blockFlags
	}

	pd[idx] = ptPhys | PTE_PRESENT | PTE_WRITABLE
	return ptPhys
}

// mapPage4K maps a single 4KB page in the page tables.
func mapPage4K(pml4Phys, va, pa, flags uint64) {
	pml4 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(pml4Phys)))

	idx4 := pml4Index(va)
	idx3 := pdptIndex(va)
	idx2 := pdIndex(va)
	idx1 := ptIndex(va)

	pdptPhys := ensurePDPT(pml4, idx4)
	if pdptPhys == 0 {
		return
	}
	pdpt := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(pdptPhys)))

	pdPhys := ensurePD(pdpt, idx3)
	if pdPhys == 0 {
		return
	}
	pd := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(pdPhys)))

	ptPhys := ensurePT(pd, idx2)
	if ptPhys == 0 {
		return
	}
	pt := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(ptPhys)))

	pt[idx1] = pa | flags | PTE_PRESENT
}

// mapStacks maps g0 and exception stacks using 4KB page entries.
func mapStacks(pml4Phys uint64, vm *KernelVM) {
	flags := uint64(PTE_PRESENT | PTE_WRITABLE)

	// Map g0 stack pages
	g0Pages := KernelG0StackSize / PageSize
	for i := uint64(0); i < uint64(g0Pages); i++ {
		va := KernelG0StackBottom + i*PageSize
		pa := vm.G0StackPhys + i*PageSize
		mapPage4K(pml4Phys, va, pa, flags)
	}

	// Map exception stack pages
	excPages := KernelExcStackSize / PageSize
	for i := uint64(0); i < uint64(excPages); i++ {
		va := KernelExcStackBottom + i*PageSize
		pa := vm.ExcStackPhys + i*PageSize
		mapPage4K(pml4Phys, va, pa, flags)
	}
}

// linearMapMaxPA is the maximum physical address that can be linearly mapped.
// PA + KernelVAOffset must remain in upper canonical range.
// For KernelVAOffset = 0xFFFFFFFF00000000, max PA = 0x100000000 (4GB).
const linearMapMaxPA = uint64(0x100000000)

// createLinearMap creates a linear map of physical RAM using 2MB pages.
// Maps PA range [ramStart, ramEnd) to VA range [PA + KernelVAOffset).
// Caps at linearMapMaxPA to avoid 64-bit wraparound.
func createLinearMap(pml4Phys, ramStart, ramEnd uint64) {
	const blockSize2MB = 0x200000

	// Round start down and end up to 2MB boundary
	ramStart = ramStart &^ (blockSize2MB - 1)
	ramEnd = (ramEnd + blockSize2MB - 1) &^ (blockSize2MB - 1)

	// Cap at max PA to avoid VA wraparound
	if ramEnd > linearMapMaxPA {
		ramEnd = linearMapMaxPA
	}

	pml4 := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(pml4Phys)))

	blocksCreated := uint64(0)

	for pa := ramStart; pa < ramEnd; pa += blockSize2MB {
		va := pa + KernelVAOffset

		idx4 := pml4Index(va)
		idx3 := pdptIndex(va)
		idx2 := pdIndex(va)

		pdptPhys := ensurePDPT(pml4, idx4)
		if pdptPhys == 0 {
			printString("LinearMap: PDPT alloc failed\r\n")
			return
		}
		pdpt := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(pdptPhys)))

		pdPhys := ensurePD(pdpt, idx3)
		if pdPhys == 0 {
			printString("LinearMap: PD alloc failed\r\n")
			return
		}
		pd := (*[ENTRIES_PER_TABLE]uint64)(unsafe.Pointer(uintptr(pdPhys)))

		// Skip if entry is already valid (preserves 4KB page mappings)
		if pd[idx2]&PTE_PRESENT != 0 {
			continue
		}

		// Create 2MB page (PTE_PS = large page)
		pd[idx2] = pa | PTE_PRESENT | PTE_WRITABLE | PTE_PS
		blocksCreated++
	}

	printString("LinearMap: ")
	printHex(blocksCreated)
	printString(" 2MB pages\r\n")
}

// mapKernelCode maps kmazarin's code/data at its expected virtual addresses
// using 4KB pages.
//
// On x86_64, the kernel ELF's VA range (e.g. 0x437FF000-0x43B4F600) falls
// within the UEFI identity-map range. The UEFI identity map maps those VAs
// to the wrong physical addresses (VA == PA), but the kernel was loaded to
// a different physical address (e.g. PA 0x77E00000).
//
// We use 4KB pages (not 2MB) because the kernel VA's intra-2MB offset
// doesn't match the physical address's intra-2MB offset (LowestVirt is at
// offset 0x1FF000 within its 2MB page, but PhysBase is 2MB-aligned). 2MB
// pages require the VA and PA offsets to match.
//
// UEFI's existing 2MB PD entries for this VA range are split into 4KB PT
// entries by ensurePT, then the relevant PT entries are overwritten.
func mapKernelCode(pml4Phys uint64, kernel *LoadedKernel) {
	virtStart := kernel.LowestVirt &^ (PageSize - 1)
	virtEnd := (kernel.HighestVirt + PageSize - 1) &^ (PageSize - 1)
	physStart := kernel.PhysBase

	flags := uint64(PTE_PRESENT | PTE_WRITABLE)

	pagesCreated := uint64(0)

	// Caller must ensure CR0.WP is disabled — we modify UEFI-owned
	// page tables and split UEFI's 2MB PD entries into 4KB pages.
	for offset := uint64(0); offset < virtEnd-virtStart; offset += PageSize {
		va := virtStart + offset
		pa := physStart + offset
		mapPage4K(pml4Phys, va, pa, flags)
		pagesCreated++
	}

	printString("KernelMap: ")
	printHex(pagesCreated)
	printString(" 4KB pages at VA ")
	printHex(virtStart)
	printString(" -> PA ")
	printHex(physStart)
	printString("\r\n")
}

// mapMMIO maps MMIO device regions for x86_64.
// LAPIC at 0xFEE00000 and I/O APIC at 0xFEC00000.
func mapMMIO(pml4Phys uint64) {
	// Map LAPIC and IOAPIC using 4KB pages (they're small regions)
	flags := uint64(PTE_PRESENT | PTE_WRITABLE | PTE_PCD) // cache-disable for MMIO

	// LAPIC: PA 0xFEE00000 → VA KernelVAOffset + 0xFEE00000
	mapPage4K(pml4Phys, KernelVAOffset+mmioLAPICBase, mmioLAPICBase, flags)

	// IOAPIC: PA 0xFEC00000 → VA KernelVAOffset + 0xFEC00000
	mapPage4K(pml4Phys, KernelVAOffset+mmioIOAPICBase, mmioIOAPICBase, flags)
}

// kernelPageTablePhys returns the kernel page table root physical address.
func kernelPageTablePhys(vm *KernelVM) uint64 {
	return vm.PML4Phys
}

// getDiplomatPFHandlerAddr returns the address of the page fault handler.
// Defined in exc_vectors_amd64.s.
func getDiplomatPFHandlerAddr() uint64

// getDiplomatSyscallHandlerAddr returns the address of the INT $0x80 stub handler.
// Defined in exc_vectors_amd64.s.
func getDiplomatSyscallHandlerAddr() uint64

// installIDT loads an IDT using LIDT.
// Defined in exc_vectors_amd64.s.
func installIDT(idtrAddr uint64)

// readCSSelector returns the current CS segment selector value.
// Defined in exc_vectors_amd64.s.
func readCSSelector() uint16

// disableInterrupts clears the IF flag (CLI).
// Defined in exc_vectors_amd64.s.
func disableInterrupts()

// idtTable is the Interrupt Descriptor Table for diplomat (256 entries, 4096 bytes).
var idtTable [256 * 16]byte

// idtrDescriptor is the 10-byte IDTR structure: [limit:16][base:64].
var idtrDescriptor [10]byte

// InstallFaultHandler builds a minimal IDT with a #PF handler for demand paging.
// This allows kmazarin's Go runtime to generate page faults during init for
// heap allocations, which are handled by mapping pages from the pre-allocated pool.
func InstallFaultHandler(vm *KernelVM) error {
	// Set up global state for the assembly fault handler
	demandPagePool.current = vm.HeapPagePoolBase
	demandPagePool.end = vm.HeapPagePoolEnd
	// PT pages for page table allocation during demand paging
	demandPagePool.ptCurrent = vm.HeapPagePoolEnd
	demandPagePool.ptEnd = vm.HeapPagePoolEnd + ptExtraPages*PageSize
	demandPagePool.pml4Phys = vm.PML4Phys

	// Zero the IDT
	for i := range idtTable {
		idtTable[i] = 0
	}

	// Get the page fault handler address and current CS selector
	handlerAddr := getDiplomatPFHandlerAddr()
	cs := readCSSelector()

	// Set up IDT entry for vector 14 (#PF)
	setDiplomatIDTEntry(14, handlerAddr, cs)

	// Set up IDT entry for vector 128 (INT $0x80) — syscall stub
	// During Go runtime init, Syscall6 uses INT $0x80 (via overlay).
	// This stub returns 0 for all syscalls (prctl, epoll_wait, etc.).
	syscallAddr := getDiplomatSyscallHandlerAddr()
	setDiplomatIDTEntry(128, syscallAddr, cs)

	// Build IDTR descriptor: [limit:16][base:64]
	limit := uint16(len(idtTable) - 1)
	base := uint64(uintptr(unsafe.Pointer(&idtTable[0])))

	idtrDescriptor[0] = byte(limit)
	idtrDescriptor[1] = byte(limit >> 8)
	idtrDescriptor[2] = byte(base)
	idtrDescriptor[3] = byte(base >> 8)
	idtrDescriptor[4] = byte(base >> 16)
	idtrDescriptor[5] = byte(base >> 24)
	idtrDescriptor[6] = byte(base >> 32)
	idtrDescriptor[7] = byte(base >> 40)
	idtrDescriptor[8] = byte(base >> 48)
	idtrDescriptor[9] = byte(base >> 56)

	printString("Fault handler: IDT at ")
	printHex(base)
	printString(" pool=")
	printHex(vm.HeapPagePoolBase)
	printString("-")
	printHex(vm.HeapPagePoolEnd)
	printString("\r\n")

	// NOTE: We do NOT install the IDT here. UEFI has timer interrupts active
	// and our IDT only handles #PF. If we LIDT now, any subsequent UEFI call
	// (printString, BuildStartupEnv) may re-enable interrupts (STI internally),
	// and the UEFI timer would hit a non-present IDT entry → triple fault.
	//
	// The IDT is installed in jumpToKmazarinWithStack, right after CLI,
	// just before jumping to kmazarin.

	return nil
}


// setDiplomatIDTEntry populates a single IDT entry with the given CS selector.
func setDiplomatIDTEntry(vector int, handler uint64, cs uint16) {
	offset := vector * 16

	// offset[15:0]
	idtTable[offset+0] = byte(handler)
	idtTable[offset+1] = byte(handler >> 8)

	// segment selector (from current UEFI GDT)
	idtTable[offset+2] = byte(cs)
	idtTable[offset+3] = byte(cs >> 8)

	// IST = 0
	idtTable[offset+4] = 0x00

	// type/attributes = 0x8E (present, DPL=0, 64-bit interrupt gate)
	idtTable[offset+5] = 0x8E

	// offset[31:16]
	idtTable[offset+6] = byte(handler >> 16)
	idtTable[offset+7] = byte(handler >> 24)

	// offset[63:32]
	idtTable[offset+8] = byte(handler >> 32)
	idtTable[offset+9] = byte(handler >> 40)
	idtTable[offset+10] = byte(handler >> 48)
	idtTable[offset+11] = byte(handler >> 56)

	// reserved
	idtTable[offset+12] = 0
	idtTable[offset+13] = 0
	idtTable[offset+14] = 0
	idtTable[offset+15] = 0
}

// updateIDTWithKmazarinISRs replaces diplomat's stub syscall handler with
// kmazarin's real ISR entry points. This mirrors the ARM64 approach where
// cardinal installs kmazarin's VBAR before jumping to kmazarin, so all
// syscalls during Go runtime init go through kmazarin's real dispatch.
//
// IDT[14] (#PF) is kept as diplomat's assembly demand-paging handler because
// kmazarin's page fault handler calls Go functions that may not be ready
// during very early boot. IDT[128] (INT $0x80) is switched to kmazarin's
// isr128 so clone/futex/sched_yield work through real dispatch.
func updateIDTWithKmazarinISRs(kernel *LoadedKernel, relocDelta uint64) {
	cs := readCSSelector()
	printString("IDT CS selector: ")
	printHex(uint64(cs))
	printString("\r\n")

	// Resolve and install kmazarin's syscall handler (critical for clone/futex)
	isr128Addr := findKernelSymbol(kernel, "main.isr128")
	if isr128Addr != 0 {
		isr128Addr += relocDelta
		setDiplomatIDTEntry(128, isr128Addr, cs)
		printString("IDT[128]: kmazarin isr128 = ")
		printHex(isr128Addr)
		printString("\r\n")
	} else {
		printString("WARN: main.isr128 not found, using diplomat stub\r\n")
	}

	// Install timer IRQ handler (needed once timer is enabled)
	isr48Addr := findKernelSymbol(kernel, "main.isr48")
	if isr48Addr != 0 {
		isr48Addr += relocDelta
		setDiplomatIDTEntry(48, isr48Addr, cs)
	}

	// Install spurious IRQ handler
	isr255Addr := findKernelSymbol(kernel, "main.isr255")
	if isr255Addr != 0 {
		isr255Addr += relocDelta
		setDiplomatIDTEntry(255, isr255Addr, cs)
	}
}

// Pre-allocated errors
var (
	errVMAllocFailed       = blockDevError{"kernelvm: allocation failed"}
	errPTPoolAllocFailed   = blockDevError{"kernelvm: PT pool allocation failed"}
	errStackAllocFailed    = blockDevError{"kernelvm: stack allocation failed"}
	errHeapPoolAllocFailed = blockDevError{"kernelvm: heap pool allocation failed"}
)
