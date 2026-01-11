//go:build qemuvirt && aarch64

package ksyscall

// SyscallExit implements the exit(2) syscall (syscall 93)
// For now, just panic - we don't have a clean shutdown mechanism yet
//
//go:nosplit
func SyscallExit(status, _, _, _, _, _ uint64) int64 {
	// Convert status to a message
	// For now, just call KernelPanic with a generic message
	KernelPanic("Process called exit()")
	return 0 // unreachable
}

// SyscallExitGroup implements the exit_group(2) syscall (syscall 94)
// Same as exit for a single-threaded kernel
//
//go:nosplit
func SyscallExitGroup(status, _, _, _, _, _ uint64) int64 {
	KernelPanic("Process called exit_group()")
	return 0 // unreachable
}
