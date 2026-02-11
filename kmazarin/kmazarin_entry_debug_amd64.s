// kmazarin entry point debug breadcrumb overlay for x86_64
// This replaces the first instruction of _rt0_amd64 with debug output

#include "textflag.h"

// TEXT _rt0_amd64(SB),NOSPLIT,$0
//
// Original code:
//   mov rdi, qword ptr [rsp]  // 48 8b 3c 24
//
// Replace with debug output then original instruction
TEXT _rt0_amd64(SB),NOSPLIT,$0
	// Output 'M' to debug port to prove we reached kmazarin
	BYTE $0xB0; BYTE $'M'          // MOV AL, 'M'
	BYTE $0xBA; WORD $0xE9; WORD $0x00  // MOV DX, 0xE9
	BYTE $0xEE                     // OUT DX, AL

	// Now do the original first instruction
	MOVQ	0(SP), DI              // mov rdi, [rsp]
	LEAQ	8(SP), SI              // lea rsi, [rsp+8] (second instruction)
	JMP	runtime·rt0_go(SB)     // jump to rt0_go (third instruction)
