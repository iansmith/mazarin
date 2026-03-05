package main

// mapExceptionToSignal maps an x86_64 exception vector number to a Linux signal.
//
//go:nosplit
func mapExceptionToSignal(vector uint64) int {
	switch vector {
	case 0: // #DE - Divide Error
		return _SIGFPE
	case 6: // #UD - Invalid Opcode
		return _SIGILL
	case 7: // #NM - Device Not Available (FPU)
		return _SIGFPE
	case 11: // #NP - Segment Not Present
		return _SIGBUS
	case 12: // #SS - Stack-Segment Fault
		return _SIGSEGV
	case 13: // #GP - General Protection Fault
		return _SIGSEGV
	case 14: // #PF - Page Fault
		return _SIGSEGV
	case 16: // #MF - x87 FP Exception
		return _SIGFPE
	case 17: // #AC - Alignment Check
		return _SIGBUS
	case 19: // #XM/#XF - SIMD FP Exception
		return _SIGFPE
	default:
		return _SIGSEGV
	}
}

// mapExceptionToSICode maps an x86_64 exception vector to a Linux si_code.
// For SIGSEGV page faults, defaults to SEGV_MAPERR (we don't have the
// page fault error code here to distinguish present vs not-present).
//
//go:nosplit
func mapExceptionToSICode(signum int, vector uint64) int32 {
	if signum == _SIGSEGV {
		return _SEGV_MAPERR
	}
	return 1
}
