//go:build qemuvirt && aarch64

package ksyscall

// SyscallExit implements the exit(2) syscall
// For now, just panic - we don't have a clean shutdown mechanism yet
//
//go:nosplit
func SyscallExit(status, _, _, _, _, _ uint64) int64 {
	// Convert status to a message
	// For now, just call KernelPanic with a generic message
	KernelPanic("Process called exit()")
	return 0 // unreachable
}
