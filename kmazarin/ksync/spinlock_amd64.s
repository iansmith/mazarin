// spinlock_amd64.s - the kernel's ONE x86_64 IRQ save/restore + spin-yield
// implementation (MAZ-167). Mirrors spinlock_arm64.s; see the mask-width note
// there. x86 has no encoding hazard (PUSHFQ/CLI/POPFQ are real mnemonics), but
// the single-definition rule applies the same.

#include "textflag.h"

// SaveAndDisableIRQs saves RFLAGS, disables interrupts (CLI), returns RFLAGS.
//
// func SaveAndDisableIRQs() uint64
TEXT ·SaveAndDisableIRQs(SB), NOSPLIT, $0-8
	PUSHFQ
	POPQ	AX
	CLI
	MOVQ	AX, ret+0(FP)
	RET

// RestoreIRQs restores RFLAGS from saved (re-enabling IRQs if they were on).
//
// func RestoreIRQs(saved uint64)
TEXT ·RestoreIRQs(SB), NOSPLIT, $0-8
	MOVQ	saved+0(FP), AX
	PUSHQ	AX
	POPFQ
	RET

// yieldProcessor executes PAUSE to relax a spin-wait: reduces power and lets
// the sibling hyperthread use more resources. The x86 equivalent of WFE.
//
// func yieldProcessor()
TEXT ·yieldProcessor(SB), NOSPLIT, $0
	BYTE $0xF3; BYTE $0x90  // PAUSE instruction
	RET
