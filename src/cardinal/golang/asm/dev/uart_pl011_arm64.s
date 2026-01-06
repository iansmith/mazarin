// uart_pl011_arm64.s - PL011 UART Driver for QEMU virt machine
//
// This file contains the PL011 UART initialization and character output functions.
// The PL011 is the standard UART on QEMU's virt machine at 0x09000000.
//
// NOTE: These functions use Go 1.17+ register-based calling convention.
// Parameters arrive in R0, R1, etc. Return values go in R0.

#include "textflag.h"

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
