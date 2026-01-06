package main

import (
	"unsafe"

	"cardinal/asm"
)

// =============================================================================
// Demand Paging Support
// =============================================================================

// mmapPromise tracks a virtual memory promise from mmap
// We track ranges of virtual addresses that were promised but not yet mapped
type mmapPromise struct {
	start uintptr // Start of virtual range
	end   uintptr // End of virtual range (exclusive)
}

// Maximum number of mmap promises we track (should be enough for Go runtime)
const maxMmapPromises = 256

var (
	mmapPromises    [maxMmapPromises]mmapPromise
	mmapPromiseCount int
)

// addMmapPromise records a virtual memory promise
// Returns true on success, false if promise table is full
//
//go:nosplit
func addMmapPromise(start, size uintptr) bool {
	if mmapPromiseCount >= maxMmapPromises {
		uartPutsDirect("MMU: mmap promise table full!\r\n")
		return false
	}

	mmapPromises[mmapPromiseCount] = mmapPromise{
		start: start,
		end:   start + size,
	}
	mmapPromiseCount++
	return true
}

// isAddressPromised checks if a virtual address was promised via mmap
//
//go:nosplit
func isAddressPromised(va uintptr) bool {
	for i := 0; i < mmapPromiseCount; i++ {
		if va >= mmapPromises[i].start && va < mmapPromises[i].end {
			return true
		}
	}
	return false
}

// GetPageFaultCounter returns the current page fault count
// This allows exceptions.go to access the counter for debugging
//go:nosplit
func GetPageFaultCounter() uint32 {
	return pageFaultCounter
}

// preMapPages pre-maps specific pages that are known to cause issues
// This is a workaround to test if demand paging at certain addresses causes hangs
//go:nosplit
func preMapPages() {
	// Pre-map the 64KB boundary page that causes fault #17 to hang
	// VA 0x4000010000 is exactly at a 64KB boundary
	const targetVA = uintptr(0x4000010000)

	// Allocate a physical frame
	physFrame := allocPhysFrame()
	if physFrame == 0 {
		return
	}

	// Zero the physical frame (it's identity-mapped, so we can access it directly)
	bzero4K(unsafe.Pointer(physFrame), PAGE_SIZE)

	// Map the page
	mapPage(targetVA, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// Ensure the mapping is visible
	asm.Dsb()
	asm.InvalidateTlbAll()
	asm.Isb()
}

// HandlePageFault handles a page fault for demand paging
// Called from the exception handler when a data abort occurs
// Returns true if the fault was handled (page mapped), false otherwise
//
// Parameters:
//   - faultAddr: The faulting virtual address (FAR_EL1)
//   - faultStatus: The fault status from ESR_EL1 (lower bits)
//
// Simplified design: Any address in the mmap virtual range (0x60000000-0x200000000)
// is considered valid. The Go runtime won't access addresses it didn't request,
// so any fault in this range is from a legitimate mmap allocation.
//
// NOTE: nosplit removed - called from syncExceptionDispatchInternal on g0 stack.
//
//go:noinline
func HandlePageFault(faultAddr uintptr, faultStatus uint64) bool {
	// ==========================================================================
	// CRITICAL: NESTED PAGE FAULT DETECTION
	// ==========================================================================
	// If we're already in a page fault handler, we have a nested fault.
	// This is FATAL - it means the page fault handler itself is accessing
	// unmapped memory, which would cause infinite recursion.
	//
	// The inPageFaultHandler_global variable MUST be in BSS (pre-mapped before
	// MMU enable) so that reading/writing it cannot itself trigger a page fault.
	//
	if inPageFaultHandler_global != 0 {
		// NESTED PAGE FAULT DETECTED - FATAL ERROR
		uartPutsDirect("\r\n")
		uartPutsDirect("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!\r\n")
		uartPutsDirect("! FATAL: NESTED PAGE FAULT DETECTED                         !\r\n")
		uartPutsDirect("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!\r\n")
		uartPutsDirect("Nested fault address: VA=0x")
		uartPutHex64Direct(uint64(faultAddr))
		uartPutsDirect("\r\n")
		uartPutsDirect("Fault status: 0x")
		uartPutHex64Direct(faultStatus)
		uartPutsDirect("\r\n")
		uartPutsDirect("\r\n")
		uartPutsDirect("This means the page fault handler accessed unmapped memory.\r\n")
		uartPutsDirect("Likely causes:\r\n")
		uartPutsDirect("  1. A global variable used by HandlePageFault is not pre-mapped\r\n")
		uartPutsDirect("  2. physFrameAllocatorState_global not in identity-mapped BSS\r\n")
		uartPutsDirect("  3. mmapSpans array not in identity-mapped BSS\r\n")
		uartPutsDirect("  4. pageTableL0 pointer not accessible\r\n")
		uartPutsDirect("\r\n")
		uartPutsDirect("Run VerifyCriticalGlobalsMapped() at KernelMain start to debug.\r\n")
		uartPutsDirect("\r\n")
		// Print a simple backtrace using saved registers
		uartPutsDirect("Halting immediately.\r\n")
		for {
			// Infinite loop - halt the system
		}
	}

	// Mark that we're now inside the page fault handler
	inPageFaultHandler_global = 1

	// DEBUG: Print simple breadcrumb to show demand paging is working
	// Also print the actual fault address for low address faults (more interesting)
	if faultAddr >= 0x4000000000 {
		uartPutcDirect('*') // High address page fault
	} else {
		// For low addresses, print the fault address
		uartPutcDirect('[')
		uartPutHex64Direct(uint64(faultAddr))
		uartPutcDirect(']')
	}

	// CRITICAL: Validate that the fault address is in a registered mmap span
	//
	// This is our security boundary - only addresses that were explicitly mmap'd
	// are eligible for demand paging. This automatically rejects:
	// - NULL pointers (0x0)
	// - ROM/Flash region (0x0-0x8000000)
	// - MMIO devices (0x8000000-0x40000000)
	// - Unmapped high addresses (>1PB)
	// - Any other region that wasn't explicitly requested via mmap
	//
	if !isInMmapSpan(faultAddr) {
		uartPutsDirect("\r\n!PAGE FAULT at unmapped address: VA=0x")
		uartPutHex64Direct(uint64(faultAddr))
		uartPutsDirect("\r\n")
		uartPutsDirect("Not in any mmap span. Possible causes:\r\n")
		uartPutsDirect("  - NULL pointer dereference\r\n")
		uartPutsDirect("  - ROM/Flash access (not supported)\r\n")
		uartPutsDirect("  - MMIO access (use direct MMIO functions)\r\n")
		uartPutsDirect("  - Access to memory not allocated via mmap\r\n")
		inPageFaultHandler_global = 0 // Clear re-entrancy guard before return
		return false
	}

	// faultAddr is now validated to be in a legitimate mmap'd region

	// Align fault address to page boundary
	pageAddr := faultAddr &^ (PAGE_SIZE - 1)

	// uartPutsDirect(" check...")  // DISABLED

	// CHECK: Is this page already mapped?
	// This shouldn't happen - page faults should only occur for unmapped pages
	// But if it does happen, we want to detect it
	existingPA := getPhysicalAddress(pageAddr)

	// uartPutsDirect(" exist=0x")  // DISABLED
	// uartPutHex64Direct(uint64(existingPA))  // DISABLED

	if existingPA != 0 {
		// DEBUG: Get the actual PTE to see what flags are set
		va64 := uint64(pageAddr)
		l0Idx := uint16((va64 >> 39) & 0x1FF)
		l1Idx := uint16((va64 >> 30) & 0x1FF)
		l2Idx := uint16((va64 >> 21) & 0x1FF)
		l3Idx := uint16((va64 >> 12) & 0x1FF)

		l0Entry := (*uint64)(unsafe.Pointer(pageTableL0 + uintptr(l0Idx)*PTE_SIZE))
		l1Table := uintptr(*l0Entry & PTE_ADDR_MASK)
		l1Entry := (*uint64)(unsafe.Pointer(l1Table + uintptr(l1Idx)*PTE_SIZE))
		l2Table := uintptr(*l1Entry & PTE_ADDR_MASK)
		l2Entry := (*uint64)(unsafe.Pointer(l2Table + uintptr(l2Idx)*PTE_SIZE))
		l3Table := uintptr(*l2Entry & PTE_ADDR_MASK)
		l3Entry := (*uint64)(unsafe.Pointer(l3Table + uintptr(l3Idx)*PTE_SIZE))

		uartPutsDirect("\r\n!DUPLICATE FAULT at VA=0x")
		uartPutHex64Direct(uint64(pageAddr))
		uartPutsDirect(" PA=0x")
		uartPutHex64Direct(uint64(existingPA))
		uartPutsDirect(" PTE=0x")
		uartPutHex64Direct(*l3Entry)
		uartPutsDirect(" IFSC=0x")
		uartPutHex64Direct(faultStatus)
		// Print full ESR by reading it directly
		esr := asm.ReadEsrEl1()
		uartPutsDirect(" ESR=0x")
		uartPutHex64Direct(esr)
		// Print ELR to see what address we're returning to
		elr := asm.ReadElrEl1()
		uartPutsDirect(" ELR=0x")
		uartPutHex64Direct(elr)
		uartPutsDirect("\r\n")

		// CRITICAL FIX: Flush TLB for this address!
		// The page is already mapped in page tables, but TLB has stale entry
		// Use full TLB invalidation instead of VA-specific, as VA-specific
		// flush may not work correctly for all address ranges
		asm.Dsb()                  // Ensure all memory operations complete
		asm.InvalidateTlbAll()     // Invalidate ALL TLBs (nuclear option)
		asm.Dsb()                  // Ensure TLB invalidation completes
		asm.Isb()                  // Synchronize context

		// This is already mapped - return success without allocating
		inPageFaultHandler_global = 0 // Clear re-entrancy guard before return
		return true
	}

	// Allocate a physical frame
	physFrame := allocPhysFrame()
	if physFrame == 0 {
		// Out of physical memory - this is fatal for demand paging
		uartPutsDirect("\r\nDEMAND PAGE OOM at VA=0x")
		uartPutHex64Direct(uint64(faultAddr))
		uartPutsDirect("\r\n")
		inPageFaultHandler_global = 0 // Clear re-entrancy guard before return
		return false
	}

	// uartPutsDirect(" PA=0x")  // DISABLED
	// uartPutHex64Direct(uint64(physFrame))  // DISABLED

	// CRITICAL: Verify physical frame is in valid range
	if physFrame >= PHYS_FRAME_END {
		uartPutsDirect("\r\n!INVALID PHYS FRAME (beyond 256MB): 0x")
		uartPutHex64Direct(uint64(physFrame))
		uartPutsDirect(" end=0x")
		uartPutHex64Direct(uint64(PHYS_FRAME_END))
		uartPutsDirect("\r\n")
		for {} // Hang
	}

	// Verify frame is not in page table region
	// This runs after MMU is enabled, so string access is OK, but use direct calls for consistency
	pageTableStart := asm.GetPageTablesStartAddr()
	pageTableEnd := asm.GetPageTablesEndAddr()
	if physFrame >= pageTableStart && physFrame < pageTableEnd {
		uartPutsDirect("\r\n!PHYS FRAME IN PAGE TABLE REGION: 0x")
		uartPutHex64Direct(uint64(physFrame))
		uartPutsDirect("\r\n")
		for {} // Hang
	}

	// uartPutsDirect(" map...")  // DISABLED

	// CRITICAL: Map the page FIRST, then zero via VA (not PA)
	// We cannot safely access the physical frame directly because it might not
	// be identity-mapped. After creating the VA→PA mapping, we can safely zero
	// via the virtual address.
	mapPage(pageAddr, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// Ensure page table writes are visible before TLB flush
	asm.Dsb()

	// PERFORMANCE: Invalidate TLB only for this specific VA, not entire TLB
	// This keeps the TLB warm for other addresses, dramatically improving performance
	asm.InvalidateTlbVa(pageAddr)

	// Ensure TLB invalidation completes before returning
	asm.Isb()

	// uartPutsDirect(" bzero...")  // DISABLED

	// NOW zero the page via the VA (not PA!)
	// After mapPage() and TLB invalidation, the VA is accessible and mapped to physFrame
	// SECURITY: Always zero new pages to prevent leaking old data
	bzero4K(unsafe.Pointer(pageAddr), PAGE_SIZE)

	// CRITICAL: Data Synchronization Barrier after zeroing the page
	// Without this barrier, the zero writes may still be in the CPU's store buffer
	// when we return from the exception. The retried instruction (e.g., writing
	// span.startAddr) would execute, but subsequent reads could see stale data
	// (the buffered zeroes) if the store buffer hasn't drained.
	//
	// DSB ensures all memory writes (including the bzero) are visible to all
	// observers before any subsequent instructions execute.
	asm.Dsb()

	// DEBUG: Print completion for ALL faults to track success
	// uartPutsDirect(" OK")  // DISABLED

	// Clear re-entrancy guard - page fault handled successfully
	inPageFaultHandler_global = 0
	return true
}
