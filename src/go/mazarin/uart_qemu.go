//go:build qemu

package main

// QEMU virt machine UART constants
// The virt machine uses PL011 UART at 0x9000000 (different from Raspberry Pi)
const (
	// PL011 UART base address for QEMU virt machine
	// Try both formats - sometimes written as 0x09000000
	QEMU_UART_BASE = 0x09000000 // PL011 UART base for virt machine (0x09000000)

	QEMU_UART_DR   = QEMU_UART_BASE + 0x00
	QEMU_UART_FR   = QEMU_UART_BASE + 0x18
	QEMU_UART_IBRD = QEMU_UART_BASE + 0x24
	QEMU_UART_FBRD = QEMU_UART_BASE + 0x28
	QEMU_UART_LCRH = QEMU_UART_BASE + 0x2C
	QEMU_UART_CR   = QEMU_UART_BASE + 0x30
	QEMU_UART_ICR  = QEMU_UART_BASE + 0x44
)

// uartInit initializes the UART for QEMU virt machine
// Uses PL011 UART at 0x09000000
// QEMU pre-initializes it, so we can use empty function
//
//go:nosplit
func uartInit() {
	// Empty - QEMU handles UART initialization
	// We can write directly to UART without initialization
}

// uartPutc outputs a character via UART (QEMU virt machine)
// PL011 FR register: bit 5 = TXFF (transmit FIFO full), bit 7 = TXFE (transmit FIFO empty)
// We wait for TXFF to be clear (FIFO has space)
//
//go:nosplit
func uartPutc(c byte) {
	// Use the exact same pattern as kernel.go direct writes that work
	// Define constants locally to match exactly
	const uartBase uintptr = 0x09000000
	const uartFR = uartBase + 0x18
	const uartDR = uartBase + 0x00

	// Wait for transmit FIFO to have space (same pattern as kernel.go)
	for mmio_read(uartFR)&(1<<5) != 0 {
		// Wait
	}
	// Write character
	mmio_write(uartDR, uint32(c))
}

// uartGetc reads a character from UART (QEMU virt machine)
//
//go:nosplit
func uartGetc() byte {
	for mmio_read(QEMU_UART_FR)&(1<<4) != 0 {
		// Wait for receive FIFO to have data
	}
	return byte(mmio_read(QEMU_UART_DR))
}
