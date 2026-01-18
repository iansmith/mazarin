package ksyscall

import (
	"unsafe"

	"kmazarin/kthread"
)

// SyscallLaunch creates a new priest from an ELF file.
// Returns: 64-bit token (pointer to PCB) or negative errno.
func SyscallLaunch(filenamePtr, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	// TODO: Implement
	// 1. Read filename from userspace
	// 2. Allocate PCB via kthread.CreatePCB()
	// 3. Load ELF (requires filesystem and ELF loader)
	// 4. Set up page tables
	// 5. Look up required symbols (AsyncEventHandler, etc.)
	// 6. Create initial KThread
	// 7. Return uint64(uintptr(unsafe.Pointer(pcb)))
	_ = filenamePtr
	return -38 // ENOSYS - not implemented yet
}

// SyscallRun creates a thin client in the current priest.
// Returns: 64-bit token (pointer to ThinCB) or negative errno.
func SyscallRun(filenamePtr, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	// TODO: Implement
	// 1. Get current PCB via kthread.GetCurrentPCB()
	// 2. Allocate ThinCB via kthread.CreateThinCB(pcb)
	// 3. Load thin ELF
	// 4. Patch runtime trampolines
	// 5. Map into priest's address space
	// 6. Set up stack with poison pill
	// 7. Return uint64(uintptr(unsafe.Pointer(thin)))
	_ = filenamePtr
	return -38 // ENOSYS - not implemented yet
}

// SyscallStartPriest starts a priest's first thread.
func SyscallStartPriest(priestToken, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	pcb := (*kthread.PCB)(unsafe.Pointer(uintptr(priestToken)))
	if pcb == nil {
		return -22 // EINVAL
	}
	// TODO: Start first thread in pcb.Threads
	// 1. Get first thread from pcb.Threads
	// 2. Add to run queue
	// 3. If this is a blocking call, wait for priest to exit
	return -38 // ENOSYS - not implemented yet
}

// SyscallStartThin starts a thin client.
func SyscallStartThin(thinToken, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	thin := (*kthread.ThinCB)(unsafe.Pointer(uintptr(thinToken)))
	if thin == nil {
		return -22 // EINVAL
	}
	// TODO: Set up context to jump to thin.EntryPoint
	// 1. Save current context
	// 2. Set up stack pointer to thin.StackTop
	// 3. Jump to thin.EntryPoint
	return -38 // ENOSYS - not implemented yet
}

// SyscallMazzyExit terminates current thin or priest.
// This is the Mazzy version (distinct from Linux exit syscall).
func SyscallMazzyExit(exitCode, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	// TODO: Implement
	// 1. Determine if in thin context or priest context
	// 2. Mark as Zombie, record exit code
	// 3. Queue async event to priest (if thin)
	// 4. Schedule next thread
	_ = exitCode
	return 0 // Does not return
}

// SyscallReap cleans up a zombie thin.
// Returns: exit code of the thin, or negative errno.
func SyscallReap(thinToken, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	thin := (*kthread.ThinCB)(unsafe.Pointer(uintptr(thinToken)))
	if thin == nil {
		return -22 // EINVAL
	}
	if thin.State != kthread.ThinZombie {
		return -22 // EINVAL - not a zombie
	}
	exitCode := thin.ExitCode
	// TODO: Call kthread.ReleaseResourcesThinCB(thin)
	return int64(exitCode)
}

// SyscallMazzyKill terminates a priest or thin.
// This is the Mazzy version (distinct from Linux kill syscall).
func SyscallMazzyKill(token, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	// TODO: Determine if token is PCB or ThinCB
	// For now, we could use a type tag or separate syscalls
	// Mark as zombie, clean up
	_ = token
	return -38 // ENOSYS - not implemented yet
}

// SyscallMazzyWait waits for a priest or thin to exit.
// This is the Mazzy version (distinct from Linux wait syscalls).
func SyscallMazzyWait(token, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	// TODO: Block until target exits, return exit code
	_ = token
	return -38 // ENOSYS - not implemented yet
}
