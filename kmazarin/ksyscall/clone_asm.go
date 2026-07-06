package ksyscall

// Forward declarations for functions provided via go:linkname from main package.

// CloneThread is provided by main package via go:linkname.
// Creates a new thread with proper context for Go's clone wrapper.
// Returns TID (int16 = slot index, used as ASID for VM).
func CloneThread(stack, returnAddr, spsr, mp, gp, fn uint64) int16

// CloneVforkThread is provided by main package via go:linkname.
// Implements kernel-emulated vfork: spawns a transient child-context thread
// and suspends the parent. Returns transient TID or negative errno.
// Types: ThreadId is int16, ShepherdId is int16.
func CloneVforkThread(stack, returnAddr, spsr uint64, parentTID, reservedPID int16) int16

// ReserveChildPID is provided by main package via go:linkname.
// Acquires a child PID from the allocator at clone time. Returns ShepherdId (int16).
func ReserveChildPID() int16

// ReleaseChildPID is provided by main package via go:linkname.
// Returns a reserved PID back to the allocator on failure/abort.
func ReleaseChildPID(pid int16)

// GetVforkReservedPIDForTID is provided by main package via go:linkname.
// Retrieves the reserved PID for the given kernel TID. Used by SyscallCloneExec
// to look up the vfork table using the CallerTID from the params blob.
// Returns ShepherdId (int16), or 0 if tid is not a known transient vfork thread.
func GetVforkReservedPIDForTID(tid int16) int16

// ReapVforkTransient is provided by main package via go:linkname.
// Terminates a transient vfork thread (by TID) from outside its own SVC context.
// Returns true if the thread was found and reaped, false otherwise.
func ReapVforkTransient(tid int16) bool

// IsTransientVforkThread is provided by main package via go:linkname.
// Reports whether the current thread is a transient vfork thread (VforkTransient=1).
//
//go:nosplit
func IsTransientVforkThread() bool

// lookupVforkParent is provided by main package via go:linkname.
// Looks up parent TID and reserved PID for a transient thread.
func lookupVforkParent(transientTID int16) (parentTID int16, reservedPID int16)

// wakeVforkParent is provided by main package via go:linkname.
// Wakes the parent thread and clears the linkage entry.
func wakeVforkParent(transientTID int16, result int64)

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

// SetSyscallCloneTLS is provided by main package via go:linkname.
// Stores the CLONE_SETTLS newtls value so the child thread gets its own FS_BASE.
func SetSyscallCloneTLS(tls uint64)
