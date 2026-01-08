package main

import (
	"unsafe"

	"cardinal/asm"
	"cardinal/constants"
)

// initMMU initializes the MMU with identity-mapped page tables
// This must be called before enabling the MMU
// Returns true on success, false on failure
//
// NOTE: Not nosplit - called from KernelMain which allows stack growth
func initMMU() bool {
	// Early breadcrumb - write directly to UART to avoid any function call issues
	uartBase := uintptr(0x09000000)
	*(*uint32)(unsafe.Pointer(uartBase)) = '1'

	// Initialize cache line size for optimized bzero
	initCacheLineSize()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'C' // Cache line init done

	// CRITICAL: Call assembly helpers directly instead of getLinkerSymbol()
	// because getLinkerSymbol() uses string comparisons that access .rodata
	// which isn't mapped yet when initMMU() is called!
	pageTableBase := asm.GetPageTablesStartAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = '2'
	pageTableEnd := asm.GetPageTablesEndAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = '3'

	// Calculate PAGE_TABLE_SIZE from the difference
	PAGE_TABLE_SIZE = pageTableEnd - pageTableBase
	*(*uint32)(unsafe.Pointer(uartBase)) = '4'

	// Allocate page table memory
	pageTableL0 = pageTableBase
	pageTableL1 = pageTableBase + TABLE_SIZE
	*(*uint32)(unsafe.Pointer(uartBase)) = '5'

	// Initialize the bump allocator after the pre-allocated L0 + L1 tables
	ptAlloc := getPageTableAllocator()
	*(*uint32)(unsafe.Pointer(uartBase)) = '6'
	ptAlloc.base = pageTableBase
	ptAlloc.offset = TABLE_SIZE * 2
	*(*uint32)(unsafe.Pointer(uartBase)) = '7'

	// Verify page table base is 4KB aligned
	if pageTableL0&0xFFF != 0 {
		// Fatal error - use direct UART (character by character to avoid .rodata access)
		uartPutcDirect('P')
		uartPutcDirect('T')
		uartPutcDirect('0')
		uartPutcDirect('!')
		uartPutcDirect(' ')
		uartPutHex64Direct(uint64(pageTableL0))
		uartPutcDirect('\r')
		uartPutcDirect('\n')
		for {
		} // Halt
	}

	// Zero out page tables (L0 + L1, each 4KB = 8KB total)
	*(*uint32)(unsafe.Pointer(uartBase)) = '8'
	uartPutHex64Direct(uint64(pageTableL0))
	*(*uint32)(unsafe.Pointer(uartBase)) = ':'
	bzero4K(unsafe.Pointer(pageTableL0), TABLE_SIZE)
	bzero4K(unsafe.Pointer(pageTableL1), TABLE_SIZE)
	*(*uint32)(unsafe.Pointer(uartBase)) = '9'

	// Set up L0 table to point to L1 table for identity mapping
	l0Entry0 := (*uint64)(unsafe.Pointer(pageTableL0 + 0*PTE_SIZE))
	*l0Entry0 = createTableEntry(pageTableL1)
	*(*uint32)(unsafe.Pointer(uartBase)) = 'a'

	// Map low memory regions with correct permissions
	// CRITICAL FIX: Data section MUST be read-write!
	//
	// Memory layout:
	//   0x000000-0x56D000: Boot code, text, rodata (read-only)
	//   0x56D000-0x632000: Data section (READ-WRITE - was causing permission faults!)
	//
	// CRITICAL: Call assembly helpers directly instead of getLinkerSymbol()
	// because getLinkerSymbol() uses string comparisons that access .rodata!
	*(*uint32)(unsafe.Pointer(uartBase)) = 'b'
	rodataStart := asm.GetRodataStartAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'c'
	rodataEnd := asm.GetRodataEndAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'd'
	if rodataStart != 0 && rodataEnd != 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '['
		mapRegionInitMMU(rodataStart, rodataEnd, rodataStart, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)
		*(*uint32)(unsafe.Pointer(uartBase)) = ']'
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = 'e'

	// Get section boundaries from linker symbols
	textStart := asm.GetTextStartAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'f'
	dataStart := asm.GetDataStartAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'g'
	endAddr := asm.GetEndAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'h'

	// DEBUG: Print text section range to verify it includes executing code
	uartPutsDirect("\r\n.text: 0x")
	uartPutHex64Direct(uint64(textStart))
	uartPutsDirect(" - 0x")
	uartPutHex64Direct(uint64(rodataStart))
	uartPutsDirect("\r\n")

	// Map everything before .rodata as read-only (boot code, text)
	// This includes:
	// - .text section (code)
	// - .vectors section (exception handlers)
	if textStart > 0 && rodataStart > 0 && textStart < rodataStart {
		uartPutsDirect(".text: 0x")
		uartPutHex64Direct(uint64(textStart))
		uartPutsDirect(" - 0x")
		uartPutHex64Direct(uint64(rodataStart))
		uartPutsDirect(" AP=0x")
		uartPutHex64Direct(PTE_AP_RO_EL1)
		uartPutsDirect(" EXEC_ALLOW=0x")
		uartPutHex64Direct(PTE_EXEC_ALLOW)
		uartPutsDirect(" EXEC_NEVER=0x")
		uartPutHex64Direct(PTE_EXEC_NEVER)
		uartPutsDirect("\r\n")
		mapRegionInitMMU(textStart, rodataStart, textStart, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_ALLOW)
	}

	// Map everything after .rodata up to data section as read-only
	if rodataEnd > 0 && dataStart > 0 && rodataEnd < dataStart {
		mapRegionInitMMU(rodataEnd, dataStart, rodataEnd, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)
	}

	// Map data+BSS sections as read-write
	// BSS starts where data ends, so we map from dataStart to __bss_end
	// This includes both .data (initialized) and .bss (uninitialized) sections
	bssEnd := asm.GetBssEndAddr()
	if dataStart > 0 && bssEnd > 0 {
		uartPutsDirect("Mapping .data+.bss: 0x")
		uartPutHex64Direct(uint64(dataStart))
		uartPutsDirect(" - 0x")
		uartPutHex64Direct(uint64(bssEnd))
		uartPutsDirect("\r\n")
		mapRegionInitMMU(dataStart, bssEnd, dataStart, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
		uartPutcDirect('i') // breadcrumb: .data+.bss mapped
	}

	// Map remainder after BSS up to end of kernel image as read-only (if there's anything)
	if bssEnd > 0 && endAddr > 0 && bssEnd < endAddr {
		mapRegionInitMMU(bssEnd, endAddr, bssEnd, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)
	}

	// Initialize MMIO devices array (fixed-size to avoid heap allocation)
	// CRITICAL: Call assembly functions directly instead of getLinkerSymbol()
	// getLinkerSymbol() uses string comparison which accesses misaligned .rodata
	// Before MMU is enabled, ARM64 requires strict alignment, so we must avoid
	// string access. The assembly functions return linker symbols without string ops.

	// Device 0: GIC (Generic Interrupt Controller)
	mmioDevices[0] = MMIODevice{
		start: asm.GetGicBase(),
		size:  asm.GetGicSize(),
		attr:  PTE_ATTR_DEVICE,
		ap:    PTE_AP_RW_EL1,
	}
	// Device 1: UART PL011
	mmioDevices[1] = MMIODevice{
		start: asm.GetUartBase(),
		size:  asm.GetUartSize(),
		attr:  PTE_ATTR_DEVICE,
		ap:    PTE_AP_RW_EL1,
	}
	// Device 2: QEMU fw_cfg
	mmioDevices[2] = MMIODevice{
		start: asm.GetFwcfgBase(),
		size:  asm.GetFwcfgSize(),
		attr:  PTE_ATTR_DEVICE,
		ap:    PTE_AP_RW_EL1,
	}
	// Device 3: bochs-display framebuffer
	// NOTE: bochs-display is 16MB - too large to map at boot
	// Will be mapped on-demand if/when display is used
	mmioDevices[3] = MMIODevice{
		start: asm.GetBochsDisplayBase(),
		size:  0, // Skip mapping for now - too slow
		attr:  PTE_ATTR_DEVICE,
		ap:    PTE_AP_RW_EL1,
	}
	mmioDeviceCount = 4
	uartPutcDirect('j') // breadcrumb: MMIO devices initialized

	// Map all MMIO devices
	for i := 0; i < mmioDeviceCount; i++ {
		dev := &mmioDevices[i]
		uartPutcDirect('0' + byte(i)) // which device
		uartPutHex64Direct(uint64(dev.start))
		uartPutcDirect(':')
		uartPutHex64Direct(uint64(dev.size))
		uartPutcDirect(' ')
		if dev.size > 0 {
			mapRegionInitMMU(dev.start, dev.start+dev.size, dev.start, dev.attr, dev.ap, PTE_EXEC_NEVER)
		}
		uartPutcDirect('!')
	}
	uartPutcDirect('k') // breadcrumb: MMIO devices mapped

	// Map DTB region (now that kernel starts at 0x40100000, no overlap!)
	dtbStart := asm.GetDtbBootAddr()
	dtbEnd := dtbStart + asm.GetDtbSize()
	mapRegionInitMMU(
		dtbStart, dtbEnd, dtbStart,
		PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER,  // DTB is read-only data
	)

	// Map PCI ECAM (lowmem only, minimal subset)
	// NOTE: Only map first 64KB (16 devices) to avoid slow page-by-page mapping
	// Full ECAM is 16MB (lowmem) + 256MB (highmem) = too slow
	ecamBase := uintptr(0x3F000000)
	ecamSize := uintptr(0x00010000) // Only 64KB for essential PCI access
	mapRegionInitMMU(ecamBase, ecamBase+ecamSize, ecamBase, PTE_ATTR_DEVICE, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	uartPutcDirect('l') // breadcrumb: PCI ECAM mapped

	// SKIP highmem ECAM (256MB) - will map on-demand if needed
	// SKIP PCI BAR region (240MB) - will map on-demand if needed

	// Get page table region boundaries from linker.ld
	// Note: pageTableEnd already declared earlier in function
	pageTableEnd = asm.GetPageTablesEndAddr()

	// Map kernel RAM (after cardinal image to page tables) - heap, stacks
	// CRITICAL: Start mapping AFTER our kernel image (endAddr) to avoid overlap
	// We map from end of cardinal to start of page tables
	ramStart := (endAddr + 0xFFF) &^ 0xFFF  // Round up to next page
	uartPutsDirect("endAddr=0x")
	uartPutHex64Direct(uint64(endAddr))
	uartPutsDirect(" ramStart=0x")
	uartPutHex64Direct(uint64(ramStart))
	uartPutsDirect(" pageTableBase=0x")
	uartPutHex64Direct(uint64(pageTableBase))
	uartPutsDirect("\r\n")

	// Pre-map heap region (cardinal kmalloc heap) as RW, non-executable
	// This maps from end of cardinal sections to start of page tables
	// NOTE: This is a large region (~13MB) - takes a while to map page by page
	if ramStart < pageTableBase {
		uartPutsDirect("Mapping RAM: 0x")
		uartPutHex64Direct(uint64(ramStart))
		uartPutsDirect(" - 0x")
		uartPutHex64Direct(uint64(pageTableBase))
		uartPutsDirect(" (RW)\r\n")
		mapRegionInitMMU(ramStart, pageTableBase, ramStart, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
		uartPutcDirect('m') // breadcrumb: RAM mapped
	}

	// PERFORMANCE: Map page table region as CACHEABLE
	// ARM64's hardware page table walker is cache-coherent - it will see cached updates.
	// Using Normal Cacheable memory dramatically improves performance by avoiding slow
	// memory accesses on every page table walk.
	// We use proper barriers (DSB ISH) after PTE modifications and TLB invalidation
	// to ensure coherency between CPU data cache and page table walker.
	// Page table region is from linker.ld: 0x41000000 - 0x41800000 (8MB)
	mapRegionInitMMU(pageTableBase, pageTableEnd, pageTableBase, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	uartPutcDirect('n') // breadcrumb: page tables mapped

	// NOTE: Exception vectors are now embedded in .text section at their final location
	// They no longer need a separate RAM mapping at 0x41100000 (which would conflict
	// with page tables). The vectors are mapped as part of the .text section mapping above.

	// NOTE: Physical frame pool starts after kmazarin executable and extends to PHYS_FRAME_END
	// The exact start address depends on kmazarin size (determined at runtime after ELF load)
	// This region will be identity-mapped as part of the general RAM mapping

	// NOTE: Bump allocator region (0x48000000 - 0xC8000000) is registered as a span
	// but NOT pre-mapped in page tables. Instead, pages are mapped on-demand via page
	// fault handler when kmazarin first accesses them. This saves ~512K page table entries!
	//
	// When kmazarin writes to unmapped address in bump region:
	//   1. Data abort exception occurs
	//   2. Exception handler checks if address is in registered span
	//   3. If yes: map the 4KB page on-demand (identity map VA=PA) and continue
	//   4. If no: real bug, panic with fault details

	// CRITICAL FIX: Map BOTH stacks - g0 stack and exception stack
	// Both operate at EL1 privilege level (no EL0 execution yet)
	//
	// Stack Architecture:
	// - g0 stack (SP_EL0): Used for normal kernel execution in EL1t mode (SPSel=0)
	// - Exception stack (SP_EL1): Used for exception handlers in EL1h mode (SPSel=1)
	//
	// Stack layout (grows downward from STACK_BASE):
	//   STACK_BASE (0x5F020000) ← SP_EL1 (exception stack top)
	//   ↓ exception stack (STACK_SIZE_EXCEPTION_EL1 = 128KB)
	//   0x5F000000 ← SP_EL0 (g0 stack top)
	//   ↓ g0 stack (STACK_SIZE_G0_CARDINAL = 64KB)
	//   0x5EFF0000 (g0 stack bottom)

	// Compute exception stack addresses from STACK_BASE
	exceptionStackTop := uintptr(STACK_BASE)
	exceptionStackBottom := exceptionStackTop - uintptr(STACK_SIZE_EXCEPTION_EL1)

	// Compute g0 stack addresses (grows downward from exception stack bottom)
	g0StackTop := exceptionStackBottom
	g0StackBottom := g0StackTop - uintptr(STACK_SIZE_G0_CARDINAL)

	// Map g0 stack (SP_EL0) - boot.s must set SP_EL0 to g0StackTop
	uartPutsDirect("Mapping g0 stack (SP_EL0): 0x")
	uartPutHex64Direct(uint64(g0StackBottom))
	uartPutsDirect(" - 0x")
	uartPutHex64Direct(uint64(g0StackTop))
	uartPutsDirect(" (RW)\r\n")
	mapRegionInitMMU(g0StackBottom, g0StackTop, g0StackBottom, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// Map exception stack (SP_EL1) - boot.s must set SP_EL1 to exceptionStackTop
	uartPutsDirect("Mapping exception stack (SP_EL1): 0x")
	uartPutHex64Direct(uint64(exceptionStackBottom))
	uartPutsDirect(" - 0x")
	uartPutHex64Direct(uint64(exceptionStackTop))
	uartPutsDirect(" (RW)\r\n")
	mapRegionInitMMU(exceptionStackBottom, exceptionStackTop, exceptionStackBottom, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// Initialize physical frame allocator
	uartPutcDirect('C')
	initPhysFrameAllocator()
	uartPutcDirect('a')

	// CRITICAL: Clean data cache for page tables before enabling MMU
	// The hardware page table walker reads PTEs from memory, not cache.
	// If PTEs are still in data cache (write-back), walker can't see them!
	uartPutcDirect('r')
	pageTableSize := pageTableEnd - pageTableBase
	for offset := uintptr(0); offset < pageTableSize; offset += 64 {
		asm.CleanDataCacheVA(pageTableBase + offset)
	}
	uartPutcDirect('d')

	// CRITICAL: Flush TLB to ensure all mappings are visible to CPU
	// Without this, the CPU may have stale TLB entries that don't reflect
	// the new mappings, causing exceptions when accessing newly mapped regions
	asm.Dsb()               // Ensure all page table writes and cache cleans complete
	uartPutcDirect('i')
	asm.InvalidateTlbAll()  // Invalidate all TLB entries
	uartPutcDirect('n')
	asm.Isb()               // Ensure TLB invalidation completes
	uartPutcDirect('a')

	// Initialize kernel page tables (TTBR1) for high-memory kernel
	uartPutcDirect('K')
	initKernelPageTables()
	uartPutcDirect('k')

	// Set up high-memory kernel stacks in TTBR1
	uartPutcDirect('T')
	setupKernelStacks()
	uartPutcDirect('t')

	// Map essential early MMIO devices to high memory in TTBR1
	uartPutcDirect('M')
	setupEarlyKernelMMIO()
	uartPutcDirect('m')

	// Set up demand paging infrastructure for kmazarin
	uartPutcDirect('E')
	setupKernelDemandPaging()
	uartPutcDirect('e')

	// DEBUG: Check if we ran out of page table space
	_, remaining := getPageTableAllocatorStats()
	uartPutcDirect('l')
	if remaining == 0 {
		uartPutcDirect('!')  // Out of page table space!
		for {} // Hang
	}

	return true
}

// preMapSchedInitPages pre-maps the 22 pages that would normally cause page faults
// during runtime.schedinit(). This is a debugging aid to isolate whether the
// problem is with the 22nd exception itself, or with something after 22 exceptions.
//
//go:nosplit
func preMapSchedInitPages() {
	// Get UART base for progress markers
	uartBase := asm.GetUartBase()

	// Print marker to show we're pre-mapping
	uartPutsDirect("\r\nPre-mapping 22 schedinit pages...")

	// These are the exact addresses that cause page faults during schedinit,
	// captured from a previous run. We'll pre-map them to avoid the faults.
	faultAddrs := [22]uintptr{
		0x00000000D1280000,
		0x00000000D1288000,
		0x00000000D3292000,
		0x0000000091300000,
		0x000000008D290000,
		0x000000008CA82000,
		0x000000008C980400,
		0x000000008C960080,
		0x00000000B1300000,
		0x00000000D3392000,
		0x00000000D3280000,
		0x00000000D3291008,
		0x00000000D33A2000,
		0x00000000D3290000,
		0x0000004000001F80,
		0x0000004000000000,
		0x0000004000003F80,
		0x0000004000002000,
		0x0000004000004000,
		0x000000400000FF80,
		0x000000400000E000,
		0x0000004000010000,
	}

	for i := 0; i < len(faultAddrs); i++ {
		addr := faultAddrs[i]

		// Align to page boundary
		pageAddr := addr &^ (PAGE_SIZE - 1)

		// Allocate physical frame
		physFrame := allocPhysFrame()
		if physFrame == 0 {
			uartPutsDirect("\r\nOOM during pre-mapping!\r\n")
			for {} // Hang
		}

		// Zero the frame
		bzero4K(unsafe.Pointer(physFrame), PAGE_SIZE)

		// Map it
		mapPage(pageAddr, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

		// Print progress marker every 5 pages
		if (i+1) % 5 == 0 {
			*(*uint32)(unsafe.Pointer(uartBase)) = 0x2E  // '.'
		}
	}

	// Flush TLB after all mappings
	asm.Dsb()
	asm.InvalidateTlbAll()
	asm.Isb()

	uartPutsDirect(" done!\r\n")
}

// dumpFetchMapping verifies the L3 mapping for a virtual address (silent unless error)
//
//go:nosplit
func dumpFetchMapping(label string, va uintptr) bool {
	_ = label // unused unless error

	va64 := uint64(va)
	l0Idx := uint16((va64 >> L0_SHIFT) & 0x1FF)
	l1Idx := uint16((va64 >> L1_SHIFT) & 0x1FF)
	l2Idx := uint16((va64 >> L2_SHIFT) & 0x1FF)
	l3Idx := uint16((va64 >> L3_SHIFT) & 0x1FF)

	l0e := (*uint64)(unsafe.Pointer(pageTableL0 + uintptr(l0Idx)*PTE_SIZE))
	if (*l0e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		return false
	}
	l1Base := uintptr(*l0e & PTE_ADDR_MASK)
	l1e := (*uint64)(unsafe.Pointer(l1Base + uintptr(l1Idx)*PTE_SIZE))
	if (*l1e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		return false
	}
	l2Base := uintptr(*l1e & PTE_ADDR_MASK)
	l2e := (*uint64)(unsafe.Pointer(l2Base + uintptr(l2Idx)*PTE_SIZE))
	if (*l2e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		return false
	}
	l3Base := uintptr(*l2e & PTE_ADDR_MASK)
	l3e := (*uint64)(unsafe.Pointer(l3Base + uintptr(l3Idx)*PTE_SIZE))

	if (*l3e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		return false
	}

	return true
}

// dumpPageTableWalk dumps the complete page table walk for a virtual address
// showing all L0->L1->L2->L3 entries with their bit fields.
// This is a diagnostic function to verify page table structure.
//
//go:nosplit
func dumpPageTableWalk(label string, va uintptr) {
	uartPutsDirect("\r\n=== Page Table Walk for ")
	uartPutsDirect(label)
	uartPutsDirect(" ===\r\n")
	uartPutsDirect("VA: 0x")
	uartPutHex64Direct(uint64(va))
	uartPutsDirect("\r\n")

	va64 := uint64(va)
	l0Idx := uint16((va64 >> L0_SHIFT) & 0x1FF)
	l1Idx := uint16((va64 >> L1_SHIFT) & 0x1FF)
	l2Idx := uint16((va64 >> L2_SHIFT) & 0x1FF)
	l3Idx := uint16((va64 >> L3_SHIFT) & 0x1FF)

	uartPutsDirect("Indices: L0=")
	uartPutHex64Direct(uint64(l0Idx))
	uartPutsDirect(" L1=")
	uartPutHex64Direct(uint64(l1Idx))
	uartPutsDirect(" L2=")
	uartPutHex64Direct(uint64(l2Idx))
	uartPutsDirect(" L3=")
	uartPutHex64Direct(uint64(l3Idx))
	uartPutsDirect("\r\n")

	// L0 entry
	uartPutsDirect("L0 table base: 0x")
	uartPutHex64Direct(uint64(pageTableL0))
	uartPutsDirect("\r\n")

	l0EntryAddr := pageTableL0 + uintptr(l0Idx)*PTE_SIZE
	l0e := *(*uint64)(unsafe.Pointer(l0EntryAddr))
	uartPutsDirect("L0[")
	uartPutHex64Direct(uint64(l0Idx))
	uartPutsDirect("] @ 0x")
	uartPutHex64Direct(uint64(l0EntryAddr))
	uartPutsDirect(" = 0x")
	uartPutHex64Direct(l0e)
	dumpPTEBits(l0e, true) // true = table descriptor

	if (l0e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		uartPutsDirect("  -> INVALID L0 entry! Bits[1:0]=")
		uartPutHex64Direct(l0e & 3)
		uartPutsDirect("\r\n")
		return
	}

	// L1 entry
	l1Base := uintptr(l0e & PTE_ADDR_MASK)
	l1EntryAddr := l1Base + uintptr(l1Idx)*PTE_SIZE
	l1e := *(*uint64)(unsafe.Pointer(l1EntryAddr))
	uartPutsDirect("L1[")
	uartPutHex64Direct(uint64(l1Idx))
	uartPutsDirect("] @ 0x")
	uartPutHex64Direct(uint64(l1EntryAddr))
	uartPutsDirect(" = 0x")
	uartPutHex64Direct(l1e)
	dumpPTEBits(l1e, true) // table descriptor

	if (l1e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		uartPutsDirect("  -> INVALID L1 entry! Bits[1:0]=")
		uartPutHex64Direct(l1e & 3)
		uartPutsDirect("\r\n")
		return
	}

	// L2 entry
	l2Base := uintptr(l1e & PTE_ADDR_MASK)
	l2EntryAddr := l2Base + uintptr(l2Idx)*PTE_SIZE
	l2e := *(*uint64)(unsafe.Pointer(l2EntryAddr))
	uartPutsDirect("L2[")
	uartPutHex64Direct(uint64(l2Idx))
	uartPutsDirect("] @ 0x")
	uartPutHex64Direct(uint64(l2EntryAddr))
	uartPutsDirect(" = 0x")
	uartPutHex64Direct(l2e)
	dumpPTEBits(l2e, true) // table descriptor

	if (l2e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		uartPutsDirect("  -> INVALID L2 entry! Bits[1:0]=")
		uartPutHex64Direct(l2e & 3)
		uartPutsDirect("\r\n")
		return
	}

	// L3 entry (page descriptor)
	l3Base := uintptr(l2e & PTE_ADDR_MASK)
	l3EntryAddr := l3Base + uintptr(l3Idx)*PTE_SIZE
	l3e := *(*uint64)(unsafe.Pointer(l3EntryAddr))
	uartPutsDirect("L3[")
	uartPutHex64Direct(uint64(l3Idx))
	uartPutsDirect("] @ 0x")
	uartPutHex64Direct(uint64(l3EntryAddr))
	uartPutsDirect(" = 0x")
	uartPutHex64Direct(l3e)
	dumpPTEBits(l3e, false) // false = page descriptor

	if (l3e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		uartPutsDirect("  -> INVALID L3 entry! Bits[1:0]=")
		uartPutHex64Direct(l3e & 3)
		uartPutsDirect("\r\n")
		return
	}

	// Extract and show the physical address
	pa := l3e & PTE_ADDR_MASK
	uartPutsDirect("  -> PA: 0x")
	uartPutHex64Direct(pa)
	uartPutsDirect("\r\n")
}

// dumpPTEBits prints the decoded bit fields of a PTE
// isTable: true for table descriptors (L0-L2), false for page descriptors (L3)
//
//go:nosplit
func dumpPTEBits(pte uint64, isTable bool) {
	uartPutsDirect("\r\n  bits: V=")
	if pte&PTE_VALID != 0 {
		uartPutcDirect('1')
	} else {
		uartPutcDirect('0')
	}

	uartPutsDirect(" T=")
	if pte&PTE_TABLE != 0 {
		uartPutcDirect('1')
	} else {
		uartPutcDirect('0')
	}

	if !isTable {
		// Page descriptor specific bits
		attrIdx := (pte >> 2) & 0x7
		uartPutsDirect(" AttrIdx=")
		uartPutHex64Direct(attrIdx)

		ap := (pte >> 6) & 0x3
		uartPutsDirect(" AP=")
		uartPutHex64Direct(ap)
		switch ap {
		case 0:
			uartPutsDirect("(RW@EL0)")
		case 1:
			uartPutsDirect("(RW@EL1)")
		case 2:
			uartPutsDirect("(RO@EL0)")
		case 3:
			uartPutsDirect("(RO@EL1)")
		}

		sh := (pte >> 8) & 0x3
		uartPutsDirect(" SH=")
		uartPutHex64Direct(sh)
		switch sh {
		case 0:
			uartPutsDirect("(None)")
		case 2:
			uartPutsDirect("(Outer)")
		case 3:
			uartPutsDirect("(Inner)")
		}

		uartPutsDirect(" AF=")
		if pte&PTE_AF != 0 {
			uartPutcDirect('1')
		} else {
			uartPutcDirect('0')
		}

		uartPutsDirect(" PXN=")
		if pte&PTE_PXN != 0 {
			uartPutcDirect('1')
		} else {
			uartPutcDirect('0')
		}

		uartPutsDirect(" UXN=")
		if pte&PTE_UXN != 0 {
			uartPutcDirect('1')
		} else {
			uartPutcDirect('0')
		}
	}

	uartPutsDirect("\r\n")
}

// enableMMU enables the MMU and switches to virtual addressing.
//
// This implementation follows the ARM Trusted Firmware reference exactly:
// https://github.com/ARM-software/arm-trusted-firmware/blob/master/lib/xlat_tables_v2/aarch64/enable_mmu.S
//
// The key sequence is:
//   1. TLB invalidate FIRST (before any register setup)
//   2. Write MAIR_EL1
//   3. Write TCR_EL1
//   4. Write TTBR0_EL1
//   5. DSB ISH + ISB (context synchronization before SCTLR)
//   6. Read/modify/write SCTLR_EL1 to enable MMU
//   7. Single ISB after SCTLR
//
// Previous implementation used UART writes as implicit barriers. This version
// uses explicit barriers per ARM TF reference, with only minimal UART output
// for debugging without relying on Device memory ordering effects.
//
//go:nosplit
func enableMMU() bool {
	uartBase := uintptr(0x09000000)

	if pageTableL0 == 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '!'
		return false
	}

	// =========================================================================
	// Step 1: TLB invalidate FIRST (ARM TF does this before any register setup)
	// =========================================================================
	asm.InvalidateTlbAll()

	// =========================================================================
	// Step 2: Configure MAIR_EL1
	// =========================================================================
	// MAIR[0] = 0xFF (Normal, Inner/Outer Write-Back Cacheable)
	// MAIR[1] = 0x00 (Device-nGnRnE)
	// MAIR[2] = 0x44 (Normal, Inner/Outer Non-Cacheable)
	mairValue := uint64(0xFF) |      // Attr0: Normal cacheable
		(uint64(0x00) << 8) |  // Attr1: Device
		(uint64(0x44) << 16)   // Attr2: Normal non-cacheable
	asm.WriteMairEl1(mairValue)

	// =========================================================================
	// Step 3: Configure TCR_EL1 (with TTBR1 enabled for high-memory kernel)
	// =========================================================================
	tcrValue := uint64(0)

	// TTBR0 configuration (user/low memory)
	tcrValue |= 16 << 0  // T0SZ = 16 (48-bit VA space)
	tcrValue |= 1 << 8   // IRGN0 = 1 (Inner Write-Back Cacheable)
	tcrValue |= 1 << 10  // ORGN0 = 1 (Outer Write-Back Cacheable)
	tcrValue |= 3 << 12  // SH0 = 3 (Inner Shareable)
	tcrValue |= 0 << 14  // TG0 = 0 (4KB granule for TTBR0)

	// TTBR1 configuration (kernel/high memory)
	tcrValue |= 16 << 16 // T1SZ = 16 (48-bit VA space)
	tcrValue |= 0 << 23  // EPD1 = 0 (ENABLE TTBR1 walks) *** KEY CHANGE ***
	tcrValue |= 1 << 24  // IRGN1 = 1 (Inner Write-Back Cacheable)
	tcrValue |= 1 << 26  // ORGN1 = 1 (Outer Write-Back Cacheable)
	tcrValue |= 3 << 28  // SH1 = 3 (Inner Shareable)
	tcrValue |= 2 << 30  // TG1 = 2 (4KB granule for TTBR1) - NOTE: different encoding!

	tcrValue |= 2 << 32  // IPS = 2 (40-bit PA space)
	asm.WriteTcrEl1(tcrValue)

	// =========================================================================
	// Step 4: Configure TTBR0_EL1 and TTBR1_EL1
	// =========================================================================
	// CRITICAL: kernelPageTableL0 must be non-zero before calling enableMMU.
	// initKernelPageTables() MUST be called first. Writing 0 to TTBR1_EL1
	// with EPD1=0 would cause page table walks from PA 0x0 (undefined behavior).
	if kernelPageTableL0 == 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '!'
		*(*uint32)(unsafe.Pointer(uartBase)) = 'T'
		*(*uint32)(unsafe.Pointer(uartBase)) = '1'
		return false // Cannot enable MMU without kernel page tables
	}

	asm.WriteTtbr0El1(uint64(pageTableL0))
	asm.WriteTtbr1El1(uint64(kernelPageTableL0))

	// =========================================================================
	// Step 5: DSB ISH + ISB (ARM TF uses Inner Shareable barrier)
	// This is the CRITICAL synchronization point before enabling MMU
	// =========================================================================
	asm.DsbIsh()
	asm.Isb()

	// =========================================================================
	// Step 6: Read/modify/write SCTLR_EL1 to enable MMU
	// =========================================================================
	sctlr := asm.ReadSctlrEl1()
	sctlr |= 1 << 0   // M = 1 (MMU enable)
	sctlr &^= 1 << 2  // C = 0 (data cache DISABLED initially)
	sctlr &^= 1 << 12 // I = 0 (instruction cache DISABLED initially)

	asm.WriteSctlrEl1(sctlr)

	// =========================================================================
	// Step 7: Single ISB after SCTLR (per ARM TF reference)
	// =========================================================================
	asm.Isb()

	// MMU is now ON - print a single character to confirm
	*(*uint32)(unsafe.Pointer(uartBase)) = 'M'

	return true
}

// =============================================================================
// Kernel Page Table Initialization (TTBR1)
// =============================================================================

// initKernelPageTables creates and initializes the TTBR1 page tables for
// high-memory kernel space (VAs with bit 63 = 1).
//
// For VA 0xFFFF_FFFF_4xxx_xxxx (kernel base):
//   L0 index = (VA >> 39) & 0x1FF = 511
//   L1 index = (VA >> 30) & 0x1FF = 511
//
// This function allocates L0 and L1 tables and links them properly.
//
//go:nosplit
func initKernelPageTables() {
	uartBase := uintptr(0x09000000)
	*(*uint32)(unsafe.Pointer(uartBase)) = 'K' // K = Kernel page tables init

	// Allocate L0 table for TTBR1 (allocatePageTable already zeros it)
	kernelPageTableL0 = allocatePageTable()
	if kernelPageTableL0 == 0 {
		kernelPanic("Failed to allocate kernel L0 page table")
	}

	// Allocate L1 table for index 511 (high memory) (allocatePageTable already zeros it)
	kernelPageTableL1 = allocatePageTable()
	if kernelPageTableL1 == 0 {
		kernelPanic("Failed to allocate kernel L1 page table")
	}

	// Link L1 table into L0[511]
	l0Entry := (*uint64)(unsafe.Pointer(kernelPageTableL0 + 511*8))
	*l0Entry = uint64(kernelPageTableL1) | PTE_VALID | PTE_TABLE

	*(*uint32)(unsafe.Pointer(uartBase)) = 'k' // k = Kernel page tables done
}

// setupKernelStacks allocates and maps high-memory kernel stacks in TTBR1.
//
// This function creates two stacks for kmazarin execution:
//   - Kernel g0 stack (SP_EL0): 16KB at KernelG0StackBottom-KernelG0StackTop
//   - Kernel exception stack (SP_EL1): 8KB at KernelExcStackBottom-KernelExcStackTop
//
// Stack sizes are tuned for the tail-call ABI stub pattern used throughout
// the codebase, where ABI stubs use JMP (not CALL) to minimize stack usage.
//
// Note: These stacks are separate from Cardinal's bootstrap stacks and are
// only used when kmazarin is executing at high memory addresses.
//
//go:nosplit
func setupKernelStacks() {
	uartBase := uintptr(0x09000000)
	*(*uint32)(unsafe.Pointer(uartBase)) = 'T' // T = sTack setup

	// High-memory kernel stack addresses (from constants/layout.go)
	const (
		KernelG0StackTop      = uintptr(0xFFFFFFFF5F000000)
		KernelG0StackSize     = uintptr(0x4000) // 16KB
		KernelExcStackTop     = uintptr(0xFFFFFFFF5F002000)
		KernelExcStackSize    = uintptr(0x2000) // 8KB
	)
	KernelG0StackBottom := KernelG0StackTop - KernelG0StackSize

	// Calculate number of pages needed for each stack
	const g0StackPages = uintptr(KernelG0StackSize / PAGE_SIZE)       // 16KB = 4 pages
	const excStackPages = uintptr(KernelExcStackSize / PAGE_SIZE)    // 8KB = 2 pages

	// Allocate and map g0 stack (SP_EL0, 16KB)
	uartPutsDirect("Mapping kernel g0 stack (TTBR1): 0x")
	uartPutHex64Direct(uint64(KernelG0StackBottom))
	uartPutsDirect(" - 0x")
	uartPutHex64Direct(uint64(KernelG0StackTop))
	uartPutsDirect(" (16KB)\r\n")

	for i := uintptr(0); i < g0StackPages; i++ {
		physFrame := allocPhysFrame()
		if physFrame == 0 {
			kernelPanic("setupKernelStacks: Failed to allocate g0 stack page")
		}

		// Zero the physical frame
		bzero4K(unsafe.Pointer(physFrame), PAGE_SIZE)

		// Map at high-memory VA
		va := KernelG0StackBottom + i*PAGE_SIZE
		mapKernelPage(va, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}

	// Allocate and map exception stack (SP_EL1, 8KB) in high memory
	// SP_EL1 will be set to 0xFFFFFFFF5F002000 via HVC call before jumping to kmazarin
	KernelExcStackBottom := KernelExcStackTop - KernelExcStackSize

	uartPutsDirect("Mapping kernel exception stack (TTBR1): 0x")
	uartPutHex64Direct(uint64(KernelExcStackBottom))
	uartPutsDirect(" - 0x")
	uartPutHex64Direct(uint64(KernelExcStackTop))
	uartPutsDirect(" (8KB)\r\n")

	for i := uintptr(0); i < excStackPages; i++ {
		physFrame := allocPhysFrame()
		if physFrame == 0 {
			kernelPanic("setupKernelStacks: Failed to allocate exception stack page")
		}

		// Zero the physical frame
		bzero4K(unsafe.Pointer(physFrame), PAGE_SIZE)

		// Map at high-memory VA
		va := KernelExcStackBottom + i*PAGE_SIZE
		mapKernelPage(va, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}

	*(*uint32)(unsafe.Pointer(uartBase)) = 't' // t = sTack setup done
}

// setupEarlyKernelMMIO maps essential early-boot MMIO devices to high memory in TTBR1.
// This allows kmazarin (running at high memory) to access UART, GIC during early boot.
// The high-memory MMIO addresses follow the pattern: physical + 0xFFFFFFFF00000000
// Additional MMIO devices will be discovered and mapped during DTB scan.
func setupEarlyKernelMMIO() {
	uartBase := uintptr(0x09000000)
	*(*uint32)(unsafe.Pointer(uartBase)) = 'M' // M = MMIO setup

	// Map UART (0x09000000 → 0xFFFFFFFF09000000)
	// Size: 64KB (0x10000)
	const uartPhys = uintptr(0x09000000)
	const uartSize = uintptr(0x10000)
	const kernelUartVA = uintptr(0xFFFFFFFF09000000)

	uartPutsDirect("Mapping kernel UART (TTBR1): PA 0x")
	uartPutHex64Direct(uint64(uartPhys))
	uartPutsDirect(" -> VA 0x")
	uartPutHex64Direct(uint64(kernelUartVA))
	uartPutsDirect("\r\n")

	// Map 64KB in 4KB pages (16 pages)
	const uartPages = uartSize / PAGE_SIZE
	for i := uintptr(0); i < uartPages; i++ {
		va := kernelUartVA + i*PAGE_SIZE
		pa := uartPhys + i*PAGE_SIZE
		mapKernelPage(va, pa, PTE_ATTR_DEVICE, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}

	// Map GIC (0x08000000 → 0xFFFFFFFF08000000)
	// Size: 128KB (0x20000) for GICD + GICC
	const gicPhys = uintptr(0x08000000)
	const gicSize = uintptr(0x20000)
	const kernelGicVA = uintptr(0xFFFFFFFF08000000)

	uartPutsDirect("Mapping kernel GIC (TTBR1): PA 0x")
	uartPutHex64Direct(uint64(gicPhys))
	uartPutsDirect(" -> VA 0x")
	uartPutHex64Direct(uint64(kernelGicVA))
	uartPutsDirect("\r\n")

	// Map 128KB in 4KB pages (32 pages)
	const gicPages = gicSize / PAGE_SIZE
	for i := uintptr(0); i < gicPages; i++ {
		va := kernelGicVA + i*PAGE_SIZE
		pa := gicPhys + i*PAGE_SIZE
		mapKernelPage(va, pa, PTE_ATTR_DEVICE, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}

	*(*uint32)(unsafe.Pointer(uartBase)) = 'm' // m = MMIO setup done
}

// setupKernelDemandPaging sets up the memory infrastructure that allows kmazarin
// to implement demand paging for its heap. This involves:
//
// 1. Identity-mapping the TTBR1 page tables into high memory (PA + KernelVAOffset)
//    This allows kmazarin to access any page table by computing VA = PA + offset
//
// 2. Pre-allocating and mapping the PT Pool region - physical frames that kmazarin
//    can use to allocate new L2/L3 page tables for demand paging
//
// The heap VA space itself (KernelHeapStart to KernelHeapEnd) is NOT pre-mapped.
// Kmazarin will handle page faults in that region by allocating physical frames
// and mapping them on-demand.
//
//go:nosplit
func setupKernelDemandPaging() {
	uartBase := uintptr(0x09000000)
	*(*uint32)(unsafe.Pointer(uartBase)) = 'E' // E = dEmand paging setup

	// =========================================================================
	// Step 1: Identity-map the page table region to high memory
	// =========================================================================
	// Map the entire Cardinal page table region (0x41000000-0x41800000) to
	// high memory using identity mapping: VA = PA + 0xFFFFFFFF00000000
	// This allows kmazarin to access any page table (TTBR0 or TTBR1) by simply
	// adding the kernel VA offset to the physical address.
	//
	// This is much simpler than the previous approach of mapping specific tables
	// to a fixed VA region, because now kmazarin can compute any page table's VA
	// from its PA trivially.

	const KernelVAOffset = uintptr(0xFFFFFFFF00000000)
	ptBase := getPageTableAllocator().base  // 0x41000000

	uartPutsDirect("Identity-mapping page table region to high memory\r\n")
	uartPutsDirect("  PA range: 0x")
	uartPutHex64Direct(uint64(ptBase))
	uartPutsDirect(" - 0x")
	uartPutHex64Direct(uint64(ptBase + PAGE_TABLE_SIZE))
	uartPutsDirect("\r\n")
	uartPutsDirect("  VA range: 0x")
	uartPutHex64Direct(uint64(ptBase + KernelVAOffset))
	uartPutsDirect(" - 0x")
	uartPutHex64Direct(uint64(ptBase + PAGE_TABLE_SIZE + KernelVAOffset))
	uartPutsDirect("\r\n")

	// Get current allocation offset to know how many pages are in use
	allocOffset := getPageTableAllocator().offset
	usedPages := (allocOffset + PAGE_SIZE - 1) / PAGE_SIZE

	// Map each 4KB page of the page table region
	// We map the entire region that could be used, not just currently allocated
	for pa := ptBase; pa < ptBase+PAGE_TABLE_SIZE; pa += PAGE_SIZE {
		va := pa + KernelVAOffset
		mapKernelPage(va, pa, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}

	uartPutsDirect("  Mapped ")
	uartPutHex64Direct(uint64(PAGE_TABLE_SIZE / PAGE_SIZE))
	uartPutsDirect(" pages (")
	uartPutHex64Direct(uint64(usedPages))
	uartPutsDirect(" currently in use)\r\n")

	// Print TTBR1 table addresses for reference
	uartPutsDirect("  TTBR1 L0: PA 0x")
	uartPutHex64Direct(uint64(kernelPageTableL0))
	uartPutsDirect(" -> VA 0x")
	uartPutHex64Direct(uint64(kernelPageTableL0 + KernelVAOffset))
	uartPutsDirect("\r\n")
	uartPutsDirect("  TTBR1 L1: PA 0x")
	uartPutHex64Direct(uint64(kernelPageTableL1))
	uartPutsDirect(" -> VA 0x")
	uartPutHex64Direct(uint64(kernelPageTableL1 + KernelVAOffset))
	uartPutsDirect("\r\n")

	// =========================================================================
	// Step 2: Pre-allocate and map the PT Pool region
	// =========================================================================
	// Kmazarin needs physical frames to allocate new L2/L3 tables for demand paging.
	// We pre-allocate a pool of pages and map them to a known VA range.
	// These pages will be used for L2/L3 tables when kmazarin needs to extend
	// the page table structure for demand paging.

	uartPutsDirect("Mapping PT Pool: VA 0x")
	uartPutHex64Direct(uint64(constants.KernelPTPoolStart))
	uartPutsDirect(" - 0x")
	uartPutHex64Direct(uint64(constants.KernelPTPoolEnd))
	uartPutsDirect("\r\n")

	ptPoolPages := uintptr(constants.KernelPTPoolEnd-constants.KernelPTPoolStart) / PAGE_SIZE
	for i := uintptr(0); i < ptPoolPages; i++ {
		physFrame := allocPhysFrame()
		if physFrame == 0 {
			uartPutsDirect("ERROR: Failed to allocate PT pool frame ")
			uartPutHex64Direct(uint64(i))
			uartPutsDirect("\r\n")
			kernelPanic("setupKernelDemandPaging: Out of physical frames for PT pool")
		}

		// Zero the frame (page tables should start zeroed)
		bzero4K(unsafe.Pointer(physFrame), PAGE_SIZE)

		// Map to high-memory VA (PT pool at fixed VA)
		va := constants.KernelPTPoolStart + i*PAGE_SIZE
		mapKernelPage(va, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

		// ALSO identity-map to high memory so kmazarin can compute VA from PA
		identityVA := physFrame + KernelVAOffset
		mapKernelPage(identityVA, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}
	uartPutsDirect("  Allocated ")
	uartPutHex64Direct(uint64(ptPoolPages))
	uartPutsDirect(" pages for PT pool\r\n")

	// =========================================================================
	// Step 3: Record demand paging region boundaries
	// =========================================================================
	// The heap VA range (KernelHeapStart to KernelHeapEnd) is NOT mapped yet.
	// Kmazarin will use the constants to know the boundaries and will
	// handle page faults in this region by allocating from the frame pool.

	uartPutsDirect("Heap VA range (demand-paged): 0x")
	uartPutHex64Direct(uint64(constants.KernelHeapStart))
	uartPutsDirect(" - 0x")
	uartPutHex64Direct(uint64(constants.KernelHeapEnd))
	uartPutsDirect("\r\n")

	*(*uint32)(unsafe.Pointer(uartBase)) = 'e' // e = demand paging setup done
}

// mapKernelPage maps a physical address to a high-memory virtual address
// in the TTBR1 page tables. The VA must have bit 63 = 1 (kernel space).
//
// This is similar to mapPage but uses the kernel page table structures.
func mapKernelPage(va, pa uintptr, attrs uint64, ap uint64, exec uint64) {
	// Validate VA is in kernel space (bit 63 must be 1)
	if (va >> 63) != 1 {
		kernelPanic("mapKernelPage: VA not in kernel space")
	}

	// Extract page table indices
	l0Idx := (va >> 39) & 0x1FF
	l1Idx := (va >> 30) & 0x1FF
	l2Idx := (va >> 21) & 0x1FF
	l3Idx := (va >> 12) & 0x1FF

	// L0 entry should point to L1 (already set up in initKernelPageTables)
	l0Entry := (*uint64)(unsafe.Pointer(kernelPageTableL0 + l0Idx*8))
	if (*l0Entry & PTE_VALID) == 0 {
		kernelPanic("mapKernelPage: L0 entry not valid")
	}

	// Get L1 table address
	l1Table := uintptr(*l0Entry & PTE_ADDR_MASK)

	// Check/allocate L2 table
	l1Entry := (*uint64)(unsafe.Pointer(l1Table + l1Idx*8))
	var l2Table uintptr
	if (*l1Entry & PTE_VALID) == 0 {
		// Allocate new L2 table
		l2Table = allocatePageTable()
		if l2Table == 0 {
			kernelPanic("mapKernelPage: Failed to allocate L2 table")
		}
		bzero4K(unsafe.Pointer(l2Table), TABLE_SIZE)
		*l1Entry = uint64(l2Table) | PTE_VALID | PTE_TABLE
	} else {
		l2Table = uintptr(*l1Entry & PTE_ADDR_MASK)
	}

	// Check/allocate L3 table
	l2Entry := (*uint64)(unsafe.Pointer(l2Table + l2Idx*8))
	var l3Table uintptr
	if (*l2Entry & PTE_VALID) == 0 {
		// Allocate new L3 table
		l3Table = allocatePageTable()
		if l3Table == 0 {
			kernelPanic("mapKernelPage: Failed to allocate L3 table")
		}
		bzero4K(unsafe.Pointer(l3Table), TABLE_SIZE)
		*l2Entry = uint64(l3Table) | PTE_VALID | PTE_TABLE
	} else {
		l3Table = uintptr(*l2Entry & PTE_ADDR_MASK)
	}

	// Write L3 entry (page mapping)
	l3Entry := (*uint64)(unsafe.Pointer(l3Table + l3Idx*8))
	*l3Entry = createPageTableEntry(pa, attrs, ap, exec)
}

// =============================================================================
// Critical Globals Verification
// =============================================================================
// These functions verify that all globals used by the page fault handler are
// properly mapped in the page tables BEFORE any demand paging can occur.
// If any of these are not mapped, the page fault handler would trigger
// a nested page fault (infinite recursion) when trying to access them.

// VerifyCriticalGlobalsMapped walks the page tables to verify all critical
// globals used by HandlePageFault are already mapped.
// Should be called at the start of KernelMain after MMU is enabled.
//
// Returns true if all critical globals are mapped, false if any are unmapped.
// Prints detailed diagnostic information about each global.
func VerifyCriticalGlobalsMapped() bool {
	uartBase := uintptr(0x09000000)
	*(*uint32)(unsafe.Pointer(uartBase)) = 'W' // W = entered VerifyCriticalGlobalsMapped

	// Simplified verification - just check mappings without verbose output
	// (print() causes page faults if rodata isn't properly mapped)
	allMapped := true

	// 1. Check pageTableL0 pointer itself (stored in BSS)
	pageTableL0Addr := uintptr(unsafe.Pointer(&pageTableL0))
	if getPhysicalAddress(pageTableL0Addr) == 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '!'
		allMapped = false
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = '1'

	// 2. Check physFrameAllocatorState_global
	physAllocAddr := uintptr(unsafe.Pointer(&physFrameAllocatorState_global))
	if getPhysicalAddress(physAllocAddr) == 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '!'
		allMapped = false
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = '2'

	// 3. Check totalKernelPages_global
	totalPagesAddr := uintptr(unsafe.Pointer(&totalKernelPages_global))
	if getPhysicalAddress(totalPagesAddr) == 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '!'
		allMapped = false
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = '3'

	// 4. Check inPageFaultHandler_global (re-entrancy guard)
	reentryAddr := uintptr(unsafe.Pointer(&inPageFaultHandler_global))
	if getPhysicalAddress(reentryAddr) == 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '!'
		allMapped = false
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = '4'

	// 5. Check mmapSpans array (defined in syscall.go)
	// Skip for now - the verifyMmapSpansMapped function also uses print()
	*(*uint32)(unsafe.Pointer(uartBase)) = '5'

	// 6. Verify the page table L0 base itself is valid and mapped
	if getPhysicalAddress(pageTableL0) == 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '!'
		allMapped = false
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = '6'

	if allMapped {
		uartPutsDirect("Globals OK\r\n")
	} else {
		uartPutsDirect("GLOBALS FAILED\r\n")
	}

	return allMapped
}

// printHex64ForMMU prints a 64-bit value in hex format (helper for verification)
// Uses print() which is safe after MMU is enabled
// Named differently from kernel.go's printHex64 to avoid redeclaration
func printHex64ForMMU(v uint64) {
	const digits = "0123456789abcdef"
	var buf [16]byte
	for i := 15; i >= 0; i-- {
		buf[i] = digits[v&0xF]
		v >>= 4
	}
	print(string(buf[:]))
}
