#include "textflag.h"

// jumpToUserspace transitions to a new execution context on RISC-V.
// Sets sepc = entry point, sp = stack, sstatus with SPIE set, then SRET.
//
// func jumpToUserspace(entryPoint, stackPtr uint64)
TEXT ·jumpToUserspace(SB), NOSPLIT|NOFRAME, $0-16
	MOV	entryPoint+0(FP), A0	// A0 = entry point
	MOV	stackPtr+8(FP), A1	// A1 = stack pointer

	// Set sepc = entry point (SRET will jump here)
	// CSRW sepc, a0
	WORD	$0x14151073		// csrw sepc, a0

	// Set sstatus: SPIE=1 (enable interrupts on SRET), SPP=0
	MOV	$0x20, A2		// SPIE = bit 5
	// CSRW sstatus, a2
	WORD	$0x10061073		// csrw sstatus, a2

	// Set stack pointer
	MOV	A1, X2			// sp = stackPtr

	// Clear all registers for clean start
	MOV	$0, A0
	MOV	$0, A1
	MOV	$0, A2
	MOV	$0, A3
	MOV	$0, A4
	MOV	$0, A5
	MOV	$0, A6
	MOV	$0, A7
	MOV	$0, T0
	MOV	$0, T1
	MOV	$0, T2
	MOV	$0, T3
	MOV	$0, T4
	MOV	$0, T5
	MOV	$0, T6
	MOV	$0, S0
	MOV	$0, S1
	MOV	$0, S2
	MOV	$0, S3
	MOV	$0, S4
	MOV	$0, S5
	MOV	$0, S6
	MOV	$0, S7
	MOV	$0, S8
	MOV	$0, S9
	MOV	$0, S10

	// SFENCE.VMA before SRET
	WORD	$0x12000073		// sfence.vma zero,zero

	// SRET - return from trap (jumps to sepc, loads sstatus)
	WORD	$0x10200073		// SRET

	// Should never reach here
hang:
	WORD	$0x10500073		// WFI
	JMP	hang
