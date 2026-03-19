package ksyscall

// Signal-related forward declarations — provided by main package via go:linkname.

// GetSignalAction returns the signal action for the given signal number.
// Provided by main.getSignalActionForKsyscall.
//go:nosplit
func GetSignalAction(sig int) signalAction

// SetSignalAction sets a signal action for the given signal.
// Provided by main.setSignalActionForKsyscall.
//go:nosplit
func SetSignalAction(sig int, handler, flags, restorer, mask uint64)

// ThreadLookupByTID finds a thread by TID and returns its raw pointer.
// Returns 0 if not found.
// Provided by main.threadLookupByTIDForKsyscall.
//go:nosplit
func ThreadLookupByTID(tid int32) uintptr

// ThreadLookupByPID finds the first thread belonging to a shepherd with the given PID.
// Returns 0 if not found.
// Provided by main.threadLookupByPIDForKsyscall.
//go:nosplit
func ThreadLookupByPID(pid int16) uintptr

// SetThreadPendingSignal sets a pending signal bit on a thread.
// Provided by main.setThreadPendingSignalForKsyscall.
//go:nosplit
func SetThreadPendingSignal(threadPtr uintptr, signum int)

// GetThreadSignalStack returns the signal stack info for a thread.
// Provided by main.getThreadSignalStackForKsyscall.
//go:nosplit
func GetThreadSignalStack(threadPtr uintptr) (base, sp, size uint64)

// SetThreadSignalStack sets the signal stack info for a thread.
// Provided by main.setThreadSignalStackForKsyscall.
//go:nosplit
func SetThreadSignalStack(threadPtr uintptr, base, sp, size uint64)

// GetThreadPID returns the PID (ShepherdId) of a thread.
// Provided by main.getThreadPIDForKsyscall.
//go:nosplit
func GetThreadPID(threadPtr uintptr) int16

// RestoreFromSignalFrame restores registers from signal frame ucontext,
// clears InSignalHandler, and sets SigreturnPending flag.
// Provided by main.restoreFromSignalFrameForKsyscall.
//go:nosplit
func RestoreFromSignalFrame(threadPtr uintptr)

// WakeThreadForSignal moves a blocked thread (futex/sleep/softIRQ) to the
// ready queue so that pending signals are delivered at the next context switch.
// Provided by main.wakeThreadForSignalForKsyscall.
//go:nosplit
func WakeThreadForSignal(threadPtr uintptr)

// GetThreadTID returns the TID of a thread.
// Provided by main.getThreadTIDForKsyscall.
//go:nosplit
func GetThreadTID(threadPtr uintptr) int32

// ReservedKernelTIDs returns the number of TIDs reserved for kernel threads.
// Userspace must not target TIDs in [0, ReservedKernelTIDs).
// Provided by main.reservedKernelTIDsForKsyscall.
//go:nosplit
func ReservedKernelTIDs() int32

// ReservedKernelPIDs returns the number of PIDs reserved for the kernel.
// Userspace must not target PIDs in [0, ReservedKernelPIDs).
// Provided by main.reservedKernelPIDsForKsyscall.
//go:nosplit
func ReservedKernelPIDs() int16
