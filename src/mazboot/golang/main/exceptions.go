package main

import (
	_ "unsafe" // Required for //go:linkname directives

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

// Link to assembly functions (now in asm package)
// Functions are accessed via asm package: asm.SetVbarEl1(), asm.EnableIrqs(), etc.

// InitializeExceptions sets up the exception vector table
// This must be called early in kernel initialization
//
//go:nosplit
//go:noinline
func InitializeExceptions() error {
	uartPuts("DEBUG: InitializeExceptions called\r\n")
	uartPuts("DEBUG: Step 1 complete\r\n")

	// Get the address of the exception vector table using assembly function
	// This avoids linker symbol issues
	uartPuts("DEBUG: Getting exception vector address...\r\n")

	// TEMPORARY: Skip assembly call that's hanging, use linker symbol directly
	// TODO: Fix get_exception_vectors_addr() assembly function
	uartPuts("DEBUG: Using linker symbol exception_vectors_start directly...\r\n")

	// Access linker symbol the same way as __end
	// The linker provides exception_vectors_start, we need to access it
	// For now, let's try accessing it via unsafe.Pointer like __end
	// But first, let's just use a reasonable address to test if the rest works
	// Exception vectors are typically placed right after kernel text
	// Kernel starts at 0x200000, so vectors should be shortly after
	// Let's use a placeholder that we'll fix properly later
	uartPuts("DEBUG: Temporarily using placeholder address for testing...\r\n")
	// Actual address from readelf: 0x2a5000 (found via: target-readelf -s kernel-qemu.elf | grep exception_vectors)
	exceptionVectorAddr := uintptr(0x2a5000) // TODO: Fix proper lookup
	uartPuts("DEBUG: Using address: 0x")
	uartPutHex64(uint64(exceptionVectorAddr))
	uartPuts("\r\n")
	uartPuts("WARNING: Using hardcoded address - this is temporary!\r\n")
	uartPuts("DEBUG: Got address: 0x")
	uartPutHex64(uint64(exceptionVectorAddr))
	uartPuts("\r\n")

	uartPuts("Setting VBAR_EL1 to 0x")
	uartPutHex64(uint64(exceptionVectorAddr))
	uartPuts("\r\n")

	// Verify address is 2KB aligned (required for VBAR_EL1)
	if exceptionVectorAddr&0x7FF != 0 {
		uartPuts("ERROR: Exception vector address not 2KB aligned!\r\n")
		uartPuts("Address: 0x")
		uartPutHex64(uint64(exceptionVectorAddr))
		uartPuts(" (alignment check: 0x")
		uartPutHex64(uint64(exceptionVectorAddr & 0x7FF))
		uartPuts(")\r\n")
		// Don't continue if address is wrong
		return nil
	}
	uartPuts("DEBUG: Address alignment verified (2KB aligned)\r\n")

	// Note: Interrupts should already be disabled during early kernel boot
	// We don't call disable_irqs() here because if VBAR_EL1 isn't set yet,
	// accessing DAIF might trigger an exception that can't be handled

	// VBAR_EL1 is now set in boot.s before Go code runs
	// This avoids potential issues with Go runtime triggering exceptions
	// when trying to set VBAR_EL1 from Go code
	uartPuts("DEBUG: VBAR_EL1 should already be set by boot.s\r\n")
	uartPuts("DEBUG: Skipping VBAR_EL1 setup - already done in assembly\r\n")
	// Skip readback verification - reading VBAR_EL1 causes a synchronous exception

	// Add a memory barrier to ensure VBAR_EL1 is set before continuing
	asm.Dsb()
	uartPuts("DEBUG: Memory barrier complete\r\n")

	uartPuts("DEBUG: After set_vbar_el1, before re-enabling interrupts\r\n")

	// Keep interrupts disabled for now - we'll enable them after GIC init
	// enable_irqs()  // Don't enable yet - wait for GIC init

	uartPuts("Exception handlers initialized\r\n")

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
func handleException(excInfo ExceptionInfo) {
	// CRITICAL: Print immediately to verify handler is called
	// Use direct UART output to avoid any stack/heap issues
	uartPutcDirect('*') // '*' = Exception handler in Go code called

	// Extract exception class from ESR_EL1
	ec := (excInfo.ESR >> 26) & 0x3F

	uartPutsDirect("EXCEPTION: ELR=0x")
	uartPutHex64Direct(excInfo.ELR)
	uartPutsDirect(" ESR=0x")
	uartPutHex64Direct(excInfo.ESR)
	uartPutsDirect(" EC=0x")
	uartPutHex8Direct(uint8(ec))
	uartPutsDirect(" SPSR=0x")
	uartPutHex64Direct(excInfo.SPSR)
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

	case EC_DATA_ABORT_ELx:
		uartPutsDirect("Data abort at 0x")
		uartPutHex64Direct(excInfo.ELR)
		uartPutsDirect(" (fault address: 0x")
		uartPutHex64Direct(excInfo.FAR)
		uartPutsDirect(")\r\n")

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
	uartPutc('I')

	// Handle interrupt - interrupt ID already acknowledged in assembly
	// irqID passed from assembly (read from GICC_IAR immediately on entry)
	gicHandleInterruptWithID(irqID)

	// Print 'i' to show IRQ handler returning
	uartPutc('i')
}

// fiqHandlerGo is the actual Go implementation
//
//go:nosplit
func fiqHandlerGo() {
	uartPuts("FIQ fired (not implemented)\r\n")
}

// serrorHandlerGo is the actual Go implementation
//
//go:nosplit
func serrorHandlerGo() {
	uartPuts("SError occurred - system error (not recoverable)\r\n")
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
