//go:build !amd64

package ksyscall

// SyscallArchPrctl is a stub on non-x86_64 platforms.
// arch_prctl is x86_64-specific; returns ENOSYS on other architectures.
//
//go:nosplit
func SyscallArchPrctl(_, _, _, _, _, _ uint64) int64 {
	return -38 // ENOSYS
}
