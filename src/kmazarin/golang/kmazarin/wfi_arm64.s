
#include "textflag.h"

// OVERVIEW: CPU idle instruction for power-efficient waiting
//
// WaitForInterrupt executes the ARM64 WFI (Wait For Interrupt) instruction,
// putting the CPU into a low-power idle state until an interrupt arrives.
// This is used by the idle loop when all threads are blocked, reducing
// CPU usage while waiting for timer interrupts to process deadlines.
//
// The WFI instruction:
// - Puts the processor into a low-power standby state
// - Wakes immediately when an interrupt becomes pending
// - Is a hint instruction - the processor may also wake spuriously
//
// This is encoded as HINT #1 in Go assembler since WFI is not directly
// supported as an instruction mnemonic.

// func WaitForInterrupt()
TEXT ·WaitForInterrupt(SB), NOSPLIT|NOFRAME, $0-0
	// WFI - Wait For Interrupt
	// Encoded as HINT #1 in Go assembler
	HINT	$1
	RET
