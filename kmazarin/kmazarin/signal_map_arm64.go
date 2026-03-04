package main

// mapExceptionToSignal maps an ARM64 ESR_EL1 value to a Linux signal number.
// The exception class (EC) is in bits [31:26] of the ESR.
//
//go:nosplit
func mapExceptionToSignal(esr uint64) int {
	ec := (esr >> 26) & 0x3F
	switch ec {
	case 0x20, 0x21: // Instruction abort from EL0/EL1
		return _SIGSEGV
	case 0x24, 0x25: // Data abort from EL0/EL1
		// Check ISS for alignment fault (DFSC bits [5:0])
		dfsc := esr & 0x3F
		if dfsc == 0x21 { // Alignment fault
			return _SIGBUS
		}
		return _SIGSEGV
	case 0x22, 0x26: // PC/SP alignment fault
		return _SIGBUS
	case 0x00: // Unknown exception
		return _SIGILL
	case 0x0E: // Illegal Execution state
		return _SIGILL
	case 0x2C: // FP/SIMD trap
		return _SIGFPE
	case 0x15: // SVC — should not reach here
		return 0
	default:
		return _SIGSEGV // Default to SIGSEGV for unknown
	}
}
