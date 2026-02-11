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
	// RISC-V entry for diplomat - minimal g0/m0 setup, no runtime
	// Same approach as ARM64/x86_64 UEFI entries
	// ========================================

	// Write 'D' to prove entry
	// LUI X5, 0x10000 (X5 = UART base)
	WORD	$0x100002b7
	// ADDI X6, X0, 'D'
	WORD	$0x04400313
	// SB X6, 0(X5)
	WORD	$0x00628023

	// Save firmware params
	MOV	A0, S0		// Hart ID
	MOV	A1, S1		// FDT pointer

	// Write '1' after saving params
	WORD	$0x03100313	// ADDI X6, X0, '1'
	WORD	$0x00628023	// SB X6, 0(X5)

	// Set up stack using LUI+ADDI (0x81218000)
	// LUI SP, 0x81218
	WORD	$0x81218137
	// No ADDI needed since lower 12 bits are 0

	// Write '2' after stack setup
	WORD	$0x03200313	// ADDI X6, X0, '2'
	WORD	$0x00628023	// SB X6, 0(X5)

	// ========================================
	// Initialize g0/m0 (minimal Go runtime)
	// ========================================

	// Write '3' before loading g0
	WORD	$0x03300313	// ADDI X6, X0, '3'
	WORD	$0x00628023	// SB X6, 0(X5)

	// Set g register (X27) to point to runtime.g0
	MOV	$runtime·g0(SB), g

	// Write '4' after loading g0
	WORD	$0x03400313	// ADDI X6, X0, '4'
	WORD	$0x00628023	// SB X6, 0(X5)

	// Set up stack guards for g0
	// Use X28/X29 (T3/T4) to avoid conflict with X5 (UART base)
	// Copy SP to X28 and X29 using ADDI instructions
	WORD	$0x00010e13	// ADDI X28, SP, 0 (x28 = x2 + 0)
	WORD	$0x00010e93	// ADDI X29, SP, 0 (x29 = x2 + 0)
	// Load 64KB into X30 and subtract from X29
	WORD	$0x00010f37	// LUI X30, 0x10 (x30 = 0x10000)
	WORD	$0x41ee8eb3	// SUB X29, X29, X30 (x29 = x29 - x30)

	// Write '5' after calculating guards
	WORD	$0x03500313	// ADDI X6, X0, '5'
	WORD	$0x00628023	// SB X6, 0(X5)

	// g.stackguard0 and g.stackguard1 (offsets 16, 24)
	// X29 contains stack guard (SP - 64KB), X28 contains SP
	MOV	X29, 16(g)		// g0.stackguard0
	MOV	X29, 24(g)		// g0.stackguard1

	// g.stack.lo and g.stack.hi (offsets 0, 8)
	MOV	X29, 0(g)		// g0.stack.lo
	MOV	X28, 8(g)		// g0.stack.hi

	// Write '6' after setting stack guards
	WORD	$0x03600313	// ADDI X6, X0, '6'
	WORD	$0x00628023	// SB X6, 0(X5)

	// Link g0 and m0 (use X30 as temp)
	MOV	$runtime·m0(SB), X30
	MOV	X30, 48(g)		// g0.m = &m0 (offset 48)
	MOV	g, (X30)		// m0.g0 = &g0 (offset 0)

	// Write '7' after linking g0/m0
	WORD	$0x03700313	// ADDI X6, X0, '7'
	WORD	$0x00628023	// SB X6, 0(X5)

	// ========================================
	// Set up TLS (TP register)
	// ========================================
	// Use X30 for TLS setup (doesn't conflict with UART at X5)
	MOV	$main·tlsBlock(SB), X30
	MOV	g, (X30)		// Store g0 at tlsBlock[0]
	ADD	$8, X30			// X30 = tlsBlock + 8
	// Set TP (X4) = X30 using ADDI instruction
	WORD	$0x000f0213	// ADDI X4, X30, 0 (TP = X30)

	// Write 'T' to prove TLS set up
	WORD	$0x05400313	// ADDI X6, X0, 'T'
	WORD	$0x00628023	// SB X6, 0(X5)

	// ========================================
	// Call DiplomatEntry (skip rt0_go!)
	// ========================================
	// Write 'C' before call
	WORD	$0x04300313	// ADDI X6, X0, 'C'
	WORD	$0x00628023	// SB X6, 0(X5)

	MOV	$main·DiplomatEntry(SB), X30
	JALR	ZERO, X30

	// Should never return
	WORD	$0xffdff06f		// JAL back to self
