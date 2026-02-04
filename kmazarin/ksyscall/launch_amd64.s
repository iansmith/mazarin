//go:build !test_stubs

#include "textflag.h"

// jumpToUserspace transitions to a new execution context on x86_64.
// Since we run everything in Ring 0 (no user/kernel split yet),
// this sets up an IRETQ frame to load the new RIP, RSP, and RFLAGS.
//
// func jumpToUserspace(entryPoint, stackPtr uint64)
TEXT ·jumpToUserspace(SB), NOSPLIT|NOFRAME, $0-16
	MOVQ	entryPoint+0(FP), AX	// AX = entry point
	MOVQ	stackPtr+8(FP), BX	// BX = stack pointer

	// Clear all GPRs for clean start
	XORQ	CX, CX
	XORQ	DX, DX
	XORQ	SI, SI
	XORQ	DI, DI
	XORQ	BP, BP
	XORQ	R8, R8
	XORQ	R9, R9
	XORQ	R10, R10
	XORQ	R11, R11
	XORQ	R12, R12
	XORQ	R13, R13
	XORQ	R14, R14
	XORQ	R15, R15

	// Build IRETQ frame on current stack
	// IRETQ pops: RIP, CS, RFLAGS, RSP, SS
	PUSHQ	$0			// SS (kernel data segment, or 0)
	PUSHQ	BX			// RSP = stack pointer
	PUSHFQ				// RFLAGS (current, will have IF set)
	POPQ	BX			// Read it back to set IF
	ORQ	$0x200, BX		// Ensure IF is set
	PUSHQ	BX			// Push modified RFLAGS
	PUSHQ	$0x08			// CS (kernel code segment)
	PUSHQ	AX			// RIP = entry point

	// Clear AX and BX (were holding entry/stack)
	XORQ	AX, AX
	XORQ	BX, BX

	IRETQ

	// Should never reach here
hang:
	HLT
	JMP	hang
