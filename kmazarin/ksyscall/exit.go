package ksyscall

import (
	"mazzy/kmazarin/klog"
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
// Nosplit trampoline — thin wrapper so the dispatch table entry keeps its
// nosplit guarantee. All logic lives in exitGroupImpl (non-nosplit).
//
//go:nosplit
func SyscallExitGroup(status, _, _, _, _, _ uint64) int64 {
	return exitGroupImpl(status)
}

// exitGroupImpl is the non-nosplit body of SyscallExitGroup. Separated to allow
// calls to klog, TerminateShepherd, and IsTransientVforkThread without blowing
// the nosplit stack budget.
func exitGroupImpl(status uint64) int64 {
	// Transient vfork thread (MAZ-127): the child block calls exit_group after
	// execve returns. The transient shares the parent's PID, so we must NOT
	// call TerminateShepherd — that would kill the parent. Just exit the thread.
	if IsTransientVforkThread() {
		nextCtx := ThreadExit()
		if nextCtx == 0 {
			haltForever()
		}
		SetSyscallSwitchTarget(nextCtx)
		return 0
	}

	p := proc.CurrentShepherd()
	pid := proc.ShepherdId(0)
	if p != nil {
		pid = p.PID
	}
	if pid == 0 {
		// Kernel exit_group — this is a fatal error (runtime.throw).
		// Halt instead of tearing down kernel threads.
		klog.Criticalf("KERNEL EXIT GROUP", "KERNEL EXIT GROUP — halting\n")
		haltForever()
	}

	// Log shepherd exit to UART for diagnostics (Criticalf uses polling, always visible).
	klog.Criticalf("E", "E%02d status=%d\n", int16(pid), int64(status))

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
		klog.Criticalf("KERNEL SysExit", "KERNEL SysExit — halting\n")
		haltForever()
	}

	nextCtx := TerminateShepherd(pid, int64(status))
	if nextCtx == 0 {
		haltForever()
	}
	SetSyscallSwitchTarget(nextCtx)
	return 0
}

