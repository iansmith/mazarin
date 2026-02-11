//go:build linux && riscv64

// DIPLOMAT ENTRY POINT for RISC-V 64-bit
//
// This file REPLACES runtime/rt0_linux_riscv64.s via overlay system.
// Provides minimal Go runtime initialization and calls DiplomatEntry.
//
// Entry conditions:
//   - PC at 0x80000000 (firmware base, -bios none mode)
//   - M-mode (machine mode, no supervisor)
//   - No stack setup yet
//   - a0 (x10) = hart ID
//   - a1 (x11) = FDT pointer (device tree)
//
// Boot sequence:
//   1. Set up stack
//   2. Save FDT pointer
//   3. Initialize g0/m0 (minimal Go runtime)
//   4. Set up TLS via TP register
//   5. Call DiplomatEntry (Go function)

#include "textflag.h"

// _rt0_riscv64_linux is the entry point called by bootstrap stub
TEXT _rt0_riscv64_linux(SB),NOSPLIT|NOFRAME,$0
	// ========================================
	// Minimal RISC-V entry - skip MMU, just try Go runtime
	// ========================================

	// Write 'D' to prove entry
	// LUI X5, 0x10000
	WORD	$0x100002b7
	// ADDI X6, X0, 'D'
	WORD	$0x04400313
	// SB X6, 0(X5)
	WORD	$0x00628023

	// Save firmware params
	MOV	A0, S0		// Hart ID
	MOV	A1, S1		// FDT pointer

	// Write 'S' to prove we saved params
	// ADDI X6, X0, 'S'
	WORD	$0x05300313
	// SB X6, 0(X5)
	WORD	$0x00628023

	// Set up stack (use physical address 0x81210000 + 32KB)
	MOV	$0x81218000, SP

	// Write 'T' to prove stack set up
	// ADDI X6, X0, 'T'
	WORD	$0x05400313
	// SB X6, 0(X5)
	WORD	$0x00628023

	// Try jumping to runtime.rt0_go to let Go initialize itself
	MOV	$runtime·rt0_go(SB), T0
	JALR	ZERO, T0
