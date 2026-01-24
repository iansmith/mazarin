// semihosting_arm64.s - QEMU Semihosting Functions
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This file contains debugging and testing functions for QEMU semihosting support.
// Semihosting allows communication with the QEMU host for exit and debugging.
//
// NOTE: Semihosting is currently DISABLED due to QEMU internal issues.
// Functions fall back to busy-wait loops instead.
//
// Functions:
//   - qemu_exit() - Exit QEMU (disabled, busy-waits instead)
//   - SemihostingExit() - Exit with "Kernel Exit" message
//   - jump_to_null() - Trigger prefetch abort for testing exception handlers
//   - breadcrumb_exit(c byte) - Print character and exit (for debugging hangs)
//
// Macros:
//   - UART_PUTC_SAFE - Safe UART character output (preserves registers)
//
// ABI NOTES:
// - These functions use Go 1.17+ register-based calling convention
// - Parameters arrive in R0, R1, etc.
// - These are debugging/testing functions, not performance-critical

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

// ============================================================================
// qemu_exit() - Exit QEMU via Semihosting
// ============================================================================
// Exit QEMU using semihosting
// Requires QEMU to be run with -semihosting flag
//
// NOTE: Semihosting is DISABLED - this function busy-waits instead
//
// Segments:
//   1. Set up semihosting parameter block on stack
//   2. Prepare semihosting call registers (disabled)
//   3. Busy-wait loop (fallback when semihosting disabled)
//
TEXT qemu_exit(SB), NOSPLIT, $16-0
	// Segment 1: Set up parameter block
	// Reserve 16 bytes: [0] = reason code, [8] = status code

	// Store exit reason code: ADP_Stopped_ApplicationExit (0x20026)
	MOVD	$0x20026, R1
	MOVD	R1, (RSP)		// Store reason at SP+0

	// Store status code: 0 (success)
	MOVD	ZR, 8(RSP)		// Store 0 at SP+8

	// Segment 2: Prepare semihosting call (disabled)
	MOVD	RSP, R1			// R1 = pointer to parameter block
	MOVW	$0x18, R0		// R0 = SYS_EXIT (0x18)

	// Trigger semihosting call: HLT #0xF000
	// ARM64 HLT encoding: 1101 0100 010 imm16 00000
	// For imm16=0xF000: 0xD45E0000
	// DISABLED: Semihosting causes QEMU internal problems
	// WORD	$0xD45E0000

	// Segment 3: Busy-wait (fallback)
qemu_exit_halt:
	B	qemu_exit_halt

	RET

// ============================================================================
// SemihostingExit() - Exit with "Kernel Exit" Message
// ============================================================================
// ARM Semihosting SYS_EXIT call with exit code 0
// If semihosting fails (not enabled), prints "Kernel Exit" and busy-waits
//
// Segments:
//   1. Wait for UART TX FIFO to empty
//   2. Wait for UART not busy
//   3. Set up semihosting parameter block
//   4. Print "Kernel Exit" message to UART
//   5. Busy-wait loop (fallback when semihosting disabled)
//
TEXT SemihostingExit(SB), NOSPLIT, $16-0
	// Segment 1: Wait for UART TX FIFO to empty
	// PL011 UART Flag Register (FR) is at UART_BASE + 0x18
	MOVD	$0x09000000, R2		// UART base
	ADD	$0x18, R2, R3		// R3 = UART_FR address

	// Wait for TX FIFO to be empty (bit 7 = TXFE)
semihosting_txfe_wait:
	MOVW	(R3), R4		// Read Flag Register
	AND	$0x80, R4, R4		// Isolate TXFE bit (bit 7)
	CBZ	R4, semihosting_txfe_wait	// Loop while TXFE=0 (FIFO not empty)

	// Segment 2: Wait for UART not busy
semihosting_busy_wait:
	MOVW	(R3), R4		// Read Flag Register
	AND	$8, R4, R4		// Isolate BUSY bit (bit 3)
	CBNZ	R4, semihosting_busy_wait	// Loop while BUSY=1

	// Segment 3: Set up semihosting parameter block
	// [SP+0] = exit reason code: ADP_Stopped_ApplicationExit (0x20026)
	// [SP+8] = status code: 0 (success)
	MOVD	$0x20026, R1
	MOVD	R1, (RSP)		// Store reason at SP+0
	MOVD	ZR, 8(RSP)		// Store status 0 at SP+8

	// Prepare semihosting call (disabled)
	MOVD	RSP, R1			// R1 = pointer to parameter block
	MOVW	$0x18, R0		// R0 = SYS_EXIT
	// DISABLED: Semihosting causes QEMU internal problems
	// WORD	$0xD45E0000		// HLT #0xF000

	// Segment 4: Print "Kernel Exit" message
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

	// Segment 5: Busy-wait (fallback)
semihosting_halt:
	B	semihosting_halt	// Busy wait forever

// ============================================================================
// Test/Debug Functions
// ============================================================================

// ============================================================================
// jump_to_null() - Trigger Prefetch Abort for Testing
// ============================================================================
// Jumps to address 0 to trigger a prefetch abort
// Used for testing exception handler traceback functionality
//
// Segments:
//   1. Load address 0 into R0
//   2. Jump to NULL (triggers exception)
//
TEXT jump_to_null(SB), NOSPLIT|NOFRAME, $0-0
	// Segment 1: Load NULL address
	MOVD	ZR, R0			// Load address 0

	// Segment 2: Jump to NULL
	JMP	(R0)			// Branch to NULL - will cause prefetch abort

// ============================================================================
// Breadcrumb Exit (Debugging Aid)
// ============================================================================

// ============================================================================
// breadcrumb_exit(c byte) - Debug Helper: Print Character and Exit
// ============================================================================
// Prints a single character to UART, then exits via semihosting.
// Useful for debugging hangs - place calls at suspected locations to narrow
// down where the kernel hangs using binary search.
//
// The character is printed directly to UART, then semihosting exit flushes
// all buffers, guaranteeing the output appears even if QEMU times out.
//
// Example: breadcrumb_exit('X') will print "X\r\n" then exit QEMU
//
// Usage in Go:
//   import "cardinal/asm"
//   func debugFunction() {
//       dev.BreadcrumbExit('A')  // Prints "A\r\n" and exits
//   }
//
// Segments:
//   1. Load character argument from stack
//   2. Print character, CR, and LF to UART
//   3. Wait for UART transmission complete
//   4. Issue memory barrier
//   5. Set up semihosting parameter block (disabled)
//   6. Busy-wait loop (fallback when semihosting disabled)
//
TEXT breadcrumb_exit(SB), NOSPLIT, $16-1
	// Segment 1: Load character argument
	MOVBU	c+0(FP), R0

	// Segment 2: Print character, CR, and LF to UART
	MOVD	$0x09000000, R10	// UART base address
	MOVW	R0, 0(R10)		// Write character

	// Print carriage return
	MOVW	$'\r', R0
	MOVW	R0, 0(R10)

	// Print newline
	MOVW	$'\n', R0
	MOVW	R0, 0(R10)

	// Segment 3: Wait for UART transmission complete
	// PL011 Flag Register is at UART_BASE + 0x18
	// FR_BUSY (bit 3) = 1 means transmission in progress
	ADD	$0x18, R10, R11		// R11 = UART_FR address (0x09000018)
wait_uart_tx:
	MOVW	0(R11), R0		// Read Flag Register
	AND	$8, R0, R0		// Isolate FR_BUSY (bit 3)
	CBNZ	R0, wait_uart_tx	// Loop while BUSY

	// Segment 4: Memory barrier
	DSB	$15

	// Segment 5: Set up semihosting (disabled)
	// Set up parameter block on stack
	MOVD	$0x20026, R1		// ADP_Stopped_ApplicationExit
	MOVD	R1, (RSP)		// Store reason at SP+0
	MOVD	ZR, 8(RSP)		// Store status 0 at SP+8

	// Execute SYS_EXIT semihosting call
	MOVD	RSP, R1			// R1 = pointer to parameter block
	MOVW	$0x18, R0		// R0 = SYS_EXIT (0x18)

	// HLT #0xF000 - semihosting call
	// ARM64 HLT encoding: 1101 0100 010 imm16 00000
	// For imm16=0xF000: 0xD45E0000
	// DISABLED: Semihosting causes QEMU internal problems
	// WORD	$0xD45E0000

	// Segment 6: Busy-wait (fallback)
breadcrumb_exit_halt:
	B	breadcrumb_exit_halt
