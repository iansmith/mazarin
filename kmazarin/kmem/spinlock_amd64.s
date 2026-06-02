// spinlock_amd64.s - x86_64 assembly for spinlock support

#include "textflag.h"

// yieldProcessor executes PAUSE instruction.
// This hints to the CPU that we're in a spin-wait loop, improving
// performance on hyperthreaded cores by reducing power consumption
// and allowing the sibling logical processor to use more resources.
//
// PAUSE is the x86_64 equivalent of ARM64's WFE instruction.
//
// func yieldProcessor()
TEXT ·yieldProcessor(SB), NOSPLIT, $0
	BYTE $0xF3; BYTE $0x90  // PAUSE instruction
	RET

// saveAndDisableIRQsLocal saves RFLAGS, disables interrupts (CLI), returns RFLAGS.
// kmem-local copy of the kernel's SaveAndDisableIRQs (kmem cannot import main).
//
// func saveAndDisableIRQsLocal() uint64
TEXT ·saveAndDisableIRQsLocal(SB), NOSPLIT, $0-8
	PUSHFQ
	POPQ	AX
	CLI
	MOVQ	AX, ret+0(FP)
	RET

// restoreIRQsLocal restores RFLAGS from saved (re-enabling IRQs if they were on).
//
// func restoreIRQsLocal(saved uint64)
TEXT ·restoreIRQsLocal(SB), NOSPLIT, $0-8
	MOVQ	saved+0(FP), AX
	PUSHQ	AX
	POPFQ
	RET
