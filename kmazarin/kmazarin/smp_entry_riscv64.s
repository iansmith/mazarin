#include "textflag.h"

// sbiHartStartAsm calls SBI HSM hart_start to wake a secondary hart.
//
// SBI calling convention:
//   A7 = extension ID (0x48534D = "HSM")
//   A6 = function ID (0 = hart_start)
//   A0 = hart ID to start
//   A1 = start address (where hart begins executing in S-mode)
//   A2 = opaque value (passed to hart in A1)
// Returns: A0 = SBI error code (0 = success)
//
// func sbiHartStartAsm(hartID uint64, startAddr uintptr, opaque uint64) int64
TEXT ·sbiHartStartAsm(SB), NOSPLIT|NOFRAME, $0-32
	MOV	hartID+0(FP), A0	// A0 = hart ID
	MOV	startAddr+8(FP), A1	// A1 = start address
	MOV	opaque+16(FP), A2	// A2 = opaque value
	MOV	$0, A6			// A6 = function ID 0 (hart_start)
	MOV	$0x48534D, A7		// A7 = extension ID "HSM"
	WORD	$0x00000073		// ECALL
	MOV	A0, ret+24(FP)		// Return SBI error code
	RET

// secondaryCPUEntry is the entry point for secondary harts on RISC-V.
//
// Harts arrive here in S-mode after SBI hart_start:
//   A0 = hart ID (same as hartID passed to SBI)
//   A1 = opaque value (same as opaque passed to SBI)
//
// Steps:
// 1. Save hart ID to tp register (S-mode convention)
// 2. Compute perCPU pointer: &perCPUData + hartID * PerCPUSize
// 3. Load G0StackTop (offset 32) -> set sp
// 4. Load ExceptionStackTop (offset 48) -> set sscratch CSR
// 5. TODO: Set stvec when RISC-V exception vectors are implemented
// 6. Store 1 to Online (offset 64) with release fence
// 7. Enable interrupts: set sstatus.SIE
// 8. WFI loop
//
// func secondaryCPUEntry()
TEXT ·secondaryCPUEntry(SB), NOSPLIT|NOFRAME, $0
	// Step 1: Save hart ID to tp register
	// A0 contains hart ID from SBI
	MOV	A0, TP			// tp = hart ID (S-mode convention)
	MOV	A0, S1			// S1 = hart ID (preserved across steps)

	// Step 2: Compute per-CPU data address
	MOV	$·perCPUData(SB), S2	// S2 = base of perCPUData array
	MOV	·PerCPUSize(SB), S3	// S3 = size of one PerCPU struct
	MUL	S1, S3, S3		// S3 = hartID * PerCPUSize
	ADD	S2, S3, S2		// S2 = pointer to our PerCPU struct

	// Step 3: Set up stack from G0StackTop (offset 32)
	MOV	32(S2), S4		// S4 = G0StackTop

	// Check if stack is allocated
	BEQ	S4, ZERO, stack_not_ready

	MOV	S4, X2			// sp = G0StackTop (X2 is sp)

	// Step 4: Load ExceptionStackTop (offset 48) -> set sscratch
	MOV	48(S2), A1		// A1 = ExceptionStackTop
	// CSRW sscratch, A1
	WORD	$0x14059073		// sscratch = A1

	// Step 5: TODO: set stvec when RISC-V exception vectors are implemented

	// Step 6: Mark hart as online (offset 64)
	// FENCE rw,rw for release semantics
	WORD	$0x0330000F		// FENCE rw,rw
	MOV	$1, A1
	MOVW	A1, 64(S2)		// Online = 1

	// Step 7: Enable FPU and S-mode interrupts in sstatus.
	// Set FS (bits 14:13) = 01 (Initial) to allow FP instructions.
	// Set SIE (bit 1) to enable S-mode interrupts.
	// Primary hart gets FS from diplomat; secondary harts must set it explicitly.
	WORD	$0x100024F3		// CSRR S1, sstatus
	MOV	$0x6000, T0		// Clear FS field (bits 14:13)
	NOT	T0, T0
	AND	T0, S1, S1
	MOV	$0x2002, T0		// FS=01 (bit 13) + SIE (bit 1)
	OR	T0, S1, S1
	WORD	$0x10049073		// CSRW sstatus, S1

	// Step 8: WFI loop - wait for interrupt
secondary_idle_loop:
	WORD	$0x10500073		// WFI
	JMP	secondary_idle_loop

stack_not_ready:
	// Stacks not yet allocated - busy wait and retry
	MOV	$0x100000, A1
delay_loop:
	ADD	$-1, A1, A1
	BNE	A1, ZERO, delay_loop
	// Reload hart ID from tp and jump back
	MOV	TP, A0
	JMP	·secondaryCPUEntry(SB)

// getSecondaryCPUEntryAddr returns the address of secondaryCPUEntry
// for use with SBI hart_start.
//
// func getSecondaryCPUEntryAddr() uintptr
TEXT ·getSecondaryCPUEntryAddr(SB), NOSPLIT|NOFRAME, $0-8
	MOV	$·secondaryCPUEntry(SB), A0
	MOV	A0, ret+0(FP)
	RET
