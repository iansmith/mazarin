
package kmem

import (
	"mazzy/kmazarin/console"
	"mazzy/shared/constants"
	"unsafe"
)

// Import from ksyscall to validate mmap addresses
//
//go:linkname getUserMmapAllocEnd mazzy/kmazarin/ksyscall.GetUserMmapAllocEnd
func getUserMmapAllocEnd() uint64

//go:linkname isAddressInSpan mazzy/kmazarin/ksyscall.IsAddressInSpan
func isAddressInSpan(addr uint64) bool

// debugPaging enables verbose page fault debugging output
// Set to false for production, true for debugging
const debugPaging = false

// debugPrint conditionally outputs a character if debugging is enabled
// Uses console abstraction which provides spinlock protection
//
//go:nosplit
func debugPrint(c byte) {
	if debugPaging {
		console.KWriteByte(c)
	}
}

// debugPrintHex conditionally outputs a hex value if debugging is enabled
// Uses console abstraction which provides spinlock protection
//
//go:nosplit
func debugPrintHex(val uint64) {
	if debugPaging {
		console.KPrintHex64(val)
	}
}

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
	PTE_ATTR_DEVICE = 1 << 2 // Device-nGnRnE (MAIR[1])

	// Access permissions (AP[2:1] field)
	PTE_AP_RW_EL1    = 0 << 6 // Read-write at EL1, no EL0 access (AP=00)
	PTE_AP_RW_ALL    = 1 << 6 // Read-write at EL1 and EL0 (AP=01)
	PTE_AP_RO_EL1    = 2 << 6 // Read-only at EL1, no EL0 access (AP=10)
	PTE_AP_RO_ALL    = 3 << 6 // Read-only at EL1 and EL0 (AP=11)

	// Execute permission for user pages
	PTE_UXN = 1 << 54 // User execute never
	PTE_PXN = 1 << 53 // Privileged execute never

	// Shareability
	PTE_SH_INNER = 3 << 8 // Inner shareable

	// Non-global flag - CRITICAL for ASID-based address space separation!
	// nG=1 means this entry is process-specific and uses ASID for TLB matching.
	// nG=0 (global) means entry is shared across all ASIDs - WRONG for userspace!
	PTE_NG = 1 << 11 // Non-global (must be set for userspace pages with ASIDs)

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
	ttbr1L0PA         uintptr // Physical address of TTBR1 L0 table (lazy init)
	ttbr0L0PA         uintptr // Physical address of TTBR0 L0 table (Cardinal's original)
	// NOTE: processL0PA global removed - use readTTBR0EL1() to get current L0PA
	ttbr1L1PA uintptr // Physical address of TTBR1 L1 table (lazy init)
	// NOTE: ptPoolNext removed - PT allocation now uses unified pool

	// Cache for allocated page table VAs
	// Since we can't compute VA from PA (Cardinal doesn't map all RAM),
	// we need to track the VAs of page tables we allocate.
	// Key: PA of page table, Value: VA of page table
	// CRITICAL: If this fills up, page table lookups will fail because
	// PT pool pages are NOT identity-mapped - paToVA fallback won't work!
	// 512 entries should support 6-8 priests comfortably.
	ptVACache     [512]ptVACacheEntry // Simple fixed-size cache
	ptVACacheSize int
)

type ptVACacheEntry struct {
	pa uintptr
	va uintptr
}

// getKmazarinSize returns the kmazarin binary size from startup params.
// Uses direct startup params access to avoid the deep fullConfig chain.
//
//go:nosplit
func getKmazarinSize() uint64 {
	return getStartupConfigValue(40) // KmazarinSize offset in RuntimeConfig
}

// InitPaging initializes the paging subsystem.
// All values come from runtime configuration (auxv from Cardinal).
//
//go:nosplit
func InitPaging() {
	// Read TTBR values directly from startup params to avoid the deep
	// fullConfig→DTB parsing chain that exceeds nosplit stack budget.
	// Offsets match shared/constants.RuntimeConfig field positions.
	ttbr1L0PA = uintptr(getStartupConfigValue(80)) // TTBR1L0Phys
	ttbr0L0PA = uintptr(getStartupConfigValue(88)) // TTBR0L0Phys
	pagingInitialized = true

	// Initialize the unified pool for PT allocation
	InitUnifiedPool()

	// L1 PA will be discovered by lazy init when needed (read from L0 entry)
}

// GetTTBR0L0PA returns the physical address of the TTBR0 L0 page table.
// This is used for debugging page table issues.
//
//go:nosplit
func GetTTBR0L0PA() uintptr {
	if !pagingInitialized {
		InitPaging()
	}
	return ttbr0L0PA
}

// ReadCurrentTTBR0 reads the current TTBR0_EL1 register value.
// Returns the raw register value including ASID in upper bits.
// Use (result & 0x0000FFFFFFFFFFFF) to extract just the L0PA.
//
//go:nosplit
func ReadCurrentTTBR0() uint64 {
	return uint64(readTTBR0EL1())
}

// TranslateUserVA uses hardware address translation (AT S1E0R) to translate
// a userspace VA to PA as if accessed from EL0. This verifies the MMU configuration.
// Returns (PA, success). If translation fails, PA contains fault info.
//
//go:nosplit
func TranslateUserVA(va uintptr) (uint64, bool) {
	par := atS1E0R(va)
	if par&1 != 0 {
		// Translation failed - bit 0 = 1
		return par, false
	}
	// Success - extract PA from PAR
	pa := (par & 0x0000FFFFFFFFF000) | (uint64(va) & 0xFFF)
	return pa, true
}

// CreateProcessPageTable allocates a fresh L0 page table for a userspace process.
// This creates a completely clean address space, separate from Cardinal's TTBR0.
// Returns the physical address of the new L0 table.
//
// IMPORTANT: After creating the process page table, all subsequent calls to
// mapUserPage will use this new L0 table until a new process is created.
//
//go:nosplit
func CreateProcessPageTable() uintptr {
	if !pagingInitialized {
		InitPaging()
	}

	// Allocate a new L0 page table
	l0VA := allocPTPage()
	if l0VA == 0 {
		return 0
	}

	// Get the physical address using simple linear map arithmetic.
	// walkPageTable() has issues with 2MB block descriptors in the linear map,
	// but since allocPTPage() uses VA = PA + KernelVAOffset, the reverse is trivial.
	l0PA := vaToPa(l0VA)

	if l0PA == 0 {
		return 0
	}

	// Cache the PA -> VA mapping
	cachePTVA(l0PA, l0VA)

	// NOTE: We no longer set a global processL0PA here.
	// The caller must store this L0PA in the Thread struct and use
	// SwitchTTBR0WithASID() to activate it when switching to this process.

	return l0PA
}

// NOTE: GetProcessL0PA() and SwitchToProcessPageTable() were removed.
// They used a global processL0PA which caused race conditions with multiple priests.
// Use SwitchToProcessPageTableWithL0() or SwitchTTBR0WithASID() with explicit L0PA.
// For reading the current L0PA, use readTTBR0EL1() and mask out the ASID.

// SwitchToProcessPageTableWithL0 switches TTBR0 to the specified L0 page table.
// This should be called before ERET to userspace.
// Using an explicit l0PA parameter avoids race conditions with multiple processes.
// Page fault handlers now read from TTBR0_EL1 directly instead of a global.
//
//go:nosplit
func SwitchToProcessPageTableWithL0(l0PA uintptr) {
	if l0PA == 0 {
		return
	}

	// Linux-style TTBR0 switch sequence (from Linux context.c)

	// 1. Full memory barrier before any page table operations
	//    Ensures all prior memory writes (including PTEs) are visible
	dsbISH()

	// 2. Write new TTBR0 value (ASID=0, just the physical address)
	writeTTBR0Asm(uint64(l0PA))

	// 3. Invalidate all TLB entries (Inner Shareable for multi-core safety)
	//    This must be AFTER the TTBR0 write so we invalidate entries
	//    for the NEW address space, not the old one
	tlbiVMALLE1IS()

	// 4. Barrier to ensure TLB invalidation completes
	dsbISH()

	// 5. Instruction synchronization to clear pipeline
	isbSY()
}

// SwitchTTBR0ToPA switches TTBR0 to the specified physical address with ASID=0.
// DEPRECATED: Use SwitchTTBR0WithASID for proper ASID handling.
// This is used for context switching between threads with different page tables.
// Performs full TLB invalidation to ensure new mappings take effect.
// CRITICAL: Also updates processL0PA so page fault handlers use the correct page table.
//
//go:nosplit
func SwitchTTBR0ToPA(l0PA uintptr) {
	SwitchTTBR0WithASID(l0PA, 0)
}

// SwitchTTBR0WithASID switches TTBR0 to the specified physical address with ASID.
// ASID (Address Space Identifier) allows TLB entries from different processes to
// coexist, avoiding full TLB flush on every context switch.
//
// On ARM64, TTBR0 format is:
//   Bits [63:48]: ASID (16-bit)
//   Bits [47:1]:  Physical address of page table
//   Bit [0]:      CnP (Common not Private) - we use 0
//
// Page fault handlers read TTBR0_EL1 directly to get the current L0PA,
// so no global state update is needed here.
//
//go:nosplit
func SwitchTTBR0WithASID(l0PA uintptr, asid uint16) {
	if l0PA == 0 {
		return // No page table to switch to
	}

	// Encode ASID in upper 16 bits of TTBR0
	// PA must be in bits [47:1], ASID in bits [63:48]
	ttbr0Val := (uint64(asid) << 48) | uint64(l0PA)

	// Linux-style TTBR0 switch sequence
	dsbISH()                // Memory barrier before page table operations
	writeTTBR0Asm(ttbr0Val) // Write new TTBR0 value with ASID

	// DEBUG: Force TLB flush to diagnose ASID issues
	// This should not be necessary with correct ASID handling, but helps diagnose
	tlbiVMALLE1IS() // Invalidate all TLB entries

	dsbISH() // Barrier to ensure TTBR0 write and TLB flush complete
	isbSY()  // Instruction barrier to ensure new translations take effect
}

// paToVA converts a physical address to a virtual address using identity mapping.
// Cardinal maps page tables with VA = PA + KernelVAOffset.
//
//go:nosplit
func paToVA(pa uintptr) uintptr {
	return pa + constants.KernelMMIOOffset
}

// cachePTVA stores a PA -> VA mapping for an allocated page table.
//
//go:nosplit
func cachePTVA(pa, va uintptr) {
	if ptVACacheSize < len(ptVACache) {
		ptVACache[ptVACacheSize] = ptVACacheEntry{pa: pa, va: va}
		ptVACacheSize++
	} else {
		// CACHE FULL - this could cause issues!
		uartPuts("[kmem] WARN: ptVACache FULL!\r\n")
	}
}

// lookupPTVA looks up the VA for a given PA in the cache.
// Returns 0 if not found.
//
//go:nosplit
func lookupPTVA(pa uintptr) uintptr {
	for i := 0; i < ptVACacheSize; i++ {
		if ptVACache[i].pa == pa {
			return ptVACache[i].va
		}
	}
	return 0
}

// GetPTVACacheStats returns the current ptVACache usage stats.
//
//go:nosplit
func GetPTVACacheStats() (used, capacity int) {
	return ptVACacheSize, len(ptVACache)
}

// paToVAOrCache converts a PA to VA, checking the cache first for PT pool pages.
// Falls back to paToVA if not in cache (for pre-mapped pages).
//
//go:nosplit
func paToVAOrCache(pa uintptr) uintptr {
	if va := lookupPTVA(pa); va != 0 {
		return va
	}
	return paToVA(pa)
}

// vaToPa converts a high-memory virtual address to a physical address.
// For identity-mapped regions, PA = VA - KernelVAOffset.
//
//go:nosplit
func vaToPa(va uintptr) uintptr {
	return va - constants.KernelMMIOOffset
}

// GetPTPoolStats returns the current PT pool allocation state.
// Now uses the unified pool accounting for kernel PT pages.
//
//go:nosplit
func GetPTPoolStats() (allocatedPages, totalPages uint64, nextVA, endVA uintptr) {
	stats := GetPoolStats()
	// Return kernel PT pages from unified pool accounting
	// Total and remaining are from the entire pool
	return stats.KernelPTPages, stats.TotalPages, 0, 0
}

// allocPTPage allocates a page from the unified pool for a new L2 or L3 table.
// Returns the VA of the allocated page (mapped via kernel VA offset).
//
//go:nosplit
func allocPTPage() uintptr {
	// Allocate from unified pool (tracks as kernel PT page)
	pa := AllocPage(PageKernelPT)
	if pa == 0 {
		uartPuts("[kmem] PT OOM!\r\n")
		return 0
	}

	// Convert PA to VA using kernel VA offset (use constant to avoid deep config chain)
	va := pa + constants.KernelMMIOOffset

	// Zero the page
	ptr := (*[512]uint64)(unsafe.Pointer(va))
	for i := 0; i < 512; i++ {
		ptr[i] = 0
	}

	// Queue deferred record for bottom-half page tracking
	QueueDeferredRecord(DeferredPageRecord{
		PA:       pa,
		VA:       va,
		Type:     PageAllocKernelPT,
		PriestID: -1,
		ThreadID: -1,
		Order:    0,
	})

	return va
}

// WalkPageTable translates a VA to PA by walking the page tables.
// This works even for non-identity-mapped addresses.
// CRITICAL: This is needed because the PT pool VAs are NOT identity-mapped!
// They were mapped by cardinal to different PAs.
// Exported for use by device drivers (VirtIO) to get actual physical addresses.
//
//go:nosplit
func WalkPageTable(va uintptr) uintptr {
	return walkPageTable(va)
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

	// L0 table
	l0PA := ttbr1L0PA
	l0VA := l0PA + constants.KernelMMIOOffset
	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	if (l0Entry & PTE_VALID) == 0 {
		return 0
	}

	// L1 table
	l1PA := uintptr(l0Entry & PTE_ADDR_MASK)
	l1VA := l1PA + constants.KernelMMIOOffset
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if (l1Entry & PTE_VALID) == 0 {
		return 0
	}

	// L2 table
	l2PA := uintptr(l1Entry & PTE_ADDR_MASK)
	l2VA := l2PA + constants.KernelMMIOOffset
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if (l2Entry & PTE_VALID) == 0 {
		return 0
	}

	// Check if L2 entry is a block descriptor (2MB) or table pointer
	// Block: bits[1:0] = 01, Table: bits[1:0] = 11
	if (l2Entry & 0x2) == 0 {
		// L2 block descriptor (2MB) - extract PA from block address + page offset
		blockPA := uintptr(l2Entry & PTE_ADDR_MASK)
		pageOffset := va & ((1 << L2Shift) - 1) // offset within 2MB block
		return blockPA | pageOffset
	}

	// L3 table (L2 is a table pointer)
	l3PA := uintptr(l2Entry & PTE_ADDR_MASK)
	l3VA := l3PA + constants.KernelMMIOOffset
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
	debugPrint('G')
	// DEBUG: Breadcrumb H = HandlePageFault entry
	debugPrint('H')

	// Lazy initialization - call InitPaging to read config and set up TTBR0/TTBR1
	if !pagingInitialized {
		debugPrint('I') // DEBUG: Init path
		InitPaging()
		debugPrint('i') // DEBUG: InitPaging done
	}

	debugPrint('5') // DEBUG: After init

	// Check if fault is in manageable range
	// Use constants directly to avoid deep config chain in nosplit context
	kmazarinVAStart := uintptr(constants.KernelTextBase)
	kmazarinVAEnd := kmazarinVAStart + uintptr(getKmazarinSize())
	heapStart := uintptr(constants.KernelHeapStart)
	heapEnd := uintptr(constants.KernelHeapEnd)
	debugPrint('7') // DEBUG: Calculated ranges

	debugPrint('8') // DEBUG: Before stack checks

	// Check if fault is in stack regions (should be pre-mapped by Cardinal!)
	g0StackBottom := uintptr(constants.KernelG0StackBottom)
	g0StackTop := uintptr(constants.KernelG0StackTop)
	excStackBottom := uintptr(constants.KernelExcStackBottom)
	excStackTop := uintptr(constants.KernelExcStackTop)

	debugPrint('9') // DEBUG: Stack vars calculated

	if faultAddr >= g0StackBottom && faultAddr < g0StackTop {
		// Fault in g0 stack region - should not happen!
		debugPrint('S')
		debugPrint('0')
		debugPrint('!')
		debugPrint('[')
		debugPrintHex(uint64(faultAddr))
		debugPrint(']')
		return false
	}
	debugPrint('a') // DEBUG: passed g0 stack check

	if faultAddr >= excStackBottom && faultAddr < excStackTop {
		// Fault in exception stack region - should not happen!
		debugPrint('S')
		debugPrint('1')
		debugPrint('!')
		debugPrint('[')
		debugPrintHex(uint64(faultAddr))
		debugPrint(']')
		return false
	}
	debugPrint('b') // DEBUG: passed exc stack check

	// Check if fault is in valid range: either in kmazarin binary OR in heap
	// These ranges may not be contiguous (heap can be in lower TTBR1 space)
	inKmazarin := faultAddr >= kmazarinVAStart && faultAddr < kmazarinVAEnd
	inHeap := faultAddr >= heapStart && faultAddr < heapEnd
	if !inKmazarin && !inHeap {
		// DEBUG: R = Range check failed, print fault address
		debugPrint('R')
		debugPrint('!')
		debugPrint('[')
		debugPrintHex(uint64(faultAddr))
		debugPrint(']')
		return false
	}
	debugPrint('c') // DEBUG: passed range check

	// Align to page boundary
	pageAddr := faultAddr &^ (PageSize - 1)
	debugPrint('d') // DEBUG: aligned

	// Allocate a physical frame
	frame := AllocKernelFrame()
	if frame == 0 {
		// DEBUG: A = Alloc failed
		debugPrint('A')
		debugPrint('!')
		return false
	}

	debugPrint('e') // DEBUG: about to map

	// Map the page FIRST (before zeroing!)
	// ZeroFrame accesses the VA, so the page must be mapped first.
	if !mapPage(pageAddr, frame) {
		// DEBUG: M = Map failed
		debugPrint('M')
		debugPrint('!')
		return false
	}
	debugPrint('m') // DEBUG: mapped

	// Zero the frame using the just-mapped VA (pageAddr), NOT via physAddr + KernelVAOffset!
	// The frame pool physical memory isn't mapped at PA + KernelVAOffset.
	// But pageAddr was just mapped to the physical frame, so we can zero via pageAddr.
	debugPrint('Z') // DEBUG: about to zero
	debugPrint('[')
	debugPrintHex(uint64(pageAddr))
	debugPrint(']')
	debugPrint('>')
	debugPrintHex(uint64(frame))
	debugPrint('<')

	// Extra barrier before accessing newly-mapped memory
	dsbSY()
	isbSY()
	debugPrint('B') // DEBUG: barriers done

	// SKIP zeroing for now to test if mapping works
	// The Go runtime will access the page and we'll see if it works
	debugPrint('S') // DEBUG: skipping zero, returning success
	_ = pageAddr // suppress unused warning

	// Queue deferred record for bottom-half page tracking
	QueueDeferredRecord(DeferredPageRecord{
		PA:       frame,
		VA:       pageAddr,
		Type:     PageAllocKernelHeap,
		PriestID: -1,
		ThreadID: -1,
		Order:    0,
	})

	return true
}

// Userspace mmap region constants (must match ksyscall/mmap.go)
const (
	userMmapStart = 0x00400000           // 4MB - above ELF load region
	userMmapEnd   = 0x0000700000000000   // 112TB - plenty of VA space
)

// HandleUserPageFault handles a page fault at a userspace virtual address.
// This is called from the EL0 exception handler for data aborts in userspace.
// Returns true if the fault was handled successfully, false otherwise.
//
// For userspace mmap addresses, this function:
// 1. Validates the address was actually allocated by mmap
// 2. Allocates a physical frame from the USERSPACE frame pool
// 3. Allocates L2/L3 tables if needed
// 4. Maps the VA to the PA with EL0-accessible permissions
//
//go:nosplit
func HandleUserPageFault(faultAddr uintptr) bool {
	// Lazy initialization
	if !pagingInitialized {
		InitPaging()
	}

	// Get the current mmap allocation end (addresses >= this were NOT allocated)
	allocEnd := getUserMmapAllocEnd()

	// Userspace has three valid VA regions:
	// 1. MAP_FIXED region: 0x10000 to userMmapStart (ELF, thread stacks, etc.)
	// 2. Bump-allocated region: userMmapStart to allocEnd
	// 3. Hint-based allocations: tracked in the span list (can be anywhere in userspace range)
	//
	// Accept faults in any of these regions
	const minUserAddr = 0x10000 // Minimum userspace address (64KB, above NULL guard)
	inMapFixedRegion := uint64(faultAddr) >= minUserAddr && uint64(faultAddr) < userMmapStart
	inBumpRegion := uint64(faultAddr) >= userMmapStart && uint64(faultAddr) < allocEnd
	inSpanRegion := isAddressInSpan(uint64(faultAddr))

	if !inMapFixedRegion && !inBumpRegion && !inSpanRegion {
		return false
	}

	// Align to page boundary
	pageAddr := faultAddr &^ (PageSize - 1)

	// IMPORTANT: Check if the page is already mapped.
	// This can happen when multiple allocations (bump + MAP_FIXED) overlap.
	// If already mapped, just return success - no need to allocate a new frame.
	existingPA := WalkUserPageTable(faultAddr)
	if existingPA != 0 {
		// Page is already mapped - this is a TLB miss, not a true page fault
		// Just invalidate TLB and return success
		tlbiVAE1IS(pageAddr)
		dsbSY()
		isbSY()
		return true
	}

	// Allocate a physical frame from userspace pool
	framePA := AllocUserFrame()
	if framePA == 0 {
		return false
	}

	// Map the page with RW permissions for userspace (ELF_PF_R | ELF_PF_W)
	// Use flags that allow EL0 access
	elfFlags := uint32(ELF_PF_R | ELF_PF_W) // Read + Write

	// CRITICAL: Get the current process's L0PA from TTBR0_EL1, NOT from the global processL0PA.
	// The global can be stale if multiple processes are running. TTBR0 always contains
	// the correct page table for the currently executing process.
	// TTBR0 format: bits [63:48] = ASID, bits [47:1] = PA, bit [0] = CnP
	ttbr0 := readTTBR0EL1()
	currentL0PA := uintptr(ttbr0 & 0x0000FFFFFFFFFFFF) // Mask out ASID

	if !mapUserPageWithL0(pageAddr, framePA, elfFlags, currentL0PA) {
		return false
	}

	// Zero the page using kernel scratch mapping
	// CRITICAL: We MUST zero the page before giving it to userspace!
	// If we can't map the scratch VA, the page fault MUST fail.
	scratchVA := MapPAToKernelScratch(framePA)
	if scratchVA == 0 {
		return false // FAIL - can't give uninitialized page to userspace
	}
	zeroPageSlow(scratchVA)

	// CRITICAL SYNCHRONIZATION SEQUENCE:
	// 1. Clean the data cache for the entire page to push zeros to memory
	CleanPageCache(scratchVA)

	// 2. Full DSB to ensure all cache operations complete
	dsbSY()

	// 3. Invalidate ALL TLB entries for this address space
	tlbiVMALLE1IS()
	dsbSY()

	// 4. ISB to synchronize the instruction stream
	isbSY()

	// Queue deferred record for bottom-half page tracking
	QueueDeferredRecord(DeferredPageRecord{
		PA:       framePA,
		VA:       pageAddr,
		Type:     PageAllocUser,
		PriestID: -1, // TODO: get current priest ID
		ThreadID: -1, // TODO: get current thread ID
		Order:    0,
	})

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
	debugPrint('[')
	debugPrint('I')
	debugPrintHex(uint64(l1Idx))
	debugPrint('/')
	debugPrintHex(uint64(l2Idx))
	debugPrint('/')
	debugPrintHex(uint64(l3Idx))
	debugPrint(']')

	// Determine which translation table to use based on VA bit 55
	// Bit 55 = 0 -> TTBR0 (user space), Bit 55 = 1 -> TTBR1 (kernel space)
	var l0PA uintptr
	if (va>>55)&1 == 0 {
		// TTBR0 (user space / heap)
		debugPrint('T')
		debugPrint('0')
		l0PA = ttbr0L0PA
		debugPrint('{')
		debugPrintHex(uint64(l0PA))
		debugPrint('}')
	} else {
		// TTBR1 (kernel space)
		debugPrint('T')
		debugPrint('1')
		l0PA = ttbr1L0PA
	}

	// Get L0 table VA
	l0VA := paToVA(l0PA)
	debugPrint('V')
	debugPrintHex(uint64(l0VA))
	if l0VA == 0 {
		debugPrint('0')
		debugPrint('!')
		return false
	}

	// Calculate L0 entry address and print for debug
	l0EntryAddr := l0VA + l0Idx*8
	debugPrint('E')
	debugPrintHex(uint64(l0EntryAddr))

	// Read L0 entry
	debugPrint('R')
	l0Entry := (*uint64)(unsafe.Pointer(l0EntryAddr))

	// DEBUG: Print L0 entry value
	debugPrint('(')
	debugPrintHex(*l0Entry)
	debugPrint(')')

	var l1VA uintptr

	if (*l0Entry & PTE_VALID) == 0 {
		// Need to allocate L1 table (new for expanded VA space support)
		debugPrint('L')
		debugPrint('1')
		debugPrint('N') // DEBUG: New L1 table needed

		l1VA = allocPTPage()
		if l1VA == 0 {
			debugPrint('1')
			debugPrint('a')
			debugPrint('!')
			return false
		}

		// Get physical address of new L1 table
		l1PA := walkPageTable(l1VA)
		if l1PA == 0 {
			debugPrint('1')
			debugPrint('w')
			debugPrint('!')
			return false
		}

		// Cache the VA for this PA so we can find it later
		cachePTVA(l1PA, l1VA)

		// Link new L1 table into L0
		*l0Entry = uint64(l1PA) | PTE_VALID | PTE_TABLE

		// Cache clean and barriers for L0 update
		dcCIVAC(uintptr(unsafe.Pointer(l0Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		// Get existing L1 table VA - check cache first for PT pool pages
		l1PA := uintptr(*l0Entry & PTE_ADDR_MASK)
		l1VA = paToVAOrCache(l1PA)
	}
	if l1VA == 0 {
		debugPrint('2')
		debugPrint('!')
		return false
	}

	// Read L1 entry
	l1Entry := (*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	// DEBUG: Print L1 entry value
	debugPrint('L')
	debugPrint('1')
	debugPrint('=')
	debugPrintHex(*l1Entry)
	debugPrint('|')

	var l2VA uintptr

	if (*l1Entry & PTE_VALID) == 0 {
		// Need to allocate L2 table
		l2VA = allocPTPage()
		if l2VA == 0 {
			debugPrint('3')
			debugPrint('!')
			return false
		}

		// CRITICAL FIX: Use walkPageTable instead of vaToPa!
		l2PA := walkPageTable(l2VA)
		if l2PA == 0 {
			debugPrint('w')
			debugPrint('!')
			return false
		}

		// Cache the VA for this PA
		cachePTVA(l2PA, l2VA)

		// Link new L2 table into L1
		*l1Entry = uint64(l2PA) | PTE_VALID | PTE_TABLE
	} else {
		l2PA := uintptr(*l1Entry & PTE_ADDR_MASK)
		l2VA = paToVAOrCache(l2PA)
		if l2VA == 0 {
			debugPrint('4')
			debugPrint('!')
			return false
		}
	}

	// Read L2 entry
	l2Entry := (*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	// DEBUG: Print L2 entry value before modification
	debugPrint('L')
	debugPrint('2')
	debugPrint('=')
	debugPrintHex(*l2Entry)
	debugPrint('|')

	var l3VA uintptr

	if (*l2Entry & PTE_VALID) == 0 {
		debugPrint('N') // DEBUG: New L3 table needed
		// Need to allocate L3 table
		l3VA = allocPTPage()
		if l3VA == 0 {
			debugPrint('5')
			debugPrint('!')
			return false
		}

		// CRITICAL FIX: Use walkPageTable instead of vaToPa!
		// PT pool VAs are NOT identity-mapped. They map to different PAs
		// that were allocated by cardinal. We must walk the page tables
		// to find the actual PA.
		l3PA := walkPageTable(l3VA)
		if l3PA == 0 {
			debugPrint('W')
			debugPrint('!')
			return false
		}
		// DEBUG: Print both VAs and PAs
		debugPrint('{')
		debugPrintHex(uint64(l3VA))
		debugPrint(':')
		debugPrintHex(uint64(l3PA))
		debugPrint('}')

		// Cache the VA for this PA
		cachePTVA(l3PA, l3VA)

		// Link new L3 table into L2
		*l2Entry = uint64(l3PA) | PTE_VALID | PTE_TABLE

		// DEBUG: Print new L2 entry value
		debugPrint('L')
		debugPrint('2')
		debugPrint('N')
		debugPrint('=')
		debugPrintHex(*l2Entry)
		debugPrint('|')

		// Clean cache for L2 entry so hardware page walker can see it
		dcCIVAC(uintptr(unsafe.Pointer(l2Entry)))
		dsbSY()

		// Verify L2 entry readback
		readback := *l2Entry
		if readback != uint64(l3PA)|PTE_VALID|PTE_TABLE {
			debugPrint('V')
			debugPrint('!')
			debugPrintHex(readback)
			return false
		}
		debugPrint('V') // DEBUG: Verified L2
	} else {
		l3PA := uintptr(*l2Entry & PTE_ADDR_MASK)
		l3VA = paToVAOrCache(l3PA)
		if l3VA == 0 {
			debugPrint('6')
			debugPrint('!')
			return false
		}
	}

	// Write L3 entry
	l3Entry := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	pteValue := uint64(pa) | PTE_VALID | PTE_TABLE | PTE_AF |
		PTE_ATTR_NORMAL | PTE_AP_RW_EL1 | PTE_SH_INNER | PTE_EXEC_NEVER
	*l3Entry = pteValue

	// DEBUG: Print L3 entry details
	debugPrint('L')
	debugPrint('3')
	debugPrint('@')
	debugPrintHex(uint64(l3VA + l3Idx*8))
	debugPrint('=')
	debugPrintHex(pteValue)
	debugPrint('|')

	// Clean cache for L3 entry so hardware page walker can see it
	dcCIVAC(uintptr(unsafe.Pointer(l3Entry)))
	dsbSY()
	debugPrint('C') // DEBUG: Cache cleaned

	// Verify L3 entry readback
	l3Readback := *l3Entry
	if l3Readback != pteValue {
		debugPrint('X')
		debugPrint('!')
		return false
	}
	debugPrint('X') // DEBUG: Verified L3

	// Memory barriers and TLB invalidate
	tlbiVAE1IS(va)
	dsbSY()
	isbSY()

	debugPrint('T') // DEBUG: TLB invalidated

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

// CleanPageCache cleans and invalidates the data cache for an entire page.
// This ensures that all writes to the page are visible to other observers
// (e.g., userspace reading via a different virtual address mapping).
// The va must be the kernel's scratch VA for the page.
//
//go:nosplit
func CleanPageCache(va uintptr) {
	// ARM64 cache line size is typically 64 bytes
	// 4KB page = 64 cache lines
	const cacheLineSize = 64
	const linesPerPage = PageSize / cacheLineSize

	for i := uintptr(0); i < linesPerPage; i++ {
		dcCIVAC(va + i*cacheLineSize)
	}
	dsbSY()
}

// SyncExecutablePage synchronizes the data and instruction caches for a code page.
// This MUST be called after writing code to memory before executing it.
// The kernel writes via the scratch VA but instructions will be fetched via userspace VA.
//
// ARM64 requires this sequence for self-modifying code / loaded code:
// 1. DC CVAU - Clean data cache to Point of Unification (where I/D caches meet)
// 2. DSB ISH - Ensure clean completes before invalidate
// 3. IC IVAU - Invalidate instruction cache line
// 4. DSB ISH - Ensure invalidate completes
// 5. ISB - Synchronize instruction stream
//
// The scratchVA is the kernel's VA used for writing.
// The userVA is the userspace VA that will be used for instruction fetch.
//
//go:nosplit
func SyncExecutablePage(scratchVA, userVA uintptr) {
	const cacheLineSize = 64
	const linesPerPage = PageSize / cacheLineSize

	// Clean the data cache using the scratch VA (where we wrote the data)
	for i := uintptr(0); i < linesPerPage; i++ {
		dcCVAUAsm(scratchVA + i*cacheLineSize)
	}
	dsbSY()

	// Invalidate the instruction cache using the userspace VA (where code will execute)
	for i := uintptr(0); i < linesPerPage; i++ {
		icIVAUAsm(userVA + i*cacheLineSize)
	}
	dsbSY()
	isbSY()
}

// InvalidateAllICache invalidates the entire instruction cache.
// This is a more aggressive invalidation than per-VA invalidation.
// Call this after loading all executable code, before jumping to userspace.
//
//go:nosplit
func InvalidateAllICache() {
	dsbSY()
	icIALLUAsm()
	dsbSY()
	isbSY()
}

// FinalUserspaceSync performs comprehensive cache and TLB maintenance
// before transitioning to userspace. This is the nuclear option to ensure
// all kernel writes are visible to userspace.
//
//go:nosplit
func FinalUserspaceSync() {
	// 1. Data synchronization barrier - ensure all prior stores complete
	dsbSY()

	// 2. Invalidate entire TLB for this VMID (TTBR0 mappings)
	// This ensures no stale TLB entries exist
	tlbiVMALLE1()
	dsbSY()

	// 3. Clean and Invalidate entire D-cache by Set/Way
	// This is aggressive but GUARANTEES all written data is visible to userspace
	dcCleanInvalidateAll()
	dsbSY()

	// 4. Invalidate entire instruction cache
	icIALLUAsm()
	dsbSY()

	// 5. Instruction synchronization barrier - synchronize context
	isbSY()
}

// dcCleanInvalidateAll cleans and invalidates the entire D-cache
// Implemented in asm_barriers_arm64.s
func dcCleanInvalidateAll()

// tlbiVMALLE1 invalidates all TLB entries for EL1&0 translation regime
// This is implemented in asm_barriers_arm64.s
func tlbiVMALLE1()
func tlbiVMALLE1IS() // Inner Shareable version - broadcasts to all CPUs
func dsbISH()        // DSB Inner Shareable

// These are implemented in asm_barriers_arm64.s
func dsbSYAsm()
func tlbiVAE1ISAsm(va uintptr)
func isbSYAsm()
func dcCIVACAsm(va uintptr)
func dcCVAUAsm(va uintptr)
func icIVAUAsm(va uintptr)
func icIALLUAsm()
func readTTBR0EL1() uintptr
func readTTBR1EL1() uintptr
func dcZVAAsm(addr uintptr)
func bzero4KAsm(ptr uintptr)
func writeTTBR0Asm(val uint64)
func atS1E0R(va uintptr) uint64 // Hardware address translation EL0 read
func tlbiASIDE1ISAsm(asid uint16) // Invalidate TLB by ASID (inner shareable)

// Bzero4K zeros a 4KB page using DC ZVA for maximum performance.
// ptr must be a valid virtual address that is page-aligned.
// This is the kernel's fast page zeroing function.
//
//go:nosplit
func Bzero4K(ptr uintptr) {
	bzero4KAsm(ptr)
}

// TlbiVMALLE1 invalidates all TLB entries for EL1&0 translation regime.
// Exported wrapper for use from other packages.
//
//go:nosplit
func TlbiVMALLE1() {
	tlbiVMALLE1()
}

// TlbiASIDE1IS invalidates all TLB entries for a specific ASID (inner shareable).
// This broadcasts the invalidation to all CPUs in the inner shareable domain.
// Used for aggressive ASID reuse: when a priest exits and its ASID will be
// reused by a new priest, all old TLB entries must be invalidated first.
//
//go:nosplit
func TlbiASIDE1IS(asid uint16) {
	dsbISH()              // Ensure all prior memory ops complete
	tlbiASIDE1ISAsm(asid) // Invalidate TLB entries for this ASID
	dsbISH()              // Ensure TLBI completes
	isbSY()               // Synchronize instruction stream
}

// DsbISH performs a DSB Inner Shareable barrier.
// Exported wrapper for use from other packages.
//
//go:nosplit
func DsbISH() {
	dsbISH()
}

// IsbSY performs an ISB barrier.
// Exported wrapper for use from other packages.
//
//go:nosplit
func IsbSY() {
	isbSY()
}

// zeroPageSlow zeros a 4KB page using regular stores.
// This is slower than Bzero4K but more reliable for debugging.
//
//go:nosplit
func zeroPageSlow(ptr uintptr) {
	// Zero 4KB in 8-byte chunks (512 iterations)
	p := (*[512]uint64)(unsafe.Pointer(ptr))
	for i := 0; i < 512; i++ {
		p[i] = 0
	}
}

// WriteTTBR0 writes a new value to TTBR0_EL1.
// Used for switching between priest page tables.
// val should be (asid << 48) | l0_physical_address
//
//go:nosplit
func WriteTTBR0(val uint64) {
	writeTTBR0Asm(val)
	isbSY()
}

// ReadHWTTBR0 reads the actual hardware TTBR0_EL1 register value.
// This is useful for debugging to verify the page table root matches expected.
//
//go:nosplit
func ReadHWTTBR0() uintptr {
	return readTTBR0EL1()
}

// MapDeviceMMIO maps a physical MMIO region to the corresponding high-memory
// kernel virtual address with device memory attributes.
//
// This is used by device drivers during initialization to map device registers
// before accessing them. The physical address and size typically come from DTB parsing.
//
// Example:
//
//	reg := node.Reg[0]  // From DTB
//	if err := kmem.MapDeviceMMIO(reg.Address, reg.Size); err != nil {
//	    return err
//	}
//	// Now can access via reg.Address + KernelVAOffset
//
// Returns nil on success, error on failure.
func MapDeviceMMIO(physAddr uintptr, size uint64) error {
	// Lazy initialization
	if !pagingInitialized {
		InitPaging()
	}

	// Calculate number of pages needed (round up)
	if size == 0 {
		size = PageSize // Default to one page if size not specified
	}
	numPages := (size + PageSize - 1) / PageSize

	// Map all pages in the region
	for i := uint64(0); i < numPages; i++ {
		pagePhys := (physAddr &^ (PageSize - 1)) + uintptr(i*PageSize)
		pageVA := pagePhys + constants.KernelMMIOOffset

		if !mapDevicePage(pageVA, pagePhys) {
			return &MappingError{addr: physAddr + uintptr(i*PageSize), msg: "failed to map device page"}
		}
	}

	return nil
}

// MappingError represents a page mapping failure
type MappingError struct {
	addr uintptr
	msg  string
}

func (e *MappingError) Error() string {
	return e.msg
}

// mapDevicePage maps a VA to PA with device memory attributes.
// Similar to mapPage but uses PTE_ATTR_DEVICE instead of PTE_ATTR_NORMAL.
//
//go:nosplit
func mapDevicePage(va, pa uintptr) bool {
	// Extract indices
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	// Use TTBR1 for kernel high memory (bit 55 = 1)
	if (va>>55)&1 == 0 {
		return false // Device MMIO must be in kernel space
	}
	l0PA := ttbr1L0PA

	// Get L0 table VA
	l0VA := paToVA(l0PA)
	if l0VA == 0 {
		return false
	}

	// Read L0 entry
	l0Entry := (*uint64)(unsafe.Pointer(l0VA + l0Idx*8))

	var l1VA uintptr
	if (*l0Entry & PTE_VALID) == 0 {
		// Need to allocate L1 table
		l1VA = allocPTPage()
		if l1VA == 0 {
			return false
		}
		l1PA := walkPageTable(l1VA)
		if l1PA == 0 {
			return false
		}
		cachePTVA(l1PA, l1VA)
		*l0Entry = uint64(l1PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l0Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		l1PA := uintptr(*l0Entry & PTE_ADDR_MASK)
		l1VA = paToVAOrCache(l1PA)
	}
	if l1VA == 0 {
		return false
	}

	// Read L1 entry
	l1Entry := (*uint64)(unsafe.Pointer(l1VA + l1Idx*8))

	var l2VA uintptr
	if (*l1Entry & PTE_VALID) == 0 {
		l2VA = allocPTPage()
		if l2VA == 0 {
			return false
		}
		l2PA := walkPageTable(l2VA)
		if l2PA == 0 {
			return false
		}
		cachePTVA(l2PA, l2VA)
		*l1Entry = uint64(l2PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l1Entry)))
		dsbSY()
	} else {
		l2PA := uintptr(*l1Entry & PTE_ADDR_MASK)
		l2VA = paToVAOrCache(l2PA)
		if l2VA == 0 {
			return false
		}
	}

	// Read L2 entry
	l2Entry := (*uint64)(unsafe.Pointer(l2VA + l2Idx*8))

	var l3VA uintptr
	if (*l2Entry & PTE_VALID) == 0 {
		// No L2 entry — allocate a fresh L3 table
		l3VA = allocPTPage()
		if l3VA == 0 {
			return false
		}
		l3PA := walkPageTable(l3VA)
		if l3PA == 0 {
			return false
		}
		cachePTVA(l3PA, l3VA)
		*l2Entry = uint64(l3PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l2Entry)))
		dsbSY()
	} else if (*l2Entry & 0x2) == 0 {
		// L2 is a 2MB block descriptor — must split into 512 L3 page entries.
		// Extract the block's base PA and attributes.
		blockPA := uintptr(*l2Entry & PTE_ADDR_MASK)
		blockAttrs := *l2Entry & ^uint64(PTE_ADDR_MASK) // preserve attribute bits

		// Allocate an L3 page table page
		l3VA = allocPTPage()
		if l3VA == 0 {
			return false
		}
		l3PA := walkPageTable(l3VA)
		if l3PA == 0 {
			return false
		}
		cachePTVA(l3PA, l3VA)

		// Fill all 512 L3 entries replicating the block mapping at 4KB granularity.
		// L3 page descriptors use bit[1]=1 (PTE_TABLE) unlike L2 blocks.
		for i := uintptr(0); i < 512; i++ {
			entryPA := blockPA + i*PageSize
			// Use block attrs but set bit 1 for L3 page descriptor
			l3E := (uint64(entryPA) & PTE_ADDR_MASK) | (blockAttrs | PTE_TABLE)
			*(*uint64)(unsafe.Pointer(l3VA + i*8)) = l3E
		}

		// Overwrite L2 from block → table pointer
		*l2Entry = uint64(l3PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l2Entry)))
		// Flush all L3 entries
		for i := uintptr(0); i < 512; i++ {
			dcCIVAC(l3VA + i*8)
		}
		dsbSY()
		// Invalidate entire TLB — the 2MB block covered many pages
		tlbiVMALLE1IS()
		dsbSY()
		isbSY()
	} else {
		// L2 is already a table pointer — look up L3 table
		l3PA := uintptr(*l2Entry & PTE_ADDR_MASK)
		l3VA = paToVAOrCache(l3PA)
		if l3VA == 0 {
			return false
		}
	}

	// Write L3 entry with DEVICE attributes (not NORMAL).
	// Overwrite even if already mapped — the existing mapping may have
	// Normal-Cacheable attributes from the linear map / demand paging.
	l3Entry := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))

	if (*l3Entry & PTE_VALID) != 0 {
		existingPA := uintptr(*l3Entry & PTE_ADDR_MASK)
		if existingPA != pa {
			return false // PA conflict
		}
	}

	// Device memory: non-cacheable, non-gathering, non-reordering
	pteValue := uint64(pa) | PTE_VALID | PTE_TABLE | PTE_AF |
		PTE_ATTR_DEVICE | PTE_AP_RW_EL1 | PTE_EXEC_NEVER
	*l3Entry = pteValue

	// Clean cache and invalidate TLB
	dcCIVAC(uintptr(unsafe.Pointer(l3Entry)))
	dsbSY()
	tlbiVAE1IS(va)
	dsbSY()
	isbSY()

	return true
}

// ELF permission flags (from ksyscall/launch.go)
const (
	ELF_PF_X = 1 // Executable
	ELF_PF_W = 2 // Writable
	ELF_PF_R = 4 // Readable
)

// MapUserPage allocates a physical frame and maps it to a virtual address
// in userspace (TTBR0, low memory) with the specified ELF permissions.
//
// This is used for loading userspace programs (priests).
// The permissions are derived from ELF program header flags.
//
// Returns nil on success, error on failure.
func MapUserPage(va uintptr, elfFlags uint32) error {
	_, err := MapUserPageWithPA(va, elfFlags)
	return err
}

// MapUserPageWithPA allocates a physical frame from the USERSPACE frame pool
// and maps it to a virtual address in userspace (TTBR0, low memory) with the
// specified ELF permissions.
// Returns the physical address of the allocated frame for kernel-space access.
//
// The caller can use MapPAToKernelScratch() to get a kernel-accessible VA
// for copying data to the userspace page.
//
// IMPORTANT: Uses AllocUserFrame() which allocates from the userspace frame pool,
// NOT the kernel frame pool. This keeps userspace memory completely separate.
func MapUserPageWithPA(va uintptr, elfFlags uint32) (uintptr, error) {
	// Lazy initialization
	if !pagingInitialized {
		InitPaging()
	}

	// Allocate a physical frame from userspace pool (NOT kernel pool!)
	framePA := AllocUserFrame()
	if framePA == 0 {
		return 0, &MappingError{addr: va, msg: "failed to allocate frame for user page (userspace pool exhausted)"}
	}

	// Map the page with user-accessible permissions
	if !mapUserPage(va, framePA, elfFlags) {
		return 0, &MappingError{addr: va, msg: "failed to map user page"}
	}

	return framePA, nil
}

// mapUserPage maps a VA to PA with user-accessible permissions.
// Reads the current process's page table from TTBR0_EL1, which is always
// correct even when context switches occur.
// Falls back to the inherited TTBR0 from Cardinal if TTBR0 is not set.
//
//go:nosplit
func mapUserPage(va, pa uintptr, elfFlags uint32) bool {
	return mapUserPageWithL0(va, pa, elfFlags, 0) // 0 = read from TTBR0_EL1
}

// mapUserPageWithL0 maps a VA to PA with user-accessible permissions,
// using an explicit L0 page table PA. This is safe to use when context
// switches may occur.
//
// If l0PAParam is 0, reads the current process's page table from TTBR0_EL1.
//
//go:nosplit
func mapUserPageWithL0(va, pa uintptr, elfFlags uint32, l0PAParam uintptr) bool {
	// Extract indices
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	// Use TTBR0 for userspace (bit 55 = 0)
	if (va>>55)&1 != 0 {
		return false // User pages must be in low memory
	}

	// Use explicit L0 if provided, otherwise read from TTBR0_EL1.
	// CRITICAL: Don't use the global processL0PA - it can be stale when multiple
	// processes are running. TTBR0 always contains the correct page table.
	l0PA := l0PAParam
	if l0PA == 0 {
		ttbr0 := readTTBR0EL1()
		l0PA = uintptr(ttbr0 & 0x0000FFFFFFFFFFFF) // Mask out ASID
	}
	if l0PA == 0 {
		l0PA = ttbr0L0PA
	}

	// Get L0 table VA
	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		return false
	}

	// Read L0 entry
	l0Entry := (*uint64)(unsafe.Pointer(l0VA + l0Idx*8))

	var l1VA uintptr
	if (*l0Entry & PTE_VALID) == 0 {
		// Need to allocate L1 table
		l1VA = allocPTPage()
		if l1VA == 0 {
			return false
		}
		l1PA := walkPageTable(l1VA)
		if l1PA == 0 {
			return false
		}
		cachePTVA(l1PA, l1VA)
		*l0Entry = uint64(l1PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l0Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		l1PA := uintptr(*l0Entry & PTE_ADDR_MASK)
		l1VA = paToVAOrCache(l1PA)
	}
	if l1VA == 0 {
		return false
	}

	// Read L1 entry
	l1Entry := (*uint64)(unsafe.Pointer(l1VA + l1Idx*8))

	var l2VA uintptr
	if (*l1Entry & PTE_VALID) == 0 {
		l2VA = allocPTPage()
		if l2VA == 0 {
			return false
		}
		l2PA := walkPageTable(l2VA)
		if l2PA == 0 {
			return false
		}
		cachePTVA(l2PA, l2VA)
		*l1Entry = uint64(l2PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l1Entry)))
		dsbSY()
	} else {
		l2PA := uintptr(*l1Entry & PTE_ADDR_MASK)
		l2VA = paToVAOrCache(l2PA)
		if l2VA == 0 {
			return false
		}
	}

	// Read L2 entry
	l2Entry := (*uint64)(unsafe.Pointer(l2VA + l2Idx*8))

	var l3VA uintptr
	if (*l2Entry & PTE_VALID) == 0 {
		l3VA = allocPTPage()
		if l3VA == 0 {
			return false
		}
		l3PA := walkPageTable(l3VA)
		if l3PA == 0 {
			return false
		}
		cachePTVA(l3PA, l3VA)
		*l2Entry = uint64(l3PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l2Entry)))
		dsbSY()
	} else {
		l3PA := uintptr(*l2Entry & PTE_ADDR_MASK)
		l3VA = paToVAOrCache(l3PA)
		if l3VA == 0 {
			return false
		}
	}

	// Write L3 entry with USER permissions
	l3Entry := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))

	// Check if already mapped
	if (*l3Entry & PTE_VALID) != 0 {
		// Already mapped - check if same PA
		existingPA := uintptr(*l3Entry & PTE_ADDR_MASK)
		if existingPA == pa {
			return true // Already correctly mapped
		}
		return false // Conflict!
	}

	// Build PTE value for user page
	// Base: valid, page descriptor, access flag, normal memory, inner shareable
	// CRITICAL: PTE_NG (non-global) is required for userspace pages when using ASIDs!
	// Without nG=1, the TLB entry would be global and match any ASID, causing
	// conflicts when multiple processes map the same VA to different PAs.
	pteValue := uint64(pa) | PTE_VALID | PTE_TABLE | PTE_AF |
		PTE_ATTR_NORMAL | PTE_SH_INNER | PTE_NG

	// Set access permissions based on ELF flags
	if (elfFlags & ELF_PF_W) != 0 {
		// Writable - use RW for both EL1 and EL0
		pteValue |= PTE_AP_RW_ALL
	} else {
		// Read-only
		pteValue |= PTE_AP_RO_ALL
	}

	// Set execute permissions
	if (elfFlags & ELF_PF_X) == 0 {
		// Not executable - set UXN (user execute never)
		pteValue |= PTE_UXN
	}
	// Always set PXN - kernel should not execute user code
	pteValue |= PTE_PXN

	*l3Entry = pteValue

	// Clean cache and invalidate TLB
	dcCIVAC(uintptr(unsafe.Pointer(l3Entry)))
	dsbSY()
	tlbiVAE1IS(va)
	dsbSY()
	isbSY()

	return true
}

// MapUserDevicePage maps a physical address to a userspace VA with device memory attributes.
// This is used for mapping MMIO regions (like framebuffer) into priest address space.
// Unlike mapUserPage, this does NOT allocate a frame - it maps the given PA directly.
//
// The mapping is:
// - RW accessible by both EL1 and EL0
// - Device memory attributes (non-cacheable, strongly ordered)
// - No execute (PXN + UXN)
//
// Returns true on success, false on failure.
//
//go:nosplit
func MapUserDevicePage(va, pa uintptr) bool {
	// Lazy initialization
	if !pagingInitialized {
		InitPaging()
	}

	// Extract indices
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	// Use TTBR0 for userspace (bit 55 = 0)
	if (va>>55)&1 != 0 {
		return false // User pages must be in low memory
	}

	// CRITICAL: Get the current process's L0PA from TTBR0_EL1, NOT from a global.
	// Globals can be stale when multiple processes are running.
	// TTBR0 format: bits [63:48] = ASID, bits [47:1] = PA, bit [0] = CnP
	ttbr0 := readTTBR0EL1()
	l0PA := uintptr(ttbr0 & 0x0000FFFFFFFFFFFF) // Mask out ASID
	if l0PA == 0 {
		l0PA = ttbr0L0PA // Fallback to Cardinal's original page table
	}

	// Get L0 table VA
	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		return false
	}

	// Read L0 entry
	l0Entry := (*uint64)(unsafe.Pointer(l0VA + l0Idx*8))

	var l1VA uintptr
	if (*l0Entry & PTE_VALID) == 0 {
		// Need to allocate L1 table
		l1VA = allocPTPage()
		if l1VA == 0 {
			return false
		}
		l1PA := walkPageTable(l1VA)
		if l1PA == 0 {
			return false
		}
		cachePTVA(l1PA, l1VA)
		*l0Entry = uint64(l1PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l0Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		l1PA := uintptr(*l0Entry & PTE_ADDR_MASK)
		l1VA = paToVAOrCache(l1PA)
	}
	if l1VA == 0 {
		return false
	}

	// Read L1 entry
	l1Entry := (*uint64)(unsafe.Pointer(l1VA + l1Idx*8))

	var l2VA uintptr
	if (*l1Entry & PTE_VALID) == 0 {
		l2VA = allocPTPage()
		if l2VA == 0 {
			return false
		}
		l2PA := walkPageTable(l2VA)
		if l2PA == 0 {
			return false
		}
		cachePTVA(l2PA, l2VA)
		*l1Entry = uint64(l2PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l1Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		l2PA := uintptr(*l1Entry & PTE_ADDR_MASK)
		l2VA = paToVAOrCache(l2PA)
	}
	if l2VA == 0 {
		return false
	}

	// Read L2 entry
	l2Entry := (*uint64)(unsafe.Pointer(l2VA + l2Idx*8))

	var l3VA uintptr
	if (*l2Entry & PTE_VALID) == 0 {
		l3VA = allocPTPage()
		if l3VA == 0 {
			return false
		}
		l3PA := walkPageTable(l3VA)
		if l3PA == 0 {
			return false
		}
		cachePTVA(l3PA, l3VA)
		*l2Entry = uint64(l3PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l2Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		l3PA := uintptr(*l2Entry & PTE_ADDR_MASK)
		l3VA = paToVAOrCache(l3PA)
	}
	if l3VA == 0 {
		return false
	}

	// Write L3 entry with device attributes
	l3Entry := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))

	// Check if already mapped
	if (*l3Entry & PTE_VALID) != 0 {
		// Already mapped - verify it points to same PA
		existingPA := uintptr(*l3Entry & PTE_ADDR_MASK)
		if existingPA == pa {
			return true // Already correctly mapped
		}
		return false // Conflict!
	}

	// Device memory: non-cacheable, RW for user and kernel, no execute
	// PTE_NG required for userspace pages with ASIDs
	pteValue := uint64(pa) | PTE_VALID | PTE_TABLE | PTE_AF |
		PTE_ATTR_DEVICE | PTE_AP_RW_ALL | PTE_EXEC_NEVER | PTE_NG

	*l3Entry = pteValue

	// Clean cache and invalidate TLB
	dcCIVAC(uintptr(unsafe.Pointer(l3Entry)))
	dsbSY()
	tlbiVAE1IS(va)
	dsbSY()
	isbSY()

	return true
}

// MapUserFramebuffer maps the framebuffer physical memory into userspace.
// This maps FramebufferSize bytes from FramebufferPhysAddr to UserFramebufferVA.
// Returns true on success.
func MapUserFramebuffer() bool {
	// Use constants directly — the RuntimeConfig struct layouts between
	// packages are mismatched, so reading through the interface gives wrong values.
	framebufferPA := uintptr(constants.FramebufferPhysAddr)
	framebufferSize := uintptr(constants.FramebufferSize)

	// Fixed userspace VA for framebuffer (matches ksyscall.UserFramebufferVA)
	const framebufferVA = 0x00007FFE00000000

	pageSize := uintptr(0x1000)
	numPages := framebufferSize / pageSize

	for i := uintptr(0); i < numPages; i++ {
		va := uintptr(framebufferVA) + i*pageSize
		pa := framebufferPA + i*pageSize
		if !MapUserDevicePage(va, pa) {
			return false
		}
	}
	return true
}

// ============================================================================
// Kernel Scratch Page for ELF Loading
// ============================================================================
// The kernel can't directly access userspace pages (PAN / permissions).
// We use a scratch VA in kernel space that we can remap to different physical
// addresses when copying data to userspace pages.

// KernelScratchVA is a dedicated kernel virtual address for temporary PA mapping.
// Located after the PT pool region, safely before the Go heap.
const KernelScratchVA = 0xFFFFFFFF42260000

var kernelScratchMapped bool
var kernelScratchCurrentPA uintptr

// DEBUG: Watchpoint mechanism to track when a specific PA gets corrupted.
// PrintPoolRanges prints the memory pool ranges for debugging overlap issues.
func PrintPoolRanges() {
	cfg := getRuntimeConfigTyped()
	console.KWriteString("[POOLS] KernelPTPool:    0x")
	console.KPrintHex64(cfg.KernelPTPoolStart)
	console.KWriteString(" - 0x")
	console.KPrintHex64(cfg.KernelPTPoolEnd)
	console.KWriteString("\r\n")
	console.KWriteString("[POOLS] KernelFramePool: 0x")
	console.KPrintHex64(cfg.FramePoolStart)
	console.KWriteString(" - 0x")
	console.KPrintHex64(cfg.FramePoolEnd)
	console.KWriteString("\r\n")
	console.KWriteString("[POOLS] UserFramePool:   0x")
	console.KPrintHex64(cfg.UserspaceFramePoolStart)
	console.KWriteString(" - 0x")
	console.KPrintHex64(cfg.UserspaceFramePoolEnd)
	console.KWriteString("\r\n")
	console.KWriteString("[POOLS] UserPTPool:      0x")
	console.KPrintHex64(cfg.UserspacePTPoolStart)
	console.KWriteString(" - 0x")
	console.KPrintHex64(cfg.UserspacePTPoolEnd)
	console.KWriteString("\r\n")
}

// MapPAToKernelScratch maps a physical address to the kernel scratch VA.
// Returns the kernel VA where the PA can be accessed for reading/writing.
// The mapping is temporary - calling this again with a different PA will
// remap the scratch VA.
//
// CRITICAL: This is NOT thread-safe. Only use during single-threaded boot
// for ELF loading, before the Go scheduler starts multiple goroutines.
func MapPAToKernelScratch(pa uintptr) uintptr {
	// If already mapped to this PA, just return
	if kernelScratchMapped && kernelScratchCurrentPA == pa {
		return KernelScratchVA
	}

	// Lazy initialization
	if !pagingInitialized {
		InitPaging()
	}

	// Map the PA to kernel scratch VA using TTBR1 page tables
	if !mapKernelScratchPage(KernelScratchVA, pa) {
		return 0
	}

	kernelScratchMapped = true
	kernelScratchCurrentPA = pa

	return KernelScratchVA
}

// WalkUserPageTable translates a userspace VA to PA by walking TTBR0 page tables.
// Reads the current process's page table from TTBR0_EL1.
// Returns the physical address, or 0 if not mapped.
//
//go:nosplit
func WalkUserPageTable(va uintptr) uintptr {
	return WalkUserPageTableWithL0(va, 0) // 0 = read from TTBR0
}

// WalkUserPageTableWithL0 translates a userspace VA to PA by walking the given page table.
// If l0PA is 0, reads the current process's page table from TTBR0_EL1.
// Returns the physical address, or 0 if not mapped.
//
//go:nosplit
func WalkUserPageTableWithL0(va uintptr, l0PAParam uintptr) uintptr {
	// Lazy initialization
	if !pagingInitialized {
		InitPaging()
	}

	// Userspace addresses must have bit 55 = 0
	if (va>>55)&1 != 0 {
		return 0
	}

	// Extract indices
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	// Use explicit L0 if provided, otherwise read from TTBR0_EL1.
	// CRITICAL: Don't use the global processL0PA - it can be stale when multiple
	// processes are running. TTBR0 always contains the correct page table.
	l0PA := l0PAParam
	if l0PA == 0 {
		ttbr0 := readTTBR0EL1()
		l0PA = uintptr(ttbr0 & 0x0000FFFFFFFFFFFF) // Mask out ASID
	}
	if l0PA == 0 {
		l0PA = ttbr0L0PA
	}
	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		return 0
	}

	// L0 entry
	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	if (l0Entry & PTE_VALID) == 0 {
		return 0
	}

	// L1 table
	l1PA := uintptr(l0Entry & PTE_ADDR_MASK)
	l1VA := paToVAOrCache(l1PA)
	if l1VA == 0 {
		return 0
	}
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if (l1Entry & PTE_VALID) == 0 {
		return 0
	}

	// L2 table
	l2PA := uintptr(l1Entry & PTE_ADDR_MASK)
	l2VA := paToVAOrCache(l2PA)
	if l2VA == 0 {
		return 0
	}
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if (l2Entry & PTE_VALID) == 0 {
		return 0
	}

	// L3 table
	l3PA := uintptr(l2Entry & PTE_ADDR_MASK)
	l3VA := paToVAOrCache(l3PA)
	if l3VA == 0 {
		return 0
	}
	l3Entry := *(*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	if (l3Entry & PTE_VALID) == 0 {
		return 0
	}

	// Extract PA from L3 entry and add page offset
	pa := uintptr(l3Entry & PTE_ADDR_MASK)
	return pa | (va & (PageSize - 1))
}

// DumpUserPTEWithL0 walks the page table for a userspace VA and prints each level's entry.
// Used for debugging page table issues.
//
//go:nosplit
func DumpUserPTEWithL0(va uintptr, l0PAParam uintptr) {
	// Userspace addresses must have bit 55 = 0
	if (va>>55)&1 != 0 {
		uartPuts("[DumpPTE] VA has bit55=1 (kernel addr)\r\n")
		return
	}

	// Extract indices
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	console.KPrintf("[DumpPTE] VA=0x%x L0PA=0x%x indices=[%d,%d,%d,%d]\n",
		va, l0PAParam, l0Idx, l1Idx, l2Idx, l3Idx)

	// Use explicit L0 if provided
	l0PA := l0PAParam
	if l0PA == 0 {
		ttbr0 := readTTBR0EL1()
		l0PA = uintptr(ttbr0 & 0x0000FFFFFFFFFFFF)
	}
	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		uartPuts("[DumpPTE] Failed to get L0 VA from cache\r\n")
		return
	}

	// L0 entry
	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	console.KPrintf("[DumpPTE] L0[%d]=0x%016x (VA=0x%x)\n", l0Idx, l0Entry, l0VA+l0Idx*8)
	if (l0Entry & PTE_VALID) == 0 {
		uartPuts("[DumpPTE] L0 entry INVALID\r\n")
		return
	}

	// L1 table
	l1PA := uintptr(l0Entry & PTE_ADDR_MASK)
	l1VA := paToVAOrCache(l1PA)
	if l1VA == 0 {
		console.KPrintf("[DumpPTE] Failed to get L1 VA from cache (L1PA=0x%x)\n", l1PA)
		return
	}
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	console.KPrintf("[DumpPTE] L1[%d]=0x%016x (PA=0x%x VA=0x%x)\n", l1Idx, l1Entry, l1PA, l1VA+l1Idx*8)
	if (l1Entry & PTE_VALID) == 0 {
		uartPuts("[DumpPTE] L1 entry INVALID\r\n")
		return
	}

	// L2 table
	l2PA := uintptr(l1Entry & PTE_ADDR_MASK)
	l2VA := paToVAOrCache(l2PA)
	if l2VA == 0 {
		console.KPrintf("[DumpPTE] Failed to get L2 VA from cache (L2PA=0x%x)\n", l2PA)
		return
	}
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	console.KPrintf("[DumpPTE] L2[%d]=0x%016x (PA=0x%x VA=0x%x)\n", l2Idx, l2Entry, l2PA, l2VA+l2Idx*8)
	if (l2Entry & PTE_VALID) == 0 {
		uartPuts("[DumpPTE] L2 entry INVALID\r\n")
		return
	}

	// L3 table
	l3PA := uintptr(l2Entry & PTE_ADDR_MASK)
	l3VA := paToVAOrCache(l3PA)
	if l3VA == 0 {
		console.KPrintf("[DumpPTE] Failed to get L3 VA from cache (L3PA=0x%x)\n", l3PA)
		return
	}
	l3Entry := *(*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	console.KPrintf("[DumpPTE] L3[%d]=0x%016x (PA=0x%x VA=0x%x)\n", l3Idx, l3Entry, l3PA, l3VA+l3Idx*8)
	if (l3Entry & PTE_VALID) == 0 {
		uartPuts("[DumpPTE] L3 entry INVALID\r\n")
		return
	}

	// Decode PTE attributes
	pa := l3Entry & PTE_ADDR_MASK
	ap := (l3Entry >> 6) & 0x3
	sh := (l3Entry >> 8) & 0x3
	attr := (l3Entry >> 2) & 0x7
	af := (l3Entry >> 10) & 0x1
	uxn := (l3Entry >> 54) & 0x1
	pxn := (l3Entry >> 53) & 0x1

	console.KPrintf("[DumpPTE] PA=0x%x AP=%d SH=%d ATTR=%d AF=%d UXN=%d PXN=%d\n",
		pa, ap, sh, attr, af, uxn, pxn)
}

// UnmapUserPage removes the mapping for a userspace page.
// Clears the L3 PTE and invalidates TLB.
// Returns the physical address that was mapped (for optional frame release), or 0 if not mapped.
// Does NOT free the physical frame - caller is responsible for that if desired.
//
//go:nosplit
func UnmapUserPage(va uintptr) uintptr {
	// Lazy initialization
	if !pagingInitialized {
		InitPaging()
	}

	// Userspace addresses must have bit 55 = 0
	if (va>>55)&1 != 0 {
		return 0
	}

	// Align to page boundary
	pageVA := va &^ (PageSize - 1)

	// Extract indices
	l0Idx := (pageVA >> L0Shift) & 0x1FF
	l1Idx := (pageVA >> L1Shift) & 0x1FF
	l2Idx := (pageVA >> L2Shift) & 0x1FF
	l3Idx := (pageVA >> L3Shift) & 0x1FF

	// CRITICAL: Get the current process's L0PA from TTBR0_EL1, NOT from the global processL0PA.
	// The global can be stale if multiple processes are running. TTBR0 always contains
	// the correct page table for the currently executing process.
	ttbr0 := readTTBR0EL1()
	l0PA := uintptr(ttbr0 & 0x0000FFFFFFFFFFFF) // Mask out ASID
	if l0PA == 0 {
		l0PA = ttbr0L0PA
	}
	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		return 0
	}

	// L0 entry
	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	if (l0Entry & PTE_VALID) == 0 {
		return 0 // Not mapped
	}

	// L1 table
	l1PA := uintptr(l0Entry & PTE_ADDR_MASK)
	l1VA := paToVAOrCache(l1PA)
	if l1VA == 0 {
		return 0
	}
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if (l1Entry & PTE_VALID) == 0 {
		return 0 // Not mapped
	}

	// L2 table
	l2PA := uintptr(l1Entry & PTE_ADDR_MASK)
	l2VA := paToVAOrCache(l2PA)
	if l2VA == 0 {
		return 0
	}
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if (l2Entry & PTE_VALID) == 0 {
		return 0 // Not mapped
	}

	// L3 table
	l3PA := uintptr(l2Entry & PTE_ADDR_MASK)
	l3VA := paToVAOrCache(l3PA)
	if l3VA == 0 {
		return 0
	}
	l3EntryPtr := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	l3Entry := *l3EntryPtr
	if (l3Entry & PTE_VALID) == 0 {
		return 0 // Not mapped
	}

	// Get PA before clearing
	pa := uintptr(l3Entry & PTE_ADDR_MASK)

	// Clear the L3 entry (unmap the page)
	*l3EntryPtr = 0

	// Ensure PTE write is visible
	dsbSY()

	// Invalidate TLB for this VA
	tlbiVAE1IS(pageVA)
	dsbSY()
	isbSY()

	return pa
}

// GetUserL3PTE returns the raw L3 PTE for a userspace VA.
// Uses the process-specific page table if one exists.
// Useful for debugging page table entries.
func GetUserL3PTE(va uintptr) uint64 {
	if !pagingInitialized {
		InitPaging()
	}
	if (va>>55)&1 != 0 {
		return 0
	}
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	// CRITICAL: Get the current process's L0PA from TTBR0_EL1, NOT from the global processL0PA.
	// The global can be stale if multiple processes are running. TTBR0 always contains
	// the correct page table for the currently executing process.
	ttbr0 := readTTBR0EL1()
	l0PA := uintptr(ttbr0 & 0x0000FFFFFFFFFFFF) // Mask out ASID
	if l0PA == 0 {
		l0PA = ttbr0L0PA
	}
	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		return 0
	}
	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	if (l0Entry & PTE_VALID) == 0 {
		return 0
	}
	l1PA := uintptr(l0Entry & PTE_ADDR_MASK)
	l1VA := paToVAOrCache(l1PA)
	if l1VA == 0 {
		return 0
	}
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if (l1Entry & PTE_VALID) == 0 {
		return 0
	}
	l2PA := uintptr(l1Entry & PTE_ADDR_MASK)
	l2VA := paToVAOrCache(l2PA)
	if l2VA == 0 {
		return 0
	}
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if (l2Entry & PTE_VALID) == 0 {
		return 0
	}
	l3PA := uintptr(l2Entry & PTE_ADDR_MASK)
	l3VA := paToVAOrCache(l3PA)
	if l3VA == 0 {
		return 0
	}
	return *(*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
}

// ReadUserByteDirect attempts to read a byte directly from a userspace VA.
// This bypasses scratch mapping and reads via TTBR0 translation directly.
// Used for debugging to compare with scratch mapping results.
// WARNING: This may fault if PAN is enabled or userspace pages aren't accessible.
//
//go:nosplit
func ReadUserByteDirect(va uintptr) byte {
	return *(*byte)(unsafe.Pointer(va))
}

// ReadUserByte reads a single byte from a userspace virtual address.
// Uses kernel scratch mapping since kernel can't access userspace directly.
// Returns the byte value and true if successful, 0 and false otherwise.
func ReadUserByte(va uintptr) (byte, bool) {
	// Walk userspace page tables to find the physical address
	pa := WalkUserPageTable(va)
	if pa == 0 {
		return 0, false
	}

	// Page-align the PA and calculate offset
	pagePA := pa &^ (PageSize - 1)
	pageOffset := pa & (PageSize - 1)

	// Map to kernel scratch VA
	kernelVA := MapPAToKernelScratch(pagePA)
	if kernelVA == 0 {
		return 0, false
	}

	// Read the byte
	return *(*byte)(unsafe.Pointer(kernelVA + pageOffset)), true
}

// ReadUserUint64 reads a 64-bit value from a userspace virtual address.
// Uses kernel scratch mapping since kernel can't access userspace directly (PAN).
// Returns the value and true if successful, 0 and false otherwise.
//
// NOTE: This assumes the value doesn't cross a page boundary. If va is within
// 7 bytes of a page boundary, this could read incorrect data. For stack values
// which are 8-byte aligned, this should not be an issue.
func ReadUserUint64(va uintptr) (uint64, bool) {
	// Walk userspace page tables to find the physical address
	pa := WalkUserPageTable(va)
	if pa == 0 {
		return 0, false
	}

	// Page-align the PA and calculate offset
	pagePA := pa &^ (PageSize - 1)
	pageOffset := pa & (PageSize - 1)

	// Map to kernel scratch VA
	kernelVA := MapPAToKernelScratch(pagePA)
	if kernelVA == 0 {
		return 0, false
	}

	// Read the uint64
	return *(*uint64)(unsafe.Pointer(kernelVA + pageOffset)), true
}

// AllocAndMapUserPage allocates, maps, and zeros a userspace page in one operation.
// This is the SINGLE unified mechanism for issuing pages to userspace.
// Reads the current process's page table from TTBR0_EL1, which is always
// correct even when context switches occur.
//
// The function:
//  1. Allocates a physical frame from the USERSPACE frame pool
//  2. Maps the frame to the specified userspace VA with ELF permissions
//  3. Maps the frame to the kernel scratch VA for kernel access
//  4. Zeros the page using DC ZVA (fast cache-line zeroing)
//
// Returns:
//   - framePA: the physical address of the allocated frame (for later scratch remapping)
//   - scratchVA: the kernel scratch VA for immediate data copying (may be remapped later)
//
// CRITICAL: This ensures all userspace pages are zeroed before use.
// This prevents information leakage and ensures the Go runtime sees clean memory.
func AllocAndMapUserPage(userVA uintptr, elfFlags uint32) (framePA uintptr, scratchVA uintptr) {
	return AllocAndMapUserPageWithL0(userVA, elfFlags, 0) // 0 = read from TTBR0_EL1
}

// AllocAndMapUserPageWithL0 allocates, maps, and zeros a userspace page using
// an explicit L0 page table PA. This is safe to use when context switches may
// occur, as it doesn't rely on the global processL0PA.
//
// If l0PA is 0, reads the current process's page table from TTBR0_EL1.
//
// Returns:
//   - framePA: the physical address of the allocated frame (for later scratch remapping)
//   - scratchVA: the kernel scratch VA for immediate data copying (may be remapped later)
func AllocAndMapUserPageWithL0(userVA uintptr, elfFlags uint32, l0PA uintptr) (framePA uintptr, scratchVA uintptr) {
	// Lazy initialization
	if !pagingInitialized {
		InitPaging()
	}

	// Step 1: Allocate a physical frame from userspace pool
	framePA = AllocUserFrame()
	if framePA == 0 {
		uartPuts("[kmem] AllocAndMapUserPage: frame alloc failed\r\n")
		return 0, 0
	}

	// Step 2: Map the frame to userspace VA using explicit page table
	if !mapUserPageWithL0(userVA, framePA, elfFlags, l0PA) {
		uartPuts("[kmem] AllocAndMapUserPage: user map failed\r\n")
		return 0, 0
	}

	// Step 3: Map the frame to kernel scratch VA
	scratchVA = MapPAToKernelScratch(framePA)
	if scratchVA == 0 {
		uartPuts("[kmem] AllocAndMapUserPage: scratch map failed\r\n")
		return 0, 0
	}

	// Step 4: Zero the page (via scratch VA)
	// Using byte-by-byte zeroing for now (DC ZVA might have issues)
	zeroPageSlow(scratchVA)

	// CRITICAL: Clean the data cache for the entire page!
	// The zeros were written via scratchVA but userspace will read via userVA.
	// Without cache cleaning, userspace may see stale/uninitialized data.
	CleanPageCache(scratchVA)

	// TLB invalidate for the userspace VA
	tlbiVAE1IS(userVA)
	dsbSY()
	isbSY()

	return framePA, scratchVA
}

// AllocAndMapUserPageNoZero is like AllocAndMapUserPage but skips zeroing.
// Only use this for pages that will be entirely overwritten (e.g., code pages
// where every byte will be copied from the ELF file).
//
// For data pages, BSS, or stack - ALWAYS use AllocAndMapUserPage with zeroing.
func AllocAndMapUserPageNoZero(userVA uintptr, elfFlags uint32) (framePA uintptr, scratchVA uintptr) {
	if !pagingInitialized {
		InitPaging()
	}

	framePA = AllocUserFrame()
	if framePA == 0 {
		return 0, 0
	}

	if !mapUserPage(userVA, framePA, elfFlags) {
		return 0, 0
	}

	scratchVA = MapPAToKernelScratch(framePA)
	if scratchVA == 0 {
		return 0, 0
	}

	return framePA, scratchVA
}

// mapKernelScratchPage maps a PA to the kernel scratch VA in TTBR1.
// This is similar to mapPage but specifically for the scratch region.
//
//go:nosplit
func mapKernelScratchPage(va, pa uintptr) bool {
	// Extract indices
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	// Use TTBR1 for kernel space (bit 55 = 1)
	if (va>>55)&1 == 0 {
		return false // Scratch must be in kernel high memory
	}
	l0PA := ttbr1L0PA

	// Get L0 table VA
	l0VA := paToVA(l0PA)
	if l0VA == 0 {
		return false
	}

	// Read L0 entry
	l0Entry := (*uint64)(unsafe.Pointer(l0VA + l0Idx*8))

	var l1VA uintptr
	if (*l0Entry & PTE_VALID) == 0 {
		// Need to allocate L1 table
		l1VA = allocPTPage()
		if l1VA == 0 {
			return false
		}
		l1PA := walkPageTable(l1VA)
		if l1PA == 0 {
			return false
		}
		cachePTVA(l1PA, l1VA)
		*l0Entry = uint64(l1PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l0Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		l1PA := uintptr(*l0Entry & PTE_ADDR_MASK)
		l1VA = paToVAOrCache(l1PA)
	}
	if l1VA == 0 {
		return false
	}

	// Read L1 entry
	l1Entry := (*uint64)(unsafe.Pointer(l1VA + l1Idx*8))

	var l2VA uintptr
	if (*l1Entry & PTE_VALID) == 0 {
		l2VA = allocPTPage()
		if l2VA == 0 {
			return false
		}
		l2PA := walkPageTable(l2VA)
		if l2PA == 0 {
			return false
		}
		cachePTVA(l2PA, l2VA)
		*l1Entry = uint64(l2PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l1Entry)))
		dsbSY()
	} else {
		l2PA := uintptr(*l1Entry & PTE_ADDR_MASK)
		l2VA = paToVAOrCache(l2PA)
		if l2VA == 0 {
			return false
		}
	}

	// Read L2 entry
	l2Entry := (*uint64)(unsafe.Pointer(l2VA + l2Idx*8))

	var l3VA uintptr
	if (*l2Entry & PTE_VALID) == 0 {
		l3VA = allocPTPage()
		if l3VA == 0 {
			return false
		}
		l3PA := walkPageTable(l3VA)
		if l3PA == 0 {
			return false
		}
		cachePTVA(l3PA, l3VA)
		*l2Entry = uint64(l3PA) | PTE_VALID | PTE_TABLE
		dcCIVAC(uintptr(unsafe.Pointer(l2Entry)))
		dsbSY()
	} else {
		l3PA := uintptr(*l2Entry & PTE_ADDR_MASK)
		l3VA = paToVAOrCache(l3PA)
		if l3VA == 0 {
			return false
		}
	}

	// Write L3 entry with kernel RW permissions
	l3Entry := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))

	// Build PTE for kernel-only RW access (normal memory, inner shareable)
	pteValue := uint64(pa) | PTE_VALID | PTE_TABLE | PTE_AF |
		PTE_ATTR_NORMAL | PTE_AP_RW_EL1 | PTE_SH_INNER | PTE_EXEC_NEVER

	*l3Entry = pteValue

	// Clean cache and invalidate TLB
	dcCIVAC(uintptr(unsafe.Pointer(l3Entry)))
	dsbSY()
	tlbiVAE1IS(va)
	dsbSY()
	isbSY()

	return true
}
