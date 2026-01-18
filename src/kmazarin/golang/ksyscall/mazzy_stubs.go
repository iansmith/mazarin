package ksyscall

// Stub implementations for Mazzy syscalls.
// These will be replaced with real implementations in Phase 3.

// SyscallLaunch creates a new priest from an ELF file.
// Returns: 64-bit token (pointer to PCB) or negative errno.
func SyscallLaunch(filenamePtr, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	return -38 // ENOSYS - not implemented
}

// SyscallRun creates a thin client in the current priest.
// Returns: 64-bit token (pointer to ThinCB) or negative errno.
func SyscallRun(filenamePtr, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	return -38 // ENOSYS - not implemented
}

// SyscallAllocPages allocates page-aligned memory.
// count: number of 4KB pages
// Returns: virtual address or negative errno.
func SyscallAllocPages(count, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	return -38 // ENOSYS - not implemented
}

// SyscallMazzyExit terminates current thin or priest.
// This is the Mazzy version (distinct from Linux exit syscall).
func SyscallMazzyExit(exitCode, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	return -38 // ENOSYS - not implemented
}

// SyscallReap cleans up a zombie thin.
// Returns: exit code of the thin, or negative errno.
func SyscallReap(thinToken, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	return -38 // ENOSYS - not implemented
}

// SyscallStartThin starts a thin client.
func SyscallStartThin(thinToken, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	return -38 // ENOSYS - not implemented
}

// SyscallEventHandled signals that event handling is complete.
func SyscallEventHandled(arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	return -38 // ENOSYS - not implemented
}

// SyscallCreateThread creates a new kernel thread in the current priest.
// Returns: 64-bit token (pointer to KThread) or negative errno.
func SyscallCreateThread(entryPoint, stackSize, arg2, arg3, arg4, arg5 uint64) int64 {
	return -38 // ENOSYS - not implemented
}

// SyscallExitThread terminates the current thread.
func SyscallExitThread(exitCode, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	return -38 // ENOSYS - not implemented
}

// SyscallStartPriest starts a priest's first thread.
func SyscallStartPriest(priestToken, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	return -38 // ENOSYS - not implemented
}

// SyscallMazzyKill terminates a priest or thin.
// This is the Mazzy version (distinct from Linux kill syscall).
func SyscallMazzyKill(token, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	return -38 // ENOSYS - not implemented
}

// SyscallMazzyWait waits for a priest or thin to exit.
// This is the Mazzy version (distinct from Linux wait syscalls).
func SyscallMazzyWait(token, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	return -38 // ENOSYS - not implemented
}
