//go:build !test_stubs

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
// Implemented in wfi_arm64.s / wfi_amd64.s / wfi_riscv64.s
func WaitForInterrupt()

// EnableIRQsAndWait atomically enables interrupts and halts the CPU until
// the next interrupt fires. On x86_64 this exploits the STI shadow (the
// instruction after STI executes with IRQs still masked) to make STI+HLT
// an atomic pair — the first interrupt that can fire does so during HLT.
// On ARM64/RISC-V, enables IRQs then executes WFI.
// After the interrupt handler returns, IRQs are disabled again (CLI on
// AMD64; ARM64/RISC-V exception return restores the pre-WFI IRQ state).
//
// Implemented in wfi_arm64.s / wfi_amd64.s / wfi_riscv64.s
func EnableIRQsAndWait()
