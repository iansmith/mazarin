//go:build arm64 || amd64 || riscv64

package main

import (
	"mazzy/kmazarin/serial"
	"mazzy/shared/hid"
	"sync/atomic"
)

// ============================================================================
// Input Routing — Window Manager Completion Ring
// ============================================================================
//
// The window manager shepherd (rachel) receives ALL input events via a shared
// completion ring page. The IRQ top-half writes events directly to the ring
// and sends a mailbox notification. Rachel classifies events in userspace
// and forwards to focused shepherds via ring buffer IPC.
//
// Syscall interface:
//   - SysRequestWindowManager: claim WM role (first-come-first-served)

// windowManagerSID is the shepherd that claimed WM role. -1 = none.
var windowManagerSID int32 = -1

// routeInputEvent pushes an HID event to the WM's shared completion ring.
// Called from the nosplit top-half.
//
//go:nosplit
//go:noinline
func routeInputEvent(ev hid.HIDEvent, class int) {
	_ = class // class is no longer used for routing; kept for call-site compat
	wmSID := atomic.LoadInt32(&windowManagerSID)
	if wmSID >= 0 && wmInputRingKVA != 0 {
		completionRingPush(wmInputRingKVA, ev)
	}
}

// wakeInputConsumers is a no-op when the WM uses the shared completion ring.
// The caller invokes wakeWMViaMailbox() separately after all events are pushed.
// Kept as a function to avoid changing all top-half call sites.
//
//go:nosplit
//go:noinline
func wakeInputConsumers(class int) {
	// Legacy focused-shepherd waking removed. All input goes through rachel.
}

// wakeWMViaMailbox sends a single InputEventCode mailbox notification to the
// WM shepherd. Called from the top-half AFTER all events have been pushed to
// the shared ring.
//
//go:nosplit
//go:noinline
func wakeWMViaMailbox() {
	if wmInputRingKVA != 0 {
		mailboxSendFromIRQ(wmInputRingOwnerSID, hid.InputEventCode)
	}
}

// CleanupInputFocusForShepherd clears WM state for a dying shepherd.
// Called from TerminateShepherd.
func CleanupInputFocusForShepherd(shepherdID int16) {
	sid := int32(shepherdID)
	atomic.CompareAndSwapInt32(&windowManagerSID, sid, -1)

	serial.RawUARTPuts("[InputFocus] cleaned for shepherd ")
	serial.RawUARTDecimal(uint64(shepherdID))
	serial.RawUARTPuts("\r\n")
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
