// lib_misc.s - Miscellaneous utility functions in Go/Plan9 assembly
//
// This file contains memory operations, UART, QEMU exit, and other utilities.
//
// Migrated from asm/aarch64/lib.s

#include "textflag.h"

// UART debug output macro
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
// Memory Functions
// ============================================================================

// bzero(ptr unsafe.Pointer, size uint32)
// Zeroes size bytes starting at ptr
// OPTIMIZED: Uses 128-bit stores (STP) for 16x speedup
//
// NOTE: Go's internal ABI passes arguments in REGISTERS (x0, w1), not on stack.
// The linkname directive connects asm.Bzero to this function.
// When called from Go, arguments arrive in: R0 = ptr, R1 = size (lower 32 bits)
// We do NOT use FP-relative addressing since that doesn't work with Go's ABI.
TEXT bzero(SB), NOSPLIT|NOFRAME, $0-12
	// Arguments arrive in registers: R0 = ptr, R1 = size
	// No need to load from stack - Go's register ABI passes them directly!

	// R0 = ptr, R1 = size (already in registers from Go ABI)
	CBZ	R1, bzero_done
	MOVD	ZR, R2			// Zero value
	MOVD	ZR, R3			// Zero value (for pair store)

bzero_loop_16:
	CMP	$16, R1
	BLT	bzero_loop_8
	STP	(R2, R3), (R0)
	ADD	$16, R0
	SUB	$16, R1
	B	bzero_loop_16

bzero_loop_8:
	CMP	$8, R1
	BLT	bzero_loop_1
	MOVD	R2, (R0)
	ADD	$8, R0
	SUB	$8, R1
	B	bzero_loop_8

bzero_loop_1:
	CBZ	R1, bzero_done
	MOVB	R2, (R0)
	ADD	$1, R0
	SUB	$1, R1
	B	bzero_loop_1

bzero_done:
	RET

// MemmoveBytes(dest unsafe.Pointer, src unsafe.Pointer, n uint32)
// Copy n bytes from src to dest
// Optimized for speed using 16-byte chunks
//
// NOTE: Go 1.17+ register-based ABI:
//   R0 = dest pointer
//   R1 = src pointer
//   R2 = n (lower 32 bits used)
TEXT MemmoveBytes(SB), NOSPLIT|NOFRAME, $0-20
	// Arguments already in registers from Go ABI:
	// R0 = dest, R1 = src, R2 = size (as uint32)
	CBZ	R2, memmove_done

	// Check if we can do 16-byte copies
	CMP	$16, R2
	BLT	memmove_bytes

memmove_16:
	LDP	(R1), (R3, R4)		// Load 16 bytes
	ADD	$16, R1
	STP	(R3, R4), (R0)		// Store 16 bytes
	ADD	$16, R0
	SUB	$16, R2
	CMP	$16, R2
	BGE	memmove_16

memmove_bytes:
	CBZ	R2, memmove_done
	MOVBU	(R1), R3
	ADD	$1, R1
	MOVB	R3, (R0)
	ADD	$1, R0
	SUB	$1, R2
	B	memmove_bytes

memmove_done:
	RET

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
