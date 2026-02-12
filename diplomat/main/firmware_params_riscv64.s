//go:build riscv64

// diplomat/main/firmware_params_riscv64.s - Read firmware parameters from S0/S1
//
// The bootstrap stub (inject.go) saves A0/A1 to S0/S1 before jumping.
// These functions allow Go code to read those saved values.

#include "textflag.h"

// readBootHartID returns the hart ID saved in S0 by the bootstrap stub
TEXT ·readBootHartID(SB), NOSPLIT|NOFRAME, $0-8
	MOV	S0, X10		// X10 = S0 (return value in A0)
	MOV	X10, ret+0(FP)	// Store to return slot
	RET

// readFDTPointer returns the FDT pointer saved in S1 by the bootstrap stub
TEXT ·readFDTPointer(SB), NOSPLIT|NOFRAME, $0-8
	MOV	S1, X10		// X10 = S1 (return value in A0)
	MOV	X10, ret+0(FP)	// Store to return slot
	RET
