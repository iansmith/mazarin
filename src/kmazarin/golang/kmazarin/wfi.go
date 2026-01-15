//go:build qemuvirt && aarch64

package main

// WaitForInterrupt puts the CPU into a low-power idle state until
// an interrupt arrives. Used by the idle loop when all threads are blocked.
//
// This is a thin wrapper around the ARM64 WFI instruction.
// The processor will wake when:
// - A timer interrupt fires (for deadline processing)
// - Any other interrupt becomes pending
// - Spuriously (WFI is a hint, not a guarantee)
//
// Implemented in wfi_arm64.s
func WaitForInterrupt()
