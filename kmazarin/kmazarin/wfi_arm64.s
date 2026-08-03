
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

// EnableIRQsAndWait enables IRQs and halts until the next interrupt.
// MSR DAIFClr, #2 clears the I bit to enable IRQs, then WFI halts.
// Exception return restores the caller's original DAIF state.
// func EnableIRQsAndWait()
TEXT ·EnableIRQsAndWait(SB), NOSPLIT|NOFRAME, $0-0
	MSR	$2, DAIFClr	// enable IRQs
	ISB	$15
	// WFI
	HINT	$1
	// DMB OSH — data memory barrier (outer shareable) after WFI wake.
	// Under HVF, the VirtIO backend runs on a separate host thread. Its DMA
	// writes (used ring) may not be visible to this vCPU without a barrier
	// after the interrupt handler returns via ERET. ERET is a context sync
	// event (ISB-like) but does NOT imply a data barrier. This DMB ensures
	// the device's DMA writes are ordered before any subsequent loads
	// (e.g., reading the used ring in DoBlockIOComplete).
	// On TCG this is harmless (single-threaded, trivially consistent).
	WORD	$0xD50333BF
	MSR	$2, DAIFSet	// re-disable IRQs
	RET
