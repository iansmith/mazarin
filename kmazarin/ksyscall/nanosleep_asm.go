package ksyscall

// Forward declarations for functions provided via go:linkname from main package.
// These are excluded during test builds and replaced by stubs.

// GetCurrentThread is provided by main package via go:linkname.
// Returns the current thread index.
func GetCurrentThread() uintptr

// GetCurrentThreadTID is provided by main package via go:linkname.
// Returns the TID of the current thread.
func GetCurrentThreadTID() int16

// AddDeadline is provided by main package via go:linkname.
// Adds a deadline to the queue. Returns false if queue not initialized.
// Note: tid parameter is the thread TID (not index), used by WakeThreadAction.
func AddDeadline(deadline uint64, tid int32) bool

// AddDeadlineStatic is provided by main package via go:linkname.
// Adds a deadline to the static (nosplit-safe) deadline queue.
func AddDeadlineStatic(deadline uint64, tid int32)

// ThreadBlockSleep is provided by main package via go:linkname.
// Marks the current thread as sleeping and returns the next ready thread index.
func ThreadBlockSleep() uintptr

// IsCurrentThreadUserspace is provided by main package via go:linkname.
// Returns true if the current thread belongs to a userspace shepherd.
func IsCurrentThreadUserspace() bool

// WakeNetpollThread is provided by main package via go:linkname.
// Wakes a thread that is sleeping (e.g., blocked in SyscallEpollPwait).
// Used by write(eventfd) to implement Go's netpollBreak mechanism.
func WakeNetpollThread(tid int32)
