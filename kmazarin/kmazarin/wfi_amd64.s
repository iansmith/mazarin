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
// NOTE: no XMM backup is needed around the STI+HLT window. A nested
// interrupt's common_exception_entry now saves the interrupted XMM into its
// own per-exception-frame slot (framePtr-256) rather than a shared global
// (MAZ-139), so nested-interrupt XMM is protected automatically and there is
// nothing here to preserve.
//
// func EnableIRQsAndWait()
TEXT ·EnableIRQsAndWait(SB), NOSPLIT|NOFRAME, $0-0
	STI
	HLT
	CLI
	RET
