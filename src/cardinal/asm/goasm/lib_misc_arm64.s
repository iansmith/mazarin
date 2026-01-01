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

// ============================================================================
// PL011 UART Functions for QEMU virt machine
// ============================================================================

// PL011 UART constants
#define QEMU_UART_BASE		0x09000000
#define UART_DR_OFFSET		0x00	// Data Register
#define UART_FR_OFFSET		0x18	// Flag Register
#define UART_IBRD_OFFSET	0x24	// Integer Baud Rate Divisor
#define UART_FBRD_OFFSET	0x28	// Fractional Baud Rate Divisor
#define UART_LCRH_OFFSET	0x2C	// Line Control Register High
#define UART_CR_OFFSET		0x30	// Control Register
#define UART_IMSC_OFFSET	0x38	// Interrupt Mask Set/Clear Register
#define UART_DMACR_OFFSET	0x48	// DMA Control Register

// Bit definitions
#define CR_UARTEN		(1 << 0)	// UART Enable bit
#define CR_TXEN			(1 << 8)	// Transmit Enable bit
#define CR_RXEN			(1 << 9)	// Receive Enable bit
#define FR_BUSY			(1 << 3)	// BUSY bit in Flag Register
#define FR_TXFF			(1 << 5)	// Transmit FIFO Full bit
#define LCR_FEN			(1 << 4)	// FIFO Enable bit

// uart_init_pl011()
// Initializes the PL011 UART for QEMU virt machine
// Follows proper PL011 initialization sequence from specification
TEXT uart_init_pl011(SB), NOSPLIT, $0-0
	MOVD	$QEMU_UART_BASE, R1

	// Step 1: Disable UART (clear UARTEN bit)
	MOVW	UART_CR_OFFSET(R1), R2
	AND	$~CR_UARTEN, R2
	MOVW	R2, UART_CR_OFFSET(R1)
	DSB	$15			// Memory barrier

	// Step 2: Wait for any ongoing transmission to complete
wait_tx_complete:
	MOVW	UART_FR_OFFSET(R1), R2
	TST	$FR_BUSY, R2		// Test BUSY bit
	BNE	wait_tx_complete	// If busy, keep waiting

	// Step 3: Flush FIFOs (clear FEN bit in UARTLCR_H)
	MOVW	UART_LCRH_OFFSET(R1), R2
	AND	$~LCR_FEN, R2		// Clear FEN bit to flush FIFOs
	MOVW	R2, UART_LCRH_OFFSET(R1)
	DSB	$15

	// Step 4: Configure Baud Rate divisors
	// For QEMU, use simple divisors (115200 baud with 24MHz clock)
	MOVW	$1, R2			// IBRD = 1
	MOVW	R2, UART_IBRD_OFFSET(R1)
	MOVW	ZR, UART_FBRD_OFFSET(R1)	// FBRD = 0
	DSB	$15

	// Step 5: Configure Line Control (UARTLCR_H)
	// 8 data bits: WLEN = 3 (bits 5-6 = 0b11)
	// FIFO enabled: FEN = 1 (bit 4)
	// 1 stop bit: STP2 = 0 (bit 3)
	// No parity: PEN = 0 (bit 1)
	// Value: 0x70 (0b01110000)
	MOVW	$0x70, R2
	MOVW	R2, UART_LCRH_OFFSET(R1)
	DSB	$15

	// Step 6: Mask all interrupts (UARTIMSC)
	MOVW	$0x7FF, R2		// Mask all 11 interrupt sources
	MOVW	R2, UART_IMSC_OFFSET(R1)
	DSB	$15

	// Step 7: Disable DMA (UARTDMACR)
	MOVW	ZR, UART_DMACR_OFFSET(R1)
	DSB	$15

	// Step 8: Enable Transmitter (TXE bit)
	MOVW	$CR_TXEN, R2		// Enable TXE only
	MOVW	R2, UART_CR_OFFSET(R1)
	DSB	$15

	// Step 9: Enable UART (UARTEN bit) - must be last step
	MOVW	$(CR_TXEN | CR_UARTEN), R2	// Enable both TXE and UARTEN
	MOVW	R2, UART_CR_OFFSET(R1)
	DSB	$15			// Memory barrier to ensure enable is visible

	// Wait for UART to be ready by checking that it's not busy
wait_uart_ready:
	MOVW	UART_FR_OFFSET(R1), R2
	TST	$FR_BUSY, R2		// Check BUSY bit
	BNE	wait_uart_ready		// If busy, keep waiting

	// Verify UART is enabled by reading control register
	MOVW	UART_CR_OFFSET(R1), R2
	TST	$CR_UARTEN, R2		// Check UARTEN bit
	BEQ	uart_init_failed	// If not enabled, something went wrong

	RET

uart_init_failed:
	// UART initialization failed - loop forever
	HINT	$1			// WFI instruction
	B	uart_init_failed

// uart_putc_pl011(c byte)
// Sends a single character via PL011 UART
// Parameters: R0 = character to send (byte)
TEXT uart_putc_pl011(SB), NOSPLIT|NOFRAME, $0-1
	// R0 = character (already in R0 from register ABI)
	MOVD	$QEMU_UART_BASE, R1

	// Verify UART is enabled before writing
	// Check UARTEN bit (bit 0) and TXE bit (bit 8) in UART_CR
	MOVW	UART_CR_OFFSET(R1), R2
	TST	$CR_UARTEN, R2
	BEQ	uart_not_enabled	// If UARTEN not set, skip write
	TST	$CR_TXEN, R2
	BEQ	uart_not_enabled	// If TXE not set, skip write

check_tx_full:
	MOVW	UART_FR_OFFSET(R1), R2
	TST	$FR_TXFF, R2		// Test if TXFF bit (bit 5) is set
	BNE	check_tx_full		// If set, branch back and wait

	MOVB	R0, UART_DR_OFFSET(R1)	// Store the character
	RET

uart_not_enabled:
	// UART not enabled - just return (don't write)
	RET
