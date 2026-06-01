//go:build arm64

package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/shared/linuxabi"
)

// SyscallClone implements the clone(2) syscall for ARM64 and RISC-V.
//
// THREAD-flavor clones (CLONE_THREAD set) spawn a new thread via CloneThread,
// running the Go entry function (fn) on the new stack.
//
// PROCESS-flavor clones (CLONE_THREAD clear) implement kernel-emulated vfork (MAZ-127):
// the kernel spawns a transient child-context thread and suspends the parent. The
// transient runs the Go child block (dup3/close/fcntl/chdir/execve); the parent is
// parked in ThreadBlockedKernelWork state, woken by the child's execve with the
// child PID (or an errno on failure/abort).
//
// On these architectures, Go's runtime pushes mp, gp, fn onto the child stack
// before calling clone. We extract these values from the stack.
//
// Stack layout (positive offsets from passed stack pointer, after Go's SUB $32):
//
//	stack+0:  saved LR (Go's clone caller return address, not used by us)
//	stack+8:  fn (entry function)
//	stack+16: gp (g pointer)
//	stack+24: mp (m pointer)
//
// Note: No //go:nosplit because CloneThread / CloneVforkThread allocate memory for thread nodes.
func SyscallClone(flags, stack, ptid, tls, ctid, _ uint64) int64 {
	// Get the actual return address (instruction after SVC) for both parent and child
	returnAddr := GetSyscallELR()
	spsr := GetSyscallSPSR()
	parentTID := GetCurrentThreadTID()

	// Suppress unused warnings
	_ = ptid
	_ = tls
	_ = ctid

	// PROCESS-flavor clone (CLONE_THREAD clear) — kernel-emulated vfork (MAZ-127)
	if linuxabi.IsProcessClone(flags) {
		// Reserve a child PID at clone time (D3, vfork-faithful)
		reservedPID := ReserveChildPID()
		if reservedPID == 0 {
			return -12 // ENOMEM - PID allocator exhausted
		}

		// Spawn the transient child-context thread and suspend the parent
		transientTID := CloneVforkThread(stack, returnAddr, spsr, parentTID, int16(reservedPID))
		if transientTID < 0 {
			// Failed — release the reserved PID
			ReleaseChildPID(reservedPID)
			return int64(transientTID)
		}

		// Parent is now suspended in ThreadBlockedKernelWork state.
		// The transient thread will run next (via SetSyscallSwitchTarget).
		// The parent's return value (child PID) will be injected by the execve worker.
		// The child (transient) returns with x0=0 (its own TID semantically, but x0=0 for vfork).
		return 0 // Parent returns here on resume; child continues below
	}

	// THREAD-flavor clone (CLONE_THREAD set) — regular thread spawn
	// Extract mp, gp, fn from the stack (same as before)
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
	mp, ok1 := kmem.ReadUserUint64(uintptr(stack - 8))
	gp, ok2 := kmem.ReadUserUint64(uintptr(stack - 16))
	fn, ok3 := kmem.ReadUserUint64(uintptr(stack - 24))
	if !ok1 || !ok2 || !ok3 {
		return -14 // EFAULT
	}

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
