//go:build !qemu

package main

// UART functions for Raspberry Pi (not for QEMU)
// QEMU builds use uart_qemu.go instead

//go:nosplit
func uartInit() {
	mmio_write(UART0_CR, 0x00000000)

	mmio_write(GPPUD, 0x00000000)
	delay(150)

	mmio_write(GPPUDCLK0, (1<<14)|(1<<15))
	delay(150)

	mmio_write(GPPUDCLK0, 0x00000000)

	mmio_write(UART0_ICR, 0x7FF)

	mmio_write(UART0_IBRD, 1)
	mmio_write(UART0_FBRD, 40)

	mmio_write(UART0_LCRH, (1<<4)|(1<<5)|(1<<6))

	mmio_write(UART0_IMSC, (1<<1)|(1<<4)|(1<<5)|(1<<6)|
		(1<<7)|(1<<8)|(1<<9)|(1<<10))

	mmio_write(UART0_CR, (1<<0)|(1<<8)|(1<<9))
}

//go:nosplit
func uartPutc(c byte) {
	for mmio_read(UART0_FR)&(1<<5) != 0 {
		// Wait for transmit FIFO to have space
	}
	mmio_write(UART0_DR, uint32(c))
}

//go:nosplit
func uartGetc() byte {
	for mmio_read(UART0_FR)&(1<<4) != 0 {
		// Wait for receive FIFO to have data
	}
	return byte(mmio_read(UART0_DR))
}






