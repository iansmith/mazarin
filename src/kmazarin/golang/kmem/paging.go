//go:build qemuvirt && aarch64

package kmem

import (
	"unsafe"
)

// uartPutcDirect writes a byte to UART (linked from kmazarin package)
//go:linkname uartPutcDirect kmazarin/kmem.uartPutcDirect
func uartPutcDirect(c byte)

// uartPutHex64Direct writes a 64-bit hex value to UART (linked from kmazarin package)
//go:linkname uartPutHex64Direct kmazarin/kmem.uartPutHex64Direct
func uartPutHex64Direct(val uint64)

// Page table constants for ARM64 4KB granule
const (
	// Page table indices - 48-bit VA, 4KB pages, 4-level tables
	L0Shift = 39
	L1Shift = 30
	L2Shift = 21
	L3Shift = 12

	// Page Table Entry bits
	PTE_VALID = 1 << 0  // Entry is valid
	PTE_TABLE = 1 << 1  // Entry points to next-level table (or is L3 page)
	PTE_AF    = 1 << 10 // Access flag

	// Memory attributes (MAIR index)
	PTE_ATTR_NORMAL = 0 << 2 // Normal cacheable (MAIR[0])

	// Access permissions
	PTE_AP_RW_EL1 = 1 << 6 // Read-write at EL1, no EL0 access

	// Shareability
	PTE_SH_INNER = 3 << 8 // Inner shareable

	// Execute permission
	PTE_EXEC_NEVER = (1 << 53) | (1 << 54) // PXN + UXN

	// Address mask for PTEs (bits 47:12)
	PTE_ADDR_MASK = 0x0000FFFFFFFFF000
)

// Memory layout constants come from runtime configuration (auxv).
// NO hardcoded addresses - everything is derived at runtime from Cardinal.

// Globals set during initialization
// All globals use lazy initialization to avoid relocation issues.
// The relocation script would corrupt compile-time initialized values
// that look like physical addresses or kernel VAs.
var (
	pagingInitialized bool
	ptPoolInitialized bool
	ttbr1L0PA         uintptr // Physical address of TTBR1 L0 table (lazy init)
	ttbr1L1PA         uintptr // Physical address of TTBR1 L1 table (lazy init)
	ptPoolNext        uintptr // Next page in PT pool (lazy init)
)

// InitPaging initializes the paging subsystem.
// All values come from runtime configuration (auxv from Cardinal).
//
//go:nosplit
func InitPaging() {
	cfg := getRuntimeConfigTyped()
	ttbr1L0PA = uintptr(cfg.TTBR1L0Phys)
	ptPoolNext = uintptr(cfg.KernelPTPoolStart)
	ptPoolInitialized = true
	pagingInitialized = true

	// L1 PA will be discovered by lazy init when needed (read from L0 entry)
}

// paToVA converts a physical address to a virtual address using identity mapping.
// Cardinal maps page tables with VA = PA + KernelVAOffset.
//
//go:nosplit
func paToVA(pa uintptr) uintptr {
	cfg := getRuntimeConfigTyped()
	return pa + uintptr(cfg.KernelVAOffset)
}

// vaToPa converts a high-memory virtual address to a physical address.
// For identity-mapped regions, PA = VA - KernelVAOffset.
//
//go:nosplit
func vaToPa(va uintptr) uintptr {
	cfg := getRuntimeConfigTyped()
	return va - uintptr(cfg.KernelVAOffset)
}

// allocPTPage allocates a page from the PT pool for a new L2 or L3 table.
// Returns the VA of the allocated page (already mapped by Cardinal).
//
//go:nosplit
func allocPTPage() uintptr {
	// Lazy initialization from runtime config
	if !ptPoolInitialized {
		cfg := getRuntimeConfigTyped()
		ptPoolNext = uintptr(cfg.KernelPTPoolStart)
		ptPoolInitialized = true
	}

	cfg := getRuntimeConfigTyped()
	if ptPoolNext >= uintptr(cfg.KernelPTPoolEnd) {
		uartPuts("[kmem] PT OOM!\r\n")
		return 0
	}

	page := ptPoolNext
	ptPoolNext += PageSize

	// Zero the page
	ptr := (*[512]uint64)(unsafe.Pointer(page))
	for i := 0; i < 512; i++ {
		ptr[i] = 0
	}

	return page
}

// HandlePageFault handles a page fault at the given virtual address.
// Returns true if the fault was handled successfully, false otherwise.
//
// For heap addresses, this function:
// 1. Allocates a physical frame
// 2. Allocates L2/L3 tables if needed
// 3. Maps the VA to the PA
//
//go:nosplit
func HandlePageFault(faultAddr uintptr) bool {
	// DEBUG: Print 'G' at absolute entry (before anything else)
	uartPutcDirect('G')
	// DEBUG: Breadcrumb H = HandlePageFault entry
	uartPutcDirect('H')

	// Lazy initialization - read TTBR1_EL1 to get L0 table PA
	if !pagingInitialized {
		uartPutcDirect('I') // DEBUG: Init path
		ttbr1L0PA = readTTBR1EL1()
		// L1 is found by reading the L0 entry for the high memory region
		// For 0xFFFFFFFF... addresses, L0 index is 511 (0x1FF)
		l0VA := paToVA(ttbr1L0PA)
		l0Entry := (*uint64)(unsafe.Pointer(l0VA + 511*8))
		if (*l0Entry & PTE_VALID) != 0 {
			ttbr1L1PA = uintptr(*l0Entry & PTE_ADDR_MASK)
		}
		pagingInitialized = true
	}

	// Check if fault is in manageable range
	// Accept faults from kmazarin VA start to heap end
	cfg := getRuntimeConfigTyped()

	// Calculate kmazarin VA start: PhysAddr + VAOffset
	kmazarinVAStart := uintptr(cfg.KmazarinPhysAddr + cfg.KernelVAOffset)
	heapEnd := uintptr(cfg.KernelHeapEnd)

	// Check if fault is in stack regions (should be pre-mapped by Cardinal!)
	g0StackBottom := uintptr(cfg.G0StackBottom)
	g0StackTop := uintptr(cfg.G0StackTop)
	excStackTop := uintptr(cfg.ExceptionStackTop)
	excStackSize := uintptr(cfg.ExceptionStackSize)
	excStackBottom := excStackTop - excStackSize

	if faultAddr >= g0StackBottom && faultAddr < g0StackTop {
		// Fault in g0 stack region - should not happen!
		uartPutcDirect('S')
		uartPutcDirect('0')
		uartPutcDirect('!')
		uartPutcDirect('[')
		uartPutHex64Direct(uint64(faultAddr))
		uartPutcDirect(']')
		return false
	}

	if faultAddr >= excStackBottom && faultAddr < excStackTop {
		// Fault in exception stack region - should not happen!
		uartPutcDirect('S')
		uartPutcDirect('1')
		uartPutcDirect('!')
		uartPutcDirect('[')
		uartPutHex64Direct(uint64(faultAddr))
		uartPutcDirect(']')
		return false
	}

	if faultAddr < kmazarinVAStart || faultAddr >= heapEnd {
		// DEBUG: R = Range check failed, print fault address
		uartPutcDirect('R')
		uartPutcDirect('!')
		uartPutcDirect('[')
		uartPutHex64Direct(uint64(faultAddr))
		uartPutcDirect(']')
		return false
	}

	// Align to page boundary
	pageAddr := faultAddr &^ (PageSize - 1)

	// Allocate a physical frame
	frame := AllocFrame()
	if frame == 0 {
		// DEBUG: A = Alloc failed
		uartPutcDirect('A')
		uartPutcDirect('!')
		return false
	}

	// Map the page FIRST (before zeroing!)
	// ZeroFrame accesses the VA, so the page must be mapped first.
	if !mapPage(pageAddr, frame) {
		// DEBUG: M = Map failed
		uartPutcDirect('M')
		uartPutcDirect('!')
		return false
	}

	// Now zero the frame (using the just-mapped VA)
	ZeroFrame(frame)

	return true
}

// mapPage maps a virtual address to a physical address in TTBR1.
// Allocates L2/L3 tables from PT pool as needed.
//
//go:nosplit
func mapPage(va, pa uintptr) bool {
	// Extract indices
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	// Get L0 table VA
	l0VA := paToVA(ttbr1L0PA)
	if l0VA == 0 {
		uartPutcDirect('0')
		uartPutcDirect('!')
		return false
	}

	// Read L0 entry (should be valid - points to L1)
	l0Entry := (*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	if (*l0Entry & PTE_VALID) == 0 {
		uartPutcDirect('1')
		uartPutcDirect('!')
		return false
	}

	// Get L1 table VA
	l1PA := uintptr(*l0Entry & PTE_ADDR_MASK)
	l1VA := paToVA(l1PA)
	if l1VA == 0 {
		uartPutcDirect('2')
		uartPutcDirect('!')
		return false
	}

	// Read L1 entry
	l1Entry := (*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	var l2VA uintptr

	if (*l1Entry & PTE_VALID) == 0 {
		// Need to allocate L2 table
		l2VA = allocPTPage()
		if l2VA == 0 {
			uartPutcDirect('3')
			uartPutcDirect('!')
			return false
		}

		// Calculate PA from VA (PT pool is identity-mapped)
		l2PA := vaToPa(l2VA)

		// Link new L2 table into L1
		*l1Entry = uint64(l2PA) | PTE_VALID | PTE_TABLE
	} else {
		l2PA := uintptr(*l1Entry & PTE_ADDR_MASK)
		l2VA = paToVA(l2PA)
		if l2VA == 0 {
			uartPutcDirect('4')
			uartPutcDirect('!')
			return false
		}
	}

	// Read L2 entry
	l2Entry := (*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	var l3VA uintptr

	if (*l2Entry & PTE_VALID) == 0 {
		// Need to allocate L3 table
		l3VA = allocPTPage()
		if l3VA == 0 {
			uartPutcDirect('5')
			uartPutcDirect('!')
			return false
		}

		// Calculate PA from VA (PT pool is identity-mapped)
		l3PA := vaToPa(l3VA)

		// Link new L3 table into L2
		*l2Entry = uint64(l3PA) | PTE_VALID | PTE_TABLE
	} else {
		l3PA := uintptr(*l2Entry & PTE_ADDR_MASK)
		l3VA = paToVA(l3PA)
		if l3VA == 0 {
			uartPutcDirect('6')
			uartPutcDirect('!')
			return false
		}
	}

	// Write L3 entry
	l3Entry := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	*l3Entry = uint64(pa) | PTE_VALID | PTE_TABLE | PTE_AF |
		PTE_ATTR_NORMAL | PTE_AP_RW_EL1 | PTE_SH_INNER | PTE_EXEC_NEVER

	// Memory barriers and TLB invalidate
	dsbSY()
	tlbiVAE1IS(va)
	dsbSY()
	isbSY()

	return true
}

// Assembly barrier stubs
//
//go:nosplit
func dsbSY() {
	// DSB SY - implemented in asm_barriers_arm64.s
	dsbSYAsm()
}

//go:nosplit
func tlbiVAE1IS(va uintptr) {
	// TLBI VAE1IS - implemented in asm_barriers_arm64.s
	tlbiVAE1ISAsm(va >> 12)
}

//go:nosplit
func isbSY() {
	// ISB SY - implemented in asm_barriers_arm64.s
	isbSYAsm()
}

// These are implemented in asm_barriers_arm64.s
func dsbSYAsm()
func tlbiVAE1ISAsm(va uintptr)
func isbSYAsm()
func readTTBR1EL1() uintptr
