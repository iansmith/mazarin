//go:build !test_stubs

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

// ThreadBlockSleep is provided by main package via go:linkname.
// Marks the current thread as sleeping and returns the next ready thread index.
func ThreadBlockSleep() uintptr
