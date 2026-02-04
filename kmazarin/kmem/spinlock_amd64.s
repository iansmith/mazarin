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
