package kirq

import (
	_ "unsafe" // for go:linkname
)

// Forward declarations for functions provided via go:linkname or assembly.
// These are excluded during test builds and replaced by stubs.

// getPreemptOffsets accesses runtime.GetPreemptOffsets via linkname.
// This function is provided by the runtime overlay (runtime-patches/preempt.go)
// and computes offsets from the real runtime.g and runtime.m structs using
// unsafe.Offsetof, so they are guaranteed correct for the compiled Go version.
//
//go:linkname getPreemptOffsets runtime.GetPreemptOffsets
func getPreemptOffsets() preemptOffsetsType

// TimerIRQHandlerAsm is the pure assembly timer IRQ handler.
// Implemented in preempt_arm64.s. Does NOT call any Go functions.
// Sets g.preempt and g.stackguard0 directly for cooperative preemption.
func TimerIRQHandlerAsm()
