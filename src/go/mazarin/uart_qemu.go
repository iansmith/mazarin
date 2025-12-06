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
// Follows proper PL011 initialization sequence
//
//go:nosplit
func uartInit() {
	// Initialize UART using proper PL011 sequence
	uart_init_pl011()
}

// uartPutc outputs a character via UART (QEMU virt machine)
// Uses proper PL011 UART assembly function
//
//go:nosplit
func uartPutc(c byte) {
	uart_putc_pl011(c)
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
