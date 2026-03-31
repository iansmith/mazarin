package ksyscall

import (
	_ "unsafe" // for go:linkname
)

// Forward declarations for input focus functions provided via go:linkname.

// RequestWindowManagerKernel atomically claims the WM role for a shepherd.
// Returns true if successful.
//
//go:linkname RequestWindowManagerKernel main.requestWindowManagerKernel
func RequestWindowManagerKernel(sid int32) bool

// GetWindowManagerSID returns the current WM shepherd ID (-1 if none).
//
//go:linkname GetWindowManagerSID main.GetWindowManagerSID
func GetWindowManagerSID() int32
