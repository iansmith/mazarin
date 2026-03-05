package main

// mapExceptionToSignal maps a RISC-V scause value to a Linux signal.
// Only synchronous exceptions (bit 63 = 0) should reach here.
//
//go:nosplit
func mapExceptionToSignal(scause uint64) int {
	// Mask off the interrupt bit (should be 0 for exceptions)
	cause := scause & 0x7FFFFFFFFFFFFFFF
	switch cause {
	case 0: // Instruction address misaligned
		return _SIGBUS
	case 1: // Instruction access fault
		return _SIGSEGV
	case 2: // Illegal instruction
		return _SIGILL
	case 3: // Breakpoint
		return _SIGTRAP
	case 4: // Load address misaligned
		return _SIGBUS
	case 5: // Load access fault
		return _SIGBUS
	case 6: // Store/AMO address misaligned
		return _SIGBUS
	case 7: // Store/AMO access fault
		return _SIGBUS
	case 12: // Instruction page fault
		return _SIGSEGV
	case 13: // Load page fault
		return _SIGSEGV
	case 15: // Store/AMO page fault
		return _SIGSEGV
	default:
		return _SIGSEGV
	}
}

// mapExceptionToSICode maps a RISC-V scause to a Linux si_code.
// For SIGSEGV page faults, defaults to SEGV_MAPERR.
//
//go:nosplit
func mapExceptionToSICode(signum int, scause uint64) int32 {
	if signum == _SIGSEGV {
		return _SEGV_MAPERR
	}
	return 1
}
