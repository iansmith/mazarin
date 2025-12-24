package main

import (
	"mazboot/asm"
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
// Functions are accessed via asm package: asm.SetVbarEl1(), asm.EnableIrqs(), etc.

// Assembly function to set VBAR_EL1 to a specific address
//go:linkname setVbarEl1ToAddr set_vbar_el1_to_addr
//go:nosplit
func setVbarEl1ToAddr(addr uintptr)

// Relocated exception vectors in safe RAM (non-cacheable)
// Must be 2KB aligned and 2KB in size
const (
	EXCEPTION_VECTOR_RAM_ADDR = uintptr(0x41100000) // Safe address in kernel RAM
	EXCEPTION_VECTOR_SIZE     = 0x800               // 2KB
)

// InitializeExceptions sets up the exception vector table
// CRITICAL: This now RELOCATES exception vectors from ROM to RAM at a safe address
// and marks the RAM region as non-cacheable to avoid cache coherency issues.
//
//go:nosplit
//go:noinline
func InitializeExceptions() {
	// DEBUG: Mark entry to this function
	uartBase := getLinkerSymbol("__uart_base")
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x52 // 'R' - Relocating vectors

	// Get original exception vector address from linker (in ROM)
	romVectorAddr := uintptr(asm.GetExceptionVectorsAddr())

	uartPutsDirect("Relocating exception vectors from ROM 0x")
	uartPutHex64Direct(uint64(romVectorAddr))
	uartPutsDirect(" to RAM 0x")
	uartPutHex64Direct(uint64(EXCEPTION_VECTOR_RAM_ADDR))
	uartPutsDirect("\r\n")

	// Verify ROM address is 2KB aligned
	if romVectorAddr&0x7FF != 0 {
		print("FATAL: ROM exception vector not 2KB aligned\r\n")
		for {} // Hang
	}

	// Copy exception vectors from ROM to RAM (2KB)
	romPtr := (*[EXCEPTION_VECTOR_SIZE]byte)(unsafe.Pointer(romVectorAddr))
	ramPtr := (*[EXCEPTION_VECTOR_SIZE]byte)(unsafe.Pointer(EXCEPTION_VECTOR_RAM_ADDR))
	for i := uintptr(0); i < EXCEPTION_VECTOR_SIZE; i++ {
		ramPtr[i] = romPtr[i]
	}

	// Clean data cache to ensure writes are visible in memory
	for addr := EXCEPTION_VECTOR_RAM_ADDR; addr < EXCEPTION_VECTOR_RAM_ADDR+EXCEPTION_VECTOR_SIZE; addr += 64 {
		asm.CleanDataCacheVA(addr)
	}
	asm.Dsb()

	// Invalidate instruction cache to ensure CPU fetches new code
	// This is CRITICAL for executing from the relocated exception vectors
	asm.InvalidateInstructionCacheAll()

	// Verify the copy succeeded - check sync_exception_el1 (offset +0x200)
	syncExceptionAddr := EXCEPTION_VECTOR_RAM_ADDR + 0x200
	firstInst := *(*uint32)(unsafe.Pointer(syncExceptionAddr))
	if (firstInst>>26) != 0x05 && (firstInst>>26) != 0x06 {
		uartPutsDirect("\r\n!VECTOR COPY FAILED!\r\n")
		uartPutsDirect("sync_exception_el1 @ 0x")
		uartPutHex64Direct(uint64(syncExceptionAddr))
		uartPutsDirect(" = 0x")
		uartPutHex64Direct(uint64(firstInst))
		uartPutsDirect("\r\n")
		for {} // Hang
	}

	// Update VBAR_EL1 to point to new RAM location
	// (This will be done via assembly helper)
	uartPutsDirect("Updating VBAR_EL1 to 0x")
	uartPutHex64Direct(uint64(EXCEPTION_VECTOR_RAM_ADDR))
	uartPutsDirect("\r\n")

	// Set VBAR_EL1 to new address (via assembly)
	uartPutsDirect("DEBUG: About to set VBAR_EL1...\r\n")
	setVbarEl1ToAddr(EXCEPTION_VECTOR_RAM_ADDR)
	uartPutsDirect("DEBUG: VBAR_EL1 set\r\n")

	asm.Dsb()
	uartPutsDirect("DEBUG: DSB done\r\n")
	asm.Isb()
	uartPutsDirect("DEBUG: ISB done\r\n")

	uartPutsDirect("Exception vectors relocated successfully\r\n")

	uartPutsDirect("DEBUG: About to return from InitializeExceptions\r\n")

	// Raw UART write to verify we reach this point
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x5A // 'Z' - about to return
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
	// CRITICAL: Do NOT access global variables (exceptionCount)!
	// They might not be mapped yet and would cause nested exceptions

	// DISABLED: accessing exceptionCount global causes nested exception
	// // DEBUG: Print details BEFORE incrementing counter to see what exception triggers the crash
	// if exceptionCount == 49 {
	// 	print("\r\nDEBUG: BEFORE exception #50 - ELR=0x")
	// 	printHex64(elr)
	// 	print(" FAR=0x")
	// 	printHex64(far)
	// 	print(" savedG=0x")
	// 	printHex64(savedG)
	// 	print(" savedLR=0x")
	// 	printHex64(savedLR)
	// 	print("\r\n")
	// }
	//
	// // Increment exception counter
	// exceptionCount++
	//
	// // DEBUG: Catch the readgstatus crash before it calls PrintTraceback
	// if exceptionCount == 50 {
	// 	print("DEBUG: Exception #50 IS the readgstatus crash\r\n")
	// 	print("  This means exception #49 called PrintTraceback\r\n")
	// 	print("  Which then crashed in readgstatus\r\n")
	// 	print("HANGING to avoid crash loop\r\n")
	// 	for {}
	// }
	//
	// // DEBUG: Print marker for exceptions after #17 to detect loops
	// if exceptionCount == 18 || exceptionCount == 19 || exceptionCount == 20 {
	// 	uartBase := getLinkerSymbol("__uart_base")
	// 	*(*uint32)(unsafe.Pointer(uartBase)) = 0x58  // 'X' - exception after #17
	// }

	// CRITICAL: Do NOT access global variables (exceptionCount, inExceptionHandler)!
	// They might not be mapped yet and would cause nested exceptions
	// All exception tracking and nested exception detection DISABLED

	// VERY EARLY DEBUG: Print before any complex logic
	// DISABLED: accessing globals causes nested exception
	// if inExceptionHandler == 0 {
	// 	uartPutsDirect("!E1:")
	// } else {
	// 	uartPutsDirect("!E2:")
	// }
	// uartPutHex64Direct(far)

	// Detect exception storms (>100 exceptions suggests infinite loop)
	// DISABLED: accessing exceptionCount global causes nested exception
	// if exceptionCount > 100 {
	// 	uartPutsDirect("\r\n!EXCEPTION STORM! Count=")
	// 	uartPutHex64Direct(uint64(exceptionCount))
	// 	uartPutsDirect("\r\nESR=0x")
	// 	uartPutHex64Direct(esr)
	// 	uartPutsDirect(" FAR=0x")
	// 	uartPutHex64Direct(far)
	// 	uartPutsDirect(" ELR=0x")
	// 	uartPutHex64Direct(elr)
	// 	uartPutsDirect("\r\n")
	// 	for {} // Hang on exception storm
	// }

	// Detect nested exceptions (exception during exception handling)
	// DISABLED: accessing inExceptionHandler global causes nested exception
	// if inExceptionHandler != 0 {
	// 	uartPutsDirect("\r\n!NESTED EXCEPTION!\r\n")
	// 	uartPutsDirect("ESR=0x")
	// 	uartPutHex64Direct(esr)
	// 	uartPutsDirect(" FAR=0x")
	// 	uartPutHex64Direct(far)
	// 	uartPutsDirect("\r\n")
	// 	for {} // Hang on nested exception
	// }
	// inExceptionHandler = 1  // DISABLED

	// Check for stack overflow (option 2)
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

	// DEBUG: For page faults, print ELR IMMEDIATELY
	ec := (esr >> 26) & 0x3F
	if ec == EC_DATA_ABORT_ELx {
		// uartPutcDirect('!')  // Breadcrumb: data abort detected - BREADCRUMB DISABLED
		pfCount := GetPageFaultCounter()
		// uartPutcDirect('0' + byte(pfCount/10))  // Print tens digit - BREADCRUMB DISABLED
		// uartPutcDirect('0' + byte(pfCount%10))  // Print ones digit - BREADCRUMB DISABLED
		if pfCount >= 15 {
			// Print immediately so it appears in THIS exception's output
			uartPutsDirect(" ELR=0x")
			uartPutHex64Direct(elr)
		}
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

	// inExceptionHandler = 0  // DISABLED: accessing global causes issues

	// Breadcrumb: returning from exception handler
	// uartPutcDirect('R') // BREADCRUMB DISABLED
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
	// Breadcrumb: entered handleException
	// uartPutcDirect('H') // BREADCRUMB DISABLED

	// Extract exception class from ESR_EL1
	ec := (excInfo.ESR >> 26) & 0x3F

	// Fast path for data aborts (page faults) - suppress output for normal operation
	if ec == EC_DATA_ABORT_ELx {
		// Try demand paging first - this handles page faults in mmap region
		if HandlePageFault(uintptr(excInfo.FAR), excInfo.ESR&0x3F) {
			// Page fault handled successfully - return silently
			return
		}
		// Page fault failed - print error info
		uartPutsDirect("DATA ABORT: ELR=0x")
		uartPutHex64Direct(excInfo.ELR)
		uartPutsDirect(" FAR=0x")
		uartPutHex64Direct(excInfo.FAR)
		uartPutsDirect(" ESR=0x")
		uartPutHex64Direct(excInfo.ESR)
		uartPutsDirect("\r\n")
		return
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
		// Supervisor call from EL1 (AArch64)
		uartPutsDirect("SVC from EL1 at 0x")
		uartPutHex64Direct(excInfo.ELR)
		uartPutsDirect(" (immediate: ")
		// Keep the old helper for decimal; it's fine if it uses ring buffer before it's initialized,
		// but for now this path is not used during our debugging.
		uartPutUint32(uint32(excInfo.ESR & 0xFFFF))
		uartPutsDirect(")\r\n")

	case EC_SVC_EL0_A64:
		// Supervisor call from EL0 (AArch64)
		uartPutsDirect("SVC from EL0 at 0x")
		uartPutHex64Direct(excInfo.ELR)
		uartPutsDirect(" (immediate: ")
		uartPutUint32(uint32(excInfo.ESR & 0xFFFF))
		uartPutsDirect(")\r\n")

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
	// Print 'I' to show IRQ handler was called
	printChar('I')

	// Handle interrupt - interrupt ID already acknowledged in assembly
	// irqID passed from assembly (read from GICC_IAR immediately on entry)
	gicHandleInterruptWithID(irqID)

	// Print 'i' to show IRQ handler returning
	printChar('i')
}

// fiqHandlerGo is the actual Go implementation
//
//go:nosplit
func fiqHandlerGo() {
	print("FIQ fired (not implemented)\r\n")
}

// serrorHandlerGo is the actual Go implementation
//
//go:nosplit
func serrorHandlerGo() {
	print("SError occurred - system error (not recoverable)\r\n")
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
	// Print syscall number for debugging (use direct UART to avoid recursion) - BREADCRUMB DISABLED
	// uartPutcDirect('S')
	// uartPutcDirect('Y')
	// uartPutcDirect('S')
	// uartPutcDirect(':')
	// uartPutHex64Direct(syscallNum)
	// uartPutcDirect('\r')
	// uartPutcDirect('\n')

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
		// Exit syscalls - use Go print() to test interrupt-driven UART path
		print("\r\nEXIT:")
		uartPutHex64Direct(arg0) // exit code (keep direct for hex output)
		print("\r\n")
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

		// Debug output showing allocation (compact: pages allocated) - BREADCRUMB DISABLED
		// uartPutcDirect('M')
		// uartPutHex64Direct(uint64(alignedLength) >> 12) // Show pages allocated
		// uartPutcDirect('@')
		// uartPutHex64Direct(uint64(result) >> 20) // Show base in MB
		// uartPutcDirect(' ')

		return uint64(result)

	case 226: // mprotect
		// mprotect - return success
		return 0

	case 233: // madvise
		// madvise - give advice about memory usage
		// Arguments: arg0=addr, arg1=length, arg2=advice
		// Common advice values: MADV_DONTNEED=4, MADV_FREE=8
		// For now, just accept all advice and return success

		// DEBUG: Print madvise call
		uartPutsDirect("\r\nmadvise(addr=0x")
		uartPutHex64Direct(arg0)
		uartPutsDirect(", len=0x")
		uartPutHex64Direct(arg1)
		uartPutsDirect(", advice=")
		uartPutHex64Direct(arg2)
		uartPutsDirect(")\r\n")

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
