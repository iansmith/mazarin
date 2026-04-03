package ksyscall

import (
	"mazzy/kmazarin/proc"
	"mazzy/shared/hid"
	_ "unsafe" // for go:linkname
)

// Forward declarations for input focus functions provided via go:linkname.

// BlockOnInputQueue blocks current thread on an input focus queue.
// Returns context pointer of next thread, or 0.
//
//go:linkname BlockOnInputQueue main.BlockOnInputQueue
func BlockOnInputQueue(slot proc.ShepherdSlot, class int) uintptr

// DrainInputQueue drains events from a per-shepherd input queue.
//
//go:linkname DrainInputQueue main.DrainInputQueue
func DrainInputQueue(slot proc.ShepherdSlot, class int, buf []hid.HIDEvent, max int) int

// RequestWindowManagerKernel atomically claims the WM role for a shepherd.
// Returns true if successful.
//
//go:linkname RequestWindowManagerKernel main.requestWindowManagerKernel
func RequestWindowManagerKernel(sid int32) bool

// SetInputFocusKernel sets focus for a device class.
//
//go:linkname SetInputFocusKernel main.setInputFocusKernel
func SetInputFocusKernel(target int32, class int) int64

// GetWindowManagerSID returns the current WM shepherd ID (-1 if none).
//
//go:linkname GetWindowManagerSID main.GetWindowManagerSID
func GetWindowManagerSID() int32

// getCurrentThreadSlotWrapper returns the shepherd list slot of the current thread.
// Returns -1 for kernel threads.
//
//go:linkname getCurrentThreadSlotWrapper main.getCurrentThreadSlotWrapper
func getCurrentThreadSlotWrapper() int16
