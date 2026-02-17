//go:build !test_stubs

package ksyscall

// Forward declarations for functions provided via go:linkname from main package.

// CloneThread is provided by main package via go:linkname.
// Creates a new thread with proper context for Go's clone wrapper.
// Returns TID (int16 = slot index, used as ASID for VM).
func CloneThread(stack, returnAddr, spsr, mp, gp, fn uint64) int16

// GetSyscallELR is provided by main package via go:linkname.
// Returns the ELR_EL1 (return address) for the current syscall.
func GetSyscallELR() uint64

// GetSyscallSPSR is provided by main package via go:linkname.
// Returns the SPSR_EL1 (processor state) for the current syscall.
func GetSyscallSPSR() uint64

// GetSyscallCloneRegs is provided by main package via go:linkname.
// Returns saved R12(fn), R13(mp), R9(gp) from the exception frame.
// On AMD64, the standard Go runtime's clone keeps these in callee-saved
// registers instead of storing on the child stack.
func GetSyscallCloneRegs() (r12, r13, r9 uint64)
