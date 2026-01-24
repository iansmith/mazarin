// uart_pl011_arm64.s - PL011 UART Driver for QEMU virt machine
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This file contains PL011 UART driver functions for QEMU virt machine.
// The PL011 is the standard UART peripheral at 0x09000000.
//
// Functions:
//   - uart_init_pl011() - Initialize UART following PL011 specification
//   - uart_putc_pl011(c byte) - Output single character to UART
//
// Initialization follows proper 9-step PL011 sequence:
//   1. Disable UART (clear UARTEN)
//   2. Wait for transmission complete
//   3. Flush FIFOs
//   4. Configure baud rate divisors
//   5. Configure line control (8N1 with FIFO)
//   6. Mask all interrupts
//   7. Disable DMA
//   8. Enable transmitter
//   9. Enable UART
//
// ABI NOTES:
// - These functions use Go 1.17+ register-based calling convention
// - Parameters arrive in R0, R1, etc.

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

// ============================================================================
// uart_init_pl011() - Initialize PL011 UART
// ============================================================================
// Initializes the PL011 UART for QEMU virt machine
// Follows proper PL011 initialization sequence from specification
//
// Segments:
//   1. Load UART base address
//   2. Disable UART (clear UARTEN bit)
//   3. Wait for transmission complete
//   4. Flush FIFOs (clear FEN bit)
//   5. Configure baud rate divisors
//   6. Configure line control (8N1 with FIFO)
//   7. Mask all interrupts
//   8. Disable DMA
//   9. Enable transmitter (TXE bit)
//  10. Enable UART (UARTEN bit)
//  11. Wait for UART ready
//  12. Verify UART is enabled
//  13. Return to caller (or loop on failure)
//
TEXT uart_init_pl011(SB), NOSPLIT, $0-0
	// Segment 1: Load UART base address
	MOVD	$QEMU_UART_BASE, R1

	// Segment 2: Disable UART
	MOVW	UART_CR_OFFSET(R1), R2
	AND	$~CR_UARTEN, R2
	MOVW	R2, UART_CR_OFFSET(R1)
	DSB	$15			// Memory barrier

	// Segment 3: Wait for transmission complete
wait_tx_complete:
	MOVW	UART_FR_OFFSET(R1), R2
	TST	$FR_BUSY, R2		// Test BUSY bit
	BNE	wait_tx_complete	// If busy, keep waiting

	// Segment 4: Flush FIFOs
	MOVW	UART_LCRH_OFFSET(R1), R2
	AND	$~LCR_FEN, R2		// Clear FEN bit to flush FIFOs
	MOVW	R2, UART_LCRH_OFFSET(R1)
	DSB	$15

	// Segment 5: Configure baud rate divisors
	// For QEMU, use simple divisors (115200 baud with 24MHz clock)
	MOVW	$1, R2			// IBRD = 1
	MOVW	R2, UART_IBRD_OFFSET(R1)
	MOVW	ZR, UART_FBRD_OFFSET(R1)	// FBRD = 0
	DSB	$15

	// Segment 6: Configure line control (8N1 with FIFO)
	// 8 data bits: WLEN = 3 (bits 5-6 = 0b11)
	// FIFO enabled: FEN = 1 (bit 4)
	// 1 stop bit: STP2 = 0 (bit 3)
	// No parity: PEN = 0 (bit 1)
	// Value: 0x70 (0b01110000)
	MOVW	$0x70, R2
	MOVW	R2, UART_LCRH_OFFSET(R1)
	DSB	$15

	// Segment 7: Mask all interrupts
	MOVW	$0x7FF, R2		// Mask all 11 interrupt sources
	MOVW	R2, UART_IMSC_OFFSET(R1)
	DSB	$15

	// Segment 8: Disable DMA
	MOVW	ZR, UART_DMACR_OFFSET(R1)
	DSB	$15

	// Segment 9: Enable transmitter
	MOVW	$CR_TXEN, R2		// Enable TXE only
	MOVW	R2, UART_CR_OFFSET(R1)
	DSB	$15

	// Segment 10: Enable UART (must be last)
	MOVW	$(CR_TXEN | CR_UARTEN), R2	// Enable both TXE and UARTEN
	MOVW	R2, UART_CR_OFFSET(R1)
	DSB	$15			// Memory barrier to ensure enable is visible

	// Segment 11: Wait for UART ready
wait_uart_ready:
	MOVW	UART_FR_OFFSET(R1), R2
	TST	$FR_BUSY, R2		// Check BUSY bit
	BNE	wait_uart_ready		// If busy, keep waiting

	// Segment 12: Verify UART is enabled
	MOVW	UART_CR_OFFSET(R1), R2
	TST	$CR_UARTEN, R2		// Check UARTEN bit
	BEQ	uart_init_failed	// If not enabled, something went wrong

	// Segment 13: Return
	RET

uart_init_failed:
	// Initialization failed - loop forever
	HINT	$1			// WFI instruction
	B	uart_init_failed

// ============================================================================
// uart_putc_pl011(c byte) - Output Character to UART
// ============================================================================
// Sends a single character via PL011 UART
// Parameters: R0 = character to send (byte)
//
// Segments:
//   1. Load UART base address
//   2. Verify UART is enabled (UARTEN and TXE bits)
//   3. Wait for TX FIFO not full
//   4. Write character to data register
//   5. Return to caller
//
TEXT uart_putc_pl011(SB), NOSPLIT|NOFRAME, $0-1
	// Segment 1: Load UART base address
	// R0 = character (already in R0 from register ABI)
	MOVD	$QEMU_UART_BASE, R1

	// Segment 2: Verify UART is enabled
	// Check UARTEN bit (bit 0) and TXE bit (bit 8) in UART_CR
	MOVW	UART_CR_OFFSET(R1), R2
	TST	$CR_UARTEN, R2
	BEQ	uart_not_enabled	// If UARTEN not set, skip write
	TST	$CR_TXEN, R2
	BEQ	uart_not_enabled	// If TXE not set, skip write

	// Segment 3: Wait for TX FIFO not full
check_tx_full:
	MOVW	UART_FR_OFFSET(R1), R2
	TST	$FR_TXFF, R2		// Test if TXFF bit (bit 5) is set
	BNE	check_tx_full		// If set, branch back and wait

	// Segment 4: Write character
	MOVB	R0, UART_DR_OFFSET(R1)	// Store the character

	// Segment 5: Return
	RET

uart_not_enabled:
	// UART not enabled - just return (don't write)
	RET
