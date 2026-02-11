//go:build linux && riscv64

// DIPLOMAT TRAMPOLINE: Minimal stub at firmware jump address (0x80200000)
//
// OpenSBI jumps to image_low_addr (first PT_LOAD segment address).
// This trampoline uses ONLY position-independent code with no external references.
//
// This file REPLACES runtime/rt0_linux_riscv64.s via overlay system.
// The symbol name _rt0_riscv64_linux ensures this is placed first in .text.

#include "textflag.h"

// _rt0_riscv64_linux is the stub that OpenSBI jumps to.
// NOSPLIT prevents stack checks. NOFRAME means no frame pointer setup.
TEXT _rt0_riscv64_linux(SB),NOSPLIT|NOFRAME,$0
	// Write '!' to UART in a loop using LUI/ADDI for large immediate
	// UART base: 0x10000000

	// LUI t0, 0x10000 (load upper immediate)
	WORD	$0x100002b7

	// ADDI t1, zero, '!' (0x21)
	WORD	$0x02100313

spin:
	// SB t1, 0(t0) (store byte to UART)
	WORD	$0x00628023

	// JAL zero, spin (jump back)
	WORD	$0xffdff06f
