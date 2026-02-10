package ksyscall

// SyscallRegisterAsyncPreempt registers the asyncPreempt function address for
// the current priest (userspace process). This enables goroutine-level preemption
// within the priest by allowing the kernel to inject asyncPreempt when a goroutine
// has been running too long.
//
// The priest should call this early during initialization, after runtime.init()
// completes but before spawning goroutines. The address should be obtained via:
//   go:linkname asyncPreempt runtime.asyncPreempt
//   func asyncPreempt()
//   addr := reflect.ValueOf(asyncPreempt).Pointer()
//
// Args:
//   arg0: Address of the priest's runtime.asyncPreempt function
//
// Returns:
//   0 on success
//   -EINVAL if address is 0 or not aligned
//   -ENOENT if current thread is not a priest thread
//   -ESRCH if priest not found
//
//go:nosplit
func SyscallRegisterAsyncPreempt(asyncPreemptAddr, _, _, _, _, _ uint64) int64 {
	// Validate address
	if asyncPreemptAddr == 0 {
		return -22 // EINVAL
	}

	// Must be 4-byte aligned (RISC-V instruction alignment)
	if asyncPreemptAddr&3 != 0 {
		return -22 // EINVAL
	}

	// Call the main package implementation via linkname
	return RegisterAsyncPreemptAddr(asyncPreemptAddr)
}
