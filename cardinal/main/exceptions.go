package main

import (
	"mazzy/cardinal/asm"
	"mazzy/cardinal/constants"
	"unsafe"
)

// Exception types
const (
	SYNC_EXCEPTION = 0
	IRQ            = 1
	FIQ            = 2
	SERROR         = 3
)

// ESR_EL1 (Exception Syndrome Register) field extraction
// EC = Exception Class (bits 31:26)
const (
	EC_UNKNOWN             = 0b000000
	EC_TRAP_WFx            = 0b000001
	EC_TRAP_MCR_MRC_CP14   = 0b000011
	EC_TRAP_MCRR_MRRC_CP14 = 0b000100
	EC_TRAP_MCR_MRC_CP15   = 0b000101
	EC_TRAP_MCRR_MRRC_CP15 = 0b000110
	EC_TRAP_MSR_MRS_SYSTEM = 0b010001
	EC_TRAP_SVE            = 0b010100
	EC_PREFETCH_ABORT_EL0  = 0b100000
	EC_PREFETCH_ABORT_ELx  = 0b100001
	EC_DATA_ABORT_EL0      = 0b100100
	EC_DATA_ABORT_ELx      = 0b100101
	EC_BREAKPOINT_EL0      = 0b110000
	EC_BREAKPOINT_ELx      = 0b110001
	EC_STEP_EL0            = 0b110010
	EC_STEP_ELx            = 0b110011
	EC_WATCHPOINT_EL0      = 0b110100
	EC_WATCHPOINT_ELx      = 0b110101
	EC_SVC_EL0             = 0b010101 // Supervisor call from EL0 (AArch32)
	EC_SVC_EL1             = 0b010110 // Supervisor call from EL1 (AArch32)
	EC_HVC                 = 0b011000
	EC_SMC                 = 0b011001
	EC_SVC_EL0_A64         = 0b010100 // SVC from AArch64 EL0
	EC_SVC_EL1_A64         = 0b010101 // SVC from AArch64 EL1
	EC_ERET                = 0b011100
	EC_ILLEGAL_EXECUTION   = 0b011110
	EC_SERROR              = 0b101111
)

// ExceptionInfo contains details about an exception for logging/handling
type ExceptionInfo struct {
	ExceptionType uint32 // SYNC_EXCEPTION, IRQ, FIQ, SERROR
	ESR           uint64 // Exception Syndrome Register
	ELR           uint64 // Exception Link Register (return address)
	SPSR          uint64 // Saved Program Status Register
	FAR           uint64 // Fault Address Register
	SavedFP       uint64 // Saved frame pointer x29 (from exception frame, for stack walking)
	SavedLR       uint64 // Saved link register x30 (from exception frame, for stack walking)
	SavedG        uint64 // Saved g pointer x28 (from exception frame, for traceback)
}

// mmap bump allocator state
// Region: 0x60000000 - 0x200000000 (6.5GB for Go runtime heap)
// CRITICAL: Start at 0x60000000 (after page table region at 0x5F100000-0x60000000)
// to avoid overwriting page tables during mmap allocations!
// Note: Go reserves large virtual address ranges but only touches a fraction.
// This is normal behavior - we just need enough address space, not physical RAM.
var (
	mmapBase uintptr = 0x60000000      // Start of mmap region (after page tables)
	mmapEnd  uintptr = 0x200000000     // End of mmap region (6.5GB for 8GB QEMU)
	mmapNext uintptr = 0x60000000      // Next available address (bump allocator)
)

// Link to assembly functions (now in asm package)
// Functions are accessed via asm package: asm.SetVbarEl1ToAddr(), asm.EnableIrqs(), etc.

// Relocated exception vectors in safe RAM (non-cacheable)
// Must be 2KB aligned and 2KB in size
const (
	EXCEPTION_VECTOR_SIZE = 0x800 // 2KB
)

// Exception vectors - BSS global to avoid circular dependency with heap
// Allocate 4KB to ensure 2KB alignment (extra space for alignment padding)
var exceptionVectorRAM_global [4096]byte

// getExceptionVectorRAMAddr returns the 2KB-aligned address of exception vectors
//
//go:nosplit
func getExceptionVectorRAMAddr() uintptr {
	// Calculate 2KB-aligned address within the buffer
	addr := uintptr(unsafe.Pointer(&exceptionVectorRAM_global[0]))
	return (addr + 0x7FF) &^ 0x7FF // Round up to 2KB boundary
}

// InitializeExceptions sets up the exception vector table
// CRITICAL: This now RELOCATES exception vectors from ROM to RAM at a safe address
// and marks the RAM region as non-cacheable to avoid cache coherency issues.
//
//go:nosplit
//go:noinline
func InitializeExceptions() {
	// Get original exception vector address from linker (in ROM)
	romVectorAddr := uintptr(asm.GetExceptionVectorsAddr())

	// Get aligned exception vector RAM address
	exceptionVectorRAMAddr := getExceptionVectorRAMAddr()

	// Verify ROM address is 2KB aligned
	if romVectorAddr&0x7FF != 0 {
		kernelPanic("Exception vector ROM address not 2KB aligned")
	}

	// Copy exception vectors from ROM to RAM (2KB)
	romPtr := (*[EXCEPTION_VECTOR_SIZE]byte)(unsafe.Pointer(romVectorAddr))
	ramPtr := (*[EXCEPTION_VECTOR_SIZE]byte)(unsafe.Pointer(exceptionVectorRAMAddr))
	for i := uintptr(0); i < EXCEPTION_VECTOR_SIZE; i++ {
		ramPtr[i] = romPtr[i]
	}

	// Clean data cache to ensure writes are visible in memory
	for addr := exceptionVectorRAMAddr; addr < exceptionVectorRAMAddr+EXCEPTION_VECTOR_SIZE; addr += 64 {
		asm.CleanDataCacheVA(addr)
	}
	asm.Dsb()

	// Invalidate instruction cache to ensure CPU fetches new code
	// This is CRITICAL for executing from the relocated exception vectors
	asm.InvalidateInstructionCacheAll()

	// Verify the copy succeeded - check sync_exception_el1 (offset +0x200)
	syncExceptionAddr := exceptionVectorRAMAddr + 0x200
	firstInst := *(*uint32)(unsafe.Pointer(syncExceptionAddr))
	if (firstInst>>26) != 0x05 && (firstInst>>26) != 0x06 {
		kernelPanic("Exception vector copy failed")
	}

	// Set VBAR_EL1 to new address (via assembly)
	asm.SetVbarEl1ToAddr(exceptionVectorRAMAddr)
	asm.Dsb()
	asm.Isb()
}

// Nested exception detector
var inExceptionHandler uint32

// Exception counter to detect infinite loops
var exceptionCount uint32

// ExceptionHandler is called from assembly when a synchronous exception occurs
// It handles the exception and logs details for debugging
// savedSP, savedLR, savedG are the values from the exception frame for traceback
//
//go:nosplit
//go:noinline
func ExceptionHandler(esr uint64, elr uint64, spsr uint64, far uint64, excType uint32, savedFP uint64, savedLR uint64, savedG uint64) {
	// Check for stack overflow
	// We now use g0 stack (0x5EFF8000-0x5F000000) for all exception/syscall handlers
	sp := asm.GetCallerStackPointer()
	const minSP = uintptr(0x5EFF8000)  // Bottom of g0 stack
	const maxSP = uintptr(0x60000000)  // Top of exception stack region (allows both g0 and exception stacks)
	if sp < minSP || sp > maxSP {
		uartPutsDirect("\r\n!STACK OVERFLOW! SP=0x")
		uartPutHex64Direct(uint64(sp))
		uartPutsDirect("\r\n")
		for {} // Hang on stack overflow
	}

	excInfo := ExceptionInfo{
		ExceptionType: excType,
		ESR:           esr,
		ELR:           elr,
		SPSR:          spsr,
		FAR:           far,
		SavedFP:       savedFP,
		SavedLR:       savedLR,
		SavedG:        savedG,
	}

	handleException(excInfo)
}

// Direct UART printing for exception context.
// We avoid uartPutc/uartPuts because those may enqueue to the ring buffer and rely on interrupts,
// which are typically masked during exception entry (so output never appears).
//
//go:nosplit
func uartPutcDirect(c byte) {
	asm.UartPutcPl011(c)
}

//go:nosplit
func uartPutsDirect(s string) {
	for i := 0; i < len(s); i++ {
		uartPutcDirect(s[i])
	}
}

//go:nosplit
func hexDigit(n uint8) byte {
	if n < 10 {
		return '0' + n
	}
	return 'A' + (n - 10)
}

//go:nosplit
func uartPutHex8Direct(v uint8) {
	uartPutcDirect(hexDigit((v >> 4) & 0xF))
	uartPutcDirect(hexDigit(v & 0xF))
}

//go:nosplit
func uartPutHex16Direct(v uint16) {
	uartPutHex8Direct(uint8(v >> 8))
	uartPutHex8Direct(uint8(v & 0xFF))
}

//go:nosplit
func uartPutHex32Direct(v uint32) {
	for shift := uint(28); ; shift -= 4 {
		uartPutcDirect(hexDigit(uint8((v >> shift) & 0xF)))
		if shift == 0 {
			break
		}
	}
}

//go:nosplit
func uartPutHex64Direct(v uint64) {
	for shift := uint(60); ; shift -= 4 {
		uartPutcDirect(hexDigit(uint8((v >> shift) & 0xF)))
		if shift == 0 {
			break
		}
	}
}

// handleException dispatches the exception to the appropriate handler
// CRITICAL: Must be nosplit to avoid stack growth during exception handling
// (which could cause recursive exceptions via mmap)
//
//go:nosplit
func handleException(excInfo ExceptionInfo) {
	// NOTE: 'H' breadcrumb removed to reduce noise from page faults
	// Page faults happen frequently during normal demand paging

	// Extract exception class from ESR_EL1
	ec := (excInfo.ESR >> 26) & 0x3F

	// Fast path for data aborts (page faults) - suppress output for normal operation
	if ec == EC_DATA_ABORT_ELx {
		// Try demand paging first - this handles page faults in mmap region
		if HandlePageFault(uintptr(excInfo.FAR), excInfo.ESR&0x3F) {
			// Page fault handled successfully - return silently
			return
		}
		// Page fault failed - print error info and HALT to avoid infinite loop
		uartPutsDirect("\r\n\r\n!FATAL DATA ABORT (cannot handle):\r\n")
		uartPutsDirect("  ELR=0x")
		uartPutHex64Direct(excInfo.ELR)
		uartPutsDirect("\r\n  FAR=0x")
		uartPutHex64Direct(excInfo.FAR)
		uartPutsDirect("\r\n  ESR=0x")
		uartPutHex64Direct(excInfo.ESR)
		uartPutsDirect("\r\n\r\nHalting to prevent infinite loop.\r\n")
		// HALT - don't return, as that would retry the faulting instruction
		for {}
	}

	// For other exceptions, print full debug info
	uartPutsDirect("*EXCEPTION: EC=0x")
	uartPutHex8Direct(uint8(ec))
	uartPutsDirect(" ELR=0x")
	uartPutHex64Direct(excInfo.ELR)
	uartPutsDirect(" ESR=0x")
	uartPutHex64Direct(excInfo.ESR)
	uartPutsDirect(" FAR=0x")
	uartPutHex64Direct(excInfo.FAR)
	uartPutsDirect("\r\n")

	switch ec {
	case EC_UNKNOWN:
		uartPutsDirect("Unknown exception at 0x")
		uartPutHex64Direct(excInfo.ELR)
		uartPutsDirect("\r\n")

	case EC_TRAP_WFx:
		uartPutsDirect("WFx trap at 0x")
		uartPutHex64Direct(excInfo.ELR)
		uartPutsDirect("\r\n")

	case EC_TRAP_MSR_MRS_SYSTEM:
		uartPutsDirect("MSR/MRS system instruction trap at 0x")
		uartPutHex64Direct(excInfo.ELR)
		uartPutsDirect("\r\n")

	case EC_PREFETCH_ABORT_ELx:
		uartPutsDirect("Prefetch abort at 0x")
		uartPutHex64Direct(excInfo.ELR)
		uartPutsDirect("\r\n")

	case EC_BREAKPOINT_ELx:
		uartPutsDirect("Breakpoint at 0x")
		uartPutHex64Direct(excInfo.ELR)
		uartPutsDirect("\r\n")

	case EC_ILLEGAL_EXECUTION:
		uartPutsDirect("Illegal execution state at 0x")
		uartPutHex64Direct(excInfo.ELR)
		uartPutsDirect("\r\n")

	case EC_SVC_EL1_A64:
		// Supervisor call from EL1 (AArch64) - handled by assembly

	case EC_SVC_EL0_A64:
		// Supervisor call from EL0 (AArch64) - handled by assembly

	default:
		uartPutsDirect("Unhandled exception class 0x")
		uartPutHex8Direct(uint8(ec))
		uartPutsDirect(" at 0x")
		uartPutHex64Direct(excInfo.ELR)
		uartPutsDirect("\r\n")
	}

	// Print stack traceback before hanging
	// Use the saved registers from the exception frame
	PrintTraceback(uintptr(excInfo.ELR), uintptr(excInfo.SavedFP), uintptr(excInfo.SavedLR), uintptr(excInfo.SavedG))

	// Hang the system
	uartPutsDirect("\r\nSystem halted\r\n")
	for {
		// Spin forever
	}
}

// irqHandlerGo is the actual Go implementation
//
//go:nosplit
func irqHandlerGo(irqID uint32) {
	// Handle interrupt - interrupt ID already acknowledged in assembly
	// irqID passed from assembly (read from GICC_IAR immediately on entry)
	gicHandleInterruptWithID(irqID)
}

// fiqHandlerGo is the actual Go implementation
//
//go:nosplit
func fiqHandlerGo() {
	// Hang
	for {
	}
}

// serrorHandlerGo is the actual Go implementation
//
//go:nosplit
func serrorHandlerGo() {
	// Hang
	for {
	}
}

// Helper: Extract EC (Exception Class) from ESR_EL1
func extractEC(esr uint64) uint8 {
	return uint8((esr >> 26) & 0x3F)
}

// Helper: Extract ISS (Instruction Specific Syndrome) from ESR_EL1
func extractISS(esr uint64) uint32 {
	return uint32(esr & 0xFFFFFF)
}

// Note: We now get the exception vector address via get_exception_vectors_addr()
// instead of using a linker symbol, to avoid linker symbol resolution issues

// HandleSyscall handles Linux syscalls by returning fake responses
// This is called from assembly when an SVC instruction is executed
// syscallNum is the Linux syscall number (in x8)
// Returns the fake syscall result
//
//go:nosplit
//go:noinline
func HandleSyscall(syscallNum, arg0, arg1, arg2, arg3, arg4, arg5 uint64) uint64 {
	switch syscallNum {
	case 64: // write
		// write(fd, buf, count) - pretend we wrote all bytes
		return arg2 // return count

	case 63: // read
		// read(fd, buf, count) - return 0 (EOF)
		return 0

	case 56: // openat
		// openat(dirfd, path, flags, mode) - return -ENOENT
		return ^uint64(1) // -2 (ENOENT)

	case 57: // close
		// close(fd) - return success
		return 0

	case 93, 94: // exit, exit_group
		// Use semihosting to gracefully exit QEMU
		asm.QemuExit()
		// If semihosting doesn't work, hang
		for {
		}

	case 98: // futex
		// futex - return success (common in Go runtime)
		return 0

	case 99: // nanosleep
		// nanosleep - return success (slept)
		return 0

	case 131: // tgkill
		// tgkill - used for signals, return success
		return 0

	case 220: // clone (used by Go runtime)
		// clone - return -EAGAIN (can't create new thread)
		return ^uint64(10) // -11 (EAGAIN)

	case 222: // mmap
		// mmap(addr, length, prot, flags, fd, offset)
		// arg0 = addr (hint or fixed), arg1 = length, arg2 = prot, arg3 = flags
		addrHint := uintptr(arg0)
		length := uintptr(arg1)
		flags := arg3

		// Align length to page size (4KB)
		const pageSize = 4096
		alignedLength := (length + pageSize - 1) &^ (pageSize - 1)

		// Check for MAP_FIXED (0x10) - must allocate at exact address
		const MAP_FIXED = 0x10
		var result uintptr

		if (flags & MAP_FIXED) != 0 && addrHint != 0 {
			// MAP_FIXED: MUST allocate at the exact requested address
			// The Go runtime uses this for its arena allocations
			result = addrHint
			// Note: We rely on demand paging to handle these addresses
		} else {
			// No MAP_FIXED: use bump allocator
			// Check if we have enough space
			if mmapNext+alignedLength > mmapEnd {
				return ^uint64(11) // -12 (ENOMEM)
			}
			result = mmapNext
			mmapNext += alignedLength
		}

		// CRITICAL: Check if allocation overlaps with critical regions
		const romEnd = uintptr(0x8000000)          // End of ROM region
		const pageTableStart = uintptr(0x5E000000) // Start of page tables
		if result < romEnd {
			uartPutsDirect("\r\n!MMAP IN ROM: 0x")
			uartPutHex64Direct(uint64(result))
			uartPutsDirect("\r\n")
			for {} // Hang
		}
		if result >= pageTableStart && result < mmapBase {
			uartPutsDirect("\r\n!MMAP IN PAGE TABLES: 0x")
			uartPutHex64Direct(uint64(result))
			uartPutsDirect("\r\n")
			for {} // Hang
		}

		return uint64(result)

	case 226: // mprotect
		// mprotect - return success
		return 0

	case 233: // madvise
		// madvise - give advice about memory usage
		// Arguments: arg0=addr, arg1=length, arg2=advice
		// Common advice values: MADV_DONTNEED=4, MADV_FREE=8
		// For now, just accept all advice and return success
		return 0

	case 261: // prlimit64
		// prlimit64 - return success but don't actually do anything
		return 0

	case 278: // getrandom
		// getrandom - return 0 bytes (can't provide random)
		return 0

	default:
		// Unknown syscall - print warning and return -ENOSYS
		uartPutsDirect("SYSCALL UNKNOWN: ")
		uartPutHex64Direct(syscallNum)
		uartPutsDirect("\r\n")
		return ^uint64(37) // -38 (ENOSYS)
	}
}

// ============================================================================
// IRQ Exception Dispatcher
// ============================================================================

// Go runtime arena high VA range (for checking if g is a kmazarin pointer)
const goArenaLow = 0x4000000000  // 256GB
const goArenaHigh = 0x8000000000 // 512GB

// kmazarinAsyncPreempt returns the asyncPreempt address from linker symbols
// Extracted from kmazarin.elf at build time and patched by compute-linker-values
//go:nosplit
func kmazarinAsyncPreempt() uint64 {
	return uint64(LinkerKmazarinAsyncPreempt)
}

// kmazarinTextStart returns the start of kmazarin's text section (load address)
//go:nosplit
func kmazarinTextStart() uint64 {
	return uint64(constants.KmazarinLoadAddr)
}

// kmazarinTextEnd returns the end of kmazarin's memory region
//go:nosplit
func kmazarinTextEnd() uint64 {
	return uint64(constants.KmazarinLoadAddr) + LinkerKmazarinSize
}

// irqExceptionDispatchInternal is the unified entry point for ALL IRQ exceptions.
// Called via ABI stub IRQExceptionDispatch.
// It handles:
//   - Timer interrupts (IRQ 27): timer tick, sleeping thread wake, preemption
//   - Other interrupts: dispatch to registered handler
//
// Parameters (passed from assembly in exc_irq.s):
//   irqID    - Interrupt ID (from GIC IAR, 10 bits)
//   framePtr - Pointer to exception frame on stack
//   savedG   - Saved x28/g pointer from interrupted context
//   elr      - ELR_EL1 (interrupted PC)
//   spEl0    - SP_EL0 (interrupted stack pointer)
//
// Returns:
//   newELR    - New ELR value (asyncPreempt addr if preempting, else 0)
//   newSP     - New SP_EL0 value (adjusted stack if preempting, else 0)
//   newLR     - New LR value (interrupted PC if preempting, else 0)
//   doPreempt - true if caller should modify frame for preemption
//
//go:nosplit
//go:noinline
func IRQDispatchGoInternal(
	irqID uint64,
	framePtr uintptr,
	savedG uint64,
	elr uint64,
	spEl0 uint64,
) (newELR, newSP, newLR uint64, doPreempt bool) {
	// Check if this is a timer interrupt (IRQ 27 = virtual timer)
	if irqID == 27 {
		return handleTimerIRQ(savedG, elr, spEl0)
	}

	// Not a timer - dispatch to registered handler via irqHandlerGo
	irqHandlerGo(uint32(irqID))

	// Signal EOI to GIC for non-timer interrupts
	gicEndOfInterrupt(uint32(irqID))

	// No preemption for non-timer interrupts
	return 0, 0, 0, false
}

// handleTimerIRQ handles timer interrupt (IRQ 27)
// Returns preemption info if we should preempt the interrupted goroutine
//
//go:nosplit
func handleTimerIRQ(savedG, elr, spEl0 uint64) (newELR, newSP, newLR uint64, doPreempt bool) {
	// Handle timer tick (increment counter, wake sleeping threads)
	TimerTickHandler()

	// Check if we should skip preemption
	if shouldSkipPreemption(savedG) {
		// Re-arm timer and send EOI, then return without preemption
		rearmTimer()
		gicEndOfInterrupt(27)
		return 0, 0, 0, false
	}

	// Re-arm timer
	rearmTimer()

	// Set up call injection for asyncPreempt
	// Allocate 16-byte frame on interrupted goroutine's stack
	adjustedSP := (spEl0 - 16) &^ 0xF // 16-byte aligned

	// Determine which asyncPreempt to use
	var asyncPreemptAddr uint64
	if isInKmazarin(elr) {
		asyncPreemptAddr = kmazarinAsyncPreempt() // Use linker-extracted address
	} else {
		asyncPreemptAddr = getAsyncPreemptBMAddr()
	}

	// Signal EOI to GIC (before ERET!)
	gicEndOfInterrupt(27)

	// Return preemption info
	// - newELR = asyncPreempt address (where ERET will jump)
	// - newSP = adjusted stack (with room for return frame)
	// - newLR = interrupted PC (asyncPreempt will return here)
	return asyncPreemptAddr, adjustedSP, elr, true
}

// shouldSkipPreemption checks if we should NOT preempt
// Returns true if:
//   - g is nil
//   - g is cardinal's g (not kmazarin)
//   - g == g.m.g0 (we're on the scheduler's g0)
//
//go:nosplit
func shouldSkipPreemption(savedG uint64) bool {
	// Check for nil g
	if savedG == 0 {
		return true
	}

	// Check if g is a kmazarin pointer (either in Go arena or kmazarin ELF)
	isKmazarinG := false
	if savedG >= goArenaLow && savedG < goArenaHigh {
		// In Go's high VA range (runtime arenas)
		isKmazarinG = true
	} else if savedG >= kmazarinTextStart() && savedG < kmazarinTextEnd() {
		// In kmazarin's ELF text section
		isKmazarinG = true
	}

	if !isKmazarinG {
		// Not a kmazarin g, skip preemption (it's cardinal's g0)
		return true
	}

	// Check if g == g.m.g0 (we're on scheduler's g0)
	// Go struct offsets:
	//   g.m is at offset 48 in g struct
	//   m.g0 is at offset 0 in m struct
	gPtr := uintptr(savedG)
	mPtr := *(*uintptr)(unsafe.Pointer(gPtr + 48)) // g.m
	if mPtr == 0 {
		return true
	}
	g0Ptr := *(*uintptr)(unsafe.Pointer(mPtr)) // m.g0
	if gPtr == g0Ptr {
		// We're on g0 (scheduler goroutine), skip preemption
		return true
	}

	// OK to preempt
	return false
}

// isInKmazarin checks if an address is within kmazarin's text section
//
//go:nosplit
func isInKmazarin(addr uint64) bool {
	return addr >= kmazarinTextStart() && addr < kmazarinTextEnd()
}

// rearmTimer sets the timer to fire again in 20ms
// QEMU virt uses 62.5 MHz timer frequency
// 20ms = 1,250,000 ticks = 0x1312D0
//
//go:nosplit
func rearmTimer() {
	// Read current counter
	cnt := asm.ReadCntvctEl0()
	// Set compare value to current + 20ms
	newCval := cnt + 1250000
	asm.WriteCntvCvalEl0(newCval)
}

// getAsyncPreemptBMAddr returns the address of cardinal's asyncPreemptBM function
// This is used when preempting cardinal code (not kmazarin)
//
//go:nosplit
func getAsyncPreemptBMAddr() uint64 {
	// Get address of asyncPreemptBM via function value
	// We use a trick: take address of the function wrapper
	return uint64(asm.GetAsyncPreemptBMAddr())
}
