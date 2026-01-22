//go:build arm64

package ksyscall

// SyscallWaitSoftIRQ blocks until a soft IRQ is delivered.
//
// Args:
//   arg0: pointer to SoftIRQBundle to fill
//
// Returns: 0 on success, negative errno on error
//
//go:nosplit
func SyscallWaitSoftIRQ(bundlePtr, _, _, _, _, _ uint64) int64 {
	if bundlePtr == 0 {
		return -14 // EFAULT
	}

	// Register as dispatcher (only one allowed)
	result := RegisterSoftIRQDispatcher()
	if result < 0 {
		return result
	}

	// Drain overflow queue first
	if GetPendingSoftIRQ(bundlePtr) {
		return 0
	}

	// Block waiting for next soft IRQ
	nextThread := ThreadBlockSoftIRQ(bundlePtr)
	if nextThread != 0 {
		SetSyscallSwitchTarget(nextThread)
	}

	return 0
}
