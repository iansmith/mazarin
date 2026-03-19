package ksyscall

import (
	"mazzy/shared/hid"
	_ "unsafe" // for go:linkname
)

// Forward declarations for input focus functions provided via go:linkname.

// BlockOnInputQueue blocks current thread on an input focus queue.
// Returns context pointer of next thread, or 0.
//
//go:linkname BlockOnInputQueue main.BlockOnInputQueue
func BlockOnInputQueue(sid int32, class int) uintptr

// DrainInputQueue drains events from a per-shepherd input queue.
//
//go:linkname DrainInputQueue main.DrainInputQueue
func DrainInputQueue(sid int32, class int, buf []hid.HIDEvent, max int) int

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
