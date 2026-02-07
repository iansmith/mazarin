//go:build amd64

package ksyscall

// SyscallRegisterAsyncPreempt registers the asyncPreempt function address for
// the current priest (userspace process). This enables goroutine-level preemption
// within the priest by allowing the kernel to inject asyncPreempt when a goroutine
// has been running too long.
//
// Args:
//
//	arg0: Address of the priest's runtime.asyncPreempt function
//
// Returns:
//
//	0 on success
//	-EINVAL if address is 0
//	-ENOENT if current thread is not a priest thread
//	-ESRCH if priest not found
//
//go:nosplit
func SyscallRegisterAsyncPreempt(asyncPreemptAddr, _, _, _, _, _ uint64) int64 {
	// Validate address
	if asyncPreemptAddr == 0 {
		return -22 // EINVAL
	}

	// x86_64 has variable-length instructions, no alignment requirement

	// Call the main package implementation via linkname
	return RegisterAsyncPreemptAddr(asyncPreemptAddr)
}
