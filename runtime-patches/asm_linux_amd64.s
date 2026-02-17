// KMAZARIN OVERLAY: Replace SYSCALL with INT $0x80 for bare-metal environment
//
// This file replaces internal/runtime/syscall/asm_linux_amd64.s
// The SYSCALL instruction requires MSR setup (STAR, LSTAR, SFMASK) which
// kmazarin hasn't configured. INT $0x80 is caught by kmazarin's IDT handler
// (vector 128) and dispatched to the syscall handler.
//
// The register convention is preserved from the original:
// - ABIInternal args: AX=num, BX=a1, CX=a2, DI=a3, SI=a4, R8=a5, R9=a6
// - Linux syscall args: AX=num, DI=a1, SI=a2, DX=a3, R10=a4, R8=a5, R9=a6

#include "textflag.h"

// func Syscall6(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr)
//
// We need to convert to the syscall ABI.
//
// arg | ABIInternal | Syscall
// ---------------------------
// num | AX          | AX
// a1  | BX          | DI
// a2  | CX          | SI
// a3  | DI          | DX
// a4  | SI          | R10
// a5  | R8          | R8
// a6  | R9          | R9
//
// r1  | AX          | AX
// r2  | BX          | DX
// err | CX          | part of AX
//
// Note that this differs from "standard" ABI convention, which would pass 4th
// arg in CX, not R10.
TEXT ·Syscall6<ABIInternal>(SB),NOSPLIT,$0
	// DEBUG: breadcrumb 'S' + 2-digit hex syscall number
	PUSHQ	AX
	PUSHQ	DX
	PUSHQ	CX
	MOVW	$0x3F8, DX
	MOVB	$'S', AX
	OUTB
	// Print syscall number as 2 hex digits from the saved AX on stack
	MOVQ	16(SP), CX		// original AX (syscall num)
	MOVQ	CX, AX
	SHRQ	$4, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	sn_d1
	ADDQ	$('A'-'0'-10), AX
sn_d1:
	OUTB
	MOVQ	CX, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	sn_d2
	ADDQ	$('A'-'0'-10), AX
sn_d2:
	OUTB
	MOVB	$' ', AX
	OUTB
	POPQ	CX
	POPQ	DX
	POPQ	AX
	// a6 already in R9.
	// a5 already in R8.
	MOVQ	SI, R10 // a4
	MOVQ	DI, DX  // a3
	MOVQ	CX, SI  // a2
	MOVQ	BX, DI  // a1
	// num already in AX.
	INT	$0x80
	CMPQ	AX, $0xfffffffffffff001
	JLS	ok
	NEGQ	AX
	MOVQ	AX, CX  // errno
	MOVQ	$-1, AX // r1
	MOVQ	$0, BX  // r2
	RET
ok:
	// r1 already in AX.
	MOVQ	DX, BX // r2
	MOVQ	$0, CX // errno
	RET
