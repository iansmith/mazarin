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
