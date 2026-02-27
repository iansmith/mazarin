//go:build amd64

package ksyscall

// SyscallRegisterAsyncPreempt is a no-op kept for backward compatibility.
// Goroutine preemption is now handled by the userspace Go runtime via SIGURG signals.
//
//go:nosplit
func SyscallRegisterAsyncPreempt(_, _, _, _, _, _ uint64) int64 {
	return 0
}
