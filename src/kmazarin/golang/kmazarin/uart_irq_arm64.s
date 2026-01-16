//go:build qemuvirt && aarch64

#include "textflag.h"

// ============================================================================
// UART Interrupt Handlers - Pure Assembly (NO Go Calls!)
// ============================================================================
//
// These handlers are called from the exception handler when UART interrupts fire.
// They must be pure assembly with no Go function calls because:
//   1. We're on the exception stack (SP_EL1), not a goroutine stack
//   2. R28 (g) points to the interrupted goroutine, which may not be related
//   3. Calling Go code from exception context violates runtime assumptions
//
// Instead, these handlers:
//   1. Drain hardware FIFOs to/from ring buffers (direct memory access)
//   2. Set atomic flags (direct memory stores)
//   3. Return immediately
//
// The bottom half processor goroutines (running in safe Go context) will
// then process the buffered data.

// UART register offsets (from PL011 spec)
#define UART_DR   0x00  // Data register
#define UART_FR   0x18  // Flag register
#define UART_IMSC 0x38  // Interrupt mask set/clear
#define UART_ICR  0x44  // Interrupt clear

// Flag register bits
#define FR_RXFE   0x10  // RX FIFO empty (bit 4)
#define FR_TXFF   0x20  // TX FIFO full (bit 5)

// Interrupt bits
#define IRQ_RX    0x10  // RX interrupt (bit 4)
#define IRQ_TX    0x20  // TX interrupt (bit 5)

// ============================================================================
// uartRxIRQHandler - Handle UART RX Interrupt
// ============================================================================
//
// Called when UART has received data (RX FIFO not empty).
// Drains the RX FIFO into the ring buffer and sets the uartRxPending flag.
//
// Input: None (reads from UART hardware and global variables)
// Output: None (writes to global variables)
// Clobbers: R10-R17
//
TEXT ·uartRxIRQHandler(SB), NOSPLIT|NOFRAME, $0
	// Load UART base address (high memory mapping)
	MOVD	$0xFFFFFFFF09000000, R10

	// Load ring buffer pointers
	MOVW	·uartRxRingHead(SB), R11  // head (read by Go)
	MOVW	·uartRxRingTail(SB), R12  // tail (written by IRQ)

rx_drain_loop:
	// Check if RX FIFO is empty
	MOVW	UART_FR(R10), R13
	TST	$FR_RXFE, R13
	BNE	rx_fifo_empty

	// Check if ring buffer is full
	// Full condition: (tail + 1) & mask == head
	ADD	$1, R12, R14              // next_tail = tail + 1
	AND	$(4096-1), R14, R15       // next_tail & mask
	CMP	R15, R11                  // next_tail == head?
	BEQ	rx_buffer_full            // Buffer full, drop remaining bytes

	// Read byte from UART DR
	MOVW	UART_DR(R10), R14

	// Write to ring buffer
	MOVD	$·uartRxRingBuffer(SB), R16
	AND	$(4096-1), R12, R17       // tail & mask
	ADD	R17, R16, R16             // R16 = &buffer[tail]
	MOVB	R14, (R16)                // buffer[tail] = byte

	// Update tail
	MOVD	R15, R12

	B	rx_drain_loop

rx_buffer_full:
	// Ring buffer is full - drain remaining FIFO to avoid overflow IRQs
	MOVW	UART_FR(R10), R13
	TST	$FR_RXFE, R13
	BNE	rx_fifo_empty
	MOVW	UART_DR(R10), R14  // Read and discard
	B	rx_buffer_full

rx_fifo_empty:
	// Store updated tail
	MOVW	R12, ·uartRxRingTail(SB)

	// Set pending flag for bottom half
	MOVW	$1, R13
	MOVW	R13, ·uartRxPending(SB)

	// Clear RX interrupt
	MOVW	$IRQ_RX, R13
	MOVW	R13, UART_ICR(R10)

	RET

// ============================================================================
// uartTxIRQHandler - Handle UART TX Interrupt
// ============================================================================
//
// Called when UART TX FIFO has space available.
// Drains the TX ring buffer into the UART FIFO.
//
// Input: None (reads from UART hardware and global variables)
// Output: None (writes to global variables)
// Clobbers: R10-R17
//
TEXT ·uartTxIRQHandler(SB), NOSPLIT|NOFRAME, $0
	// Load UART base address
	MOVD	$0xFFFFFFFF09000000, R10

	// Load ring buffer pointers
	MOVW	·uartTxRingHead(SB), R11  // head (read by IRQ)
	MOVW	·uartTxRingTail(SB), R12  // tail (written by Go)

tx_drain_loop:
	// Check if ring buffer is empty
	CMP	R11, R12
	BEQ	tx_buffer_empty

	// Check if TX FIFO is full
	MOVW	UART_FR(R10), R13
	TST	$FR_TXFF, R13
	BNE	tx_fifo_full              // FIFO full, stop for now

	// Read byte from ring buffer
	MOVD	$·uartTxRingBuffer(SB), R14
	AND	$(4096-1), R11, R15       // head & mask
	ADD	R15, R14, R14             // R14 = &buffer[head]
	MOVBU	(R14), R16                // byte = buffer[head]

	// Write to UART DR
	MOVW	R16, UART_DR(R10)

	// Update head
	ADD	$1, R11
	AND	$(4096-1), R11

	B	tx_drain_loop

tx_buffer_empty:
	// No more data to send - disable TX interrupt
	// Leave RX interrupt enabled
	MOVW	$IRQ_RX, R13              // Only RX interrupt
	MOVW	R13, UART_IMSC(R10)
	B	tx_done

tx_fifo_full:
	// FIFO full but buffer has more - keep TX interrupt enabled
	// It will fire again when FIFO has space
	MOVW	$(IRQ_RX|IRQ_TX), R13     // Both RX and TX
	MOVW	R13, UART_IMSC(R10)

tx_done:
	// Store updated head
	MOVW	R11, ·uartTxRingHead(SB)

	// Set pending flag (for statistics/monitoring)
	MOVW	$1, R13
	MOVW	R13, ·uartTxPending(SB)

	// Clear TX interrupt
	MOVW	$IRQ_TX, R13
	MOVW	R13, UART_ICR(R10)

	RET

// ============================================================================
// enableUartTxInterrupt - Enable UART TX Interrupt
// ============================================================================
//
// Called from Go code when data is added to the TX ring buffer.
// Enables the TX interrupt so the IRQ handler can drain the buffer.
//
TEXT ·enableUartTxInterrupt(SB), NOSPLIT, $0-0
	// Load UART base address
	MOVD	$0xFFFFFFFF09000000, R10

	// Enable both RX and TX interrupts
	MOVW	$(IRQ_RX|IRQ_TX), R11
	MOVW	R11, UART_IMSC(R10)

	RET
