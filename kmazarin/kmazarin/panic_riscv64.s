//go:build !test_stubs

#include "textflag.h"

// Exit terminates the system via SBI shutdown.
// Falls back to WFI loop if SBI is not available.
TEXT ·Exit(SB), NOSPLIT, $0-0
	// Disable interrupts: clear sstatus.SIE
	// CSRCI sstatus, 2
	WORD	$0x10017073

	// SBI shutdown (legacy)
	// A7 = extension ID 0x08 (legacy shutdown)
	// A6 = 0
	MOV	$0, A6
	MOV	$0x08, A7
	WORD	$0x00000073		// ECALL

	// SBI SRST (system reset) if legacy fails
	// A7 = 0x53525354 (SRST extension)
	// A6 = 0 (system_reset)
	// A0 = 0 (shutdown)
	// A1 = 0 (no reason)
	MOV	$0, A0
	MOV	$0, A1
	MOV	$0, A6
	MOV	$0x53525354, A7
	WORD	$0x00000073		// ECALL

	// Fallback: WFI loop
halt:
	WORD	$0x10500073		// WFI
	JMP	halt
