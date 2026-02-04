//go:build !test_stubs

#include "textflag.h"

// WaitForInterrupt executes the WFI instruction to wait for an interrupt.
// func WaitForInterrupt()
TEXT ·WaitForInterrupt(SB), NOSPLIT|NOFRAME, $0-0
	WORD	$0x10500073		// WFI
	RET
