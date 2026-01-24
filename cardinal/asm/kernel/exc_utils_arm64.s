// exc_utils_arm64.s - Exception Handling Debug Utilities
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This file contains self-contained debug printing functions for exception handling.
// These functions run during exception handling when the system may be in an
// inconsistent state, so they MUST NOT call other functions or depend on external state.
//
// Functions:
//   - print_hex64(val uint64)      - Print 64-bit value as 16 hex digits
//   - print_string(s uintptr)      - Print null-terminated string
//   - print_decimal_uart(val uint64) - Print decimal number
//   - print_hex_byte_uart(val byte)  - Print byte as 2 hex digits
//
// Macros:
//   - BREADCRUMB_EXIT(char) - Print character and exit (for debugging hangs)
//
// CRITICAL: All functions save and restore ALL registers (R0-R30)
// This ensures exception handlers can print debug info without corrupting state.
//
// WHY NOT DECOMPOSE:
// These functions cannot be broken into smaller primitives because:
//   1. They run during exception handling when system state may be corrupted
//   2. Any function calls risk further corruption or infinite recursion
//   3. Self-contained implementations guarantee safe execution
//   4. Register preservation must be complete and explicit
//
// ABI NOTES:
// - These are package-local functions using register-based ABI
// - R0 contains primary input parameter (value or pointer)
// - R1, R2 may contain additional parameters
// - ALL registers preserved (saved at entry, restored at exit)

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

// ============================================================================
// print_hex64(val uint64) - Print 64-bit Value as Hex
// ============================================================================
// Prints a 64-bit value as 16 hexadecimal digits to UART
// Parameters (register ABI):
//   R0: value to print
// Preserves: ALL registers (R0-R30)
//
// Segments:
//   1. Save all working registers to stack
//   2. Initialize loop counter and working value
//   3. Loop 16 times: extract nibble, convert to hex, print
//   4. Restore all registers
//   5. Return to caller
//
TEXT print_hex64(SB), NOSPLIT, $64-0
	// Segment 1: Save all working registers
	MOVD	R29, -8(RSP)
	MOVD	R30, -16(RSP)
	MOVD	R0, -24(RSP)
	MOVD	R1, -32(RSP)
	MOVD	R2, -40(RSP)
	MOVD	R3, -48(RSP)
	MOVD	R4, -56(RSP)
	MOVD	R5, -64(RSP)

	// Segment 2: Initialize loop
	MOVD	R0, R4			// R4 = value to print
	MOVD	$16, R5			// R5 = digit counter (16 hex digits)

	// Segment 3: Loop to print 16 hex digits
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

	// Segment 4: Restore all registers
	MOVD	-64(RSP), R5
	MOVD	-56(RSP), R4
	MOVD	-48(RSP), R3
	MOVD	-40(RSP), R2
	MOVD	-32(RSP), R1
	MOVD	-24(RSP), R0
	MOVD	-16(RSP), R30
	MOVD	-8(RSP), R29

	// Segment 5: Return
	RET

// ============================================================================
// print_string(s uintptr) - Print Null-Terminated String
// ============================================================================
// Prints a null-terminated string to UART
// Parameters (register ABI):
//   R0: pointer to null-terminated string
// Preserves: ALL registers
//
// Segments:
//   1. Save all working registers to stack
//   2. Initialize string pointer and UART address
//   3. Loop: load byte, check for null, print if non-null
//   4. Restore all registers
//   5. Return to caller
//
TEXT print_string(SB), NOSPLIT, $48-0
	// Segment 1: Save all working registers
	MOVD	R29, -8(RSP)
	MOVD	R30, -16(RSP)
	MOVD	R0, -24(RSP)
	MOVD	R1, -32(RSP)
	MOVD	R2, -40(RSP)
	MOVD	R3, -48(RSP)

	// Segment 2: Initialize pointers
	MOVD	R0, R2			// R2 = string pointer
	MOVD	$UART_BASE, R3		// R3 = UART base

	// Segment 3: Loop until null terminator
string_loop:
	MOVBU	(R2), R0		// Load byte, R0 = *R2
	CBZ	R0, string_done		// If null, done
	MOVB	R0, (R3)		// Write to UART
	ADD	$1, R2			// Increment pointer
	B	string_loop

string_done:
	// Segment 4: Restore all registers
	MOVD	-48(RSP), R3
	MOVD	-40(RSP), R2
	MOVD	-32(RSP), R1
	MOVD	-24(RSP), R0
	MOVD	-16(RSP), R30
	MOVD	-8(RSP), R29

	// Segment 5: Return
	RET

// ============================================================================
// print_decimal_uart(val uint64) - Print Decimal Number
// ============================================================================
// Prints a decimal number to UART
// Parameters (register ABI):
//   R0: UART address (unused - uses hardcoded UART_BASE)
//   R1: number to print
// Preserves: R0-R5
//
// Segments:
//   1. Save all working registers to stack
//   2. Initialize UART address and divisor (10)
//   3. Handle zero special case
//   4. Convert number to decimal digits (store on stack in reverse)
//   5. Print digits in correct order
//   6. Restore all registers
//   7. Return to caller
//
TEXT print_decimal_uart(SB), NOSPLIT, $80-0
	// Segment 1: Save all working registers
	MOVD	R29, -8(RSP)
	MOVD	R30, -16(RSP)
	MOVD	R0, -24(RSP)
	MOVD	R1, -32(RSP)
	MOVD	R2, -40(RSP)
	MOVD	R3, -48(RSP)
	MOVD	R4, -56(RSP)
	MOVD	R5, -64(RSP)

	// Segment 2: Initialize for decimal conversion
	MOVD	R1, R2			// R2 = number to print
	MOVD	$10, R3			// R3 = divisor
	MOVD	$UART_BASE, R0		// R0 = UART base

	// Segment 3: Handle zero special case
	CBNZ	R2, decimal_nonzero
	MOVD	$0x30, R5		// '0'
	MOVB	R5, (R0)
	B	decimal_done

decimal_nonzero:
	// Segment 4: Convert to decimal digits (store in reverse on stack)
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

	// Segment 5: Print digits in reverse order (correct direction)
decimal_print_loop:
	SUB	$1, R5			// Move back one digit
	MOVBU	(R5), R6		// Load digit
	MOVB	R6, (R0)		// Print to UART
	CMP	R4, R5			// At start?
	BNE	decimal_print_loop

decimal_done:
	// Segment 6: Restore all registers
	MOVD	-64(RSP), R5
	MOVD	-56(RSP), R4
	MOVD	-48(RSP), R3
	MOVD	-40(RSP), R2
	MOVD	-32(RSP), R1
	MOVD	-24(RSP), R0
	MOVD	-16(RSP), R30
	MOVD	-8(RSP), R29

	// Segment 7: Return
	RET

// ============================================================================
// print_hex_byte_uart(val byte) - Print Byte as 2 Hex Digits
// ============================================================================
// Prints a byte as 2 hexadecimal digits to UART
// Parameters (register ABI):
//   R1: UART address (unused - uses hardcoded UART_BASE)
//   R2: byte to print
// Preserves: R0-R3
//
// Segments:
//   1. Save working registers to stack
//   2. Initialize UART address and mask byte
//   3. Print high nibble (upper 4 bits)
//   4. Print low nibble (lower 4 bits)
//   5. Restore registers
//   6. Return to caller
//
TEXT print_hex_byte_uart(SB), NOSPLIT, $32-0
	// Segment 1: Save working registers
	MOVD	R29, -8(RSP)
	MOVD	R30, -16(RSP)
	MOVD	R0, -24(RSP)
	MOVD	R3, -32(RSP)

	// Segment 2: Initialize UART and mask byte
	MOVD	$UART_BASE, R1
	AND	$0xFF, R2, R2		// Mask to byte

	// Segment 3: Print high nibble (upper 4 bits)
	LSR	$4, R2, R3
	CMP	$10, R3
	BLT	hex_byte_digit1
	ADD	$0x37, R3, R3		// 'A'-10
	B	hex_byte_print1

hex_byte_digit1:
	ADD	$0x30, R3, R3		// '0'

hex_byte_print1:
	MOVB	R3, (R1)

	// Segment 4: Print low nibble (lower 4 bits)
	AND	$0xF, R2, R3
	CMP	$10, R3
	BLT	hex_byte_digit2
	ADD	$0x37, R3, R3
	B	hex_byte_print2

hex_byte_digit2:
	ADD	$0x30, R3, R3

hex_byte_print2:
	MOVB	R3, (R1)

	// Segment 5: Restore registers
	MOVD	-32(RSP), R3
	MOVD	-24(RSP), R0
	MOVD	-16(RSP), R30
	MOVD	-8(RSP), R29

	// Segment 6: Return
	RET
