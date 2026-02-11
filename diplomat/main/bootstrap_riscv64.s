//go:build riscv64

// Pure assembly bootstrap for RISC-V diplomat
// This file is compiled directly (not via overlay) and provides the true entry point.
// Uses raw WORD directives to ensure exact instruction encoding.

#include "textflag.h"

// _diplomatBootstrap is the true entry point for RISC-V diplomat.
// Loaded at 0x80000000 by QEMU with -bios none.
// Using underscore prefix like AMD64 to prevent dead code elimination.
TEXT ·_diplomatBootstrap(SB),NOSPLIT|NOFRAME,$0
	// Write '!' to UART in a loop
	// UART base: 0x10000000

	// LUI t0, 0x10000 (t0 = 0x10000000)
	WORD $0x100002b7

	// ADDI t1, zero, '!' (t1 = 0x21)
	WORD $0x02100313

loop:
	// SB t1, 0(t0) (write byte to UART)
	WORD $0x00628023

	// JAL zero, loop (jump back)
	WORD $0xffdff06f
