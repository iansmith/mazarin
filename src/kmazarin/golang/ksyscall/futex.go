//go:build qemuvirt && aarch64

package ksyscall

// Futex operations
const (
	FutexWait        = 0
	FutexWake        = 1
	FutexWaitPrivate = 128
	FutexWakePrivate = 129
)

// SyscallFutex implements the futex(2) syscall
// For now, we don't actually block/wake threads - just return immediately
// This is a simplified implementation to let the runtime start
// TODO: Implement proper futex with thread blocking/waking
//
//go:nosplit
func SyscallFutex(uaddr, op, val, timeout, uaddr2, val3 uint64) int64 {
	// Mask off the private flag
	opMasked := int32(op) & 0x7F

	switch opMasked {
	case FutexWait:
		// FUTEX_WAIT: Should block until woken
		// For now, just return immediately (spurious wakeup)
		// TODO: Implement proper blocking
		return 0

	case FutexWake:
		// FUTEX_WAKE: Should wake up to 'val' threads
		// For now, pretend we woke 0 threads (none were waiting)
		// TODO: Implement proper waking
		return 0

	default:
		// Unknown futex operation
		return -38 // ENOSYS
	}
}
