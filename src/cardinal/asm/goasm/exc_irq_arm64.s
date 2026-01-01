// exc_irq.s - IRQ Exception Handler in Go/Plan9 Assembly
//
// This file contains the IRQ exception handler that saves state and dispatches
// to a Go function (IRQExceptionDispatch) which handles all interrupt logic
// including timer preemption.
//
// This follows the same pattern as exc_syscall.s - minimal assembly that
// sets up parameters and calls Go for all decision-making.

#include "textflag.h"

// ============================================================================
// IRQ Frame Layout (272 bytes, 16-byte aligned)
// ============================================================================
#define IRQ_FRAME_SIZE   272
#define IRQ_FRAME_X0     0
#define IRQ_FRAME_X2     16
#define IRQ_FRAME_X4     32
#define IRQ_FRAME_X6     48
#define IRQ_FRAME_X8     64
#define IRQ_FRAME_X10    80
#define IRQ_FRAME_X12    96
#define IRQ_FRAME_X14    112
#define IRQ_FRAME_X16    128
#define IRQ_FRAME_X18    144
#define IRQ_FRAME_X20    160
#define IRQ_FRAME_X22    176
#define IRQ_FRAME_X24    192
#define IRQ_FRAME_X26    208
#define IRQ_FRAME_X28    224
#define IRQ_FRAME_X30    240
#define IRQ_FRAME_SP_EL0 248
#define IRQ_FRAME_ELR    256
#define IRQ_FRAME_SPSR   264

// ============================================================================
// IRQ Exception Handler Entry Point
// ============================================================================
// Called from vector table entry vec_irq_sp_el1
// Entry: In EL1h mode, SP = SP_EL1, SP_EL0 = interrupted stack
//
// This handler:
// 1. Saves ALL registers to exception frame
// 2. Reads GIC IAR to get interrupt ID
// 3. Calls IRQExceptionDispatch(irqID, framePtr, savedG, savedELR, savedSP_EL0)
// 4. Based on return values, either:
//    - Restore and ERET normally
//    - Modify frame for timer preemption and ERET to asyncPreempt
// 5. Signals EOI to GIC
// 6. Restores all registers and ERETs

TEXT irq_exception_el1(SB), NOSPLIT, $0
	// ========================================================================
	// SAVE ALL REGISTERS TO EXCEPTION FRAME
	// ========================================================================
	// Allocate 272-byte frame
	SUB $IRQ_FRAME_SIZE, RSP

	// Save x0, x1 IMMEDIATELY before clobbering
	STP (R0, R1), IRQ_FRAME_X0(RSP)

	// Read GIC IAR to acknowledge interrupt (MUST be done quickly)
	// GICC_IAR at 0x0801000C
	MOVD $0x0801000C, R0
	MOVW (R0), R0                    // R0 = IAR value
	ANDW $0x3FF, R0, R0              // R0 = interrupt ID (10 bits)

	// Save x2, x3 so we can use them
	STP (R2, R3), IRQ_FRAME_X2(RSP)

	// Read SP_EL0, ELR_EL1, SPSR_EL1
	MRS SP_EL0, R1                   // R1 = interrupted stack
	MRS ELR_EL1, R2                  // R2 = return address
	MRS SPSR_EL1, R3                 // R3 = saved processor state

	// Store SP_EL0, ELR, SPSR in frame
	MOVD R1, IRQ_FRAME_SP_EL0(RSP)
	MOVD R2, IRQ_FRAME_ELR(RSP)
	MOVD R3, IRQ_FRAME_SPSR(RSP)

	// Save remaining registers x4-x30
	STP (R4, R5), IRQ_FRAME_X4(RSP)
	STP (R6, R7), IRQ_FRAME_X6(RSP)
	STP (R8, R9), IRQ_FRAME_X8(RSP)
	STP (R10, R11), IRQ_FRAME_X10(RSP)
	STP (R12, R13), IRQ_FRAME_X12(RSP)
	STP (R14, R15), IRQ_FRAME_X14(RSP)
	STP (R16, R17), IRQ_FRAME_X16(RSP)
	// R18 is platform register - skip saving (frame offset 144 unused)
	MOVD R19, (IRQ_FRAME_X18+8)(RSP)   // Save R19 at offset 152
	STP (R20, R21), IRQ_FRAME_X20(RSP)
	STP (R22, R23), IRQ_FRAME_X22(RSP)
	STP (R24, R25), IRQ_FRAME_X24(RSP)
	STP (R26, R27), IRQ_FRAME_X26(RSP)
	STP (g, R29), IRQ_FRAME_X28(RSP)   // g = R28
	MOVD R30, IRQ_FRAME_X30(RSP)

	// ========================================================================
	// CALL GO DISPATCHER
	// ========================================================================
	// IRQExceptionDispatch(irqID, framePtr, savedG, elr, spEl0 uint64)
	//     returns (newELR, newSP, newLR uint64, doPreempt bool)
	//
	// R0 = irqID (already set from GIC IAR above)
	// R1 = framePtr (pointer to exception frame)
	// R2 = savedG (interrupted x28/g pointer)
	// R3 = elr (interrupted PC)
	// R4 = spEl0 (interrupted SP_EL0)

	// R0 already has irqID
	MOVD RSP, R1                     // R1 = framePtr
	MOVD IRQ_FRAME_X28(RSP), R2      // R2 = savedG (x28 from frame)
	MOVD IRQ_FRAME_ELR(RSP), R3      // R3 = elr
	MOVD IRQ_FRAME_SP_EL0(RSP), R4   // R4 = spEl0

	// CRITICAL: Reserve spill space for callee's 5 register arguments
	// IRQExceptionDispatch takes 5 args (R0-R4), needs 40 bytes of spill space
	// Plus we need to save our callee-saved registers
	// Layout: [RSP+0..47] = spill area (48 bytes), [RSP+48..111] = saved regs
	SUB $112, RSP

	// Save callee-saved registers at offset 48+
	STP (R19, R20), 48(RSP)
	STP (R21, R22), 64(RSP)
	STP (R23, R24), 80(RSP)
	STP (R25, R26), 96(RSP)

	CALL main·IRQExceptionDispatch(SB)
	// Returns: R0 = newELR, R1 = newSP, R2 = newLR, R3 = doPreempt (0 or 1)

	// Restore callee-saved registers
	LDP 48(RSP), (R19, R20)
	LDP 64(RSP), (R21, R22)
	LDP 80(RSP), (R23, R24)
	LDP 96(RSP), (R25, R26)
	ADD $112, RSP

	// Check if preemption is needed
	CBZ R3, irq_normal_return

	// ========================================================================
	// PREEMPTION PATH: Modify frame and ERET to asyncPreempt
	// ========================================================================
	// R0 = newELR (asyncPreempt address)
	// R1 = newSP (adjusted SP_EL0 with return frame)
	// R2 = newLR (interrupted PC, for asyncPreempt to return to)

	// Update frame with new values
	MOVD R0, IRQ_FRAME_ELR(RSP)      // ELR = asyncPreempt address
	MOVD R1, IRQ_FRAME_SP_EL0(RSP)   // SP_EL0 = adjusted stack
	MOVD R2, IRQ_FRAME_X30(RSP)      // LR = interrupted PC

irq_normal_return:
	// ========================================================================
	// RESTORE ALL REGISTERS AND ERET
	// ========================================================================
	// Note: Go dispatcher has already handled EOI

	// Restore ELR_EL1 and SPSR_EL1
	MOVD IRQ_FRAME_ELR(RSP), R0
	MOVD IRQ_FRAME_SPSR(RSP), R1
	MSR R0, ELR_EL1
	MSR R1, SPSR_EL1
	ISB $15

	// Restore SP_EL0
	MOVD IRQ_FRAME_SP_EL0(RSP), R0
	MSR R0, SP_EL0
	ISB $15

	// Restore all GPRs (x2-x30 first, x0/x1 last)
	LDP IRQ_FRAME_X2(RSP), (R2, R3)
	LDP IRQ_FRAME_X4(RSP), (R4, R5)
	LDP IRQ_FRAME_X6(RSP), (R6, R7)
	LDP IRQ_FRAME_X8(RSP), (R8, R9)
	LDP IRQ_FRAME_X10(RSP), (R10, R11)
	LDP IRQ_FRAME_X12(RSP), (R12, R13)
	LDP IRQ_FRAME_X14(RSP), (R14, R15)
	LDP IRQ_FRAME_X16(RSP), (R16, R17)
	// R18 is platform register - skip restoring
	MOVD (IRQ_FRAME_X18+8)(RSP), R19   // Restore R19 from offset 152
	LDP IRQ_FRAME_X20(RSP), (R20, R21)
	LDP IRQ_FRAME_X22(RSP), (R22, R23)
	LDP IRQ_FRAME_X24(RSP), (R24, R25)
	LDP IRQ_FRAME_X26(RSP), (R26, R27)
	LDP IRQ_FRAME_X28(RSP), (g, R29)   // g = R28
	MOVD IRQ_FRAME_X30(RSP), R30

	// Restore x0, x1 LAST
	LDP IRQ_FRAME_X0(RSP), (R0, R1)

	// Deallocate frame
	ADD $IRQ_FRAME_SIZE, RSP

	// Return from exception
	ERET
