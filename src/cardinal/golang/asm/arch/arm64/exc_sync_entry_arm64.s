// exc_sync_entry.s - Synchronous Exception Handler Entry Point (Go/Plan9 Assembly)
//
// This file provides sync_exception_handler, the entry point called from the
// exception vector table (vec_sync_sp_el1) for synchronous exceptions at EL1h.
//
// ============================================================================
// SEGMENT OVERVIEW (see asm/docs/exception_handling_decomposition.md)
// ============================================================================
//
// Segment 1:  Temporary Save - Push x29, x30 to current stack
// Segment 2:  Save SP_EL0 - Preserve interrupted SP before clobbering registers
// Segment 3:  Calculate Original SP - Compute SP before our push
// Segment 4:  Nested Detection - Check if SP is in exception stack range
// Segment 5:  Stack Switch - Switch to exception stack and allocate frame
// Segment 6:  Save Original SP - Store original SP in frame
// Segment 7:  Save GPRs - Save x0-x28 to frame
// Segment 8:  Recover x29/x30 - Load from temp location, save to frame
// Segment 9:  Save System Regs - Save ELR, SPSR, FAR, ESR
// Segment 10: Save SP_EL0 - Store SP_EL0 in frame
// Segment 11: Dispatch - Branch to sync_exception_entry
//
// POST-BOOT NOTE: This is exception entry code - must remain inline assembly.
// The register save sequence cannot call functions. After boot, use primitives
// from lib_sysregs for any system register access in normal kernel code.
//
// The handler:
//   1. Saves x29, x30 to current stack temporarily
//   2. Detects nested exceptions and selects appropriate exception stack
//   3. Switches to exception stack and allocates 320-byte frame
//   4. Saves ALL registers (x0-x28, x29, x30) to the frame
//   5. Saves ELR_EL1, SPSR_EL1, FAR_EL1, ESR_EL1, SP_EL0
//   6. Branches to sync_exception_entry for dispatch
//
// Exception frame layout (320 bytes):
//   [SP+0]:   x0, x1        (16 bytes)
//   [SP+16]:  x2, x3        (16 bytes)
//   [SP+32]:  x4, x5        (16 bytes)
//   [SP+48]:  x6, x7        (16 bytes)
//   [SP+64]:  x8, x9        (16 bytes)
//   [SP+80]:  x10, x11      (16 bytes)
//   [SP+96]:  x12, x13      (16 bytes)
//   [SP+112]: x14, x15      (16 bytes)
//   [SP+128]: x16, x17      (16 bytes)
//   [SP+144]: x18, x19      (16 bytes)
//   [SP+160]: x20, x21      (16 bytes)
//   [SP+176]: x22, x23      (16 bytes)
//   [SP+192]: x24, x25      (16 bytes)
//   [SP+208]: x26, x27      (16 bytes)
//   [SP+224]: x28           (8 bytes)
//   [SP+232]: x29, x30      (16 bytes)
//   [SP+248]: original SP   (8 bytes)
//   [SP+256]: ELR_EL1, SPSR_EL1 (16 bytes)
//   [SP+272]: FAR_EL1, ESR_EL1  (16 bytes)
//   [SP+288]: SP_EL0        (8 bytes)
//   [SP+296]: saved g       (8 bytes)
//   [SP+304]: saved x0      (8 bytes)
//   [SP+312]: saved LR      (8 bytes)
//
// Stack addresses:
//   Primary exception stack: 0x5F000000 - 0x5F020000 (128 KB)
//   Nested exception uses:   0x5F008000 (middle of stack)

#include "textflag.h"

// Exception frame offsets (must match exc_syscall.s)
#define EXC_FRAME_SIZE       320
#define EXC_FRAME_X0         0
#define EXC_FRAME_X28        224
#define EXC_FRAME_X29_X30    232
#define EXC_FRAME_ORIG_SP    248
#define EXC_FRAME_ELR_SPSR   256
#define EXC_FRAME_FAR_ESR    272
#define EXC_FRAME_SP_EL0     288

// Exception stack bounds (128 KB stack from 0x5F000000 to 0x5F020000)
#define EXC_STACK_LOW        0x5F000000
#define EXC_STACK_HIGH       0x5F020000
#define EXC_STACK_PRIMARY    0x5F020000
#define EXC_STACK_NESTED     0x5F008000

// ============================================================================
// sync_exception_handler - Entry point for synchronous exceptions at EL1h
// ============================================================================
// Called from exception vector table entry vec_sync_sp_el1.
// Entry state:
//   - We are in EL1h mode (exception mode, using SP_EL1)
//   - SP = SP_EL1 (whatever stack the interrupted code was using)
//   - ELR_EL1 = return address
//   - SPSR_EL1 = saved PSTATE
//   - ESR_EL1 = exception syndrome
//   - FAR_EL1 = fault address (if applicable)
//   - SP_EL0 = interrupted stack (if coming from EL1t code)
//
// CRITICAL: We must save ALL registers immediately before any operations
// to support demand paging (retry after page fault).
//
TEXT sync_exception_handler(SB), NOSPLIT|NOFRAME, $0
	// ========================================================================
	// SEGMENT 1: Temporary Save
	// ========================================================================
	// We need x29 and x30 as scratch registers to set up exception stack.
	// Push them to current stack - we'll recover them after switching stacks.
	// Use pre-decrement addressing: [sp, #-16]!
	SUB $16, RSP
	STP (R29, R30), 0(RSP)

	// ========================================================================
	// SEGMENT 2: Save SP_EL0
	// ========================================================================
	// When exception occurs in EL1t mode (using SP_EL0), the CPU switches
	// to EL1h mode (using SP_EL1). SP_EL0 is NOT automatically saved!
	// We must save it now before clobbering R29.
	MRS SP_EL0, R29                    // R29 = SP_EL0 (will be saved to frame later)

	// ========================================================================
	// SEGMENT 3: Calculate Original SP
	// ========================================================================
	ADD $16, RSP, R30                  // R30 = original SP (current SP + 16)

	// ========================================================================
	// SEGMENT 4: Nested Detection
	// ========================================================================
	// Exception stack range: 0x5F000000 - 0x5F020000 (128 KB)
	// If current SP is in this range, we're in a nested exception.
	//
	// Check lower bound: SP >= 0x5F000000?
	MOVD $EXC_STACK_LOW, R29           // R29 = 0x5F000000
	CMP R29, R30
	BLO use_primary_stack              // If SP < 0x5F000000, use primary

	// Check upper bound: SP < 0x5F020000?
	MOVD $EXC_STACK_HIGH, R29          // R29 = 0x5F020000
	CMP R29, R30
	BHS use_primary_stack              // If SP >= 0x5F020000, use primary

	// We're in a nested exception - use middle of stack
	MOVD $EXC_STACK_NESTED, R29        // Nested: 0x5F008000
	B stack_selected

use_primary_stack:
	MOVD $EXC_STACK_PRIMARY, R29       // Primary: 0x5F020000 (top)

stack_selected:
	// ========================================================================
	// SEGMENT 5: Stack Switch
	// ========================================================================
	MOVD R29, RSP                      // Switch to exception stack
	SUB $EXC_FRAME_SIZE, RSP           // Allocate 320-byte frame

	// ========================================================================
	// SEGMENT 6: Save Original SP
	// ========================================================================
	// R30 contains original SP (before we pushed x29, x30)
	MOVD R30, EXC_FRAME_ORIG_SP(RSP)

	// ========================================================================
	// SEGMENT 7: Save GPRs
	// ========================================================================
	// Save all general-purpose registers x0-x28 to exception frame.
	STP (R0, R1), (EXC_FRAME_X0+0)(RSP)
	STP (R2, R3), (EXC_FRAME_X0+16)(RSP)
	STP (R4, R5), (EXC_FRAME_X0+32)(RSP)
	STP (R6, R7), (EXC_FRAME_X0+48)(RSP)
	STP (R8, R9), (EXC_FRAME_X0+64)(RSP)
	STP (R10, R11), (EXC_FRAME_X0+80)(RSP)
	STP (R12, R13), (EXC_FRAME_X0+96)(RSP)
	STP (R14, R15), (EXC_FRAME_X0+112)(RSP)
	STP (R16, R17), (EXC_FRAME_X0+128)(RSP)
	// R18 is platform register - Go assembly doesn't support it, skip saving
	// Save only R19 at the second slot of offset 144
	MOVD R19, (EXC_FRAME_X0+144+8)(RSP)
	STP (R20, R21), (EXC_FRAME_X0+160)(RSP)
	STP (R22, R23), (EXC_FRAME_X0+176)(RSP)
	STP (R24, R25), (EXC_FRAME_X0+192)(RSP)
	STP (R26, R27), (EXC_FRAME_X0+208)(RSP)
	MOVD g, EXC_FRAME_X28(RSP)         // g = R28

	// ========================================================================
	// SEGMENT 8: Recover x29/x30
	// ========================================================================
	// We pushed them to the original stack at [originalSP - 16]
	// R30 = original SP (saved in step 6), so x29/x30 are at [R30 - 16]
	MOVD EXC_FRAME_ORIG_SP(RSP), R0    // R0 = original SP
	SUB $16, R0                        // R0 = address where we pushed x29, x30
	LDP 0(R0), (R1, R2)                // R1 = original x29, R2 = original x30
	STP (R1, R2), EXC_FRAME_X29_X30(RSP)  // Save to frame

	// ========================================================================
	// SEGMENT 9: Save System Regs
	// ========================================================================
	// Save ELR_EL1, SPSR_EL1, FAR_EL1, ESR_EL1 to exception frame.
	MRS ELR_EL1, R0                    // Return address
	MRS SPSR_EL1, R1                   // Saved PSTATE
	MRS FAR_EL1, R2                    // Fault address
	MRS ESR_EL1, R3                    // Exception syndrome
	STP (R0, R1), EXC_FRAME_ELR_SPSR(RSP)   // ELR, SPSR
	STP (R2, R3), EXC_FRAME_FAR_ESR(RSP)    // FAR, ESR

	// ========================================================================
	// SEGMENT 10: Save SP_EL0
	// ========================================================================
	// Re-read SP_EL0 (R29 was clobbered for stack selection)
	MRS SP_EL0, R0
	MOVD R0, EXC_FRAME_SP_EL0(RSP)

	// ========================================================================
	// SEGMENT 11: Dispatch
	// ========================================================================
	// sync_exception_entry (in exc_syscall.s) handles:
	//   - EC=0x15 (SVC): syscall handling
	//   - EC=0x25 (data abort): page fault / demand paging
	//   - Other: print error and exit
	B sync_exception_entry(SB)
