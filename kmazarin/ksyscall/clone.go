//go:build arm64

package ksyscall

import (
	"mazzy/kmazarin/kmem"
)

// SyscallClone implements the clone(2) syscall for ARM64 and RISC-V.
// On these architectures, Go's runtime pushes mp, gp, fn onto the child stack
// before calling clone. We extract these values from the stack.
//
// Stack layout (positive offsets from passed stack pointer, after Go's SUB $32):
//   stack+0:  saved LR (Go's clone caller return address, not used by us)
//   stack+8:  fn (entry function)
//   stack+16: gp (g pointer)
//   stack+24: mp (m pointer)
//
// Note: No //go:nosplit because CloneThread allocates memory for thread nodes.
func SyscallClone(flags, stack, ptid, tls, ctid, _ uint64) int64 {
	// Extract mp, gp, fn from the stack (same as Cardinal)
	// Go writes values at negative offsets from the original stack pointer,
	// then does SUB $32, but the syscall apparently receives the PRE-SUB stack.
	// Stack layout (negative offsets from passed stack):
	//   stack-8:  mp (m pointer)
	//   stack-16: gp (g pointer)
	//   stack-24: fn (entry function)
	//   stack-32: saved LR
	//
	// Read stack values through safe accessors.
	// These handle both kernel and user addresses automatically.
	mp, ok1 := kmem.ReadUserUint64(uintptr(stack - 8))
	gp, ok2 := kmem.ReadUserUint64(uintptr(stack - 16))
	fn, ok3 := kmem.ReadUserUint64(uintptr(stack - 24))
	if !ok1 || !ok2 || !ok3 {
		return -14 // EFAULT
	}

	// Get the actual return address (instruction after SVC) for the child
	// Both parent and child should "return" to the same place,
	// but parent gets TID in x0 and child gets 0 in x0
	returnAddr := GetSyscallELR()
	// Get the processor state (SPSR) from the parent - child should have same state
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
