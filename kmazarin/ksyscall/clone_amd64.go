//go:build amd64

package ksyscall

// SyscallClone implements the clone(2) syscall for AMD64.
// On AMD64, the standard Go runtime's clone keeps mp/gp/fn in callee-saved
// registers (R13, R9, R12) instead of storing them on the child stack
// (which ARM64 and RISC-V do). The exception handler saves these registers
// from the exception frame via SetSyscallCloneRegs.
//
// Note: No //go:nosplit because CloneThread allocates memory for thread nodes.
func SyscallClone(flags, stack, ptid, tls, ctid, _ uint64) int64 {
	// Get mp/gp/fn from saved callee-saved registers (set by exception handler).
	// R12 = fn (entry function pointer)
	// R13 = mp (m pointer)
	// R9  = gp (g pointer)
	fn, mp, gp := GetSyscallCloneRegs()

	// Get the actual return address (instruction after SYSCALL) for the child
	returnAddr := GetSyscallELR()
	// Get the processor state (RFLAGS) from the parent
	spsr := GetSyscallSPSR()

	// Suppress unused warnings
	_ = flags
	_ = ptid
	_ = tls
	_ = ctid

	// Create the thread using CloneThread from main package
	tid := CloneThread(stack, returnAddr, spsr, mp, gp, fn)

	if tid < 0 {
		return -1 // EAGAIN - no free thread slots
	}

	// Return TID to parent
	// CRITICAL: CloneThread has called SetSyscallSwitchTarget, so after this
	// syscall returns, the assembly will switch to the NEW thread (B).
	// The parent (A) will be saved to ready queue and will return from clone()
	// with this TID when it's eventually scheduled.
	return int64(tid)
}
