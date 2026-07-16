package ksyscall

import (
	"mazzy/kmazarin/proc"
	_ "unsafe" // for go:linkname
)

// Forward declarations for functions provided via assembly or go:linkname.

// jumpToUserspace performs the transition from kernel (EL1) to userspace (EL0)
// This is implemented in assembly
func jumpToUserspace(entryPoint, stackPtr uint64)

// EnableTimerIRQ is provided by main package via go:linkname
// Enables the timer IRQ for preemption before jumping to userspace
//
//go:linkname EnableTimerIRQ main.EnableTimerIRQ
func EnableTimerIRQ()

// CreateUserspaceThread is provided by main package via go:linkname
// Allocates a new thread for a userspace process and adds it to the ready queue
//
//go:linkname CreateUserspaceThread main.CreateUserspaceThread
func CreateUserspaceThread(entryPoint, stackPtr uint64, pageTableL0PA uintptr) int16

// CreateCloneExecThread is provided by main package via go:linkname (MAZ-112).
// It allocates the clone_exec child thread and populates its race-sensitive
// startup state (ParentPID + buffered intent + chdir target + MAZ-149 stdio
// redirect mask) UNDER schedulerLock before enqueue, then returns the child
// TID and PID so DoCloneExecWork needn't re-find the new shepherd by its L0
// page-table address.
// MAZ-127: reservedPID is the child PID reserved at vfork clone time (0 = none).
//
//go:linkname CreateCloneExecThread main.CreateCloneExecThread
func CreateCloneExecThread(entryPoint, stackPtr uint64, pageTableL0PA uintptr, parentPID, reservedPID proc.ShepherdId, intent []proc.CloneExecIntentOp, cwd []byte, stdioRedirectMask uint8) (int16, proc.ShepherdId)

// unsafePointer is a helper to convert uintptr to pointer
//
//go:linkname unsafePointer runtime.noescape
func unsafePointer(p uintptr) *byte
