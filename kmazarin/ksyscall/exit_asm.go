//go:build !test_stubs

package ksyscall

import (
	"mazzy/kmazarin/proc"
	_ "unsafe" // Required for go:linkname
)

// Forward declarations for functions provided via assembly or go:linkname.

// haltForever loops forever using WFI (implemented in assembly)
func haltForever()

// ThreadExit is provided by main package via go:linkname.
// Marks current thread as exited and switches to next ready thread.
// Returns context pointer of next thread, or 0 if no threads available.
//
//go:linkname ThreadExit main.ThreadExit
func ThreadExit() uintptr

// TerminatePriest kills all threads of a priest and cleans up resources.
// Returns context pointer of next thread, or 0 if no threads available.
//
//go:linkname TerminatePriest main.TerminatePriest
func TerminatePriest(pid proc.PriestId, status int64) uintptr
