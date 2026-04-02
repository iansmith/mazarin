package ksyscall

import _ "unsafe" // for go:linkname

// Forward declarations for dirty notification bridge functions.
// Implemented in kmazarin/kmazarin/notify_bridge.go.

// blockForDirtyNotify blocks the current thread waiting for dirty notifications.
// Saves syscall arg0 (resultBufPtr) for rewind restoration.
// shepherdSID is used to set BlockedTID under the scheduler lock (race-free).
// Returns the context pointer of the next thread to switch to, or 0.
//
//go:linkname blockForDirtyNotify main.BlockForDirtyNotify
func blockForDirtyNotify(syscallArg0 uint64, shepherdSID uint64) uintptr

// wakeDirtyNotifyThread wakes a thread blocked in ThreadBlockedDirtyNotify.
// Called from enqueueNotification when dirty notifications arrive.
//
//go:linkname wakeDirtyNotifyThread main.WakeDirtyNotifyThread
func wakeDirtyNotifyThread(tid int32)

// wakeDirtyNotifyThreadSchedLockHeld wakes a blocked thread when the caller
// already holds the scheduler lock (used by TopHalfTickTimeUpdate path).
//
//go:linkname wakeDirtyNotifyThreadSchedLockHeld main.WakeDirtyNotifyThreadSchedLockHeld
func wakeDirtyNotifyThreadSchedLockHeld(tid int32)
