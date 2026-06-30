
package kmem

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"mazzy/shared/constants"
	"sync/atomic"
	"unsafe"
)

//go:linkname getCurrentThreadTID main.GetCurrentThreadTID
func getCurrentThreadTID() int16

// debugPaging enables verbose page fault debugging output
// Set to false for production, true for debugging
const debugPaging = false

// pfContextShepherdID and pfContextThreadID are set at the top of HandlePageFault /
// HandleUserPageFault so that allocPTPage (deeper in the nosplit chain) can read
// them without adding function calls to its frame. Single-CPU, so globals are safe.
var pfContextShepherdID int16 = 0 // default: kernel (PID 0)
var pfContextThreadID int16 = -1

// currentShepherdID returns the PID of the current shepherd, or 0 for kernel context.
//
//go:nosplit
func currentShepherdID() int16 {
	p := proc.CurrentShepherd()
	if p == nil {
		return 0
	}
	return int16(p.PID)
}

// currentShepherdBumpEnd returns the current bump allocation end for the current
// shepherd, or userMmapStart if there is no current shepherd or no allocations yet.
//
//go:nosplit
func currentShepherdBumpEnd() uint64 {
	p := proc.CurrentShepherd()
	if p == nil {
		return userMmapStart
	}
	v := atomic.LoadUint64(&p.BumpPointer)
	if v == 0 {
		return userMmapStart
	}
	return v
}

// OnFileMappedPageFault is called when a page fault occurs in a file-backed
// mmap region. Set by ksyscall during init to avoid circular dependency.
// The handler allocates a frame, sends a read request to the linux shepherd,
// blocks the faulting thread, and returns true. Assembly checks
// GetSyscallSwitchTarget after return to perform the context switch.
var OnFileMappedPageFault func(faultAddr uintptr, fm *proc.FileMapping) bool

// currentShepherdFindFileMapping returns the FileMapping containing addr,
// or nil if none. Returns nil for kernel context.
//
//go:nosplit
func currentShepherdFindFileMapping(addr uint64) *proc.FileMapping {
	p := proc.CurrentShepherd()
	if p == nil {
		return nil
	}
	return p.FindFileMappingByVA(addr)
}

// currentShepherdSpanContains returns true if the current shepherd has a span
// covering addr. Returns false for kernel context.
//
//go:nosplit
func currentShepherdSpanContains(addr uint64) bool {
	p := proc.CurrentShepherd()
	if p == nil {
		return false
	}
	return p.Spans.Contains(addr)
}

// inAllocatedUserRegion returns true if va falls within a region the
// current shepherd has been granted: the MAP_FIXED slot below
// userMmapStart, the bump-allocated mmap region, or any registered
// span. Used as the gate before demand-mapping a frame so a crafted
// address can't force the kernel to materialize a mapping outside the
// shepherd's allocations.
//
//go:nosplit
func inAllocatedUserRegion(va uint64) bool {
	const minUserAddr = 0x10000
	if va < minUserAddr {
		return false
	}
	if va < userMmapStart {
		return true
	}
	if va < currentShepherdBumpEnd() {
		return true
	}
	return currentShepherdSpanContains(va)
}

// debugPrint conditionally outputs a character if debugging is enabled.
//
//go:nosplit
func debugPrint(c byte) {
	if debugPaging {
		serial.PollWrite(c)
	}
}

// debugPrintHex conditionally outputs a hex value if debugging is enabled.
//
//go:nosplit
func debugPrintHex(val uint64) {
	if debugPaging {
		serial.RawUARTHex64(val)
	}
}

// Page table level shift constants (shared across all architectures).
// For 4KB granule / Sv39: 4-level (ARM64/x86_64) or 3-level (RISC-V) tables.
const (
	L0Shift = 39
	L1Shift = 30
	L2Shift = 21
	L3Shift = 12

	// Address mask for PTEs (bits 47:12) — shared across ARM64 and x86_64.
	// RISC-V uses PPN encoding but this mask is still useful for PA extraction.
	PTE_ADDR_MASK = 0x0000FFFFFFFFF000
)

// Memory layout constants come from runtime configuration (auxv).
// NO hardcoded addresses - everything is derived at runtime from Cardinal.

// pfDbg* mirror HandleUserPageFault's stack-resident locals into .bss just
// before zeroPageSlow runs (MAZ-136 trample hunt). If the local pageAddr or
// framePA differs from its mirror right after the zero, the handler's OWN
// stack frame was zeroed — i.e. the freshly-allocated frame's scratch
// mapping aliased the exception stack. See the [PGCLOB] check at the
// zeroPageSlow call site.
var (
	pfDbgPageAddr  uint64
	pfDbgFramePA   uint64
	pfDbgScratchVA uint64
)

// pgClobDiagnostic prints the [PGCLOB] stack-zeroed-under-us evidence.
// Deliberately NOT nosplit (and noinline): its frame would push the
// exception-vector nosplit chain over the 792 B budget. Only reached when
// HandleUserPageFault's own frame was provably corrupted — terminal path.
//
//go:noinline
func pgClobDiagnostic(pageAddrNow, framePANow uint64) {
	serial.RawUARTPuts("[PGCLOB] stack zeroed under us: pageAddr=0x")
	serial.RawUARTHex64(pageAddrNow)
	serial.RawUARTPuts(" was=0x")
	serial.RawUARTHex64(pfDbgPageAddr)
	serial.RawUARTPuts(" framePA now=0x")
	serial.RawUARTHex64(framePANow)
	serial.RawUARTPuts(" was=0x")
	serial.RawUARTHex64(pfDbgFramePA)
	serial.RawUARTPuts(" scratchVA=0x")
	serial.RawUARTHex64(pfDbgScratchVA)
	serial.RawUARTPuts("\r\n")
}

// Globals set during initialization
// All globals use lazy initialization to avoid relocation issues.
// The relocation script would corrupt compile-time initialized values
// that look like physical addresses or kernel VAs.
var (
	pagingInitialized  bool
	ttbr1L0PA          uintptr // Physical address of TTBR1 L0 table (lazy init)
	ttbr0L0PA          uintptr // Physical address of TTBR0 L0 table (Cardinal's original)
	pfSuccessCount     uint64  // Successful page fault handling counter
	pfLastFaultAddr    uintptr // Last faulting address (to detect repeated faults)
	pfRepeatCount      uint64  // Counter for repeated faults at same page
	// NOTE: processL0PA global removed - use readCurrentL0PA() to get current L0PA
	ttbr1L1PA uintptr // Physical address of TTBR1 L1 table (lazy init)
	// NOTE: ptPoolNext removed - PT allocation now uses unified pool

	// Cache for allocated page table VAs
	// Since we can't compute VA from PA (Cardinal doesn't map all RAM),
	// we need to track the VAs of page tables we allocate.
	// Key: PA of page table, Value: VA of page table
	// CRITICAL: If this fills up, page table lookups will fail because
	// PT pool pages are NOT identity-mapped - paToVA fallback won't work!
	// 2048 entries supports many shepherds with large page tables.
	ptVACache     [2048]ptVACacheEntry // Simple fixed-size cache
	ptVACacheSize int
)

type ptVACacheEntry struct {
	pa uintptr
	va uintptr
}

// getKmazarinSize returns the kmazarin binary size from auxv.
//
//go:nosplit
func getKmazarinSize() uint64 {
	return uint64(kmazarinKmazarinSize)
}

// InitPaging initializes the paging subsystem.
// All values come from runtime configuration (auxv from bootloader).
//
//go:nosplit
func InitPaging() {
	ttbr1L0PA = kmazarinTTBR1L0Phys
	ttbr0L0PA = kmazarinTTBR0L0Phys
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

// GetKernelL0PA returns the kernel's root page table physical address.
// On ARM64 this is the TTBR1 page table; on x86_64 this is the initial CR3
// (diplomat's PML4); on RISC-V this is the initial SATP root.
// Used by x86_64 to set Thread 0's PageTableL0PA so the context switch
// restores the kernel page table instead of leaving the user's CR3 active.
//
//go:nosplit
func GetKernelL0PA() uintptr {
	if !pagingInitialized {
		InitPaging()
	}
	return ttbr1L0PA
}

// ReadCurrentL0PA reads the current page table base register and returns
// the L0 page table physical address. Arch-specific extraction is handled
// by readCurrentL0PA() in each paging_{arch}.go.
//
//go:nosplit
func ReadCurrentL0PA() uint64 {
	return uint64(readCurrentL0PA())
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

	// Arch-specific initialization: on x86_64/RISC-V, copies kernel PML4/root
	// entries so the process page table retains kernel mappings when CR3/SATP is
	// switched. On ARM64, this is a no-op (TTBR0/TTBR1 are separate).
	initProcessL0(l0VA)

	// Map the sigreturn vDSO page into the process's page table.
	// On RISC-V, U-mode can't execute kernel pages (PTE.U=0), so the sigreturn
	// trampoline must be at a user-accessible VA. On ARM64/x86_64, this is a no-op.
	mapSigreturnVDSOInProcessL0(l0PA)

	// Cache the PA -> VA mapping
	cachePTVA(l0PA, l0VA)

	// NOTE: We no longer set a global processL0PA here.
	// The caller must store this L0PA in the Thread struct and use
	// SwitchTTBR0WithASID() to activate it when switching to this process.

	return l0PA
}

// NOTE: GetProcessL0PA() and SwitchToProcessPageTable() were removed.
// They used a global processL0PA which caused race conditions with multiple shepherds.
// Use SwitchToProcessPageTableWithL0() or SwitchTTBR0WithASID() with explicit L0PA.
// For reading the current L0PA, use readCurrentL0PA().

// SwitchToProcessPageTableWithL0 switches TTBR0 to the specified L0 page table.
// This should be called before ERET to userspace.
// Using an explicit l0PA parameter avoids race conditions with multiple processes.
// Page fault handlers now read from TTBR0_EL1 directly instead of a global.
//
//go:nosplit
func SwitchToProcessPageTableWithL0(l0PA uintptr) {
	SwitchTTBR0WithASID(l0PA, 0)
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

// SwitchTTBR0WithASID switches the user-space page table register with ASID.
// ASID (Address Space Identifier) allows TLB entries from different processes to
// coexist, avoiding full TLB flush on every context switch.
//
// Register value construction is arch-specific (via constructTTBR0Value):
//   ARM64 TTBR0: [63:48]=ASID, [47:1]=PA, [0]=CnP
//   RISC-V SATP: [63:60]=MODE(9=Sv48), [59:44]=ASID, [43:0]=PPN(PA>>12)
//   x86_64 CR3:  [63:12]=PML4 PA, [11:0]=PCID
//
// Page fault handlers read TTBR0/SATP/CR3 directly to get the current L0PA,
// so no global state update is needed here.
//
//go:nosplit
func SwitchTTBR0WithASID(l0PA uintptr, asid uint16) {
	if l0PA == 0 {
		return // No page table to switch to
	}

	verifyUserspaceShepherdL0(l0PA, asid)

	// Arch-specific register value construction
	regVal := constructTTBR0Value(l0PA, asid)

	// Memory barrier, write register, flush TLB
	dsbISH()             // Memory barrier before page table operations
	writeTTBR0Asm(regVal) // Write new TTBR0/SATP/CR3 value
	tlbiVMALLE1IS()      // Invalidate all TLB entries
	dsbISH()             // Barrier to ensure write and TLB flush complete
	isbSY()              // Instruction barrier for new translations
}

// paToVA converts a physical address to a virtual address using identity mapping.
// Cardinal maps page tables with VA = PA + KernelVAOffset.
//
//go:nosplit
func paToVA(pa uintptr) uintptr {
	return pa + constants.KernelVAOffset
}

// cachePTVA stores a PA -> VA mapping for an allocated page table.
//
//go:nosplit
func cachePTVA(pa, va uintptr) {
	// Hoist the global index into a local and compare unsigned so the compiler
	// can prove the store is in bounds (0 <= n < len) and elide the bounds
	// check entirely. Indexing the global directly — or comparing signed, which
	// leaves the n>=0 lower-bound check live — pulls runtime.panicBounds64 into
	// this nosplit chain and overflows the 792 B exception-stack budget on the
	// syscallEntry→HandleUserPageFault path.
	n := ptVACacheSize
	if uint(n) < uint(len(ptVACache)) {
		ptVACache[n] = ptVACacheEntry{pa: pa, va: va}
		ptVACacheSize = n + 1
	} else {
		// CACHE FULL - this will cause page table lookup failures!
		serial.RawUARTPuts("[kmem] WARN: ptVACache FULL!\r\n")
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
	return va - constants.KernelVAOffset
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
	// Allocate from unified pool. Use pfContextShepherdID to determine ownership:
	// if we're in a userspace page fault, the PT page belongs to that shepherd.
	ptType := PageType(PageKernelPT)
	ptOwner := int16(0)
	if pfContextShepherdID > 0 {
		ptType = PageUserPT
		ptOwner = pfContextShepherdID
	}
	pa := AllocPage(ptType, ptOwner)
	if pa == 0 {
		serial.RawUARTPuts("[kmem] PT OOM!\r\n")
		return 0
	}

	// Convert PA to VA using kernel VA offset (use constant to avoid deep config chain)
	va := pa + constants.KernelVAOffset

	// Zero the page. clear() compiles to memclr without a bounds-check
	// chain — important here because allocPTPage sits on the nosplit
	// stack-budget path from SyscallWrite via CopyFromUser fault-on-miss,
	// and a manual indexed loop pulls panicBounds/panicBounds64 into the
	// reachable set (~224 bytes), blowing the 792-byte limit.
	ptr := (*[512]uint64)(unsafe.Pointer(va))
	clear(ptr[:])

	// Queue deferred record for bottom-half page tracking.
	// Use cached IDs from the current page fault context (set by HandlePageFault
	// or HandleUserPageFault before calling into page table allocation).
	QueueDeferredRecord(DeferredPageRecord{
		PA:       pa,
		VA:       va,
		Type:     PageAllocKernelPT,
		ShepherdID: pfContextShepherdID,
		ThreadID: pfContextThreadID,
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
	if !pagingInitialized {
		InitPaging()
	}
	return walkPageTable(va)
}

// walkPageTable translates a VA to PA by walking the page tables.
// Architecture-specific: see paging_arm64.go and paging_amd64.go

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
	// Cache shepherd/thread IDs for allocPTPage (avoids adding calls to nosplit chain)
	pfContextShepherdID = currentShepherdID()
	pfContextThreadID = getCurrentThreadTID()

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

	// Track repeated faults at the same page
	if pageAddr == pfLastFaultAddr {
		pfRepeatCount++
	} else {
		pfLastFaultAddr = pageAddr
		pfRepeatCount = 0
	}

	// Allocate a physical frame
	frame := AllocKernelFrame()
	if frame == 0 {
		// Always print alloc failure diagnostics (direct UART)
		hexChars := "0123456789ABCDEF"
		serial.PollWrite('O')
		serial.PollWrite('O')
		serial.PollWrite('M')
		serial.PollWrite(' ')
		serial.PollWrite('s')
		serial.PollWrite('u')
		serial.PollWrite('c')
		serial.PollWrite('=')
		for i := 60; i >= 0; i -= 4 {
			serial.PollWrite(hexChars[(pfSuccessCount>>uint(i))&0xF])
		}
		serial.PollWrite(' ')
		serial.PollWrite('r')
		serial.PollWrite('p')
		serial.PollWrite('t')
		serial.PollWrite('=')
		for i := 60; i >= 0; i -= 4 {
			serial.PollWrite(hexChars[(pfRepeatCount>>uint(i))&0xF])
		}
		serial.PollWrite(' ')
		serial.PollWrite('v')
		serial.PollWrite('a')
		serial.PollWrite('=')
		fa := uint64(faultAddr)
		for i := 60; i >= 0; i -= 4 {
			serial.PollWrite(hexChars[(fa>>uint(i))&0xF])
		}
		serial.PollWrite(' ')
		serial.PollWrite('b')
		serial.PollWrite('p')
		serial.PollWrite('=')
		bp := GetBumpAllocatedPages()
		for i := 60; i >= 0; i -= 4 {
			serial.PollWrite(hexChars[(bp>>uint(i))&0xF])
		}
		serial.PollWrite('\r')
		serial.PollWrite('\n')
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

	// Zero the page via the just-mapped VA
	zeroPageSlow(pageAddr)
	debugPrint('z') // DEBUG: zeroing done

	debugPrint('1') // DEBUG: after zero, before pfSuccessCount++
	// Track success
	pfSuccessCount++
	debugPrint('2') // DEBUG: after pfSuccessCount++

	// Queue deferred record for bottom-half page tracking
	debugPrint('3') // DEBUG: before QueueDeferredRecord
	QueueDeferredRecord(DeferredPageRecord{
		PA:       frame,
		VA:       pageAddr,
		Type:     PageAllocKernelHeap,
		ShepherdID: currentShepherdID(),
		ThreadID: getCurrentThreadTID(),
		Order:    0,
	})
	debugPrint('4') // DEBUG: after QueueDeferredRecord

	return true
}

// userMmapStart is arch-specific (see mmap_addr_*.go in this package).
// Must match ksyscall/mmap_addr_*.go values.
//
// userMmapEnd is shared across all arches.
const userMmapEnd = 0x0000700000000000 // 112TB - plenty of VA space

// Repeat-fault detector: if the same user VA faults more than repeatFaultMax times,
// something is broken (mapping not taking effect). Return false to halt.
var repeatFaultLastAddr uintptr
var repeatFaultCount int

const repeatFaultMax = 10

// userPFCount tracks total user page faults for diagnostic breadcrumbs.
var userPFCount uint64


// HandleUserPageFault handles a page fault at a userspace virtual address.
// This is called from the exception handler for data/instruction aborts from userspace.
// The caller must set PfIsPermFault before calling.
// Returns true if the fault was handled successfully, false otherwise.
//
// For userspace mmap addresses, this function:
// 1. Validates the address was actually allocated by mmap
// 2. Allocates a physical frame from the USERSPACE frame pool
// 3. Allocates L2/L3 tables if needed
// 4. Maps the VA to the PA with EL0-accessible permissions
//
//go:nosplit
func HandleUserPageFault(faultAddr uintptr, isPermFault uint64) bool {
	userPFCount++

	// Cache shepherd/thread IDs for allocPTPage (avoids adding calls to nosplit chain)
	pfContextShepherdID = currentShepherdID()
	pfContextThreadID = getCurrentThreadTID()

	// Repeat-fault detection: halt if same page faults repeatedly
	faultPage := faultAddr &^ (PageSize - 1)
	if faultPage == repeatFaultLastAddr {
		repeatFaultCount++
		if repeatFaultCount >= repeatFaultMax {
			// Call non-nosplit diagnostic function to avoid dumpUserPTERaw's
			// 312-byte frame from counting against the x86_64 nosplit chain.
			repeatFaultDiagnostic(faultAddr)
			return false // trigger halt in asm handler
		}
	} else {
		repeatFaultLastAddr = faultPage
		repeatFaultCount = 1
	}
	// Lazy initialization
	if !pagingInitialized {
		InitPaging()
	}

	// Align to page boundary
	pageAddr := faultAddr &^ (PageSize - 1)

	// Check if the page is already mapped FIRST, before address validation.
	// Pre-mapped pages (framebuffer, ELF code segments) may not be in any
	// tracked region (span list, bump range), but they ARE validly mapped
	// in the process page table. L0 and L1 are set up at process creation;
	// demand paging only needs to fill in L2/L3 entries.
	existingPA := WalkUserPageTable(faultAddr)
	if existingPA != 0 {
		if isPermFault != 0 {
			// Permission fault on an already-mapped page.
			// Check if this is a write to a file-backed mmap page.
			fm := currentShepherdFindFileMapping(uint64(pageAddr))
			if fm != nil {
				logFileMmapWrite(faultAddr, fm.FD)
			}
			// We cannot fix permission faults — return false so the exception
			// handler kills the process instead of looping forever.
			return false
		}
		// Page is already mapped — TLB miss or access-flag fault.
		// Invalidate TLB and return success.
		tlbiVAE1IS(pageAddr)
		dsbSY()
		isbSY()
		return true
	}

	// Check if this is a file-backed mmap page (translation fault).
	// If so, delegate to the file-mapped page fault handler which will
	// allocate a frame, send a read request to the linux shepherd, and
	// block the faulting thread until the data arrives.
	fm := currentShepherdFindFileMapping(uint64(pageAddr))
	if fm != nil {
		if OnFileMappedPageFault != nil {
			return OnFileMappedPageFault(faultAddr, fm)
		}
		return false
	}

	// Validate the unmapped fault address is in an allocated region:
	// MAP_FIXED slot (ELF/stacks/Go heap), bump-allocated mmap, or a
	// registered span (hint-based mmaps).
	if !inAllocatedUserRegion(uint64(faultAddr)) {
		if uint64(faultAddr) < 0x10000 {
			logLowFaultDebug(faultAddr)
		}
		serial.RawUART('!')
		return false
	}

	// Check if PTE has swap reference before allocating a new frame.
	// Currently isSwapPTE always returns false (stubs), so this never triggers.
	pte, _, pteOk := platformReadPTEAt(faultAddr)
	if pteOk && isSwapPTE(pte) {
		slot := extractSwapSlot(pte)
		_, err := SwapInPage(faultAddr, slot, proc.ShepherdId(currentShepherdID()))
		if err == nil {
			return true
		}
	}
	// Fall through to normal demand-page allocation.

	// Allocate a physical frame from userspace pool
	framePA := AllocUserFrame()
	if framePA == 0 {
		SerialPuts("[kmem] user page fault OOM va=")
		SerialHex16(uint64(faultAddr))
		SerialPuts(" sid=")
		SerialHex16(uint64(pfContextShepherdID))
		serial.PollWrite('\r')
		serial.PollWrite('\n')
		return false
	}

	// Map the page with RWX permissions for userspace demand paging.
	// ELF code pages are pre-mapped by the ELF loader with correct permissions.
	// Demand-paged pages (stack growth, heap) need RW; adding X is a pragmatic
	// choice since we don't track segment permissions here. A proper fix would
	// consult the ELF segment table to determine whether X is needed.
	elfFlags := uint32(ELF_PF_R | ELF_PF_W | ELF_PF_X)

	// CRITICAL: Get the current process's L0PA from the hardware page table register,
	// NOT from the global processL0PA. The global can be stale if multiple processes
	// are running. The hardware register always contains the correct page table.
	currentL0PA := readCurrentL0PA()

	if !mapUserPageWithL0(pageAddr, framePA, elfFlags, currentL0PA) {
		FreeUserFrame(framePA)
		return false
	}

	// Zero the page using kernel scratch mapping
	// CRITICAL: We MUST zero the page before giving it to userspace!
	// If we can't map the scratch VA, the page fault MUST fail.
	scratchVA := MapPAToKernelScratch(framePA)
	if scratchVA == 0 {
		return false // FAIL - can't give uninitialized page to userspace
	}
	// DEBUG (MAZ-136 trample hunt): mirror this invocation's stack-resident
	// state into globals BEFORE zeroing. The deterministic !F: signature is
	// this function's spilled pageAddr reading back as ZERO at the
	// verify-at-user deref below, with IST-rotation accounting PERFECTLY
	// balanced — i.e. nothing delivered onto this stack; something ZEROED it.
	// The prime suspect is zeroPageSlow itself: if framePA aliases the
	// exception stack's physical page (allocator double-hand-out) or the
	// scratch mapping resolves to the wrong PA, the next line zeroes OUR OWN
	// frame. The globals survive (they're in .bss, not on this stack); the
	// post-zero comparison below catches the clobber in the act.
	pfDbgPageAddr = uint64(pageAddr)
	pfDbgFramePA = uint64(framePA)
	pfDbgScratchVA = uint64(scratchVA)
	zeroPageSlow(scratchVA)
	// Validate all three mirrored values, including scratchVA: if only the
	// scratch VA slot was clobbered, the downstream CleanPageCache/TLB ops below
	// would otherwise run on a wrong VA undetected.
	if uint64(pageAddr) != pfDbgPageAddr || uint64(framePA) != pfDbgFramePA || uint64(scratchVA) != pfDbgScratchVA {
		// Non-nosplit diagnostic (repeatFaultDiagnostic pattern): the print
		// bodies blow the 792 B nosplit chain budget if inlined here. The
		// kernel is provably corrupt on this path, so the morestack hazard
		// of leaving the nosplit chain is moot.
		pgClobDiagnostic(uint64(pageAddr), uint64(framePA))
		return false // → pf_neither_handled: full register/ISTCT dump + halt
	}

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

	// DEBUG: Post-zeroing verification — read back first 8 bytes through the
	// linear map to confirm the page is actually zero. If not, something is
	// writing to this PA between zeroing and use.
	verifyWord := *(*uint64)(unsafe.Pointer(scratchVA))
	if verifyWord != 0 {
		serial.RawUARTPuts("[ZERO_VERIFY_FAIL] PA=0x")
		serial.RawUARTHex64(uint64(framePA))
		serial.RawUARTPuts(" VA=0x")
		serial.RawUARTHex64(uint64(pageAddr))
		serial.RawUARTPuts(" word0=0x")
		serial.RawUARTHex64(verifyWord)
		serial.RawUARTPuts("\r\n")
	}

	// Queue deferred record for bottom-half page tracking
	QueueDeferredRecord(DeferredPageRecord{
		PA:       framePA,
		VA:       pageAddr,
		Type:     PageAllocUser,
		ShepherdID: currentShepherdID(),
		ThreadID: getCurrentThreadTID(),
		Order:    0,
	})

	return true
}

// logFileMmapWrite logs a write attempt to a file-backed mmap page.
// Separated from HandleUserPageFault (nosplit) so we can use klog.
//
//go:noinline
func logFileMmapWrite(faultAddr uintptr, fd int32) {
	klog.Errf("[mmap] WRITE to read-only file-backed page VA=0x%x fd=%d\n",
		uint64(faultAddr), fd)
}

// logLowFaultDebug logs debug info when a page fault occurs below minUserAddr.
// Separated from HandleUserPageFault (nosplit) so we can use klog.
//
//go:noinline
func logLowFaultDebug(faultAddr uintptr) {
	// Quiet: per-fault diagnostic was the #2 noise source in serial logs.
	// Left as no-op so the nosplit caller doesn't need changes.
}

// DemandMapUserPage ensures a userspace page is mapped, demand-faulting if needed.
// Given a userspace VA and the process's L0 page table PA, it either:
//   - Returns the existing PA if the page is already mapped, or
//   - Allocates a physical frame, maps it, zeros it, and returns the new PA.
//
// Returns 0 if the page cannot be mapped (invalid address, out of frames, etc.).
// This is the page-level primitive for kernel→userspace data transfer: callers
// should ensure each destination page is mapped before writing to it.
//
//go:noinline
func DemandMapUserPage(va uintptr, l0PA uintptr) uintptr {
	pageAddr := va &^ (PageSize - 1)

	// Check if already mapped
	pa := WalkUserPageTableWithL0(va, l0PA)
	if pa != 0 {
		return pa
	}

	// Set shepherd/thread context for AllocUserFrame tracking
	pfContextShepherdID = currentShepherdID()
	pfContextThreadID = getCurrentThreadTID()

	if !inAllocatedUserRegion(uint64(va)) {
		klog.Errf("[DemandMap] VA 0x%x not in valid region\n", va)
		return 0
	}

	// Allocate a physical frame
	framePA := AllocUserFrame()
	if framePA == 0 {
		klog.Errf("[DemandMap] out of frames\n")
		return 0
	}

	// Map page with RWX permissions (same as HandleUserPageFault)
	elfFlags := uint32(ELF_PF_R | ELF_PF_W | ELF_PF_X)
	if !mapUserPageWithL0(pageAddr, framePA, elfFlags, l0PA) {
		klog.Errf("[DemandMap] mapUserPageWithL0 failed\n")
		FreeUserFrame(framePA)
		return 0
	}

	// Zero the page via kernel scratch mapping
	scratchVA := MapPAToKernelScratch(framePA)
	if scratchVA == 0 {
		return 0
	}
	zeroPageSlow(scratchVA)

	// Synchronize: clean cache, invalidate TLB
	CleanPageCache(scratchVA)
	dsbSY()
	tlbiVMALLE1IS()
	dsbSY()
	isbSY()

	// Track the allocation
	QueueDeferredRecord(DeferredPageRecord{
		PA:       framePA,
		VA:       pageAddr,
		Type:     PageAllocUser,
		ShepherdID: pfContextShepherdID,
		ThreadID: pfContextThreadID,
		Order:    0,
	})

	// Return PA with page offset
	return framePA | (va & (PageSize - 1))
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

	// Select root page table (arch-specific: ARM64 checks bit 63 for TTBR0/TTBR1,
	// x86_64 always uses the single PML4 from CR3).
	l0PA := selectRootPageTable(va)
	debugPrint('T')
	debugPrintHex(uint64(l0PA))

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

	if !pteIsValid(*l0Entry) {
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

		// Link new L1 table into L0 (arch-specific PTE format)
		*l0Entry = makeTablePTE(l1PA)

		// Cache clean and barriers for L0 update
		dcCIVAC(uintptr(unsafe.Pointer(l0Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		// Get existing L1 table VA - check cache first for PT pool pages
		l1PA := pteExtractPA(*l0Entry)
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

	if !pteIsValid(*l1Entry) {
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

		// Link new L2 table into L1 (arch-specific PTE format)
		*l1Entry = makeTablePTE(l2PA)
	} else {
		l2PA := pteExtractPA(*l1Entry)
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

	if !pteIsValid(*l2Entry) {
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

		// Link new L3 table into L2 (arch-specific PTE format)
		*l2Entry = makeTablePTE(l3PA)

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
		if readback != makeTablePTE(l3PA) {
			debugPrint('V')
			debugPrint('!')
			debugPrintHex(readback)
			return false
		}
		debugPrint('V') // DEBUG: Verified L2
	} else {
		l3PA := pteExtractPA(*l2Entry)
		l3VA = paToVAOrCache(l3PA)
		if l3VA == 0 {
			debugPrint('6')
			debugPrint('!')
			return false
		}
	}

	// Write L3 entry (arch-specific PTE format)
	l3Entry := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	pteValue := makeKernelPagePTE(pa)
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

// CleanCacheLine cleans and invalidates the single cache line containing va.
// Use this after writing a small amount of data (e.g., 8 bytes) to a kernel
// scratch mapping that will be read via a different VA (userspace TTBR0 mapping).
//
//go:nosplit
func CleanCacheLine(va uintptr) {
	dcCIVAC(va)
	dsbSY()
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
// readCurrentL0PA is arch-specific: reads the hardware page table base register
// and returns the L0 page table physical address.
// See paging_arm64.go, paging_riscv64.go, paging_amd64.go.
func dcZVAAsm(addr uintptr)
func bzero4KAsm(ptr uintptr)
func bzeroNAsm(ptr uintptr, n uintptr)
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

// BzeroN zeros n bytes at ptr using architecture-specific fast zeroing.
// ptr must be 8-byte aligned. n must be a multiple of 8.
// On ARM64, uses DC ZVA for 64-byte-aligned regions (≥64 bytes).
// On x86_64, uses REP STOSQ.
// On RISC-V, uses a ZERO-register store loop.
//
//go:nosplit
func BzeroN(ptr uintptr, n uintptr) {
	if n == 0 {
		return
	}
	bzeroNAsm(ptr, n)
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
// Used for aggressive ASID reuse: when a shepherd exits and its ASID will be
// reused by a new shepherd, all old TLB entries must be invalidated first.
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
// NOTE (2026-04-19): Removed stale ZERO_GUARD that hardcoded the RISC-V
// direct-boot kernel base (0x90000000-0x90400000) as the kmazarin range.
// On ARM64 (Cardinal/diplomat) and amd64, kmazarin lives at completely
// different PAs, so the guard fired spuriously on legitimate pool pages
// once the buddy allocator advanced past 0x90000000. If a real "buddy
// allocator returned a kmazarin code page" regression appears, recreate
// the check in BuddyAlloc{,Typed} using the runtime-config KmazarinPhysAddr
// and KmazarinSize — that is where the bad page would be coming from, not
// here at zero-time.
//
//go:nosplit
func zeroPageSlow(ptr uintptr) {
	// Zero 4KB in 8-byte chunks (512 iterations)
	p := (*[512]uint64)(unsafe.Pointer(ptr))
	for i := 0; i < 512; i++ {
		p[i] = 0
	}
}

// WriteTTBR0 writes a pre-constructed value to the page table register.
// Callers should use constructTTBR0Value() to build the arch-specific value.
// Prefer SwitchTTBR0WithASID() which handles barriers and TLB flush.
//
//go:nosplit
func WriteTTBR0(val uint64) {
	writeTTBR0Asm(val)
	isbSY()
}

// ReadHWL0PA reads the current L0 page table PA from the hardware register.
// This is useful for debugging to verify the page table root matches expected.
//
//go:nosplit
func ReadHWL0PA() uintptr {
	return readCurrentL0PA()
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
// MapDeviceMMIO is arch-specific — see paging_arm64.go and paging_amd64.go

// MappingError represents a page mapping failure
type MappingError struct {
	addr uintptr
	msg  string
}

func (e *MappingError) Error() string {
	return e.msg
}

// mapDevicePage is arch-specific: see paging_arm64.go, paging_amd64.go, paging_riscv64.go.

// ELF permission flags (from ksyscall/launch.go)
const (
	ELF_PF_X = 1 // Executable
	ELF_PF_W = 2 // Writable
	ELF_PF_R = 4 // Readable
)

// MapUserPage allocates a physical frame and maps it to a virtual address
// in userspace (TTBR0, low memory) with the specified ELF permissions.
//
// This is used for loading userspace programs (shepherds).
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

	// Use TTBR0 for userspace (bit 63 = 0)
	if (va>>63)&1 != 0 {
		return false // User pages must be in low memory
	}

	// Use explicit L0 if provided, otherwise read from hardware page table register.
	// CRITICAL: Don't use the global processL0PA - it can be stale when multiple
	// processes are running. The hardware register always has the correct page table.
	l0PA := l0PAParam
	if l0PA == 0 {
		l0PA = readCurrentL0PA()
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
	if !pteIsValid(*l0Entry) {
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
		*l0Entry = makeUserTablePTE(l1PA)
		dcCIVAC(uintptr(unsafe.Pointer(l0Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		l1PA := pteExtractPA(*l0Entry)
		l1VA = paToVAOrCache(l1PA)
	}
	if l1VA == 0 {
		return false
	}

	// Read L1 entry
	l1Entry := (*uint64)(unsafe.Pointer(l1VA + l1Idx*8))

	var l2VA uintptr
	if !pteIsValid(*l1Entry) {
		l2VA = allocPTPage()
		if l2VA == 0 {
			return false
		}
		l2PA := walkPageTable(l2VA)
		if l2PA == 0 {
			return false
		}
		cachePTVA(l2PA, l2VA)
		*l1Entry = makeUserTablePTE(l2PA)
		dcCIVAC(uintptr(unsafe.Pointer(l1Entry)))
		dsbSY()
	} else {
		l2PA := pteExtractPA(*l1Entry)
		l2VA = paToVAOrCache(l2PA)
		if l2VA == 0 {
			return false
		}
	}

	// Read L2 entry
	l2Entry := (*uint64)(unsafe.Pointer(l2VA + l2Idx*8))

	var l3VA uintptr
	if !pteIsValid(*l2Entry) {
		l3VA = allocPTPage()
		if l3VA == 0 {
			return false
		}
		l3PA := walkPageTable(l3VA)
		if l3PA == 0 {
			return false
		}
		cachePTVA(l3PA, l3VA)
		*l2Entry = makeUserTablePTE(l3PA)
		dcCIVAC(uintptr(unsafe.Pointer(l2Entry)))
		dsbSY()
	} else {
		l3PA := pteExtractPA(*l2Entry)
		l3VA = paToVAOrCache(l3PA)
		if l3VA == 0 {
			return false
		}
	}

	// Write L3 entry with USER permissions (arch-specific)
	l3Entry := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))

	// Check if already mapped
	if pteIsValid(*l3Entry) {
		// Already mapped - check if same PA
		existingPA := pteExtractPA(*l3Entry)
		if existingPA == pa {
			return true // Already correctly mapped
		}
		return false // Conflict!
	}

	pteValue := makeUserPagePTE(pa, elfFlags)

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
// This is used for mapping MMIO regions (like framebuffer) into shepherd address space.
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
	return MapUserDevicePageWithL0(va, pa, 0)
}

// MapUserDevicePageWithL0 maps a device page using an explicit L0 page table PA.
// If l0PAParam is 0, reads from TTBR0.
//
//go:nosplit
func MapUserDevicePageWithL0(va, pa uintptr, l0PAParam uintptr) bool {
	// Lazy initialization
	if !pagingInitialized {
		InitPaging()
	}

	// Extract indices
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	// Use TTBR0 for userspace (bit 63 = 0)
	if (va>>63)&1 != 0 {
		return false // User pages must be in low memory
	}

	// Use explicit L0 if provided, otherwise read from hardware page table register.
	l0PA := l0PAParam
	if l0PA == 0 {
		l0PA = readCurrentL0PA()
	}
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
	if !pteIsValid(*l0Entry) {
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
		*l0Entry = makeUserTablePTE(l1PA)
		dcCIVAC(uintptr(unsafe.Pointer(l0Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		l1PA := pteExtractPA(*l0Entry)
		l1VA = paToVAOrCache(l1PA)
	}
	if l1VA == 0 {
		return false
	}

	// Read L1 entry
	l1Entry := (*uint64)(unsafe.Pointer(l1VA + l1Idx*8))

	var l2VA uintptr
	if !pteIsValid(*l1Entry) {
		l2VA = allocPTPage()
		if l2VA == 0 {
			return false
		}
		l2PA := walkPageTable(l2VA)
		if l2PA == 0 {
			return false
		}
		cachePTVA(l2PA, l2VA)
		*l1Entry = makeUserTablePTE(l2PA)
		dcCIVAC(uintptr(unsafe.Pointer(l1Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		l2PA := pteExtractPA(*l1Entry)
		l2VA = paToVAOrCache(l2PA)
	}
	if l2VA == 0 {
		return false
	}

	// Read L2 entry
	l2Entry := (*uint64)(unsafe.Pointer(l2VA + l2Idx*8))

	var l3VA uintptr
	if !pteIsValid(*l2Entry) {
		l3VA = allocPTPage()
		if l3VA == 0 {
			return false
		}
		l3PA := walkPageTable(l3VA)
		if l3PA == 0 {
			return false
		}
		cachePTVA(l3PA, l3VA)
		*l2Entry = makeUserTablePTE(l3PA)
		dcCIVAC(uintptr(unsafe.Pointer(l2Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		l3PA := pteExtractPA(*l2Entry)
		l3VA = paToVAOrCache(l3PA)
	}
	if l3VA == 0 {
		return false
	}

	// Write L3 entry with device attributes (arch-specific)
	l3Entry := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))

	// Check if already mapped
	if pteIsValid(*l3Entry) {
		// Already mapped - verify it points to same PA
		existingPA := pteExtractPA(*l3Entry)
		if existingPA == pa {
			return true // Already correctly mapped
		}
		return false // Conflict!
	}

	pteValue := makeUserDevicePTE(pa)

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
// framebufferPA is the GPU's actual framebuffer physical address.
// framebufferSize is the framebuffer size in bytes.
// Returns true on success. Uses current TTBR0 to find the page table.
func MapUserFramebuffer(framebufferPA uintptr, framebufferSize uintptr) bool {
	return MapUserFramebufferWithL0(framebufferPA, framebufferSize, 0)
}

// MapUserFramebufferWithL0 maps the framebuffer using an explicit L0 page table PA.
// If l0PA is 0, reads from TTBR0.
func MapUserFramebufferWithL0(framebufferPA uintptr, framebufferSize uintptr, l0PA uintptr) bool {
	if framebufferPA == 0 || framebufferSize == 0 {
		return false
	}

	// Fixed userspace VA for framebuffer (matches ksyscall.UserFramebufferVA)
	const framebufferVA = 0x00007FFE00000000

	pageSize := uintptr(0x1000)
	numPages := framebufferSize / pageSize

	for i := uintptr(0); i < numPages; i++ {
		va := uintptr(framebufferVA) + i*pageSize
		pa := framebufferPA + i*pageSize
		if !MapUserDevicePageWithL0(va, pa, l0PA) {
			return false
		}
	}
	return true
}

// MapPAToKernelScratch returns a kernel VA for reading a physical page.
// All physical RAM is permanently identity-mapped at KernelVAOffset via 2MB
// block descriptors set up by diplomat, so this is a pure arithmetic translation
// with no TLB entries, no page table writes, and nothing to release.
// Safe to call from any goroutine at any time.
func MapPAToKernelScratch(pa uintptr) uintptr {
	return pa + constants.KernelVAOffset
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

	// Userspace addresses must have bit 63 = 0
	if (va>>63)&1 != 0 {
		return 0
	}

	// Extract indices
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	// Use explicit L0 if provided, otherwise read from hardware page table register.
	// CRITICAL: Don't use the global processL0PA - it can be stale when multiple
	// processes are running. The hardware register always has the correct page table.
	l0PA := l0PAParam
	if l0PA == 0 {
		l0PA = readCurrentL0PA()
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
	if !pteIsValid(l0Entry) {
		return 0
	}

	// L1 table
	l1PA := pteExtractPA(l0Entry)
	l1VA := paToVAOrCache(l1PA)
	if l1VA == 0 {
		return 0
	}
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if !pteIsValid(l1Entry) {
		return 0
	}

	// L2 table
	l2PA := pteExtractPA(l1Entry)
	l2VA := paToVAOrCache(l2PA)
	if l2VA == 0 {
		return 0
	}
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if !pteIsValid(l2Entry) {
		return 0
	}

	// L3 table
	l3PA := pteExtractPA(l2Entry)
	l3VA := paToVAOrCache(l3PA)
	if l3VA == 0 {
		return 0
	}
	l3Entry := *(*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	if !pteIsValid(l3Entry) {
		return 0
	}

	// Extract PA from L3 entry and add page offset
	pa := pteExtractPA(l3Entry)
	return pa | (va & (PageSize - 1))
}

// WalkUserPTLean translates a userspace VA to PA using an explicit L0 page table.
// Unlike WalkUserPageTableWithL0, this function:
//   - Skips pagingInitialized check (caller guarantees paging is initialized)
//   - Uses direct linear map (pa + KernelVAOffset) instead of VA cache
//   - Requires l0PA to be non-zero
//
// Designed for use from deep nosplit call chains (signal frame building)
// where the full WalkUserPageTableWithL0 exceeds the nosplit stack budget.
//
//go:nosplit
func WalkUserPTLean(va uintptr, l0PA uintptr) uintptr {
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	l0VA := l0PA + constants.KernelVAOffset
	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	if !pteIsValid(l0Entry) {
		return 0
	}

	l1PA := pteExtractPA(l0Entry)
	l1VA := l1PA + constants.KernelVAOffset
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if !pteIsValid(l1Entry) {
		return 0
	}

	l2PA := pteExtractPA(l1Entry)
	l2VA := l2PA + constants.KernelVAOffset
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if !pteIsValid(l2Entry) {
		return 0
	}

	l3PA := pteExtractPA(l2Entry)
	l3VA := l3PA + constants.KernelVAOffset
	l3Entry := *(*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	if !pteIsValid(l3Entry) {
		return 0
	}

	pa := pteExtractPA(l3Entry)
	return pa | (va & (PageSize - 1))
}

// repeatFaultDiagnostic prints repeat-fault diagnostic information.
// NOT nosplit — called from HandleUserPageFault only on the fatal repeat-fault path.
//
//go:noinline
func repeatFaultDiagnostic(faultAddr uintptr) {
	klog.Errf("[PF] REPEAT VA=0x%x CR3=0x%x\n", faultAddr, readCurrentL0PA())
}

// DumpUserPTEWithL0 walks the page table for a userspace VA and prints each level's entry.
// Used for diagnostic purposes — called on error paths.
func DumpUserPTEWithL0(va uintptr, l0PAParam uintptr) {
	// Userspace addresses must have bit 63 = 0
	if (va>>63)&1 != 0 {
		klog.Errf("[DumpPTE] VA has bit63=1 (kernel addr)\n")
		return
	}

	// Extract indices
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	klog.Errf("[DumpPTE] VA=0x%x L0PA=0x%x indices=[%d,%d,%d,%d]\n",
		va, l0PAParam, l0Idx, l1Idx, l2Idx, l3Idx)

	// Use explicit L0 if provided
	l0PA := l0PAParam
	if l0PA == 0 {
		l0PA = readCurrentL0PA()
	}
	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		klog.Errf("[DumpPTE] Failed to get L0 VA from cache\n")
		return
	}

	// L0 entry
	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	klog.Errf("[DumpPTE] L0[%d]=0x%016x (VA=0x%x)\n", l0Idx, l0Entry, l0VA+l0Idx*8)
	if !pteIsValid(l0Entry) {
		klog.Errf("[DumpPTE] L0 entry INVALID\n")
		return
	}

	// L1 table
	l1PA := pteExtractPA(l0Entry)
	l1VA := paToVAOrCache(l1PA)
	if l1VA == 0 {
		klog.Errf("[DumpPTE] Failed to get L1 VA from cache (L1PA=0x%x)\n", l1PA)
		return
	}
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	klog.Errf("[DumpPTE] L1[%d]=0x%016x (PA=0x%x VA=0x%x)\n", l1Idx, l1Entry, l1PA, l1VA+l1Idx*8)
	if !pteIsValid(l1Entry) {
		klog.Errf("[DumpPTE] L1 entry INVALID\n")
		return
	}

	// L2 table
	l2PA := pteExtractPA(l1Entry)
	l2VA := paToVAOrCache(l2PA)
	if l2VA == 0 {
		klog.Errf("[DumpPTE] Failed to get L2 VA from cache (L2PA=0x%x)\n", l2PA)
		return
	}
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	klog.Errf("[DumpPTE] L2[%d]=0x%016x (PA=0x%x VA=0x%x)\n", l2Idx, l2Entry, l2PA, l2VA+l2Idx*8)
	if !pteIsValid(l2Entry) {
		klog.Errf("[DumpPTE] L2 entry INVALID\n")
		return
	}

	// L3 table
	l3PA := pteExtractPA(l2Entry)
	l3VA := paToVAOrCache(l3PA)
	if l3VA == 0 {
		klog.Errf("[DumpPTE] Failed to get L3 VA from cache (L3PA=0x%x)\n", l3PA)
		return
	}
	l3Entry := *(*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	klog.Errf("[DumpPTE] L3[%d]=0x%016x (PA=0x%x VA=0x%x)\n", l3Idx, l3Entry, l3PA, l3VA+l3Idx*8)
	if !pteIsValid(l3Entry) {
		klog.Errf("[DumpPTE] L3 entry INVALID\n")
		return
	}

	// Decode PTE attributes (ARM64-specific bit positions)
	// TODO: Make arch-specific when needed for x86_64/RISC-V debugging
	pa := uint64(pteExtractPA(l3Entry))
	ap := (l3Entry >> 6) & 0x3
	sh := (l3Entry >> 8) & 0x3
	attr := (l3Entry >> 2) & 0x7
	af := (l3Entry >> 10) & 0x1
	uxn := (l3Entry >> 54) & 0x1
	pxn := (l3Entry >> 53) & 0x1

	klog.Errf("[DumpPTE] PA=0x%x AP=%d SH=%d ATTR=%d AF=%d UXN=%d PXN=%d\n",
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

	// Userspace addresses must have bit 63 = 0
	if (va>>63)&1 != 0 {
		return 0
	}

	// Align to page boundary
	pageVA := va &^ (PageSize - 1)

	// Extract indices
	l0Idx := (pageVA >> L0Shift) & 0x1FF
	l1Idx := (pageVA >> L1Shift) & 0x1FF
	l2Idx := (pageVA >> L2Shift) & 0x1FF
	l3Idx := (pageVA >> L3Shift) & 0x1FF

	// CRITICAL: Get the current process's L0PA from the hardware page table register,
	// NOT from the global processL0PA. The global can be stale if multiple processes
	// are running. The hardware register always has the correct page table.
	l0PA := readCurrentL0PA()
	if l0PA == 0 {
		l0PA = ttbr0L0PA
	}
	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		return 0
	}

	// L0 entry
	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	if !pteIsValid(l0Entry) {
		return 0 // Not mapped
	}

	// L1 table
	l1PA := pteExtractPA(l0Entry)
	l1VA := paToVAOrCache(l1PA)
	if l1VA == 0 {
		return 0
	}
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if !pteIsValid(l1Entry) {
		return 0 // Not mapped
	}

	// L2 table
	l2PA := pteExtractPA(l1Entry)
	l2VA := paToVAOrCache(l2PA)
	if l2VA == 0 {
		return 0
	}
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if !pteIsValid(l2Entry) {
		return 0 // Not mapped
	}

	// L3 table
	l3PA := pteExtractPA(l2Entry)
	l3VA := paToVAOrCache(l3PA)
	if l3VA == 0 {
		return 0
	}
	l3EntryPtr := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	l3Entry := *l3EntryPtr
	if !pteIsValid(l3Entry) {
		return 0 // Not mapped
	}

	// Get PA before clearing
	pa := pteExtractPA(l3Entry)


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

// UnmapUserPageWithL0 removes the mapping for a userspace page using an explicit L0 PA.
// This variant is safe to use when the target process is not the currently active one.
// Returns the physical address that was mapped, or 0 if not mapped.
// Does NOT free the physical frame — caller is responsible for that.
//
//go:nosplit
func UnmapUserPageWithL0(va uintptr, l0PAParam uintptr) uintptr {
	if !pagingInitialized {
		InitPaging()
	}

	// Userspace addresses must have bit 63 = 0
	if (va>>63)&1 != 0 {
		return 0
	}

	// Align to page boundary
	pageVA := va &^ (PageSize - 1)

	// Extract indices
	l0Idx := (pageVA >> L0Shift) & 0x1FF
	l1Idx := (pageVA >> L1Shift) & 0x1FF
	l2Idx := (pageVA >> L2Shift) & 0x1FF
	l3Idx := (pageVA >> L3Shift) & 0x1FF

	// Use explicit L0 if provided, otherwise read from hardware
	l0PA := l0PAParam
	if l0PA == 0 {
		l0PA = readCurrentL0PA()
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
	if !pteIsValid(l0Entry) {
		return 0
	}

	// L1 table
	l1PA := pteExtractPA(l0Entry)
	l1VA := paToVAOrCache(l1PA)
	if l1VA == 0 {
		return 0
	}
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if !pteIsValid(l1Entry) {
		return 0
	}

	// L2 table
	l2PA := pteExtractPA(l1Entry)
	l2VA := paToVAOrCache(l2PA)
	if l2VA == 0 {
		return 0
	}
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if !pteIsValid(l2Entry) {
		return 0
	}

	// L3 table
	l3PA := pteExtractPA(l2Entry)
	l3VA := paToVAOrCache(l3PA)
	if l3VA == 0 {
		return 0
	}
	l3EntryPtr := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	l3Entry := *l3EntryPtr
	if !pteIsValid(l3Entry) {
		return 0
	}

	// Get PA before clearing
	pa := pteExtractPA(l3Entry)


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

// GetUserPTEFlags walks a specific shepherd's L0 page table to the L3 leaf for
// va and returns the page's ELF permission flags (ELF_PF_R/W/X). Returns
// (0, false) if va is not mapped at L3 in this address space.
//
// Used by the TransferPages / TransferDMAClump rollback path to capture the
// caller's *original* PTE flags in Pass 1 so the rollback can restore them
// exactly. The syscall's elfFlags argument controls the target's mapping
// permissions, not the caller's — so a non-default elfFlags would silently
// change caller permissions on rollback if we used it. See MAZ-39.
//
// Walks an explicit l0PA rather than the hardware register so the helper is
// also usable for inspecting non-active address spaces (mirrors the pattern
// in UnmapUserPageWithL0). Nosplit because the only caller is the IRQ-off
// inner of SyscallTransferPages / SyscallTransferDMAClump.
//
//go:nosplit
func GetUserPTEFlags(va, l0PAParam uintptr) (uint32, bool) {
	if !pagingInitialized {
		InitPaging()
	}

	if (va>>63)&1 != 0 {
		return 0, false
	}

	pageVA := va &^ (PageSize - 1)

	l0Idx := (pageVA >> L0Shift) & 0x1FF
	l1Idx := (pageVA >> L1Shift) & 0x1FF
	l2Idx := (pageVA >> L2Shift) & 0x1FF
	l3Idx := (pageVA >> L3Shift) & 0x1FF

	l0PA := l0PAParam
	if l0PA == 0 {
		l0PA = readCurrentL0PA()
	}
	if l0PA == 0 {
		l0PA = ttbr0L0PA
	}
	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		return 0, false
	}

	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	if !pteIsValid(l0Entry) {
		return 0, false
	}

	l1PA := pteExtractPA(l0Entry)
	l1VA := paToVAOrCache(l1PA)
	if l1VA == 0 {
		return 0, false
	}
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if !pteIsValid(l1Entry) {
		return 0, false
	}

	l2PA := pteExtractPA(l1Entry)
	l2VA := paToVAOrCache(l2PA)
	if l2VA == 0 {
		return 0, false
	}
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if !pteIsValid(l2Entry) {
		return 0, false
	}

	l3PA := pteExtractPA(l2Entry)
	l3VA := paToVAOrCache(l3PA)
	if l3VA == 0 {
		return 0, false
	}
	l3Entry := *(*uint64)(unsafe.Pointer(l3VA + l3Idx*8))
	if !pteIsValid(l3Entry) {
		return 0, false
	}

	return ElfFlagsFromPTEFlags(platformPTEToFlags(l3Entry)), true
}

// MapPageInProcess maps a physical page into a specific shepherd's address space.
// Saves/restores pfContext so page table pages allocated during the mapping
// are attributed to the target shepherd.
// If elfFlags is 0, defaults to ELF_PF_R | ELF_PF_W.
// Returns true on success.
//
// Nosplit so callers in IRQ-off critical sections (e.g. transferDMAClumpInner,
// transferPagesChunkInner) don't have to rely on transitive linker analysis
// alone — the entire reachable graph (FindShepherdBySID, mapUserPageWithL0,
// etc.) is already nosplit-safe, as proven by MapContiguousUserPages.
//
//go:nosplit
func MapPageInProcess(shepherdID int16, va, pa uintptr, elfFlags uint32) bool {
	if ConsumeMapFailInjection() {
		return false
	}
	p := proc.FindShepherdBySID(proc.ShepherdId(shepherdID))
	if p == nil || p.PageTableL0PA == 0 {
		return false
	}
	if elfFlags == 0 {
		elfFlags = ELF_PF_R | ELF_PF_W
	}
	// Save/restore pfContext so allocPTPage attributes PT pages to target shepherd
	savedPID := pfContextShepherdID
	savedTID := pfContextThreadID
	pfContextShepherdID = shepherdID
	pfContextThreadID = -1
	ok := mapUserPageWithL0(va, pa, elfFlags, p.PageTableL0PA)
	pfContextShepherdID = savedPID
	pfContextThreadID = savedTID
	return ok
}

// MapContiguousUserPages maps numPages physically contiguous pages starting at
// basePA into the shepherd's address space at baseVA. All pages are mapped RW,
// no-execute. The pfContext is saved/restored so page table pages are attributed
// to the correct shepherd. Returns true if all pages were mapped successfully.
//
//go:nosplit
func MapContiguousUserPages(shepherdID int16, l0PA, baseVA, basePA uintptr, numPages int) bool {
	savedPID := pfContextShepherdID
	savedTID := pfContextThreadID
	pfContextShepherdID = shepherdID
	pfContextThreadID = -1
	elfFlags := uint32(ELF_PF_R | ELF_PF_W)
	for i := 0; i < numPages; i++ {
		va := baseVA + uintptr(i)*PageSize
		pa := basePA + uintptr(i)*PageSize
		if !mapUserPageWithL0(va, pa, elfFlags, l0PA) {
			pfContextShepherdID = savedPID
			pfContextThreadID = savedTID
			return false
		}
	}
	pfContextShepherdID = savedPID
	pfContextThreadID = savedTID
	return true
}

// GetUserL3PTE returns the raw L3 PTE for a userspace VA.
// Uses the process-specific page table if one exists.
// Useful for debugging page table entries.
func GetUserL3PTE(va uintptr) uint64 {
	if !pagingInitialized {
		InitPaging()
	}
	if (va>>63)&1 != 0 {
		return 0
	}
	l0Idx := (va >> L0Shift) & 0x1FF
	l1Idx := (va >> L1Shift) & 0x1FF
	l2Idx := (va >> L2Shift) & 0x1FF
	l3Idx := (va >> L3Shift) & 0x1FF

	// CRITICAL: Get the current process's L0PA from the hardware page table register,
	// NOT from the global processL0PA. The global can be stale if multiple processes
	// are running. The hardware register always has the correct page table.
	l0PA := readCurrentL0PA()
	if l0PA == 0 {
		l0PA = ttbr0L0PA
	}
	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		return 0
	}
	l0Entry := *(*uint64)(unsafe.Pointer(l0VA + l0Idx*8))
	if !pteIsValid(l0Entry) {
		return 0
	}
	l1PA := pteExtractPA(l0Entry)
	l1VA := paToVAOrCache(l1PA)
	if l1VA == 0 {
		return 0
	}
	l1Entry := *(*uint64)(unsafe.Pointer(l1VA + l1Idx*8))
	if !pteIsValid(l1Entry) {
		return 0
	}
	l2PA := pteExtractPA(l1Entry)
	l2VA := paToVAOrCache(l2PA)
	if l2VA == 0 {
		return 0
	}
	l2Entry := *(*uint64)(unsafe.Pointer(l2VA + l2Idx*8))
	if !pteIsValid(l2Entry) {
		return 0
	}
	l3PA := pteExtractPA(l2Entry)
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

// ReadUserByte reads a single byte from a virtual address.
// For kernel addresses, reads directly. For user addresses, walks page tables
// and reads through the kernel linear map.
// Returns the byte value and true if successful, 0 and false otherwise.
//
//go:nosplit
func ReadUserByte(va uintptr) (byte, bool) {
	if isKernelAddr(va) {
		return *(*byte)(unsafe.Pointer(va)), true
	}
	pa := WalkUserPageTable(va)
	if pa == 0 {
		return 0, false
	}
	pagePA := pa &^ (PageSize - 1)
	pageOffset := pa & (PageSize - 1)
	kernelVA := MapPAToKernelScratch(pagePA)
	if kernelVA == 0 {
		return 0, false
	}
	return *(*byte)(unsafe.Pointer(kernelVA + pageOffset)), true
}

// ReadUserUint64 reads a 64-bit value from a virtual address.
// For kernel addresses, reads directly. For user addresses, walks page tables
// and reads through the kernel linear map.
// Returns the value and true if successful, 0 and false otherwise.
//
// NOTE: This assumes the value doesn't cross a page boundary. For stack values
// which are 8-byte aligned, this should not be an issue.
//
//go:nosplit
func ReadUserUint64(va uintptr) (uint64, bool) {
	if isKernelAddr(va) {
		return *(*uint64)(unsafe.Pointer(va)), true
	}
	pa := WalkUserPageTable(va)
	if pa == 0 {
		return 0, false
	}
	pagePA := pa &^ (PageSize - 1)
	pageOffset := pa & (PageSize - 1)
	kernelVA := MapPAToKernelScratch(pagePA)
	if kernelVA == 0 {
		return 0, false
	}
	return *(*uint64)(unsafe.Pointer(kernelVA + pageOffset)), true
}

// isKernelAddr returns true if the address is in kernel VA space.
// On all architectures, kernel addresses have the top 16 bits set (0xFFFFxxxx...).
// This covers the kmazarin code region, linear map, and heap/stack allocations.
//
//go:nosplit
func isKernelAddr(va uintptr) bool {
	return va&0xFFFF000000000000 != 0
}

// ReadUserUint32 reads a 32-bit value from a virtual address.
// For kernel addresses, reads directly. For user addresses, walks page tables
// and reads through the kernel linear map.
// Returns the value and true if successful, 0 and false otherwise.
//
//go:nosplit
func ReadUserUint32(va uintptr) (uint32, bool) {
	if isKernelAddr(va) {
		return *(*uint32)(unsafe.Pointer(va)), true
	}
	pa := WalkUserPageTable(va)
	if pa == 0 {
		return 0, false
	}
	pagePA := pa &^ (PageSize - 1)
	pageOffset := pa & (PageSize - 1)
	kernelVA := MapPAToKernelScratch(pagePA)
	if kernelVA == 0 {
		return 0, false
	}
	return *(*uint32)(unsafe.Pointer(kernelVA + pageOffset)), true
}

// ReadUserInt64Pair reads two consecutive int64 values from a virtual address.
// For kernel addresses, reads directly. For user addresses, uses page table walk.
// Used for reading timespec structs {tv_sec, tv_nsec}.
//
//go:nosplit
func ReadUserInt64Pair(va uintptr) ([2]int64, bool) {
	if isKernelAddr(va) {
		return *(*[2]int64)(unsafe.Pointer(va)), true
	}
	pa := WalkUserPageTable(va)
	if pa == 0 {
		return [2]int64{}, false
	}
	pagePA := pa &^ (PageSize - 1)
	pageOffset := pa & (PageSize - 1)
	kernelVA := MapPAToKernelScratch(pagePA)
	if kernelVA == 0 {
		return [2]int64{}, false
	}
	return *(*[2]int64)(unsafe.Pointer(kernelVA + pageOffset)), true
}

// WriteUserByte writes a single byte to a virtual address.
// For kernel addresses, writes directly. For user addresses, uses page table walk.
// Returns true if successful, false otherwise.
//
//go:nosplit
func WriteUserByte(va uintptr, val byte) bool {
	if isKernelAddr(va) {
		*(*byte)(unsafe.Pointer(va)) = val
		return true
	}
	pa := WalkUserPageTable(va)
	if pa == 0 {
		return false
	}
	pagePA := pa &^ (PageSize - 1)
	pageOffset := pa & (PageSize - 1)
	kernelVA := MapPAToKernelScratch(pagePA)
	if kernelVA == 0 {
		return false
	}
	*(*byte)(unsafe.Pointer(kernelVA + pageOffset)) = val
	return true
}

// WriteUserUint32 writes a 32-bit value to a virtual address.
// For kernel addresses, writes directly. For user addresses, uses page table walk.
// Returns true if successful, false otherwise.
//
//go:nosplit
func WriteUserUint32(va uintptr, val uint32) bool {
	if isKernelAddr(va) {
		*(*uint32)(unsafe.Pointer(va)) = val
		return true
	}
	pa := WalkUserPageTable(va)
	if pa == 0 {
		return false
	}
	pagePA := pa &^ (PageSize - 1)
	pageOffset := pa & (PageSize - 1)
	kernelVA := MapPAToKernelScratch(pagePA)
	if kernelVA == 0 {
		return false
	}
	*(*uint32)(unsafe.Pointer(kernelVA + pageOffset)) = val
	return true
}

// WriteUserUint64 writes a 64-bit value to a virtual address.
// For kernel addresses, writes directly. For user addresses, uses page table walk.
// Returns true if successful, false otherwise.
//
//go:nosplit
func WriteUserUint64(va uintptr, val uint64) bool {
	if isKernelAddr(va) {
		*(*uint64)(unsafe.Pointer(va)) = val
		return true
	}
	pa := WalkUserPageTable(va)
	if pa == 0 {
		return false
	}
	pagePA := pa &^ (PageSize - 1)
	pageOffset := pa & (PageSize - 1)
	kernelVA := MapPAToKernelScratch(pagePA)
	if kernelVA == 0 {
		return false
	}
	*(*uint64)(unsafe.Pointer(kernelVA + pageOffset)) = val
	return true
}

// WriteUserUint64WithL0 writes a 64-bit value to a userspace VA using an explicit page table.
// For kernel addresses, writes directly. For user addresses, walks the given page table
// and writes through the kernel linear map.
// Assumes the value does not cross a page boundary (8-byte aligned VA).
//
//go:nosplit
func WriteUserUint64WithL0(va uintptr, val uint64, l0PA uintptr) bool {
	if isKernelAddr(va) {
		*(*uint64)(unsafe.Pointer(va)) = val
		return true
	}
	pa := WalkUserPTLean(va, l0PA)
	if pa == 0 {
		return false
	}
	kernelVA := pa + constants.KernelVAOffset
	*(*uint64)(unsafe.Pointer(kernelVA)) = val
	return true
}

// WriteUserUint32WithL0 writes a 32-bit value to a userspace VA using an explicit page table.
//
//go:nosplit
func WriteUserUint32WithL0(va uintptr, val uint32, l0PA uintptr) bool {
	if isKernelAddr(va) {
		*(*uint32)(unsafe.Pointer(va)) = val
		return true
	}
	pa := WalkUserPTLean(va, l0PA)
	if pa == 0 {
		return false
	}
	kernelVA := pa + constants.KernelVAOffset
	*(*uint32)(unsafe.Pointer(kernelVA)) = val
	return true
}

// WriteUserInt32WithL0 writes a signed 32-bit value to a userspace VA using an explicit page table.
//
//go:nosplit
func WriteUserInt32WithL0(va uintptr, val int32, l0PA uintptr) bool {
	if isKernelAddr(va) {
		*(*int32)(unsafe.Pointer(va)) = val
		return true
	}
	pa := WalkUserPTLean(va, l0PA)
	if pa == 0 {
		return false
	}
	kernelVA := pa + constants.KernelVAOffset
	*(*int32)(unsafe.Pointer(kernelVA)) = val
	return true
}

// WriteUserUint16WithL0 writes a 16-bit value to a userspace VA using an explicit page table.
//
//go:nosplit
func WriteUserUint16WithL0(va uintptr, val uint16, l0PA uintptr) bool {
	if isKernelAddr(va) {
		*(*uint16)(unsafe.Pointer(va)) = val
		return true
	}
	pa := WalkUserPTLean(va, l0PA)
	if pa == 0 {
		return false
	}
	kernelVA := pa + constants.KernelVAOffset
	*(*uint16)(unsafe.Pointer(kernelVA)) = val
	return true
}

// ReadUserUint64WithL0 reads a 64-bit value from a userspace VA using an explicit page table.
// For kernel addresses, reads directly. For user addresses, walks the given page table
// and reads through the kernel linear map.
//
//go:nosplit
func ReadUserUint64WithL0(va uintptr, l0PA uintptr) (uint64, bool) {
	if isKernelAddr(va) {
		return *(*uint64)(unsafe.Pointer(va)), true
	}
	pa := WalkUserPTLean(va, l0PA)
	if pa == 0 {
		return 0, false
	}
	kernelVA := pa + constants.KernelVAOffset
	return *(*uint64)(unsafe.Pointer(kernelVA)), true
}

// ZeroUserMemoryWithL0 zeros a region of userspace memory using an explicit page table.
// Handles page boundaries by processing one page at a time.
//
//go:nosplit
func ZeroUserMemoryWithL0(va uintptr, n uintptr, l0PA uintptr) bool {
	if isKernelAddr(va) {
		for i := uintptr(0); i < n; i++ {
			*(*byte)(unsafe.Pointer(va + i)) = 0
		}
		return true
	}
	zeroed := uintptr(0)
	for zeroed < n {
		currentVA := va + zeroed
		pa := WalkUserPTLean(currentVA, l0PA)
		if pa == 0 {
			return false
		}
		kernelVA := pa + constants.KernelVAOffset
		// Calculate how many bytes we can zero on this page
		pageOffset := currentVA & (PageSize - 1)
		pageRemain := PageSize - pageOffset
		chunk := n - zeroed
		if chunk > pageRemain {
			chunk = pageRemain
		}
		for i := uintptr(0); i < chunk; i++ {
			*(*byte)(unsafe.Pointer(kernelVA + i)) = 0
		}
		zeroed += chunk
	}
	return true
}

// EnsureUserPageMappedWithL0 ensures the 4KB page containing userVA is
// backed by a physical frame in the page table rooted at l0PA.
// Returns the page-aligned PA of the (possibly newly-allocated) frame.
// If allocation/mapping fails, returns (0, false).
//
// Safe to call from nosplit context (signal delivery, exception handlers,
// CopyToUser fault retry). Uses the same allocation path as HandleUserPageFault.
//
//go:nosplit
func EnsureUserPageMappedWithL0(userVA uintptr, l0PA uintptr) (uintptr, bool) {
	pageAddr := userVA &^ (PageSize - 1)

	if pa := WalkUserPTLean(pageAddr, l0PA); pa != 0 {
		return pa, true
	}

	// Refuse to demand-map outside the shepherd's allocated regions —
	// without this, a crafted syscall-buffer pointer routed through
	// CopyFromUser/CopyToUser fault-on-miss can force the kernel to
	// materialize a mapping anywhere in the VA space.
	if !inAllocatedUserRegion(uint64(pageAddr)) {
		return 0, false
	}

	pfContextShepherdID = currentShepherdID()
	framePA := AllocUserFrame()
	if framePA == 0 {
		return 0, false
	}

	// Map RWX (signal stack needs RW at minimum).
	elfFlags := uint32(ELF_PF_R | ELF_PF_W | ELF_PF_X)
	if !mapUserPageWithL0(pageAddr, framePA, elfFlags, l0PA) {
		// PT-page OOM is the realistic trigger here; rare.
		FreeUserFrame(framePA)
		return 0, false
	}

	scratchVA := framePA + constants.KernelVAOffset
	zeroPageSlow(scratchVA)
	CleanPageCache(scratchVA)
	dsbSY()
	tlbiVMALLE1IS()
	dsbSY()
	isbSY()

	// Track the allocation so the page is reclaimed on shepherd exit. The
	// helper is non-nosplit to keep its DeferredPageRecord struct literal
	// off this function's nosplit chain — same pattern as HandleUserPageFault's
	// repeatFaultDiagnostic call.
	queueEnsuredUserPageRecord(framePA, pageAddr)

	return framePA, true
}

// queueEnsuredUserPageRecord is the deferred-record enqueue path lifted out of
// EnsureUserPageMappedWithL0. Non-nosplit on purpose so its struct literal
// doesn't count against the nosplit caller chain.
//
//go:noinline
func queueEnsuredUserPageRecord(framePA, pageAddr uintptr) {
	QueueDeferredRecord(DeferredPageRecord{
		PA:         framePA,
		VA:         pageAddr,
		Type:       PageAllocUser,
		ShepherdID: pfContextShepherdID,
		ThreadID:   getCurrentThreadTID(),
		Order:      0,
	})
}

// resolveUserPageWithFallback returns the byte-offset PA for userVA,
// demand-paging if the PTE is missing. Used by CopyFromUser/CopyToUser to
// open-code Linux's fault-on-miss copy_to_user/copy_from_user semantics:
// walk → if miss, dispatch to the file-mapped fault handler if the page is
// file-backed, otherwise fall back to anonymous-zero demand allocation via
// EnsureUserPageMappedWithL0. The file-mapping branch is what stops the
// anonymous fallback from silently zeroing a file-backed mmap page on first
// access. Returns (0, false) on failure; caller propagates as a user fault.
//
// Non-nosplit on purpose: its own stack frame plus the chain through
// EnsureUserPageMappedWithL0 would push SyscallWrite's nosplit budget over
// the 792-byte limit. Same pattern as HandleUserPageFault →
// repeatFaultDiagnostic.
//
//go:noinline
func resolveUserPageWithFallback(va, l0PA uintptr) (uintptr, bool) {
	// Walk via the caller's captured l0PA, not the live TTBR0. The active
	// page table can change under preemption between the caller's snapshot
	// and this walk; using l0PA keeps the lookup consistent with what the
	// caller intends to copy from/to.
	if pa := WalkUserPageTableWithL0(va, l0PA); pa != 0 {
		return pa, true
	}
	if l0PA == 0 {
		return 0, false
	}
	pageAddr := va &^ (PageSize - 1)
	if fm := currentShepherdFindFileMapping(uint64(pageAddr)); fm != nil {
		if OnFileMappedPageFault == nil || !OnFileMappedPageFault(va, fm) {
			return 0, false
		}
		pa := WalkUserPageTableWithL0(va, l0PA)
		if pa == 0 {
			return 0, false
		}
		return pa, true
	}
	pagePA, ok := EnsureUserPageMappedWithL0(va, l0PA)
	if !ok {
		return 0, false
	}
	return pagePA | (va & (PageSize - 1)), true
}

// CopyFromUser copies n bytes from a VA into dst.
// For kernel addresses, copies directly. For user addresses, handles page
// boundaries by processing one page at a time.
// Returns true if all bytes were copied, false on any fault.
//
//go:nosplit
func CopyFromUser(dst []byte, userVA uintptr, n int) bool {
	if isKernelAddr(userVA) {
		for i := 0; i < n; i++ {
			dst[i] = *(*byte)(unsafe.Pointer(userVA + uintptr(i)))
		}
		return true
	}
	l0PA := uintptr(readCurrentL0PA())
	copied := 0
	for copied < n {
		va := userVA + uintptr(copied)
		pa, ok := resolveUserPageWithFallback(va, l0PA)
		if !ok {
			return false
		}
		pagePA := pa &^ (PageSize - 1)
		pageOffset := pa & (PageSize - 1)
		kernelVA := MapPAToKernelScratch(pagePA)
		if kernelVA == 0 {
			return false
		}
		pageRemain := int(PageSize - pageOffset)
		chunk := n - copied
		if chunk > pageRemain {
			chunk = pageRemain
		}
		src := kernelVA + pageOffset
		for i := 0; i < chunk; i++ {
			dst[copied+i] = *(*byte)(unsafe.Pointer(src + uintptr(i)))
		}
		copied += chunk
	}
	return true
}

// CopyToUser copies src bytes to a VA.
// For kernel addresses, copies directly. For user addresses, handles page
// boundaries by processing one page at a time.
// Returns true if all bytes were copied, false on any fault.
//
//go:nosplit
func CopyToUser(userVA uintptr, src []byte) bool {
	n := len(src)
	if isKernelAddr(userVA) {
		for i := 0; i < n; i++ {
			*(*byte)(unsafe.Pointer(userVA + uintptr(i))) = src[i]
		}
		return true
	}
	// CopyToUserWithL0 keeps the original "fail on miss" semantics because
	// it runs in delegation-reply context where the L0 doesn't match the
	// current TTBR0 and faulting would install pages in the wrong process.
	l0PA := uintptr(readCurrentL0PA())
	copied := 0
	for copied < n {
		va := userVA + uintptr(copied)
		pa, ok := resolveUserPageWithFallback(va, l0PA)
		if !ok {
			return false
		}
		pagePA := pa &^ (PageSize - 1)
		pageOffset := pa & (PageSize - 1)
		kernelVA := MapPAToKernelScratch(pagePA)
		if kernelVA == 0 {
			return false
		}
		pageRemain := int(PageSize - pageOffset)
		chunk := n - copied
		if chunk > pageRemain {
			chunk = pageRemain
		}
		dst := kernelVA + pageOffset
		for i := 0; i < chunk; i++ {
			*(*byte)(unsafe.Pointer(dst + uintptr(i))) = src[copied+i]
		}
		copied += chunk
	}
	return true
}

// CopyToUserWithL0 copies up to n bytes from src to a userspace VA using an
// explicit L0 page table. Returns the number of bytes actually copied.
// On a fault mid-copy, returns the partial count (like Linux's copy_to_user).
// This is needed when copying into a different process's address space
// (e.g., copying read() results back to the original caller from a delegated
// syscall handler running in a different shepherd).
func CopyToUserWithL0(userVA uintptr, l0PA uintptr, src []byte, n int) int {
	if n > len(src) {
		n = len(src)
	}
	copied := 0
	for copied < n {
		va := userVA + uintptr(copied)
		pa := WalkUserPageTableWithL0(va, l0PA)
		if pa == 0 {
			// Do NOT call HandleUserPageFault here — it reads TTBR0 and
			// CurrentShepherd() to walk the page table and validate the VA
			// against bump/span regions. During delegation reply, those
			// belong to the delegate handler, not the original caller.
			// Demand-mapping would allocate into the wrong address space.
			// Instead, return partial copy (Linux copy_to_user semantics).
			return copied
		}
		pagePA := pa &^ (PageSize - 1)
		pageOffset := pa & (PageSize - 1)
		kernelVA := MapPAToKernelScratch(pagePA)
		if kernelVA == 0 {
			return copied
		}
		pageRemain := int(PageSize - pageOffset)
		chunk := n - copied
		if chunk > pageRemain {
			chunk = pageRemain
		}
		dst := kernelVA + pageOffset
		for i := 0; i < chunk; i++ {
			*(*byte)(unsafe.Pointer(dst + uintptr(i))) = src[copied+i]
		}
		copied += chunk
	}
	return copied
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
		serial.RawUARTPuts("[kmem] AllocAndMapUserPage: frame alloc failed\r\n")
		return 0, 0
	}

	// Step 2: Map the frame to userspace VA using explicit page table
	if !mapUserPageWithL0(userVA, framePA, elfFlags, l0PA) {
		serial.RawUARTPuts("[kmem] AllocAndMapUserPage: user map failed\r\n")
		return 0, 0
	}

	// Step 3: Map the frame to kernel scratch VA
	scratchVA = MapPAToKernelScratch(framePA)
	if scratchVA == 0 {
		serial.RawUARTPuts("[kmem] AllocAndMapUserPage: scratch map failed\r\n")
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

// MapExistingUserPageWithL0 installs a PTE mapping userVA → pa in the shepherd's
// page table (identified by l0PA), with permissions derived from ELF phdr flags,
// and increments the PageDescriptor RefCount for pa.
//
// Unlike AllocAndMapUserPageWithL0, this does NOT allocate a new frame — pa must
// already belong to a caller (typically the shared-text cache) that holds a ref
// to keep it alive. The RefCount bump here gives the new mapping its own ref,
// which is decremented by releasePageByPA when CleanupShepherdPages runs.
//
// The data cache is NOT cleaned here — the caller is responsible for having
// done CleanPageCache at population time (the pages are already coherent from
// the original load).
//
// Returns true on success.
func MapExistingUserPageWithL0(userVA, pa uintptr, elfFlags uint32, l0PA uintptr) bool {
	if !pagingInitialized {
		InitPaging()
	}
	if !mapUserPageWithL0(userVA, pa, elfFlags, l0PA) {
		return false
	}
	if desc := GetPageDescriptor(pa); desc != nil {
		desc.RefCount++
		desc.Flags |= PD_SHARED
	}
	return true
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

	// Use TTBR1 for kernel space (bit 63 = 1)
	if (va>>63)&1 == 0 {
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
	if !pteIsValid(*l0Entry) {
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
		*l0Entry = makeTablePTE(l1PA)
		dcCIVAC(uintptr(unsafe.Pointer(l0Entry)))
		dsbSY()
		tlbiVAE1IS(0)
		dsbSY()
		isbSY()
	} else {
		l1PA := pteExtractPA(*l0Entry)
		l1VA = paToVAOrCache(l1PA)
	}
	if l1VA == 0 {
		return false
	}

	// Read L1 entry
	l1Entry := (*uint64)(unsafe.Pointer(l1VA + l1Idx*8))

	var l2VA uintptr
	if !pteIsValid(*l1Entry) {
		l2VA = allocPTPage()
		if l2VA == 0 {
			return false
		}
		l2PA := walkPageTable(l2VA)
		if l2PA == 0 {
			return false
		}
		cachePTVA(l2PA, l2VA)
		*l1Entry = makeTablePTE(l2PA)
		dcCIVAC(uintptr(unsafe.Pointer(l1Entry)))
		dsbSY()
	} else {
		l2PA := pteExtractPA(*l1Entry)
		l2VA = paToVAOrCache(l2PA)
		if l2VA == 0 {
			return false
		}
	}

	// Read L2 entry
	l2Entry := (*uint64)(unsafe.Pointer(l2VA + l2Idx*8))

	var l3VA uintptr
	if !pteIsValid(*l2Entry) {
		l3VA = allocPTPage()
		if l3VA == 0 {
			return false
		}
		l3PA := walkPageTable(l3VA)
		if l3PA == 0 {
			return false
		}
		cachePTVA(l3PA, l3VA)
		*l2Entry = makeTablePTE(l3PA)
		dcCIVAC(uintptr(unsafe.Pointer(l2Entry)))
		dsbSY()
	} else {
		l3PA := pteExtractPA(*l2Entry)
		l3VA = paToVAOrCache(l3PA)
		if l3VA == 0 {
			return false
		}
	}

	// Write L3 entry with kernel scratch permissions (arch-specific)
	l3Entry := (*uint64)(unsafe.Pointer(l3VA + l3Idx*8))

	pteValue := makeKernelScratchPTE(pa)

	*l3Entry = pteValue

	// Clean cache and invalidate TLB
	dcCIVAC(uintptr(unsafe.Pointer(l3Entry)))
	dsbSY()
	tlbiVAE1IS(va)
	dsbSY()
	isbSY()

	return true
}

// ReleaseKernelPage clears the PTE for a kernel VA (heap page) and returns
// the physical address that was mapped, or 0 if not mapped.
// The caller must free the returned PA via ReleasePageByPA.
// This is used by madvise(MADV_DONTNEED/MADV_FREE) to return kernel heap
// pages to the physical frame allocator.
//
// PRECONDITION: The caller MUST validate that va is within the kernel heap
// range (KernelHeapStart..KernelHeapEnd). This function does not perform
// range validation — passing a VA outside the heap (e.g., kernel code/data,
// page tables, exception vectors) would silently unmap critical pages.
// Currently the only caller is SyscallMadvise, which validates the range.
//
//go:nosplit
func ReleaseKernelPage(va uintptr) uintptr {
	pte, level, ok := platformReadPTEAt(va)
	if !ok || level != 3 {
		return 0 // Not mapped at 4KB level (or superpage — don't touch)
	}

	pa := pteExtractPA(pte)
	if pa == 0 {
		return 0
	}

	// Clear the PTE
	platformWritePTEAt(va, 0)

	// Flush TLB for this VA
	platformFlushTLBPage(va)

	return pa
}
