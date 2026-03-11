#include "textflag.h"

// WaitForInterrupt executes the WFI instruction to wait for an interrupt.
// func WaitForInterrupt()
TEXT ·WaitForInterrupt(SB), NOSPLIT|NOFRAME, $0-0
	WORD	$0x10500073		// WFI
	RET

// EnableIRQsAndWait enables S-mode interrupts and halts until interrupt.
// CSRSI sstatus, 2 sets the SIE bit, then WFI halts. After the interrupt
// handler returns, CSRCI sstatus, 2 re-disables interrupts.
// func EnableIRQsAndWait()
TEXT ·EnableIRQsAndWait(SB), NOSPLIT|NOFRAME, $0-0
	WORD	$0x10016073		// CSRSI sstatus, 2 (set SIE)
	WORD	$0x10500073		// WFI
	WORD	$0x10017073		// CSRCI sstatus, 2 (clear SIE)
	RET
