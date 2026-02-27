//go:build !test_stubs

package ksyscall

import _ "unsafe" // for go:linkname

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

// unsafePointer is a helper to convert uintptr to pointer
//
//go:linkname unsafePointer runtime.noescape
func unsafePointer(p uintptr) *byte
