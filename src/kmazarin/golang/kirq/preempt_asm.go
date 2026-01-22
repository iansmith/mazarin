//go:build !test_stubs

package kirq

import (
	_ "unsafe" // for go:linkname
)

// Forward declarations for functions provided via go:linkname or assembly.
// These are excluded during test builds and replaced by stubs.

// getPreemptOffsets accesses main.GetPreemptOffsets via linkname.
// This function is provided by runtime_offsets.go in main package.
//
//go:linkname getPreemptOffsets main.GetPreemptOffsets
func getPreemptOffsets() preemptOffsetsType

// TimerIRQHandlerAsm is the pure assembly timer IRQ handler.
// Implemented in preempt_arm64.s. Does NOT call any Go functions.
// Sets g.preempt and g.stackguard0 directly for cooperative preemption.
func TimerIRQHandlerAsm()
