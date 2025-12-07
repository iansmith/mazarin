package main

import (
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
}

// Link to assembly functions
//
//go:linkname set_vbar_el1 set_vbar_el1
//go:nosplit
func set_vbar_el1(addr uintptr)

//go:linkname enable_irqs enable_irqs
//go:nosplit
func enable_irqs()

//go:linkname disable_irqs disable_irqs
//go:nosplit
func disable_irqs()

//go:linkname read_spsr_el1 read_spsr_el1
//go:nosplit
func read_spsr_el1() uint64

//go:linkname write_spsr_el1 write_spsr_el1
//go:nosplit
func write_spsr_el1(value uint64)

//go:linkname read_elr_el1 read_elr_el1
//go:nosplit
func read_elr_el1() uint64

//go:linkname write_elr_el1 write_elr_el1
//go:nosplit
func write_elr_el1(value uint64)

//go:linkname read_esr_el1 read_esr_el1
//go:nosplit
func read_esr_el1() uint64

//go:linkname read_far_el1 read_far_el1
//go:nosplit
func read_far_el1() uint64

// InitializeExceptions sets up the exception vector table
// This must be called early in kernel initialization
func InitializeExceptions() error {
	// Get the address of the exception vector table
	// This is provided by the linker
	exceptionVectorAddr := uintptr(unsafe.Pointer(&exception_vectors_start))

	uartPuts("Setting VBAR_EL1 to 0x")
	uartPutHex64(uint64(exceptionVectorAddr))
	uartPuts("\r\n")

	// Set VBAR_EL1 to point to our exception vector table
	set_vbar_el1(exceptionVectorAddr)

	uartPuts("Exception handlers initialized\r\n")

	return nil
}

// ExceptionHandler is called from assembly when a synchronous exception occurs
// It handles the exception and logs details for debugging
//
//go:nosplit
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

// handleException dispatches the exception to the appropriate handler
func handleException(excInfo ExceptionInfo) {
	// Extract exception class from ESR_EL1
	ec := (excInfo.ESR >> 26) & 0x3F

	uartPuts("EXCEPTION: ELR=0x")
	uartPutHex64(excInfo.ELR)
	uartPuts(" ESR=0x")
	uartPutHex64(excInfo.ESR)
	uartPuts(" EC=0x")
	uartPutHex8(uint8(ec))
	uartPuts(" SPSR=0x")
	uartPutHex64(excInfo.SPSR)
	uartPuts(" FAR=0x")
	uartPutHex64(excInfo.FAR)
	uartPuts("\r\n")

	switch ec {
	case EC_UNKNOWN:
		uartPuts("Unknown exception at 0x")
		uartPutHex64(excInfo.ELR)
		uartPuts("\r\n")

	case EC_TRAP_WFx:
		uartPuts("WFx trap at 0x")
		uartPutHex64(excInfo.ELR)
		uartPuts("\r\n")

	case EC_TRAP_MSR_MRS_SYSTEM:
		uartPuts("MSR/MRS system instruction trap at 0x")
		uartPutHex64(excInfo.ELR)
		uartPuts("\r\n")

	case EC_DATA_ABORT_ELx:
		uartPuts("Data abort at 0x")
		uartPutHex64(excInfo.ELR)
		uartPuts(" (fault address: 0x")
		uartPutHex64(excInfo.FAR)
		uartPuts(")\r\n")

	case EC_PREFETCH_ABORT_ELx:
		uartPuts("Prefetch abort at 0x")
		uartPutHex64(excInfo.ELR)
		uartPuts("\r\n")

	case EC_BREAKPOINT_ELx:
		uartPuts("Breakpoint at 0x")
		uartPutHex64(excInfo.ELR)
		uartPuts("\r\n")

	case EC_ILLEGAL_EXECUTION:
		uartPuts("Illegal execution state at 0x")
		uartPutHex64(excInfo.ELR)
		uartPuts("\r\n")

	case EC_SVC_EL1_A64:
		// Supervisor call from EL1 (AArch64)
		uartPuts("SVC from EL1 at 0x")
		uartPutHex64(excInfo.ELR)
		uartPuts(" (immediate: ")
		uartPutUint32(uint32(excInfo.ESR & 0xFFFF))
		uartPuts(")\r\n")

	case EC_SVC_EL0_A64:
		// Supervisor call from EL0 (AArch64)
		uartPuts("SVC from EL0 at 0x")
		uartPutHex64(excInfo.ELR)
		uartPuts(" (immediate: ")
		uartPutUint32(uint32(excInfo.ESR & 0xFFFF))
		uartPuts(")\r\n")

	default:
		uartPuts("Unhandled exception class 0x")
		uartPutHex8(uint8(ec))
		uartPuts(" at 0x")
		uartPutHex64(excInfo.ELR)
		uartPuts("\r\n")
	}

	// Hang the system for now
	uartPuts("System halted\r\n")
	for {
		// Spin forever
	}
}

// IRQHandler is called from assembly when an interrupt (IRQ) occurs
// For now, it just logs and returns
// Later, this will dispatch to GIC to handle the actual interrupt
//
//go:nosplit
func IRQHandler() {
	uartPuts("IRQ fired (GIC dispatch not yet implemented)\r\n")
}

// FIQHandler is called from assembly when a fast interrupt (FIQ) occurs
// FIQ is rarely used in modern systems
//
//go:nosplit
func FIQHandler() {
	uartPuts("FIQ fired (not implemented)\r\n")
}

// SErrorHandler is called from assembly when a system error occurs
// This is a critical error that usually requires system shutdown
//
//go:nosplit
func SErrorHandler() {
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

// Linker-provided symbol for exception vector table location
// This should be defined in the linker script or assembly
var exception_vectors_start [0]byte

