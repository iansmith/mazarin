//go:build amd64

package ksyscall

import "mazzy/shared/linuxabi"

// CLONE_SETTLS tells the kernel to set the child's FS_BASE from the newtls parameter.
// Without this, the child inherits the parent's FS_BASE and corrupts the parent's TLS
// when it writes its g pointer to FS:-8.
const cloneSETTLS = 0x80000

// SyscallClone implements the clone(2) syscall for AMD64.
// On AMD64, the standard Go runtime's clone keeps mp/gp/fn in callee-saved
// registers (R13, R9, R12) instead of storing them on the child stack
// (which ARM64 and RISC-V do). The exception handler saves these registers
// from the exception frame via SetSyscallCloneRegs.
//
// Kernel syscall arg order: clone(flags=RDI, newsp=RSI, ptid=RDX, ctid=R10, newtls=R8)
// Dispatch passes: arg0=RDI, arg1=RSI, arg2=RDX, arg3=R10, arg4=R8
//
// Note: No //go:nosplit because CloneThread allocates memory for thread nodes.
func SyscallClone(flags, stack, ptid, ctid, tls, _ uint64) int64 {
	// A process-flavor clone (CLONE_THREAD clear) must NOT be turned into a
	// thread here — see the arm64 SyscallClone in clone.go for the rationale.
	// The dispatch gate only lets thread clones reach this path; a process
	// clone arriving here means the shepherd clone handler is unregistered, so
	// fail cleanly rather than spawn a thread inside the parent.
	if linuxabi.IsProcessClone(flags) {
		return -38 // ENOSYS
	}

	// Get mp/gp/fn from saved callee-saved registers (set by exception handler).
	// R12 = fn (entry function pointer)
	// R13 = mp (m pointer)
	// R9  = gp (g pointer)
	fn, mp, gp := GetSyscallCloneRegs()

	// Get the actual return address (instruction after SYSCALL) for the child
	returnAddr := GetSyscallELR()
	// Get the processor state (RFLAGS) from the parent
	spsr := GetSyscallSPSR()

	// CLONE_SETTLS: store the newtls value so the child gets its own FS_BASE.
	// On real Linux, clone with CLONE_SETTLS sets the child's FS_BASE from
	// the newtls parameter (R8 in the SYSCALL convention). The Go runtime's
	// clone.abi0 passes &m.tls[1] here. Without this, the child writes its
	// g pointer to FS:-8 using the PARENT's FS_BASE, corrupting the parent's TLS.
	if flags&cloneSETTLS != 0 && tls != 0 {
		SetSyscallCloneTLS(tls)
	}

	_ = ptid
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
