package ksyscall

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/proc"
)

// SyscallExit implements the exit(2) syscall (syscall 93)
// Gracefully exits the current thread and switches to another.
//
//go:nosplit
func SyscallExit(status, _, _, _, _, _ uint64) int64 {
	// Exit current thread and switch to next ready thread
	nextCtx := ThreadExit()
	if nextCtx == 0 {
		// No more threads - halt system
		haltForever()
	}

	// Tell syscall dispatcher to switch to next thread
	SetSyscallSwitchTarget(nextCtx)
	return 0 // Value ignored, context switch will occur
}

// SyscallExitGroup implements the exit_group(2) syscall (syscall 94)
//
// If this is the kernel (PID 0), exit_group means the Go runtime called
// throw() — this is a fatal kernel error. Halt immediately rather than
// silently switching threads and masking the real problem.
//
//go:nosplit
func SyscallExitGroup(status, _, _, _, _, _ uint64) int64 {
	p := proc.CurrentShepherd()
	pid := proc.ShepherdId(0)
	if p != nil {
		pid = p.PID
	}
	if pid == 0 {
		// Kernel exit_group — this is a fatal error (runtime.throw).
		// Halt instead of tearing down kernel threads.
		console.KWriteString("KERNEL EXIT GROUP — halting\r\n")
		haltForever()
	}

	// Userspace exit_group — kill all threads of this shepherd
	nextCtx := TerminateShepherd(pid, int64(status))
	if nextCtx == 0 {
		haltForever()
	}
	SetSyscallSwitchTarget(nextCtx)
	return 0
}

// SyscallMazzyExit implements the Mazzy SysExit syscall (0x1004)
// This is called by userspace programs through shepherd to cleanly exit.
// Terminates the calling shepherd and all its threads.
//
// arg0: exit status code
//
//go:nosplit
func SyscallMazzyExit(status, _, _, _, _, _ uint64) int64 {
	p := proc.CurrentShepherd()
	pid := proc.ShepherdId(0)
	if p != nil {
		pid = p.PID
	}
	if pid == 0 {
		console.KWriteString("KERNEL SysExit — halting\r\n")
		haltForever()
	}

	nextCtx := TerminateShepherd(pid, int64(status))
	if nextCtx == 0 {
		haltForever()
	}
	SetSyscallSwitchTarget(nextCtx)
	return 0
}

// printDecimalNonRecursive prints a uint64 as decimal to console
// Uses a fixed-size buffer to avoid recursion (for nosplit compatibility)
func printDecimalNonRecursive(n uint64) {
	// Max uint64 is 18446744073709551615 (20 digits)
	var buf [20]byte
	i := len(buf) - 1

	// Handle zero specially
	if n == 0 {
		console.KWriteByte('0')
		return
	}

	// Build digits from right to left
	for n > 0 {
		buf[i] = byte('0' + n%10)
		n /= 10
		i--
	}

	// Print digits from left to right
	for j := i + 1; j < len(buf); j++ {
		console.KWriteByte(buf[j])
	}
}
