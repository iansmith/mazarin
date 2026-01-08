//go:build qemuvirt && aarch64

// exceptions_arm64.s - Kmazarin exception handlers (Go/Plan9 assembly)
//
// This file provides the exception vector table and handlers for kmazarin.
// Cardinal sets VBAR_EL1 to point to this vector before jumping to kmazarin.
//
// CRITICAL: Exception vector MUST be 2KB aligned (ARM64 requirement)

#include "textflag.h"

// UART base for minimal debug output
// NOTE: Use high-memory UART address since kmazarin runs at high memory
// Cardinal maps UART at 0xFFFFFFFF09000000 before jumping to us
#define UART_BASE 0xFFFFFFFF09000000

// Exception frame layout (same as Cardinal for compatibility)
#define EXC_FRAME_X0         0
#define EXC_FRAME_X8         64
#define EXC_FRAME_X28        224
#define EXC_FRAME_X29_X30    232
#define EXC_FRAME_ELR_SPSR   256
#define EXC_FRAME_FAR_ESR    272
#define EXC_FRAME_SP_EL0     288
#define EXC_FRAME_SIZE       320

// ============================================================================
// EXCEPTION VECTOR TABLE
// ============================================================================
// ARM64 exception vector table - 16 entries, each 128 bytes (0x80)
// CRITICAL: Must be 2KB (0x800) aligned - enforced by .align 11
//
// We only implement "Current EL with SPx" entries (0x200-0x380)
// since kmazarin runs at EL1h mode (using SP_EL1)

// CRITICAL: Exception vector must be 2KB aligned
// Use PCALIGN $2048 to force 2KB (0x800) alignment
TEXT ·ExceptionVectorTable(SB), NOSPLIT|NOFRAME, $0
	PCALIGN $2048		// Force 2KB alignment
exception_vector_base:

// ============================================================================
// Current EL with SP0 (0x000-0x1FF) - Go runtime uses SP_EL0 at EL1
// ============================================================================
// Each vector entry must be 128 bytes - pad with NOPs
el1_sp0_sync:
	// Go runtime runs at EL1 using SP_EL0, so sync exceptions come here
	// Print '1' to show sync exception occurred
	MOVD	$UART_BASE, R10
	MOVD	$'1', R11
	MOVB	R11, (R10)
	B	sync_exception_handler
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0

el1_sp0_irq:
	// IRQs also come here when using SP_EL0
	// Print 'I' to show IRQ occurred
	MOVD	$UART_BASE, R10
	MOVD	$'I', R11
	MOVB	R11, (R10)
	B	irq_exception_handler
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0

el1_sp0_fiq:
	MOVD	$UART_BASE, R10
	MOVD	$'2', R11
	MOVB	R11, (R10)
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0

el1_sp0_serror:
	MOVD	$UART_BASE, R10
	MOVD	$'3', R11
	MOVB	R11, (R10)
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	// CRITICAL: el1_spx_sync MUST start at EXACTLY offset 0x200 (no alignment!)
el1_spx_sync:
	B	sync_exception_handler
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el1_spx_irq:
	B	irq_exception_handler
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el1_spx_fiq:
	MOVD	$UART_BASE, R10
	MOVD	$'F', R11
	MOVB	R11, (R10)
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0

el1_spx_serror:
	MOVD	$UART_BASE, R10
	MOVD	$'S', R11
	MOVB	R11, (R10)
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0

// ============================================================================
// Lower EL AArch64 (0x400-0x5FF) - Not used (no user space yet)
// ============================================================================
el0_aarch64_sync:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el0_aarch64_irq:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el0_aarch64_fiq:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el0_aarch64_serror:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

// ============================================================================
// Lower EL AArch32 (0x600-0x7FF) - Not supported
// ============================================================================
el0_aarch32_sync:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el0_aarch32_irq:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el0_aarch32_fiq:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el0_aarch32_serror:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

// ============================================================================
// EXCEPTION HANDLERS
// ============================================================================

// Unhandled exception - print 'X' with ESR info and halt
unhandled_exception:
	MOVD	$UART_BASE, R10
	MOVD	$'X', R11
	MOVB	R11, (R10)
	MOVD	$':', R11
	MOVB	R11, (R10)

	// Print ESR_EL1 to identify exception type
	MRS	ESR_EL1, R12
	LSR	$26, R12, R12  // Extract EC (bits 31:26)
	AND	$0x3F, R12

	// Print EC as 2 hex digits
	LSR	$4, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_first_digit
	ADD	$('A'-10), R11
	B	print_first
print_first_digit:
	ADD	$'0', R11
print_first:
	MOVB	R11, (R10)

	AND	$0xF, R12
	CMP	$10, R12
	BLT	print_second_digit
	ADD	$('A'-10), R12
	B	print_second
print_second_digit:
	ADD	$'0', R12
print_second:
	MOVB	R12, (R10)

	MOVD	$'\r', R11
	MOVB	R11, (R10)
	MOVD	$'\n', R11
	MOVB	R11, (R10)
hang:
	B	hang

// ============================================================================
// sync_exception_handler - Synchronous exceptions (SVC, data abort, etc.)
// ============================================================================
sync_exception_handler:
	// Print 's' to show we reached sync handler
	MOVD	$UART_BASE, R10
	MOVD	$'s', R11
	MOVB	R11, (R10)

	// Print 'E' before ESR
	MOVD	$'E', R11
	MOVB	R11, (R10)

	// Print ESR_EL1 to see what exception this is
	MRS	ESR_EL1, R12
	MOVD	$16, R13		// Counter for 16 hex digits
print_esr_loop:
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_esr_digit
	ADD	$('A'-10), R11
	B	print_esr_char
print_esr_digit:
	ADD	$'0', R11
print_esr_char:
	MOVB	R11, (R10)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_esr_loop

	// Print space after ESR
	MOVD	$' ', R11
	MOVB	R11, (R10)

	// Print 'L' before ELR
	MOVD	$'L', R11
	MOVB	R11, (R10)

	// Print ELR_EL1 to see where exception came from
	MRS	ELR_EL1, R12
	MOVD	$16, R13		// Counter for 16 hex digits
print_elr_loop:
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_elr_digit
	ADD	$('A'-10), R11
	B	print_elr_char
print_elr_digit:
	ADD	$'0', R11
print_elr_char:
	MOVB	R11, (R10)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_elr_loop

	// Print space after ELR
	MOVD	$' ', R11
	MOVB	R11, (R10)

	// Print 'F' before FAR (Fault Address Register)
	MOVD	$'F', R11
	MOVB	R11, (R10)

	// Print FAR_EL1 (faulting address for data aborts)
	MRS	FAR_EL1, R12
	MOVD	$16, R13		// Counter for 16 hex digits
print_far_loop:
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_far_digit
	ADD	$('A'-10), R11
	B	print_far_char
print_far_digit:
	ADD	$'0', R11
print_far_char:
	MOVB	R11, (R10)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_far_loop

	// Print space after FAR
	MOVD	$' ', R11
	MOVB	R11, (R10)

	// Print '<' before original RSP
	MOVD	$'<', R11
	MOVB	R11, (R10)

	// Print ORIGINAL RSP value before SUB (full 64-bit hex)
	MOVD	RSP, R12
	MOVD	$16, R13		// Counter for 16 hex digits
print_rsp_before_loop:
	// Extract top 4 bits
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_rsp_before_digit
	ADD	$('A'-10), R11
	B	print_rsp_before_char
print_rsp_before_digit:
	ADD	$'0', R11
print_rsp_before_char:
	MOVB	R11, (R10)
	// Shift left to get next nibble
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_rsp_before_loop

	// Print '>' after hex value
	MOVD	$'>', R11
	MOVB	R11, (R10)

	// Save all registers to exception frame
	SUB	$EXC_FRAME_SIZE, RSP

	// Print 'S' to show SUB succeeded, then print FULL RSP value
	MOVD	$UART_BASE, R10
	MOVD	$'S', R11
	MOVB	R11, (R10)

	// Print '[' before hex value
	MOVD	$'[', R11
	MOVB	R11, (R10)

	// Print full 64-bit RSP value in hex (16 digits)
	MOVD	RSP, R12
	MOVD	$16, R13		// Counter for 16 hex digits
print_rsp_loop:
	// Extract top 4 bits
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_rsp_digit
	ADD	$('A'-10), R11
	B	print_rsp_char
print_rsp_digit:
	ADD	$'0', R11
print_rsp_char:
	MOVB	R11, (R10)
	// Shift left to get next nibble
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_rsp_loop

	// Print ']' after hex value
	MOVD	$']', R11
	MOVB	R11, (R10)

	// DEBUG: Print 'Z' to show we completed the print loop
	MOVD	$'Z', R11
	MOVB	R11, (R10)

	// DEBUG: Print '@' then current RSP value right before STP
	MOVD	$'@', R11
	MOVB	R11, (R10)
	MOVD	RSP, R12
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_final_sp_digit
	ADD	$('A'-10), R11
	B	print_final_sp_char
print_final_sp_digit:
	ADD	$'0', R11
print_final_sp_char:
	MOVB	R11, (R10)

	// DEBUG: Print '!' right before STP
	MOVD	$'!', R11
	MOVB	R11, (R10)

	// Save X0-X7
	STP	(R0, R1), EXC_FRAME_X0(RSP)
	STP	(R2, R3), EXC_FRAME_X0+16(RSP)
	STP	(R4, R5), EXC_FRAME_X0+32(RSP)
	STP	(R6, R7), EXC_FRAME_X0+48(RSP)

	// Save X8-X27
	STP	(R8, R9), EXC_FRAME_X8(RSP)
	STP	(R10, R11), EXC_FRAME_X8+16(RSP)
	STP	(R12, R13), EXC_FRAME_X8+32(RSP)
	STP	(R14, R15), EXC_FRAME_X8+48(RSP)
	STP	(R16, R17), EXC_FRAME_X8+64(RSP)
	// R18 is platform register, use raw instruction: str x18, [sp, #144]
	WORD	$0xf9004bf2  // str x18, [sp, #144]
	// Save R19: str x19, [sp, #152]
	WORD	$0xf9004ff3  // str x19, [sp, #152]
	STP	(R20, R21), EXC_FRAME_X8+96(RSP)
	STP	(R22, R23), EXC_FRAME_X8+112(RSP)
	STP	(R24, R25), EXC_FRAME_X8+128(RSP)
	STP	(R26, R27), EXC_FRAME_X8+144(RSP)

	// Save X28-X30 (R28 is g, use raw instruction)
	// str x28, [sp, #224]
	WORD	$0xf90073fc  // str x28, [sp, #224]
	// str x29, [sp, #232]
	WORD	$0xf90077fd  // str x29, [sp, #232]
	MOVD	LR, R10
	MOVD	R10, EXC_FRAME_X28+16(RSP)

	// DEBUG: Print 'R' to show all registers saved
	MOVD	$UART_BASE, R10
	MOVD	$'R', R11
	MOVB	R11, (R10)

	// Save ELR, SPSR, FAR, ESR
	MRS	ELR_EL1, R10
	MRS	SPSR_EL1, R11
	STP	(R10, R11), EXC_FRAME_ELR_SPSR(RSP)

	// DEBUG: Print 'P' then SPSR value to see what mode we're returning to
	MOVD	$UART_BASE, R14
	MOVD	$'P', R15
	MOVB	R15, (R14)
	MOVD	R11, R12
	MOVD	$8, R13		// Print only first 8 hex digits (top 32 bits)
print_spsr_loop_sync:
	LSR	$60, R12, R15
	AND	$0xF, R15
	CMP	$10, R15
	BLT	print_spsr_digit_sync
	ADD	$('A'-10), R15
	B	print_spsr_char_sync
print_spsr_digit_sync:
	ADD	$'0', R15
print_spsr_char_sync:
	MOVB	R15, (R14)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_spsr_loop_sync
	MOVD	$' ', R15
	MOVB	R15, (R14)

	MRS	FAR_EL1, R10
	MRS	ESR_EL1, R11
	STP	(R10, R11), EXC_FRAME_FAR_ESR(RSP)

	// Save SP_EL0
	MRS	SP_EL0, R10
	MOVD	R10, EXC_FRAME_SP_EL0(RSP)

	// Extract exception class (EC) from ESR_EL1
	// EC is in bits 31:26
	LSR	$26, R11, R10
	AND	$0x3F, R10

	// Check if this is SVC (EC = 0x15)
	CMP	$0x15, R10
	BNE	not_svc

	// SVC: Call syscall dispatcher using ABI0 calling convention
	// Load arguments from exception frame first (before adjusting RSP)
	LDP	EXC_FRAME_X8(RSP), (R0, R1)        // R0 = syscall num (X8), R1 = X9
	LDP	EXC_FRAME_X0(RSP), (R2, R3)        // R2 = arg0 (X0), R3 = arg1 (X1)
	LDP	EXC_FRAME_X0+16(RSP), (R4, R5)     // R4 = arg2 (X2), R5 = arg3 (X3)
	LDP	EXC_FRAME_X0+32(RSP), (R6, R7)     // R6 = arg4 (X4), R7 = arg5 (X5)

	// Allocate frame for ABI0 call: 8 (pad) + 7*8 (args) + 8 (return) = 72, round to 80
	SUB	$80, RSP

	// Store arguments on stack for ABI0
	MOVD	R0, 8(RSP)      // syscallNum at RSP+8
	MOVD	R2, 16(RSP)     // arg0 at RSP+16
	MOVD	R3, 24(RSP)     // arg1 at RSP+24
	MOVD	R4, 32(RSP)     // arg2 at RSP+32
	MOVD	R5, 40(RSP)     // arg3 at RSP+40
	MOVD	R6, 48(RSP)     // arg4 at RSP+48
	MOVD	R7, 56(RSP)     // arg5 at RSP+56

	// Call ABI0 stub (will tail-call syscallDispatchInternal via ABIInternal)
	CALL	·SyscallDispatch(SB)

	// Read return value from stack
	MOVD	64(RSP), R0     // Return value at RSP+64
	ADD	$80, RSP

	// Store return value back to X0 in exception frame
	MOVD	R0, EXC_FRAME_X0(RSP)

	// Restore and return
	B	sync_return

not_svc:
	// Check if this is Data Abort (EC = 0x25 or 0x24)
	CMP	$0x25, R10  // Data Abort from current EL
	BEQ	data_abort
	CMP	$0x24, R10  // Data Abort from lower EL
	BEQ	data_abort

	// Unknown exception - print 'E' with EC and hang
	MOVD	$UART_BASE, R12
	MOVD	$'E', R11
	MOVB	R11, (R12)
	MOVD	$':', R11
	MOVB	R11, (R12)

	// Print EC as 2 hex digits
	LSR	$4, R10, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	not_svc_first_digit
	ADD	$('A'-10), R11
	B	not_svc_first
not_svc_first_digit:
	ADD	$'0', R11
not_svc_first:
	MOVB	R11, (R12)

	AND	$0xF, R10
	CMP	$10, R10
	BLT	not_svc_second_digit
	ADD	$('A'-10), R10
	B	not_svc_second
not_svc_second_digit:
	ADD	$'0', R10
not_svc_second:
	MOVB	R10, (R12)

	MOVD	$'\r', R11
	MOVB	R11, (R12)
	MOVD	$'\n', R11
	MOVB	R11, (R12)
not_svc_hang:
	B	not_svc_hang

data_abort:
	// Print 'D' for data abort (temporary - will add proper handling)
	MOVD	$UART_BASE, R10
	MOVD	$'D', R11
	MOVB	R11, (R10)
	MOVD	$'\r', R11
	MOVB	R11, (R10)
	MOVD	$'\n', R11
	MOVB	R11, (R10)
data_abort_hang:
	B	data_abort_hang

sync_return:
	// Restore SP_EL0
	MOVD	EXC_FRAME_SP_EL0(RSP), R10
	MSR	R10, SP_EL0

	// Restore ELR and SPSR
	LDP	EXC_FRAME_ELR_SPSR(RSP), (R10, R11)
	MSR	R10, ELR_EL1
	MSR	R11, SPSR_EL1

	// Restore X28-X30 (R28 is g, use raw instruction)
	// ldr x28, [sp, #224]
	WORD	$0xf94070fc  // ldr x28, [sp, #224]
	// ldr x29, [sp, #232]
	WORD	$0xf94074fd  // ldr x29, [sp, #232]
	MOVD	EXC_FRAME_X28+16(RSP), R10
	MOVD	R10, LR

	// Restore X8-X27
	LDP	EXC_FRAME_X8(RSP), (R8, R9)
	LDP	EXC_FRAME_X8+16(RSP), (R10, R11)
	LDP	EXC_FRAME_X8+32(RSP), (R12, R13)
	LDP	EXC_FRAME_X8+48(RSP), (R14, R15)
	LDP	EXC_FRAME_X8+64(RSP), (R16, R17)
	// R18 is platform register, use raw instruction: ldr x18, [sp, #144]
	WORD	$0xf9404bf2  // ldr x18, [sp, #144]
	// Restore R19: ldr x19, [sp, #152]
	WORD	$0xf9404ff3  // ldr x19, [sp, #152]
	LDP	EXC_FRAME_X8+96(RSP), (R20, R21)
	LDP	EXC_FRAME_X8+112(RSP), (R22, R23)
	LDP	EXC_FRAME_X8+128(RSP), (R24, R25)
	LDP	EXC_FRAME_X8+144(RSP), (R26, R27)

	// Restore X0-X7
	LDP	EXC_FRAME_X0(RSP), (R0, R1)
	LDP	EXC_FRAME_X0+16(RSP), (R2, R3)
	LDP	EXC_FRAME_X0+32(RSP), (R4, R5)
	LDP	EXC_FRAME_X0+48(RSP), (R6, R7)

	// Clean up stack and return
	ADD	$EXC_FRAME_SIZE, RSP
	ERET

// ============================================================================
// irq_exception_handler - Hardware interrupts (timer, UART, etc.)
// ============================================================================
// NOTE: Use high-memory GIC address since kmazarin runs at high memory
// Cardinal maps GIC at 0xFFFFFFFF08000000 before jumping to us
#define GIC_CPU_BASE	0xFFFFFFFF08010000
#define GICC_IAR	0x000C  // Interrupt Acknowledge Register offset
#define GICC_EOIR	0x0010  // End Of Interrupt Register offset

irq_exception_handler:
	// DEBUG: Print '!' to show IRQ entry
	MOVD	$UART_BASE, R9
	MOVD	$'!', R8
	MOVB	R8, (R9)

	// Save all registers to exception frame
	SUB	$EXC_FRAME_SIZE, RSP

	// Save X0-X7
	STP	(R0, R1), EXC_FRAME_X0(RSP)
	STP	(R2, R3), EXC_FRAME_X0+16(RSP)
	STP	(R4, R5), EXC_FRAME_X0+32(RSP)
	STP	(R6, R7), EXC_FRAME_X0+48(RSP)

	// Save X8-X27
	STP	(R8, R9), EXC_FRAME_X8(RSP)
	STP	(R10, R11), EXC_FRAME_X8+16(RSP)
	STP	(R12, R13), EXC_FRAME_X8+32(RSP)
	STP	(R14, R15), EXC_FRAME_X8+48(RSP)
	STP	(R16, R17), EXC_FRAME_X8+64(RSP)
	// R18 is platform register, use raw instruction: str x18, [sp, #144]
	WORD	$0xf9004bf2  // str x18, [sp, #144]
	// Save R19: str x19, [sp, #152]
	WORD	$0xf9004ff3  // str x19, [sp, #152]
	STP	(R20, R21), EXC_FRAME_X8+96(RSP)
	STP	(R22, R23), EXC_FRAME_X8+112(RSP)
	STP	(R24, R25), EXC_FRAME_X8+128(RSP)
	STP	(R26, R27), EXC_FRAME_X8+144(RSP)

	// Save X28-X30 (R28 is g, use raw instruction)
	// str x28, [sp, #224]
	WORD	$0xf90073fc  // str x28, [sp, #224]
	// str x29, [sp, #232]
	WORD	$0xf90077fd  // str x29, [sp, #232]
	MOVD	LR, R10
	MOVD	R10, EXC_FRAME_X28+16(RSP)

	// Save ELR, SPSR, FAR, ESR
	MRS	ELR_EL1, R10
	MRS	SPSR_EL1, R11
	STP	(R10, R11), EXC_FRAME_ELR_SPSR(RSP)

	// DEBUG: Print 'P' then SPSR value to see what mode we're returning to
	MOVD	$UART_BASE, R14
	MOVD	$'P', R15
	MOVB	R15, (R14)
	MOVD	R11, R12
	MOVD	$8, R13		// Print only first 8 hex digits (top 32 bits)
print_spsr_loop_irq:
	LSR	$60, R12, R15
	AND	$0xF, R15
	CMP	$10, R15
	BLT	print_spsr_digit_irq
	ADD	$('A'-10), R15
	B	print_spsr_char_irq
print_spsr_digit_irq:
	ADD	$'0', R15
print_spsr_char_irq:
	MOVB	R15, (R14)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_spsr_loop_irq
	MOVD	$' ', R15
	MOVB	R15, (R14)

	MRS	FAR_EL1, R10
	MRS	ESR_EL1, R11
	STP	(R10, R11), EXC_FRAME_FAR_ESR(RSP)

	// Save SP_EL0
	MRS	SP_EL0, R10
	MOVD	R10, EXC_FRAME_SP_EL0(RSP)

	// Read interrupt number from GIC CPU interface
	// IAR = GICC_BASE + 0x0C
	MOVD	$(GIC_CPU_BASE + GICC_IAR), R10
	MOVW	(R10), R0  // R0 = interrupt number

	// Store IAR value for later EOIR write
	MOVD	R0, R19  // Save full IAR value in R19

	// Mask to get interrupt ID (bits 0-9, max 1020 for GICv2)
	AND	$0x3FF, R0  // R0 = IRQ number (0-1019)

	// Prepare stack frame for ABI0 call to IRQDispatch
	// Frame layout (72 bytes, 16-byte aligned):
	//   RSP+0:  (alignment padding)
	//   RSP+8:  arg0 - irqNum
	//   RSP+16: arg1 - framePtr
	//   RSP+24: arg2 - elr
	//   RSP+32: arg3 - spEl0
	//   RSP+40: ret0 - newELR
	//   RSP+48: ret1 - newSP
	//   RSP+56: ret2 - newLR
	//   RSP+64: ret3 - doPreempt
	SUB	$80, RSP  // Allocate frame (16-byte aligned)

	// Store arguments on stack
	MOVD	R0, 8(RSP)   // irqNum
	MOVD	RSP, R1
	ADD	$80, R1      // R1 = original RSP (exception frame pointer)
	MOVD	R1, 16(RSP)  // framePtr
	MOVD	EXC_FRAME_ELR_SPSR+80(RSP), R2  // saved ELR
	MOVD	R2, 24(RSP)
	MOVD	EXC_FRAME_SP_EL0+80(RSP), R3    // saved SP_EL0
	MOVD	R3, 32(RSP)

	// DEBUG: Print '@' before call
	MOVD	$UART_BASE, R9
	MOVD	$'@', R8
	MOVB	R8, (R9)

	// Call main.IRQDispatch(irqNum, framePtr, elr, spEl0)
	CALL	·IRQDispatch(SB)

	// DEBUG: Print '#' after call
	MOVD	$UART_BASE, R9
	MOVD	$'#', R8
	MOVB	R8, (R9)

	// Read return values from stack
	MOVD	40(RSP), R20  // R20 = newELR
	MOVD	48(RSP), R21  // R21 = newSP
	MOVD	56(RSP), R22  // R22 = newLR
	MOVD	64(RSP), R23  // R23 = doPreempt

	// Clean up stack frame
	ADD	$80, RSP

	// Write End Of Interrupt (must do before modifying frame!)
	MOVD	$(GIC_CPU_BASE + GICC_EOIR), R10
	MOVW	R19, (R10)  // Write original IAR value to EOIR

	// Check if we should modify frame for preemption
	CMP	$0, R23  // doPreempt == true?
	BEQ	irq_return  // No preemption, restore normally

	// Preemption requested - modify exception frame for call injection
	// Update ELR_EL1 to point to asyncPreempt
	MOVD	R20, R10
	MOVD	R10, EXC_FRAME_ELR_SPSR(RSP)  // Update ELR in frame

	// Update SP_EL0 to adjusted stack
	MOVD	R21, EXC_FRAME_SP_EL0(RSP)

	// Update LR to interrupted PC (so asyncPreempt can return)
	MOVD	R22, EXC_FRAME_X28+16(RSP)  // Update saved LR in frame

	// Fall through to irq_return

irq_return:
	// Restore SP_EL0
	MOVD	EXC_FRAME_SP_EL0(RSP), R10
	MSR	R10, SP_EL0

	// Restore ELR and SPSR
	LDP	EXC_FRAME_ELR_SPSR(RSP), (R10, R11)
	MSR	R10, ELR_EL1
	MSR	R11, SPSR_EL1

	// Restore X28-X30 (R28 is g, use raw instruction)
	// ldr x28, [sp, #224]
	WORD	$0xf94070fc  // ldr x28, [sp, #224]
	// ldr x29, [sp, #232]
	WORD	$0xf94074fd  // ldr x29, [sp, #232]
	MOVD	EXC_FRAME_X28+16(RSP), R10
	MOVD	R10, LR

	// Restore X8-X27
	LDP	EXC_FRAME_X8(RSP), (R8, R9)
	LDP	EXC_FRAME_X8+16(RSP), (R10, R11)
	LDP	EXC_FRAME_X8+32(RSP), (R12, R13)
	LDP	EXC_FRAME_X8+48(RSP), (R14, R15)
	LDP	EXC_FRAME_X8+64(RSP), (R16, R17)
	// R18 is platform register, use raw instruction: ldr x18, [sp, #144]
	WORD	$0xf9404bf2  // ldr x18, [sp, #144]
	// Restore R19: ldr x19, [sp, #152]
	WORD	$0xf9404ff3  // ldr x19, [sp, #152]
	LDP	EXC_FRAME_X8+96(RSP), (R20, R21)
	LDP	EXC_FRAME_X8+112(RSP), (R22, R23)
	LDP	EXC_FRAME_X8+128(RSP), (R24, R25)
	LDP	EXC_FRAME_X8+144(RSP), (R26, R27)

	// Restore X0-X7
	LDP	EXC_FRAME_X0(RSP), (R0, R1)
	LDP	EXC_FRAME_X0+16(RSP), (R2, R3)
	LDP	EXC_FRAME_X0+32(RSP), (R4, R5)
	LDP	EXC_FRAME_X0+48(RSP), (R6, R7)

	// Clean up stack and return
	ADD	$EXC_FRAME_SIZE, RSP
	ERET

// ============================================================================
// GetExceptionVectorBase - Returns address of exception vector table
// ============================================================================
TEXT ·GetExceptionVectorBase(SB), NOSPLIT, $0-8
	MOVD	$·ExceptionVectorTable(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// SetVBAR - Sets VBAR_EL1 to the exception vector table address
// ============================================================================
TEXT ·SetVBAR(SB), NOSPLIT, $0-8
	MOVD	addr+0(FP), R0
	MSR	R0, VBAR_EL1
	ISB	$15		// Synchronize context
	RET

// ============================================================================
// EnableIRQs - Enable interrupts by clearing the I bit in DAIF
// ============================================================================
TEXT ·EnableIRQs(SB), NOSPLIT, $0
	// MSR DAIFCLR, #2 - Clear I bit (bit 1) = enable IRQs
	// Encoded as: 0xD50342FF
	WORD	$0xD50342FF
	ISB	$15		// Synchronize context
	RET
