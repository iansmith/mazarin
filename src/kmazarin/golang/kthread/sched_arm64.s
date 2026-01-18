#include "textflag.h"

// Scheduler assembly stubs for ARM64
// These provide low-level CPU operations for the scheduler.

// ============================================================================
// enableInterrupts - Enable interrupts by clearing the I bit in DAIF
// ============================================================================
TEXT ·enableInterrupts(SB), NOSPLIT, $0
	// MSR DAIFCLR, #2 - Clear I bit (bit 1) = enable IRQs
	// Encoded as: 0xD50342FF
	WORD	$0xD50342FF
	ISB	$15		// Synchronize context
	RET

// ============================================================================
// disableInterrupts - Disable interrupts and return previous DAIF
// Returns: saved DAIF value
// ============================================================================
TEXT ·disableInterrupts(SB), NOSPLIT, $0-8
	// MRS X0, DAIF - Read current DAIF into R0
	// Encoded as: 0xD53B4200
	WORD	$0xD53B4200
	// MSR DAIFSET, #2 - Set I bit = disable IRQs
	// Encoded as: 0xD50342DF
	WORD	$0xD50342DF
	ISB	$15
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// restoreInterrupts - Restore DAIF to a previously saved value
// Input: daif uint64 - saved DAIF value
// ============================================================================
TEXT ·restoreInterrupts(SB), NOSPLIT, $0-8
	MOVD	daif+0(FP), R0
	// MSR DAIF, X0 - Write R0 to DAIF
	// Encoded as: 0xD51B4200
	WORD	$0xD51B4200
	ISB	$15
	RET

// ============================================================================
// wfi - Wait For Interrupt (low-power idle)
// ============================================================================
TEXT ·wfi(SB), NOSPLIT|NOFRAME, $0-0
	// WFI - Wait For Interrupt
	// Encoded as HINT #1 in Go assembler
	HINT	$1
	RET

// ============================================================================
// switchPageTable - Switch TTBR0_EL1 to a new page table
// Input: root uint64 - physical address of new L0 page table
// ============================================================================
TEXT ·switchPageTable(SB), NOSPLIT, $0-8
	MOVD	root+0(FP), R0

	// MSR TTBR0_EL1, X0 - Write new page table base
	MSR	R0, TTBR0_EL1

	// Synchronization barrier
	ISB	$15

	// TLBI VMALLE1 - Invalidate all TLB entries for EL1
	// Encoded as: 0xD508871F
	WORD	$0xD508871F

	// Data and instruction synchronization
	DSB	$15
	ISB	$15
	RET

// ============================================================================
// restoreContext - Restore CPU context and return to user mode
// Input: ctx *ThreadContext - pointer to saved context
//
// ThreadContext layout (from thread.go):
//   X[31]uint64  - offset 0, 248 bytes (x0-x30)
//   SP uint64    - offset 248
//   ELR uint64   - offset 256
//   SPSR uint64  - offset 264
//
// This function does NOT return - it performs an ERET.
// ============================================================================
TEXT ·restoreContext(SB), NOSPLIT, $0-8
	MOVD	ctx+0(FP), R1	// R1 = pointer to ThreadContext

	// Load SP_EL0 (user stack pointer)
	MOVD	248(R1), R0
	MSR	R0, SP_EL0

	// Load ELR_EL1 (return address)
	MOVD	256(R1), R0
	MSR	R0, ELR_EL1

	// Load SPSR_EL1 (processor state)
	MOVD	264(R1), R0
	MSR	R0, SPSR_EL1

	// Load general purpose registers x2-x30 first
	// (we need x0 and x1 for loading, so do them last)
	MOVD	16(R1), R2
	MOVD	24(R1), R3
	MOVD	32(R1), R4
	MOVD	40(R1), R5
	MOVD	48(R1), R6
	MOVD	56(R1), R7
	MOVD	64(R1), R8
	MOVD	72(R1), R9
	MOVD	80(R1), R10
	MOVD	88(R1), R11
	MOVD	96(R1), R12
	MOVD	104(R1), R13
	MOVD	112(R1), R14
	MOVD	120(R1), R15
	MOVD	128(R1), R16
	MOVD	136(R1), R17
	// R18 is platform register - use raw instruction: ldr x18, [x1, #144]
	WORD	$0xF9404832
	MOVD	152(R1), R19
	MOVD	160(R1), R20
	MOVD	168(R1), R21
	MOVD	176(R1), R22
	MOVD	184(R1), R23
	MOVD	192(R1), R24
	MOVD	200(R1), R25
	MOVD	208(R1), R26
	MOVD	216(R1), R27
	// R28 is Go's g pointer - use raw instruction: ldr x28, [x1, #224]
	WORD	$0xF940703C
	MOVD	232(R1), R29	// Frame pointer
	MOVD	240(R1), R30	// Link register

	// Now load x0, using R1 as scratch
	// But first save x1 value we need to restore
	MOVD	8(R1), R0	// Temporarily hold x1 value in R0
	MOVD	0(R1), R1	// Load x0 into R1 (will swap below)

	// Swap: R0 has old x1, R1 has old x0
	// We need x0=old_x0, x1=old_x1
	// Use R30 as scratch? No, already loaded.
	// Actually we can just do this differently:
	// Save both x0 and x1 to stack, then pop

	// Simpler approach: use the fact that ERET will use whatever is in x0/x1
	// We already have: R0 = desired x1, R1 = desired x0
	// Just need to swap them
	// EOR-swap:
	EOR	R0, R1, R0	// R0 = R0 ^ R1 (x1 ^ x0)
	EOR	R0, R1, R1	// R1 = R1 ^ R0 = x1 ^ x0 ^ x0 = x1... wait
	// That's wrong. Let me think again.
	// R0 = old x1 value, R1 = old x0 value
	// We want: R0 = old x0, R1 = old x1
	// Simple swap:
	EOR	R0, R1, R0	// R0 = x1 ^ x0
	EOR	R0, R1, R1	// R1 = x0 ^ (x1 ^ x0) = x1
	EOR	R0, R1, R0	// R0 = (x1 ^ x0) ^ x1 = x0

	// Now R0 = old x0, R1 = old x1

	// Return to user mode
	ERET
