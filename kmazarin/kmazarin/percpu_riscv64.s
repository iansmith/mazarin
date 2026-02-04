#include "textflag.h"

// getCPUIDAsm returns the hart ID from the tp register.
// In S-mode, firmware stores hart ID in tp by convention.
//
// func getCPUIDAsm() uint64
TEXT ·getCPUIDAsm(SB), NOSPLIT|NOFRAME, $0-8
	MOV	TP, A0			// A0 = hart ID from thread pointer
	MOV	A0, ret+0(FP)
	RET

// getPerCPUPtrAsm returns pointer to current hart's PerCPU struct.
// Computes: &perCPUData[0] + (hartID * PerCPUSize)
//
// func getPerCPUPtrAsm() uintptr
TEXT ·getPerCPUPtrAsm(SB), NOSPLIT|NOFRAME, $0-8
	MOV	TP, A0				// A0 = hart ID

	// Load PerCPUSize
	MOV	·PerCPUSize(SB), A1		// A1 = size of one PerCPU struct

	// Compute offset: hartID * PerCPUSize
	MUL	A0, A1, A0			// A0 = hartID * PerCPUSize

	// Add base address
	MOV	$·perCPUData(SB), A1		// A1 = &perCPUData[0]
	ADD	A1, A0, A0			// A0 = base + offset

	MOV	A0, ret+0(FP)
	RET

// readMPIDRAsm is a compatibility function. Returns hart ID from tp.
//
// func readMPIDRAsm() uint64
TEXT ·readMPIDRAsm(SB), NOSPLIT|NOFRAME, $0-8
	MOV	TP, A0
	MOV	A0, ret+0(FP)
	RET
