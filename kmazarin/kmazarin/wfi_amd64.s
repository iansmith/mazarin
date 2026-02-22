//go:build !test_stubs

#include "textflag.h"

// WaitForInterrupt executes the HLT instruction to wait for an interrupt.
// func WaitForInterrupt()
TEXT ·WaitForInterrupt(SB), NOSPLIT|NOFRAME, $0-0
	HLT
	RET

// EnableIRQsAndWait atomically enables interrupts and halts until interrupt.
// STI has a one-instruction shadow: the next instruction (HLT) executes with
// IRQs still masked. The first pending interrupt fires during HLT, waking the
// CPU. After the interrupt handler returns, CLI re-disables interrupts.
//
// NOTE: xmmSaveArea is preserved around the STI+HLT window. A nested
// interrupt's common_exception_entry would overwrite the global XMM save,
// corrupting the outer SYSCALL exception_return's XMM restore.
//
// func EnableIRQsAndWait()
TEXT ·EnableIRQsAndWait(SB), NOSPLIT|NOFRAME, $0-0
	// Save xmmSaveArea (256 bytes) to stack before enabling interrupts.
	SUBQ	$256, SP
	MOVOU	·xmmSaveArea+0(SB), X0;   MOVOU X0, 0(SP)
	MOVOU	·xmmSaveArea+16(SB), X0;  MOVOU X0, 16(SP)
	MOVOU	·xmmSaveArea+32(SB), X0;  MOVOU X0, 32(SP)
	MOVOU	·xmmSaveArea+48(SB), X0;  MOVOU X0, 48(SP)
	MOVOU	·xmmSaveArea+64(SB), X0;  MOVOU X0, 64(SP)
	MOVOU	·xmmSaveArea+80(SB), X0;  MOVOU X0, 80(SP)
	MOVOU	·xmmSaveArea+96(SB), X0;  MOVOU X0, 96(SP)
	MOVOU	·xmmSaveArea+112(SB), X0; MOVOU X0, 112(SP)
	MOVOU	·xmmSaveArea+128(SB), X0; MOVOU X0, 128(SP)
	MOVOU	·xmmSaveArea+144(SB), X0; MOVOU X0, 144(SP)
	MOVOU	·xmmSaveArea+160(SB), X0; MOVOU X0, 160(SP)
	MOVOU	·xmmSaveArea+176(SB), X0; MOVOU X0, 176(SP)
	MOVOU	·xmmSaveArea+192(SB), X0; MOVOU X0, 192(SP)
	MOVOU	·xmmSaveArea+208(SB), X0; MOVOU X0, 208(SP)
	MOVOU	·xmmSaveArea+224(SB), X0; MOVOU X0, 224(SP)
	MOVOU	·xmmSaveArea+240(SB), X0; MOVOU X0, 240(SP)

	STI
	HLT
	CLI

	// Restore xmmSaveArea from stack (undo nested interrupt's overwrite).
	MOVOU	0(SP), X0;   MOVOU X0, ·xmmSaveArea+0(SB)
	MOVOU	16(SP), X0;  MOVOU X0, ·xmmSaveArea+16(SB)
	MOVOU	32(SP), X0;  MOVOU X0, ·xmmSaveArea+32(SB)
	MOVOU	48(SP), X0;  MOVOU X0, ·xmmSaveArea+48(SB)
	MOVOU	64(SP), X0;  MOVOU X0, ·xmmSaveArea+64(SB)
	MOVOU	80(SP), X0;  MOVOU X0, ·xmmSaveArea+80(SB)
	MOVOU	96(SP), X0;  MOVOU X0, ·xmmSaveArea+96(SB)
	MOVOU	112(SP), X0; MOVOU X0, ·xmmSaveArea+112(SB)
	MOVOU	128(SP), X0; MOVOU X0, ·xmmSaveArea+128(SB)
	MOVOU	144(SP), X0; MOVOU X0, ·xmmSaveArea+144(SB)
	MOVOU	160(SP), X0; MOVOU X0, ·xmmSaveArea+160(SB)
	MOVOU	176(SP), X0; MOVOU X0, ·xmmSaveArea+176(SB)
	MOVOU	192(SP), X0; MOVOU X0, ·xmmSaveArea+192(SB)
	MOVOU	208(SP), X0; MOVOU X0, ·xmmSaveArea+208(SB)
	MOVOU	224(SP), X0; MOVOU X0, ·xmmSaveArea+224(SB)
	MOVOU	240(SP), X0; MOVOU X0, ·xmmSaveArea+240(SB)
	ADDQ	$256, SP

	RET
