package ksyscall

import (
	"unsafe"

	"kmazarin/kthread"
)

// SyscallCreateThread creates a new kernel thread in the current priest.
// Returns: 64-bit token (pointer to KThread) or negative errno.
func SyscallCreateThread(entryPoint, stackSize, arg2, arg3, arg4, arg5 uint64) int64 {
	pcb := kthread.GetCurrentPCB()
	if pcb == nil {
		return -22 // EINVAL
	}

	// TODO: Implement via kthread.CreateKThread
	// kt := kthread.CreateKThread(pcb, entryPoint, stackSize)
	// if kt == nil {
	//     return -12 // ENOMEM
	// }
	// return int64(uintptr(unsafe.Pointer(kt)))

	_ = entryPoint
	_ = stackSize
	return -38 // ENOSYS - not implemented yet
}

// SyscallExitThread terminates the current thread.
func SyscallExitThread(exitCode, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	kt := kthread.GetCurrentKThread()
	if kt == nil {
		return -22 // EINVAL
	}

	pcb := kt.Priest

	// Mark as zombie
	kt.State = kthread.KThreadZombie

	// TODO: Remove from run queue
	// kthread.RemoveFromRunQueue(kt)

	// Check if last thread in priest
	pcb.Lock.Lock()
	isLast := pcb.Threads.Size() == 1
	pcb.Lock.Unlock()

	if isLast {
		// Last thread - terminate priest
		return SyscallMazzyExit(exitCode, 0, 0, 0, 0, 0)
	}

	// TODO: Clean up thread resources
	// kthread.ReleaseResourcesKThread(kt)

	// TODO: Schedule another thread
	// kthread.Schedule()

	// Does not return
	_ = exitCode
	_ = unsafe.Pointer(nil) // suppress import warning
	return 0
}
