// semihosting_arm64.s - QEMU Semihosting Functions
//
// This file contains functions for QEMU semihosting support.
// Semihosting allows communication with the QEMU host for exit and debugging.
// Requires QEMU to be run with -semihosting flag.
//
// NOTE: These functions use Go 1.17+ register-based calling convention.
// Parameters arrive in R0, R1, etc. Return values go in R0.

#include "textflag.h"

// UART debug output macro (for fallback when semihosting fails)
// CRITICAL: This macro must be used for all debug output, never inline UART writes!
// Expects: R0 contains the character to print (caller-saved)
// Preserves: All registers except R0 (R10 is saved/restored internally)
// Caller must save/restore R0 if needed before/after calling
#define UART_PUTC_SAFE \
	SUB $16, RSP; \
	MOVD R10, 0(RSP); \
	MOVD $0x09000000, R10; \
	MOVW R0, 0(R10); \
	MOVD 0(RSP), R10; \
	ADD $16, RSP

// ============================================================================
// QEMU Exit (Semihosting)
// ============================================================================

// qemu_exit()
// Exit QEMU using semihosting
// Requires QEMU to be run with -semihosting flag
TEXT qemu_exit(SB), NOSPLIT, $16-0
	// Set up parameter block on stack
	// Reserve 16 bytes: [0] = reason code, [8] = status code

	// Store exit reason code: ADP_Stopped_ApplicationExit (0x20026)
	MOVD	$0x20026, R1
	MOVD	R1, (RSP)		// Store reason at SP+0

	// Store status code: 0 (success)
	MOVD	ZR, 8(RSP)		// Store 0 at SP+8

	// Set up semihosting call
	MOVD	RSP, R1			// R1 = pointer to parameter block
	MOVW	$0x18, R0		// R0 = SYS_EXIT (0x18)

	// Trigger semihosting call: HLT #0xF000
	WORD	$0xD4600000 | (0xF000 << 5)  // HLT #0xF000

	RET

// SemihostingExit()
// ARM Semihosting SYS_EXIT call with exit code 0
// If semihosting fails (not enabled), prints "Kernel Exit" and busy-waits
TEXT SemihostingExit(SB), NOSPLIT, $16-0
	MOVD	ZR, R1			// R1 = exit code 0
	MOVW	$0x18, R0		// R0 = SYS_EXIT
	WORD	$0xD45E0000		// HLT #0xF000

	// If we get here, semihosting failed - print "Kernel Exit" and busy-wait
	// Print 'K'
	MOVD	$'K', R0
	UART_PUTC_SAFE
	// Print 'e'
	MOVD	$'e', R0
	UART_PUTC_SAFE
	// Print 'r'
	MOVD	$'r', R0
	UART_PUTC_SAFE
	// Print 'n'
	MOVD	$'n', R0
	UART_PUTC_SAFE
	// Print 'e'
	MOVD	$'e', R0
	UART_PUTC_SAFE
	// Print 'l'
	MOVD	$'l', R0
	UART_PUTC_SAFE
	// Print ' '
	MOVD	$' ', R0
	UART_PUTC_SAFE
	// Print 'E'
	MOVD	$'E', R0
	UART_PUTC_SAFE
	// Print 'x'
	MOVD	$'x', R0
	UART_PUTC_SAFE
	// Print 'i'
	MOVD	$'i', R0
	UART_PUTC_SAFE
	// Print 't'
	MOVD	$'t', R0
	UART_PUTC_SAFE
	// Print newline
	MOVD	$'\n', R0
	UART_PUTC_SAFE

semihosting_halt:
	B	semihosting_halt	// Busy wait forever

// ============================================================================
// Test/Debug Functions
// ============================================================================

// jump_to_null()
// Jumps to address 0 to trigger a prefetch abort
// Used for testing exception handler traceback functionality
TEXT jump_to_null(SB), NOSPLIT|NOFRAME, $0-0
	MOVD	ZR, R0			// Load address 0
	JMP	(R0)			// Branch to NULL - will cause prefetch abort
