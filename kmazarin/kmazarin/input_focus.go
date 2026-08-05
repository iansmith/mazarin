//go:build arm64 || amd64

package main

import (
	"sync/atomic"
)

// ============================================================================
// Window Manager Role
// ============================================================================
//
// The window manager shepherd (rachel) claims the WM role and receives input
// events through the WaitSoftIRQ slot rings. It classifies events in
// userspace and forwards to focused shepherds via ring buffer IPC.
//
// Syscall interface:
//   - SysRequestWindowManager: claim WM role (first-come-first-served)

// windowManagerSID is the shepherd that claimed WM role. -1 = none.
var windowManagerSID int32 = -1

// CleanupInputFocusForShepherd clears WM state for a dying shepherd.
// Called from TerminateShepherd.
func CleanupInputFocusForShepherd(shepherdID int16) {
	sid := int32(shepherdID)
	atomic.CompareAndSwapInt32(&windowManagerSID, sid, -1)
}

// GetWindowManagerSID returns the window manager shepherd ID (-1 if none).
//
//go:nosplit
func GetWindowManagerSID() int32 {
	return atomic.LoadInt32(&windowManagerSID)
}

// requestWindowManagerKernel atomically claims the WM role for a shepherd.
// Returns true if successful (first-come-first-served).
func requestWindowManagerKernel(sid int32) bool {
	return atomic.CompareAndSwapInt32(&windowManagerSID, -1, sid)
}
