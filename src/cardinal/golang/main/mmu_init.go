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
	// Initialize cache line size for optimized bzero
	initCacheLineSize()

	// CRITICAL: Call assembly helpers directly instead of getLinkerSymbol()
	// because getLinkerSymbol() uses string comparisons that access .rodata
	// which isn't mapped yet when initMMU() is called!
	pageTableBase := asm.GetPageTablesStartAddr()
	pageTableEnd := asm.GetPageTablesEndAddr()

	// Calculate PAGE_TABLE_SIZE from the difference
	PAGE_TABLE_SIZE = pageTableEnd - pageTableBase

	// Allocate page table memory
	pageTableL0 = pageTableBase
	pageTableL1 = pageTableBase + TABLE_SIZE

	// Initialize the bump allocator after the pre-allocated L0 + L1 tables
	ptAlloc := getPageTableAllocator()
	ptAlloc.base = pageTableBase
	ptAlloc.offset = TABLE_SIZE * 2

	// Verify page table base is 4KB aligned
	if pageTableL0&0xFFF != 0 {
		kernelPanic("initMMU: page table base not 4KB aligned")
	}

	// Zero out page tables (L0 + L1, each 4KB = 8KB total)
	bzero4K(unsafe.Pointer(pageTableL0), TABLE_SIZE)
	bzero4K(unsafe.Pointer(pageTableL1), TABLE_SIZE)

	// Set up L0 table to point to L1 table for identity mapping
	l0Entry0 := (*uint64)(unsafe.Pointer(pageTableL0 + 0*PTE_SIZE))
	*l0Entry0 = createTableEntry(pageTableL1)

	// Map low memory regions with correct permissions
	// CRITICAL: Call assembly helpers directly instead of getLinkerSymbol()
	// because getLinkerSymbol() uses string comparisons that access .rodata!
	rodataStart := asm.GetRodataStartAddr()
	rodataEnd := asm.GetRodataEndAddr()
	if rodataStart != 0 && rodataEnd != 0 {
		mapRegionInitMMU(rodataStart, rodataEnd, rodataStart, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)
	}

	// Get section boundaries from linker symbols
	textStart := asm.GetTextStartAddr()
	dataStart := asm.GetDataStartAddr()
	endAddr := asm.GetEndAddr()

	// Map everything before .rodata as read-only (boot code, text)
	if textStart > 0 && rodataStart > 0 && textStart < rodataStart {
		mapRegionInitMMU(textStart, rodataStart, textStart, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_ALLOW)
	}

	// Map everything after .rodata up to data section as read-only
	if rodataEnd > 0 && dataStart > 0 && rodataEnd < dataStart {
		mapRegionInitMMU(rodataEnd, dataStart, rodataEnd, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)
	}

	// Map data+BSS sections as read-write
	bssEnd := asm.GetBssEndAddr()
	if dataStart > 0 && bssEnd > 0 {
		mapRegionInitMMU(dataStart, bssEnd, dataStart, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}

	// Map remainder after BSS up to end of kernel image as read-only
	if bssEnd > 0 && endAddr > 0 && bssEnd < endAddr {
		mapRegionInitMMU(bssEnd, endAddr, bssEnd, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)
	}

	// Initialize MMIO devices array
	mmioDevices[0] = MMIODevice{start: asm.GetGicBase(), size: asm.GetGicSize(), attr: PTE_ATTR_DEVICE, ap: PTE_AP_RW_EL1}
	mmioDevices[1] = MMIODevice{start: asm.GetUartBase(), size: asm.GetUartSize(), attr: PTE_ATTR_DEVICE, ap: PTE_AP_RW_EL1}
	mmioDevices[2] = MMIODevice{start: asm.GetFwcfgBase(), size: asm.GetFwcfgSize(), attr: PTE_ATTR_DEVICE, ap: PTE_AP_RW_EL1}
	mmioDevices[3] = MMIODevice{start: asm.GetBochsDisplayBase(), size: 0, attr: PTE_ATTR_DEVICE, ap: PTE_AP_RW_EL1}
	mmioDeviceCount = 4

	// Map all MMIO devices
	for i := 0; i < mmioDeviceCount; i++ {
		dev := &mmioDevices[i]
		if dev.size > 0 {
			mapRegionInitMMU(dev.start, dev.start+dev.size, dev.start, dev.attr, dev.ap, PTE_EXEC_NEVER)
		}
	}

	// Map DTB region
	dtbStart := asm.GetDtbBootAddr()
	dtbEnd := dtbStart + asm.GetDtbSize()
	mapRegionInitMMU(dtbStart, dtbEnd, dtbStart, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)

	// Map PCI ECAM (lowmem, full 16MB window)
	ecamBase := uintptr(0x3F000000)
	ecamSize := uintptr(0x01000000) // 16MB - full ECAM window
	mapRegionInitMMU(ecamBase, ecamBase+ecamSize, ecamBase, PTE_ATTR_DEVICE, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// Map highmem PCI ECAM (needed for VirtIO GPU device enumeration)
	// QEMU virt places high-memory PCI ECAM at 0x4010000000 (256MB region)
	ecamHighBase := uintptr(0x4010000000)
	ecamHighSize := uintptr(0x10000000) // 256MB
	mapRegionInitMMU(ecamHighBase, ecamHighBase+ecamHighSize, ecamHighBase, PTE_ATTR_DEVICE, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// Map PCI MMIO region (for device BARs)
	// QEMU virt provides MMIO window at 0x10000000-0x3EFFFFFF (~750MB)
	pciMmioBase := uintptr(0x10000000)
	pciMmioSize := uintptr(0x2EFF0000) // ~750MB
	mapRegionInitMMU(pciMmioBase, pciMmioBase+pciMmioSize, pciMmioBase, PTE_ATTR_DEVICE, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// SKIP PCI BAR region (240MB) - will map on-demand if needed

	// Get page table region boundaries from linker.ld
	// Note: pageTableEnd already declared earlier in function
	pageTableEnd = asm.GetPageTablesEndAddr()

	// Map kernel RAM (after cardinal image to framebuffer) - heap
	// CRITICAL: Start mapping AFTER our kernel image (endAddr) to avoid overlap
	ramStart := (endAddr + 0xFFF) &^ 0xFFF // Round up to next page

	// Pre-map heap region (cardinal kmalloc heap) before framebuffer as RW, non-executable
	framebufferStart := uintptr(constants.FramebufferPhysAddr)
	if ramStart < framebufferStart {
		mapRegionInitMMU(ramStart, framebufferStart, ramStart, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}

	// Map VirtIO GPU framebuffer region (8 MB, read-write, Normal memory)
	// Region: 0x41000000 - 0x41800000
	framebufferEnd := uintptr(constants.FramebufferEnd)
	mapRegionInitMMU(framebufferStart, framebufferEnd, framebufferStart, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// Map page table region as CACHEABLE (ARM64's PTW is cache-coherent)
	mapRegionInitMMU(pageTableBase, pageTableEnd, pageTableBase, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

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
	mapRegionInitMMU(g0StackBottom, g0StackTop, g0StackBottom, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// Map exception stack (SP_EL1) - boot.s must set SP_EL1 to exceptionStackTop
	mapRegionInitMMU(exceptionStackBottom, exceptionStackTop, exceptionStackBottom, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// Initialize physical frame allocator
	initPhysFrameAllocator()

	// Clean data cache for page tables before enabling MMU
	pageTableSize := pageTableEnd - pageTableBase
	for offset := uintptr(0); offset < pageTableSize; offset += 64 {
		asm.CleanDataCacheVA(pageTableBase + offset)
	}

	// Flush TLB to ensure all mappings are visible
	asm.Dsb()
	asm.InvalidateTlbAll()
	asm.Isb()

	// Initialize kernel page tables (TTBR1) for high-memory kernel
	initKernelPageTables()

	// Set up high-memory kernel stacks in TTBR1
	setupKernelStacks()

	// Map essential early MMIO devices to high memory in TTBR1
	setupEarlyKernelMMIO()

	// Set up demand paging infrastructure for kmazarin
	setupKernelDemandPaging()

	// Check if we ran out of page table space
	_, remaining := getPageTableAllocatorStats()
	if remaining == 0 {
		kernelPanic("initMMU: out of page table space")
	}

	return true
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
	if pageTableL0 == 0 {
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
	if kernelPageTableL0 == 0 {
		return false
	}

	asm.WriteTtbr0El1(uint64(pageTableL0))
	asm.WriteTtbr1El1(uint64(kernelPageTableL0))

	// DSB ISH + ISB - critical synchronization before enabling MMU
	asm.DsbIsh()
	asm.Isb()

	// Read/modify/write SCTLR_EL1 to enable MMU
	sctlr := asm.ReadSctlrEl1()
	sctlr |= 1 << 0   // M = 1 (MMU enable)
	sctlr &^= 1 << 2  // C = 0 (data cache DISABLED initially)
	sctlr &^= 1 << 12 // I = 0 (instruction cache DISABLED initially)
	asm.WriteSctlrEl1(sctlr)

	// Single ISB after SCTLR (per ARM TF reference)
	asm.Isb()

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
	// Allocate L0 table for TTBR1
	kernelPageTableL0 = allocatePageTable()
	if kernelPageTableL0 == 0 {
		kernelPanic("Failed to allocate kernel L0 page table")
	}

	// Allocate L1 table for index 511 (high memory)
	kernelPageTableL1 = allocatePageTable()
	if kernelPageTableL1 == 0 {
		kernelPanic("Failed to allocate kernel L1 page table")
	}

	// Link L1 table into L0[511]
	l0Entry := (*uint64)(unsafe.Pointer(kernelPageTableL0 + 511*8))
	*l0Entry = uint64(kernelPageTableL1) | PTE_VALID | PTE_TABLE
}

// setupKernelStacks allocates and maps high-memory kernel stacks in TTBR1.
//
// This function creates two stacks for kmazarin execution:
//   - Kernel g0 stack (SP_EL0): 32KB at KernelG0StackBottom-KernelG0StackTop
//   - Kernel exception stack (SP_EL1): 16KB at KernelExcStackBottom-KernelExcStackTop
//
// Stack sizes are tuned for the tail-call ABI stub pattern used throughout
// the codebase, where ABI stubs use JMP (not CALL) to minimize stack usage.
// DOUBLED from 16KB/8KB to test if stack overflow is causing x28 corruption.
//
// Note: These stacks are separate from Cardinal's bootstrap stacks and are
// only used when kmazarin is executing at high memory addresses.
//
//go:nosplit
func setupKernelStacks() {
	// High-memory kernel stack addresses
	const (
		KernelG0StackTop   = uintptr(0xFFFFFFFF5F000000)
		KernelG0StackSize  = uintptr(0x8000) // 32KB
		KernelExcStackTop  = uintptr(0xFFFFFFFF5F004000)
		KernelExcStackSize = uintptr(0x4000) // 16KB
	)
	KernelG0StackBottom := KernelG0StackTop - KernelG0StackSize
	const g0StackPages = uintptr(KernelG0StackSize / PAGE_SIZE)
	const excStackPages = uintptr(KernelExcStackSize / PAGE_SIZE)

	// Allocate and map g0 stack (SP_EL0)
	for i := uintptr(0); i < g0StackPages; i++ {
		physFrame := allocPhysFrame()
		if physFrame == 0 {
			kernelPanic("setupKernelStacks: Failed to allocate g0 stack page")
		}
		bzero4K(unsafe.Pointer(physFrame), PAGE_SIZE)
		mapKernelPage(KernelG0StackBottom+i*PAGE_SIZE, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}

	// Allocate and map exception stack (SP_EL1)
	KernelExcStackBottom := KernelExcStackTop - KernelExcStackSize
	for i := uintptr(0); i < excStackPages; i++ {
		physFrame := allocPhysFrame()
		if physFrame == 0 {
			kernelPanic("setupKernelStacks: Failed to allocate exception stack page")
		}
		bzero4K(unsafe.Pointer(physFrame), PAGE_SIZE)
		mapKernelPage(KernelExcStackBottom+i*PAGE_SIZE, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}
}

// setupEarlyKernelMMIO maps only the essential UART to high memory in TTBR1.
// This is the minimal mapping needed for Cardinal to output text during boot.
//
// All other MMIO devices (GIC, RTC, VirtIO, etc.) are mapped on-demand by
// kmazarin during device discovery via kmem.MapDeviceMMIO().
//
// This keeps Cardinal minimal - it only touches VM tables long enough to
// get kmazarin executing its text section. Kmazarin handles all other
// device mappings based on DTB-provided addresses.
func setupEarlyKernelMMIO() {
	// Only map UART for early boot output
	// Physical: 0x09000000, Size: 64KB (one page is enough for UART)
	const uartPhys = uintptr(0x09000000)
	const uartVA = uintptr(0xFFFFFFFF09000000)

	// Map one page for UART
	mapKernelPage(uartVA, uartPhys, PTE_ATTR_DEVICE, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
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
	const KernelVAOffset = uintptr(0xFFFFFFFF00000000)
	ptBase := getPageTableAllocator().base

	// Identity-map the page table region to high memory (VA = PA + offset)
	for pa := ptBase; pa < ptBase+PAGE_TABLE_SIZE; pa += PAGE_SIZE {
		mapKernelPage(pa+KernelVAOffset, pa, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}

	// Pre-allocate and map the PT Pool region for kmazarin's demand paging
	ptPoolPages := uintptr(constants.KernelPTPoolEnd-constants.KernelPTPoolStart) / PAGE_SIZE
	for i := uintptr(0); i < ptPoolPages; i++ {
		physFrame := allocPhysFrame()
		if physFrame == 0 {
			kernelPanic("setupKernelDemandPaging: Out of physical frames for PT pool")
		}
		bzero4K(unsafe.Pointer(physFrame), PAGE_SIZE)
		mapKernelPage(constants.KernelPTPoolStart+i*PAGE_SIZE, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
		mapKernelPage(physFrame+KernelVAOffset, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}

	// Ensure all mappings are visible
	asm.Dsb()
	asm.InvalidateTlbAll()
	asm.Isb()
}

// mapKernelPage maps a physical address to a high-memory virtual address
// in the TTBR1 page tables. The VA must have bit 63 = 1 (kernel space).
//
// This is similar to mapPage but uses the kernel page table structures.
func mapKernelPage(va, pa uintptr, attrs uint64, ap uint64, exec uint64) {
	// Validate VA is in kernel space (bit 63 must be 1)
	if (va >> 63) != 1 {
		uartPutsDirect("mapKernelPage: VA=0x")
		uartPutHex64Direct(uint64(va))
		uartPutsDirect(" PA=0x")
		uartPutHex64Direct(uint64(pa))
		uartPutsDirect("\r\n")
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
	allMapped := true

	// Check critical globals are mapped
	if getPhysicalAddress(uintptr(unsafe.Pointer(&pageTableL0))) == 0 {
		allMapped = false
	}
	if getPhysicalAddress(uintptr(unsafe.Pointer(&physFrameAllocatorState_global))) == 0 {
		allMapped = false
	}
	if getPhysicalAddress(uintptr(unsafe.Pointer(&totalKernelPages_global))) == 0 {
		allMapped = false
	}
	if getPhysicalAddress(uintptr(unsafe.Pointer(&inPageFaultHandler_global))) == 0 {
		allMapped = false
	}
	if getPhysicalAddress(pageTableL0) == 0 {
		allMapped = false
	}

	return allMapped
}

