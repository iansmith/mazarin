#include "textflag.h"

// lib_getters.s - Simple getter functions for runtime symbols
// These allow Go code to access runtime symbols without hardcoding addresses

// get_g0_addr returns the address of runtime.g0
// Returns: address of runtime.g0
TEXT get_g0_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD $runtime·g0(SB), R0
	MOVD R0, ret+0(FP)
	RET

// get_m0_addr returns the address of runtime.m0
// Returns: address of runtime.m0
TEXT get_m0_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD $runtime·m0(SB), R0
	MOVD R0, ret+0(FP)
	RET

// getGRegister returns current value of g register
// This is for debugging to see what g points to
// Returns: current value of g (R28)
TEXT getGRegister(SB), NOSPLIT|NOFRAME, $0-8
	MOVD g, R0
	MOVD R0, ret+0(FP)
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

// get_phys_page_size_addr returns the address of runtime.physPageSize
// This allows Go code to set physPageSize before calling schedinit
// Returns: address of runtime.physPageSize
TEXT get_phys_page_size_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD $runtime·physPageSize(SB), R0
	MOVD R0, ret+0(FP)
	RET

// get_mcache0_addr returns the address of runtime.mcache0
// Returns: address of runtime.mcache0
TEXT get_mcache0_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD $runtime·mcache0(SB), R0
	MOVD R0, ret+0(FP)
	RET

// get_emptymspan_addr returns the address of runtime.emptymspan
// Returns: address of runtime.emptymspan
TEXT get_emptymspan_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD $runtime·emptymspan(SB), R0
	MOVD R0, ret+0(FP)
	RET
