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

// mapExceptionToSICode maps an ARM64 ESR_EL1 to a Linux si_code value
// appropriate for the given signal. For SIGSEGV, distinguishes between
// SEGV_MAPERR (translation fault) and SEGV_ACCERR (permission fault).
//
//go:nosplit
func mapExceptionToSICode(signum int, esr uint64) int32 {
	if signum == _SIGSEGV {
		// DFSC/IFSC in bits [5:0], category in bits [5:2]
		fsc := esr & 0x3F
		switch fsc & 0x3C {
		case 0x0C: // Permission fault (L0-L3)
			return _SEGV_ACCERR
		default: // Translation fault, access flag fault, etc.
			return _SEGV_MAPERR
		}
	}
	return 1 // Generic si_code for other signals
}
