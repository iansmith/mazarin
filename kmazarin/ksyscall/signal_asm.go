//go:build !test_stubs

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

// RestoreFromSignalFrame restores registers from signal frame ucontext,
// clears InSignalHandler, and sets SigreturnPending flag.
// Provided by main.restoreFromSignalFrameForKsyscall.
//go:nosplit
func RestoreFromSignalFrame(threadPtr uintptr)
