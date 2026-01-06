// exc_utils.s - Exception handling utility functions (Go/Plan9 assembly)
//
// This file contains:
// - print_hex64: Print 64-bit hex value to UART
// - print_string: Print null-terminated string to UART
// - print_decimal_uart: Print decimal number
// - print_hex_byte_uart: Print single hex byte
//
// Migrated from: asm/aarch64/exceptions.s

#include "textflag.h"

// UART base address for QEMU virt machine
#define UART_BASE 0x09000000

// ============================================================================
// BREADCRUMB EXIT MACRO - For Debugging Hangs
// ============================================================================
//
// BREADCRUMB_EXIT(char)
// Prints a single character to UART, then exits via semihosting.
// Useful for debugging hangs using binary search.
//
// Usage in assembly:
//   BREADCRUMB_EXIT($'A')  // Prints "A\r\n" then exits
//
// Important: char must be an immediate value (e.g., $'X', not a register)
#define BREADCRUMB_EXIT(char) \
	MOVD	$0x09000000, R10; \
	MOVW	char, R0; \
	MOVW	R0, 0(R10); \
	MOVW	$'\r', R0; \
	MOVW	R0, 0(R10); \
	MOVW	$'\n', R0; \
	MOVW	R0, 0(R10); \
	MOVD	$0x20026, R1; \
	MOVD	R1, (RSP); \
	MOVD	ZR, 8(RSP); \
	MOVD	RSP, R1; \
	MOVW	$0x18, R0; \
	WORD	$0xD45E0000

// ============================================================================
// SAFE DEBUG PRINTING FUNCTIONS
// ============================================================================
//
// These functions print to UART while preserving ALL registers.
// CRITICAL: All functions save/restore x0-x30 to ensure caller state unchanged.

// print_hex64: Print a 64-bit value as 16 hex digits
// Input: R0 = value to print
// Preserves: ALL registers (R0-R30)
TEXT print_hex64(SB), NOSPLIT, $64-0
	// Save ALL caller-saved and callee-saved registers
	MOVD	R29, -8(RSP)
	MOVD	R30, -16(RSP)
	MOVD	R0, -24(RSP)
	MOVD	R1, -32(RSP)
	MOVD	R2, -40(RSP)
	MOVD	R3, -48(RSP)
	MOVD	R4, -56(RSP)
	MOVD	R5, -64(RSP)

	MOVD	R0, R4			// R4 = value to print
	MOVD	$16, R5			// R5 = digit counter

hex64_loop:
	// Get top nibble: shift right by 60 bits
	LSR	$60, R4, R0
	AND	$0xF, R0, R0

	// Convert to hex character
	CMP	$10, R0
	BLT	hex64_digit
	ADD	$0x37, R0, R0		// 'A'-10 = 0x41-10 = 0x37
	B	hex64_print

hex64_digit:
	ADD	$0x30, R0, R0		// '0' = 0x30

hex64_print:
	MOVD	$UART_BASE, R1
	MOVB	R0, (R1)

	// Shift for next nibble
	LSL	$4, R4, R4
	SUB	$1, R5, R5
	CBNZ	R5, hex64_loop

	// Restore ALL registers
	MOVD	-64(RSP), R5
	MOVD	-56(RSP), R4
	MOVD	-48(RSP), R3
	MOVD	-40(RSP), R2
	MOVD	-32(RSP), R1
	MOVD	-24(RSP), R0
	MOVD	-16(RSP), R30
	MOVD	-8(RSP), R29
	RET

// print_string: Print a null-terminated string
// Input: R0 = pointer to string
// Preserves: ALL registers
TEXT print_string(SB), NOSPLIT, $48-0
	MOVD	R29, -8(RSP)
	MOVD	R30, -16(RSP)
	MOVD	R0, -24(RSP)
	MOVD	R1, -32(RSP)
	MOVD	R2, -40(RSP)
	MOVD	R3, -48(RSP)

	MOVD	R0, R2			// R2 = string pointer
	MOVD	$UART_BASE, R3		// R3 = UART base

string_loop:
	MOVBU	(R2), R0		// Load byte, R0 = *R2
	CBZ	R0, string_done		// If null, done
	MOVB	R0, (R3)		// Write to UART
	ADD	$1, R2			// Increment pointer
	B	string_loop

string_done:
	MOVD	-48(RSP), R3
	MOVD	-40(RSP), R2
	MOVD	-32(RSP), R1
	MOVD	-24(RSP), R0
	MOVD	-16(RSP), R30
	MOVD	-8(RSP), R29
	RET

// print_decimal_uart: Print decimal number to UART
// Input: R0 = UART address (unused, we use constant), R1 = number to print
// Preserves: R0-R5
// Clobbers: R6, R7
TEXT print_decimal_uart(SB), NOSPLIT, $80-0
	MOVD	R29, -8(RSP)
	MOVD	R30, -16(RSP)
	MOVD	R0, -24(RSP)
	MOVD	R1, -32(RSP)
	MOVD	R2, -40(RSP)
	MOVD	R3, -48(RSP)
	MOVD	R4, -56(RSP)
	MOVD	R5, -64(RSP)

	MOVD	R1, R2			// R2 = number to print
	MOVD	$10, R3			// R3 = divisor
	MOVD	$UART_BASE, R0		// R0 = UART base

	// Handle zero special case
	CBNZ	R2, decimal_nonzero
	MOVD	$0x30, R5		// '0'
	MOVB	R5, (R0)
	B	decimal_done

decimal_nonzero:
	// Use stack for digit buffer (up to 20 digits for 64-bit)
	// Stack layout: RSP-80 to RSP-60 = 20 bytes for digits
	SUB	$80, RSP, R4		// R4 = buffer start
	MOVD	R4, R5			// R5 = current position

decimal_convert_loop:
	UDIV	R3, R2, R6		// R6 = number / 10
	MSUB	R6, R3, R2, R7		// R7 = number % 10
	ADD	$0x30, R7, R7		// Convert to ASCII
	MOVB	R7, (R5)		// Store digit
	ADD	$1, R5			// Advance position
	MOVD	R6, R2			// number = number / 10
	CBNZ	R2, decimal_convert_loop

	// Print digits in reverse order
decimal_print_loop:
	SUB	$1, R5			// Move back one digit
	MOVBU	(R5), R6		// Load digit
	MOVB	R6, (R0)		// Print to UART
	CMP	R4, R5			// At start?
	BNE	decimal_print_loop

decimal_done:
	MOVD	-64(RSP), R5
	MOVD	-56(RSP), R4
	MOVD	-48(RSP), R3
	MOVD	-40(RSP), R2
	MOVD	-32(RSP), R1
	MOVD	-24(RSP), R0
	MOVD	-16(RSP), R30
	MOVD	-8(RSP), R29
	RET

// print_hex_byte_uart: Print byte as 2 hex digits to UART
// Input: R1 = UART address (unused), R2 = byte to print
// Preserves: R0-R3
TEXT print_hex_byte_uart(SB), NOSPLIT, $32-0
	MOVD	R29, -8(RSP)
	MOVD	R30, -16(RSP)
	MOVD	R0, -24(RSP)
	MOVD	R3, -32(RSP)

	MOVD	$UART_BASE, R1
	AND	$0xFF, R2, R2		// Mask to byte

	// High nibble
	LSR	$4, R2, R3
	CMP	$10, R3
	BLT	hex_byte_digit1
	ADD	$0x37, R3, R3		// 'A'-10
	B	hex_byte_print1

hex_byte_digit1:
	ADD	$0x30, R3, R3		// '0'

hex_byte_print1:
	MOVB	R3, (R1)

	// Low nibble
	AND	$0xF, R2, R3
	CMP	$10, R3
	BLT	hex_byte_digit2
	ADD	$0x37, R3, R3
	B	hex_byte_print2

hex_byte_digit2:
	ADD	$0x30, R3, R3

hex_byte_print2:
	MOVB	R3, (R1)

	MOVD	-32(RSP), R3
	MOVD	-24(RSP), R0
	MOVD	-16(RSP), R30
	MOVD	-8(RSP), R29
	RET
