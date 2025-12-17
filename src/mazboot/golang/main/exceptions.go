package main

import (
	"mazboot/asm"
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

// InitializeExceptions sets up the exception vector table
//
//go:nosplit
//go:noinline
func InitializeExceptions() error {
	// Exception vector address (hardcoded - VBAR_EL1 is set by boot.s)
	exceptionVectorAddr := uintptr(0x2a5000)

	// Verify address is 2KB aligned (required for VBAR_EL1)
	if exceptionVectorAddr&0x7FF != 0 {
		print("FATAL: Exception vector not 2KB aligned\r\n")
		return nil
	}

	// VBAR_EL1 is set in boot.s before Go code runs
	asm.Dsb()

	return nil
}

// ExceptionHandler is called from assembly when a synchronous exception occurs
// It handles the exception and logs details for debugging
//
//go:nosplit
//go:noinline
func ExceptionHandler(esr uint64, elr uint64, spsr uint64, far uint64, excType uint32) {
	excInfo := ExceptionInfo{
		ExceptionType: excType,
		ESR:           esr,
		ELR:           elr,
		SPSR:          spsr,
		FAR:           far,
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
func uartPutHex8Direct(v uint8) {
	const hexdigits = "0123456789ABCDEF"
	uartPutcDirect(hexdigits[(v>>4)&0xF])
	uartPutcDirect(hexdigits[v&0xF])
}

//go:nosplit
func uartPutHex64Direct(v uint64) {
	const hexdigits = "0123456789ABCDEF"
	for shift := uint(60); ; shift -= 4 {
		uartPutcDirect(hexdigits[(v>>shift)&0xF])
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

	// Hang the system for now
	uartPutsDirect("System halted\r\n")
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
	// Print syscall number for debugging (use direct UART to avoid recursion)
	uartPutcDirect('S')
	uartPutcDirect('Y')
	uartPutcDirect('S')
	uartPutcDirect(':')
	uartPutHex64Direct(syscallNum)
	uartPutcDirect('\r')
	uartPutcDirect('\n')

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
		// arg0 = addr (hint, ignored), arg1 = length, arg2 = prot, arg3 = flags
		// Use bump allocator from mmap region (0x50000000-0x78000000)
		length := uintptr(arg1)

		// Align length to page size (4KB)
		const pageSize = 4096
		alignedLength := (length + pageSize - 1) &^ (pageSize - 1)

		// Check if we have enough space
		if mmapNext+alignedLength > mmapEnd {
			// Out of memory - show detailed error
			uartPutcDirect('M')
			uartPutcDirect('!')
			uartPutcDirect('(')
			uartPutHex64Direct(uint64(alignedLength) >> 20) // Show MB requested
			uartPutcDirect('M')
			uartPutcDirect(')')
			return ^uint64(11) // -12 (ENOMEM)
		}

		// Allocate from bump allocator
		result := mmapNext
		mmapNext += alignedLength

		// Debug output showing allocation (compact: pages allocated)
		uartPutcDirect('M')
		uartPutHex64Direct(uint64(alignedLength) >> 12) // Show pages allocated
		uartPutcDirect('@')
		uartPutHex64Direct(uint64(result) >> 20) // Show base in MB
		uartPutcDirect(' ')

		return uint64(result)

	case 226: // mprotect
		// mprotect - return success
		return 0

	case 233: // munmap
		// munmap - return success
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
