#include "textflag.h"

// lib_getters.s - Simple getter functions that DON'T reference external symbols
//
// NOTE: Functions that reference Go runtime symbols (runtime.g0, runtime.m0,
// runtime.physPageSize, etc.) have been moved to linker_symbols.s (GCC assembly)
// because goasm2gnu cannot properly handle external symbol relocations.

// getGRegister returns current value of g register
// This is for debugging to see what g points to
// Returns: current value of g (R28) in R0 (Go register ABI)
TEXT getGRegister(SB), NOSPLIT|NOFRAME, $0-8
	MOVD g, R0
	// Return value already in R0 for Go register ABI
	RET

// call_mallocinit calls runtime.mallocinit from assembly
// Minimal assembly wrapper that just calls mallocinit (not full schedinit)
// physPageSize should be set from Go before calling this
TEXT call_mallocinit(SB), NOSPLIT, $0-0
	// Save frame pointer and link register
	MOVD R29, -16(RSP)
	MOVD R30, -8(RSP)
	SUB $16, RSP
	MOVD RSP, R29

	// Call runtime.mallocinit()
	// This initializes just the heap allocator, not full scheduler
	CALL runtime·mallocinit(SB)

	// Restore frame pointer and link register
	MOVD 0(RSP), R29
	MOVD 8(RSP), R30
	ADD $16, RSP
	RET
