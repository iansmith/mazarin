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
// Implemented in wfi_arm64.s / wfi_amd64.s
func WaitForInterrupt()

// EnableIRQsAndWait halts the CPU until the next interrupt fires, then returns
// with IRQs masked — unconditionally, regardless of the caller's entry state.
//
// Each arch closes the unmask/halt race differently, and neither is a plain
// "enable then halt": x86_64 exploits the STI shadow, where the instruction
// after STI still executes masked, making STI+HLT atomic. ARM64 has no such
// shadow, so it inverts the order — WFI first, then unmask — relying on WFI
// waking for a pending interrupt even with PSTATE.I set. Unmasking first on
// ARM64 would drop a wake whose interrupt landed before the WFI (MAZ-169).
//
// Implemented in wfi_arm64.s / wfi_amd64.s
func EnableIRQsAndWait()
