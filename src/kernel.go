package main

import (
	_ "unsafe"
)

// Link to external assembly functions from lib.s
//
//go:linkname mmio_write mmio_write
//go:nosplit
func mmio_write(reg uintptr, data uint32)

//go:linkname mmio_read mmio_read
//go:nosplit
func mmio_read(reg uintptr) uint32

//go:linkname delay delay
//go:nosplit
func delay(count int32)

// Peripheral base address for Raspberry Pi 4
const (
	// Peripheral base address for Raspberry Pi 4
	PERIPHERAL_BASE uintptr = 0xFE000000 // Raspberry Pi 4 (was 0x3F000000 for Pi 2/3, 0x20000000 for Pi 1)

	// The GPIO registers base address
	GPIO_BASE = PERIPHERAL_BASE + 0x200000 // 0xFE200000 for Pi 4

	GPPUD     = GPIO_BASE + 0x94
	GPPUDCLK0 = GPIO_BASE + 0x98

	// The base address for UART0 (PL011 UART)
	UART0_BASE = PERIPHERAL_BASE + 0x201000 // 0xFE201000 for Pi 4

	UART0_DR     = UART0_BASE + 0x00
	UART0_RSRECR = UART0_BASE + 0x04
	UART0_FR     = UART0_BASE + 0x18
	UART0_ILPR   = UART0_BASE + 0x20
	UART0_IBRD   = UART0_BASE + 0x24
	UART0_FBRD   = UART0_BASE + 0x28
	UART0_LCRH   = UART0_BASE + 0x2C
	UART0_CR     = UART0_BASE + 0x30
	UART0_IFLS   = UART0_BASE + 0x34
	UART0_IMSC   = UART0_BASE + 0x38
	UART0_RIS    = UART0_BASE + 0x3C
	UART0_MIS    = UART0_BASE + 0x40
	UART0_ICR    = UART0_BASE + 0x44
	UART0_DMACR  = UART0_BASE + 0x48
	UART0_ITCR   = UART0_BASE + 0x80
	UART0_ITIP   = UART0_BASE + 0x84
	UART0_ITOP   = UART0_BASE + 0x88
	UART0_TDR    = UART0_BASE + 0x8C
)

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

//go:nosplit
func uartPuts(str string) {
	for i := 0; i < len(str); i++ {
		uartPutc(str[i])
	}
}

// KernelMain is the entry point called from boot.s
// For bare metal, we ensure it's not optimized away
//
//go:nosplit
//go:noinline
func KernelMain(r0, r1, atags uint32) {
	_ = r0
	_ = r1
	_ = atags

	uartInit()
	uartPuts("Hello, Mazarin!\r\n")

	for {
		uartPutc(uartGetc())
		uartPutc('\n')
	}
}

// Dummy main() function required by Go's c-archive build mode
// This is never called - boot.s calls KernelMain directly
// We call KernelMain here to ensure it's compiled and not optimized away
func main() {
	// Call KernelMain with dummy values to ensure it's compiled
	// This will never execute in bare metal, but ensures the function exists
	KernelMain(0, 0, 0)
	// This should never execute in bare metal
	for {
	}
}
