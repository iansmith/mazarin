//go:build qemuvirt && aarch64

package ksyscall

// SyscallClone implements the clone(2) syscall
// Creates a new kernel thread for the Go runtime
//
//go:nosplit
func SyscallClone(flags, stack, ptid, tls, ctid, _ uint64) int64 {
	// Extract the actual parameters from the Go runtime's clone call
	// The Go runtime passes:
	//   flags: CLONE_* flags
	//   stack: Stack pointer for new thread
	//   ptid: Parent TID pointer (we ignore for now)
	//   tls: TLS pointer (we ignore, Go uses g register)
	//   ctid: Child TID pointer (we ignore for now)
	//
	// The actual entry function and g pointer are passed via registers
	// and will be extracted by the syscall handler

	// For now, call the handler that actually creates the thread
	// NOTE: This doesn't support context switching yet - that will be
	// added when we integrate with the SVC handler assembly

	// TODO: Extract entryFunc, mPtr, gPtr from somewhere
	// For now, return error since we can't get these parameters yet
	uartPutsDirect("[clone] Called but parameters not available\r\n")
	return -1
}
