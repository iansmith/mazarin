//go:build qemuvirt && aarch64

package ksyscall

// SyscallRaisesig is an internal syscall used by Cardinal
// Not a real Linux syscall - just for compatibility
// For now, ignore it
//
//go:nosplit
func SyscallRaisesig(_, _, _, _, _, _ uint64) int64 {
	// This is not a real Linux syscall
	// Cardinal uses it internally - we can ignore it for now
	return 0
}
