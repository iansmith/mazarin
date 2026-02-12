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

	// Set up S-mode trap handler FIRST (before anything that might trap)
	// OpenSBI jumps to us in S-mode, so use stvec instead of mtvec
	MOV	$runtime·riscvTrapHandler(SB), X30
	WORD	$0x105F1073		// csrw stvec, X30 (0x105 = stvec)

	// Write 'D' to prove entry
	// LUI X5, 0x10000 (X5 = UART base)
	WORD	$0x100002b7
	// ADDI X6, X0, 'D'
	WORD	$0x04400313
	// SB X6, 0(X5)
	WORD	$0x00628023

	// DEBUG: Print S1 value right at entry to verify bootstrap preserved it
	// Print format: 'S' then 4 hex nibbles
	WORD	$0x05300313	// ADDI X6, X0, 'S'
	WORD	$0x00628023	// SB X6, 0(X5)
	SRL	$60, S1, X6
	AND	$0xF, X6
	ADD	$0x30, X6
	WORD	$0x00628023	// SB X6, 0(X5)
	SRL	$56, S1, X6
	AND	$0xF, X6
	ADD	$0x30, X6
	WORD	$0x00628023	// SB X6, 0(X5)
	SRL	$52, S1, X6
	AND	$0xF, X6
	ADD	$0x30, X6
	WORD	$0x00628023	// SB X6, 0(X5)
	SRL	$48, S1, X6
	AND	$0xF, X6
	ADD	$0x30, X6
	WORD	$0x00628023	// SB X6, 0(X5)

	// Firmware params (A0=hart ID, A1=FDT pointer) were saved to S0/S1
	// by the bootstrap stub (inject.go). Store them in bootStack for Go access.
	// bootStack[0] = hart ID, bootStack[8] = FDT pointer
	MOV	$main·bootStack(SB), X30
	SD	S0, 0(X30)	// bootStack[0] = S0 (hart ID from A0)
	SD	S1, 8(X30)	// bootStack[8] = S1 (FDT pointer from A1)

	// Write '1' after saving params
	WORD	$0x03100313	// ADDI X6, X0, '1'
	WORD	$0x00628023	// SB X6, 0(X5)

	// Set up stack at 0x81218000
	// IMPORTANT: LUI sign-extends on RV64! 0x81218000 has bit 31 set,
	// so LUI would produce 0xFFFFFFFF81218000. Must use Go MOV instead.
	MOV	$0x81218000, SP

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

	// Set up stack bounds for g0
	// Stack is 32KB starting at SP, grows down
	// Calculate stack.lo = SP - 32KB
	MOV	SP, X28			// X28 = stack.hi (current SP)
	MOV	$0x8000, X30		// X30 = 32KB
	SUB	X30, X28, X29		// X29 = stack.lo = SP - 32KB

	// Write '5' after calculating stack bounds
	WORD	$0x03500313	// ADDI X6, X0, '5'
	WORD	$0x00628023	// SB X6, 0(X5)

	// g.stack.lo and g.stack.hi (offsets 0, 8)
	MOV	X29, 0(g)		// g0.stack.lo = SP - 32KB
	MOV	X28, 8(g)		// g0.stack.hi = SP

	// Update stackguard after setting stack bounds (like rt0_go does)
	// stackguard = stack.lo + stackGuard (928 bytes, use 1024 for safety)
	MOV	0(g), X30		// Load stack.lo
	ADD	$1024, X30		// Add stackGuard constant
	MOV	X30, 16(g)		// g0.stackguard0 = stack.lo + 1024
	MOV	X30, 24(g)		// g0.stackguard1 = stack.lo + 1024

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

	MOV	$main·diplomatEntryWrapper(SB), X30

	// Write 'J' before JALR
	WORD	$0x04a00313	// ADDI X6, X0, 'J'
	WORD	$0x00628023	// SB X6, 0(X5)

	JALR	ZERO, X30

	// Write 'R' if we return (should never happen)
	WORD	$0x05200313	// ADDI X6, X0, 'R'
	WORD	$0x00628023	// SB X6, 0(X5)

	// Infinite loop
	WORD	$0xffdff06f		// JAL back to self

// riscvTrapHandler is an S-mode trap handler that prints diagnostic info to UART.
// Output format: !<scause>:<sepc_hex>
// Hex nibbles use '0'-'9' for 0-9 and ':'-'?' for A-F (add 0x30 to nibble value).
TEXT runtime·riscvTrapHandler(SB), NOSPLIT|NOFRAME, $0
	// Load UART base into X5
	WORD	$0x100002b7		// LUI X5, 0x10000

	// Print '!' to indicate trap occurred
	WORD	$0x02100313		// ADDI X6, X0, '!'
	WORD	$0x00628023		// SB X6, 0(X5)

	// Read scause into X6 (0x142 = scause)
	WORD	$0x14202373		// csrrs X6, scause, X0

	// Print scause low byte as character (0='0', 1='1', ..., 15='?')
	AND	$0xFF, X6
	ADD	$0x30, X6
	WORD	$0x00628023		// SB X6, 0(X5)

	// Print separator ':'
	WORD	$0x03a00313		// ADDI X6, X0, ':'
	WORD	$0x00628023		// SB X6, 0(X5)

	// Read sepc into X6 (0x141 = sepc)
	WORD	$0x14102373		// csrrs X6, sepc, X0

	// Print mepc as 8 hex nibbles (32 bits, enough for our address space)
	// Nibble [31:28]
	SRL	$28, X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)
	// Nibble [27:24]
	SRL	$24, X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)
	// Nibble [23:20]
	SRL	$20, X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)
	// Nibble [19:16]
	SRL	$16, X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)
	// Nibble [15:12]
	SRL	$12, X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)
	// Nibble [11:8]
	SRL	$8, X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)
	// Nibble [7:4]
	SRL	$4, X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)
	// Nibble [3:0]
	MOV	X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)

	// Print separator ','
	WORD	$0x02c00313		// ADDI X6, X0, ','
	WORD	$0x00628023		// SB X6, 0(X5)

	// Read stval into X6 (0x143 = stval)
	WORD	$0x14302373		// csrrs X6, stval, X0

	// Print mtval as 8 hex nibbles
	// Nibble [31:28]
	SRL	$28, X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)
	// Nibble [27:24]
	SRL	$24, X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)
	// Nibble [23:20]
	SRL	$20, X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)
	// Nibble [19:16]
	SRL	$16, X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)
	// Nibble [15:12]
	SRL	$12, X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)
	// Nibble [11:8]
	SRL	$8, X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)
	// Nibble [7:4]
	SRL	$4, X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)
	// Nibble [3:0]
	MOV	X6, X7
	AND	$0xF, X7
	ADD	$0x30, X7
	WORD	$0x00728023		// SB X7, 0(X5)

	// Infinite loop (halt after printing diagnostics)
	WORD	$0xffdff06f		// JAL back to self
