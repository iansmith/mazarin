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
	ttbr0L0PA         uintptr // Physical address of TTBR0 L0 table (for heap)
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
	ttbr0L0PA = uintptr(cfg.TTBR0L0Phys)
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

// walkPageTable translates a VA to PA by walking the page tables.
// This works even for non-identity-mapped addresses.
// CRITICAL: This is needed because the PT pool VAs are NOT identity-mapped!
// They were mapped by cardinal to different PAs.
//
//go:nosplit
func walkPageTable(va uintptr) uintptr {
	// Extract indices
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	cfg := getRuntimeConfigTyped()

	// L0 table
	l0PA := ttbr1L0PA
	l0VA := l0PA + uintptr(cfg.KernelVAOffset)
	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	if (l0Entry & PTE_VALID) == 0 {
		return 0
	}

	// L1 table
	l1PA := uintptr(l0Entry & PTE_ADDR_MASK)
	l1VA := l1PA + uintptr(cfg.KernelVAOffset)
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if (l1Entry & PTE_VALID) == 0 {
		return 0
	}

	// L2 table
	l2PA := uintptr(l1Entry & PTE_ADDR_MASK)
	l2VA := l2PA + uintptr(cfg.KernelVAOffset)
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if (l2Entry & PTE_VALID) == 0 {
		return 0
	}

	// L3 table
	l3PA := uintptr(l2Entry & PTE_ADDR_MASK)
	l3VA := l3PA + uintptr(cfg.KernelVAOffset)
	l3Entry := *(*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	if (l3Entry & PTE_VALID) == 0 {
		return 0
	}

	// Extract PA and add page offset
	pa := uintptr(l3Entry & PTE_ADDR_MASK)
	return pa | (va & (PageSize - 1))
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

	// Lazy initialization - call InitPaging to read config and set up TTBR0/TTBR1
	if !pagingInitialized {
		uartPutcDirect('I') // DEBUG: Init path
		InitPaging()
		uartPutcDirect('i') // DEBUG: InitPaging done
	}

	uartPutcDirect('5') // DEBUG: After init

	// Check if fault is in manageable range
	// Accept faults from kmazarin VA start to heap end
	cfg := getRuntimeConfigTyped()
	uartPutcDirect('6') // DEBUG: Got config

	// Calculate ranges for kmazarin binary and heap
	kmazarinVAStart := uintptr(cfg.KmazarinPhysAddr + cfg.KernelVAOffset)
	kmazarinVAEnd := kmazarinVAStart + uintptr(cfg.KmazarinSize)
	heapStart := uintptr(cfg.KernelHeapStart)
	heapEnd := uintptr(cfg.KernelHeapEnd)
	uartPutcDirect('7') // DEBUG: Calculated ranges

	uartPutcDirect('8') // DEBUG: Before stack checks

	// Check if fault is in stack regions (should be pre-mapped by Cardinal!)
	g0StackBottom := uintptr(cfg.G0StackBottom)
	g0StackTop := uintptr(cfg.G0StackTop)
	excStackTop := uintptr(cfg.ExceptionStackTop)
	excStackSize := uintptr(cfg.ExceptionStackSize)
	excStackBottom := excStackTop - excStackSize

	uartPutcDirect('9') // DEBUG: Stack vars calculated

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
	uartPutcDirect('a') // DEBUG: passed g0 stack check

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
	uartPutcDirect('b') // DEBUG: passed exc stack check

	// Check if fault is in valid range: either in kmazarin binary OR in heap
	// These ranges may not be contiguous (heap can be in lower TTBR1 space)
	inKmazarin := faultAddr >= kmazarinVAStart && faultAddr < kmazarinVAEnd
	inHeap := faultAddr >= heapStart && faultAddr < heapEnd
	if !inKmazarin && !inHeap {
		// DEBUG: R = Range check failed, print fault address
		uartPutcDirect('R')
		uartPutcDirect('!')
		uartPutcDirect('[')
		uartPutHex64Direct(uint64(faultAddr))
		uartPutcDirect(']')
		return false
	}
	uartPutcDirect('c') // DEBUG: passed range check

	// Align to page boundary
	pageAddr := faultAddr &^ (PageSize - 1)
	uartPutcDirect('d') // DEBUG: aligned

	// Allocate a physical frame
	frame := AllocFrame()
	if frame == 0 {
		// DEBUG: A = Alloc failed
		uartPutcDirect('A')
		uartPutcDirect('!')
		return false
	}

	uartPutcDirect('e') // DEBUG: about to map

	// Map the page FIRST (before zeroing!)
	// ZeroFrame accesses the VA, so the page must be mapped first.
	if !mapPage(pageAddr, frame) {
		// DEBUG: M = Map failed
		uartPutcDirect('M')
		uartPutcDirect('!')
		return false
	}
	uartPutcDirect('m') // DEBUG: mapped

	// Zero the frame using the just-mapped VA (pageAddr), NOT via physAddr + KernelVAOffset!
	// The frame pool physical memory isn't mapped at PA + KernelVAOffset.
	// But pageAddr was just mapped to the physical frame, so we can zero via pageAddr.
	uartPutcDirect('Z') // DEBUG: about to zero
	uartPutcDirect('[')
	uartPutHex64Direct(uint64(pageAddr))
	uartPutcDirect(']')
	uartPutcDirect('>')
	uartPutHex64Direct(uint64(frame))
	uartPutcDirect('<')

	// Extra barrier before accessing newly-mapped memory
	dsbSY()
	isbSY()
	uartPutcDirect('B') // DEBUG: barriers done

	// SKIP zeroing for now to test if mapping works
	// The Go runtime will access the page and we'll see if it works
	uartPutcDirect('S') // DEBUG: skipping zero, returning success
	_ = pageAddr // suppress unused warning

	return true
}

// mapPage maps a virtual address to a physical address.
// Uses TTBR0 for user-space addresses (heap) and TTBR1 for kernel addresses.
// Allocates L2/L3 tables from PT pool as needed.
//
//go:nosplit
func mapPage(va, pa uintptr) bool {
	// Extract indices
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	// DEBUG: Print indices
	uartPutcDirect('[')
	uartPutcDirect('I')
	uartPutHex64Direct(uint64(l1Idx))
	uartPutcDirect('/')
	uartPutHex64Direct(uint64(l2Idx))
	uartPutcDirect('/')
	uartPutHex64Direct(uint64(l3Idx))
	uartPutcDirect(']')

	// Determine which translation table to use based on VA bit 55
	// Bit 55 = 0 -> TTBR0 (user space), Bit 55 = 1 -> TTBR1 (kernel space)
	var l0PA uintptr
	if (va>>55)&1 == 0 {
		// TTBR0 (user space / heap)
		uartPutcDirect('T')
		uartPutcDirect('0')
		l0PA = ttbr0L0PA
		uartPutcDirect('{')
		uartPutHex64Direct(uint64(l0PA))
		uartPutcDirect('}')
	} else {
		// TTBR1 (kernel space)
		uartPutcDirect('T')
		uartPutcDirect('1')
		l0PA = ttbr1L0PA
	}

	// Get L0 table VA
	l0VA := paToVA(l0PA)
	uartPutcDirect('V')
	uartPutHex64Direct(uint64(l0VA))
	if l0VA == 0 {
		uartPutcDirect('0')
		uartPutcDirect('!')
		return false
	}

	// Calculate L0 entry address and print for debug
	l0EntryAddr := l0VA + l0Idx*8
	uartPutcDirect('E')
	uartPutHex64Direct(uint64(l0EntryAddr))

	// Read L0 entry
	uartPutcDirect('R')
	l0Entry := (*uint64)(unsafe.Pointer(l0EntryAddr))

	var l1VA uintptr

	if (*l0Entry & PTE_VALID) == 0 {
		// Need to allocate L1 table (new for expanded VA space support)
		uartPutcDirect('L')
		uartPutcDirect('1')
		uartPutcDirect('N') // DEBUG: New L1 table needed

		l1VA = allocPTPage()
		if l1VA == 0 {
			uartPutcDirect('1')
			uartPutcDirect('a')
			uartPutcDirect('!')
			return false
		}

		// Get physical address of new L1 table
		l1PA := walkPageTable(l1VA)
		if l1PA == 0 {
			uartPutcDirect('1')
			uartPutcDirect('w')
			uartPutcDirect('!')
			return false
		}

		// Link new L1 table into L0
		*l0Entry = uint64(l1PA) | PTE_VALID | PTE_TABLE

		// Cache clean and barriers for L0 update
		dcCIVAC(uintptr(unsafe.Pointer(l0Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		// Get existing L1 table VA
		l1PA := uintptr(*l0Entry & PTE_ADDR_MASK)
		l1VA = paToVA(l1PA)
	}
	if l1VA == 0 {
		uartPutcDirect('2')
		uartPutcDirect('!')
		return false
	}

	// Read L1 entry
	l1Entry := (*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	// DEBUG: Print L1 entry value
	uartPutcDirect('L')
	uartPutcDirect('1')
	uartPutcDirect('=')
	uartPutHex64Direct(*l1Entry)
	uartPutcDirect('|')

	var l2VA uintptr

	if (*l1Entry & PTE_VALID) == 0 {
		// Need to allocate L2 table
		l2VA = allocPTPage()
		if l2VA == 0 {
			uartPutcDirect('3')
			uartPutcDirect('!')
			return false
		}

		// CRITICAL FIX: Use walkPageTable instead of vaToPa!
		l2PA := walkPageTable(l2VA)
		if l2PA == 0 {
			uartPutcDirect('w')
			uartPutcDirect('!')
			return false
		}

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
	// DEBUG: Print L2 entry value before modification
	uartPutcDirect('L')
	uartPutcDirect('2')
	uartPutcDirect('=')
	uartPutHex64Direct(*l2Entry)
	uartPutcDirect('|')

	var l3VA uintptr

	if (*l2Entry & PTE_VALID) == 0 {
		uartPutcDirect('N') // DEBUG: New L3 table needed
		// Need to allocate L3 table
		l3VA = allocPTPage()
		if l3VA == 0 {
			uartPutcDirect('5')
			uartPutcDirect('!')
			return false
		}

		// CRITICAL FIX: Use walkPageTable instead of vaToPa!
		// PT pool VAs are NOT identity-mapped. They map to different PAs
		// that were allocated by cardinal. We must walk the page tables
		// to find the actual PA.
		l3PA := walkPageTable(l3VA)
		if l3PA == 0 {
			uartPutcDirect('W')
			uartPutcDirect('!')
			return false
		}
		// DEBUG: Print both VAs and PAs
		uartPutcDirect('{')
		uartPutHex64Direct(uint64(l3VA))
		uartPutcDirect(':')
		uartPutHex64Direct(uint64(l3PA))
		uartPutcDirect('}')

		// Link new L3 table into L2
		*l2Entry = uint64(l3PA) | PTE_VALID | PTE_TABLE

		// DEBUG: Print new L2 entry value
		uartPutcDirect('L')
		uartPutcDirect('2')
		uartPutcDirect('N')
		uartPutcDirect('=')
		uartPutHex64Direct(*l2Entry)
		uartPutcDirect('|')

		// Clean cache for L2 entry so hardware page walker can see it
		dcCIVAC(uintptr(unsafe.Pointer(l2Entry)))
		dsbSY()

		// Verify L2 entry readback
		readback := *l2Entry
		if readback != uint64(l3PA)|PTE_VALID|PTE_TABLE {
			uartPutcDirect('V')
			uartPutcDirect('!')
			uartPutHex64Direct(readback)
			return false
		}
		uartPutcDirect('V') // DEBUG: Verified L2
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
	pteValue := uint64(pa) | PTE_VALID | PTE_TABLE | PTE_AF |
		PTE_ATTR_NORMAL | PTE_AP_RW_EL1 | PTE_SH_INNER | PTE_EXEC_NEVER
	*l3Entry = pteValue

	// DEBUG: Print L3 entry details
	uartPutcDirect('L')
	uartPutcDirect('3')
	uartPutcDirect('@')
	uartPutHex64Direct(uint64(l3VA + l3Idx*8))
	uartPutcDirect('=')
	uartPutHex64Direct(pteValue)
	uartPutcDirect('|')

	// Clean cache for L3 entry so hardware page walker can see it
	dcCIVAC(uintptr(unsafe.Pointer(l3Entry)))
	dsbSY()
	uartPutcDirect('C') // DEBUG: Cache cleaned

	// Verify L3 entry readback
	l3Readback := *l3Entry
	if l3Readback != pteValue {
		uartPutcDirect('X')
		uartPutcDirect('!')
		return false
	}
	uartPutcDirect('X') // DEBUG: Verified L3

	// Memory barriers and TLB invalidate
	tlbiVAE1IS(va)
	dsbSY()
	isbSY()

	uartPutcDirect('T') // DEBUG: TLB invalidated

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

//go:nosplit
func dcCIVAC(va uintptr) {
	// DC CIVAC - Clean and Invalidate by VA to PoC
	dcCIVACAsm(va)
}

// These are implemented in asm_barriers_arm64.s
func dsbSYAsm()
func tlbiVAE1ISAsm(va uintptr)
func isbSYAsm()
func dcCIVACAsm(va uintptr)
func readTTBR1EL1() uintptr
