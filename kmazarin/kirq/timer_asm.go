//go:build !test_stubs

package kirq

import (
	_ "unsafe" // Required for //go:linkname directives
)

// Forward declarations for functions provided via go:linkname or assembly.
// These are excluded during test builds and replaced by stubs.

// getAsyncPreemptAddr is provided by main package via go:linkname.
// Returns the address of runtime.asyncPreempt from RuntimeConfig,
// populated by Cardinal at boot time.
func getAsyncPreemptAddr() uintptr

// getReadyForAsyncPreemptAddr is provided by main package via go:linkname.
// Returns the address of the readyForAsyncPreempt flag in kmazarin,
// which controls whether timer IRQs should trigger async preemption.
func getReadyForAsyncPreemptAddr() uintptr

// processDeadlines is NO LONGER CALLED from timer IRQ context.
// Instead, the timer IRQ handler sets deadlinePending flag,
// and the deadline bottom half processor calls ProcessDeadlines
// in safe Go goroutine context.
//
// This linkname is kept for reference but is no longer used.
//
//go:linkname processDeadlines main.ProcessDeadlines
func processDeadlines()

// asm_rearmTimer is implemented in timer_arm64.s
func asm_rearmTimer(ticks uint64)

// asm_readCntfrqEl0 is implemented in timer_arm64.s
// Reads CNTFRQ_EL0 (Counter Frequency Register) and returns the timer frequency in Hz
func asm_readCntfrqEl0() uint32

// asm_readCntvctEl0 is implemented in timer_arm64.s
// Reads CNTVCT_EL0 (Counter Value Register) and returns the current counter value
func asm_readCntvctEl0() uint64
