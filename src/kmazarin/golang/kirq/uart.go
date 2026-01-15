//go:build qemuvirt && aarch64

package kirq

import (
	"shared/serial"
	"unsafe"
)

// UART PL011 constants for QEMU virt machine
const (
	QEMU_UART_BASE = 0x09000000
	QEMU_UART_DR   = QEMU_UART_BASE + 0x00 // Data Register
	QEMU_UART_FR   = QEMU_UART_BASE + 0x18 // Flag Register
	QEMU_UART_IMSC = QEMU_UART_BASE + 0x38 // Interrupt Mask Set/Clear
	QEMU_UART_ICR  = QEMU_UART_BASE + 0x44 // Interrupt Clear Register
)

// UartIRQHandler handles UART transmit ready interrupt (IRQ 33)
// Reads from the shared ring buffer and transmits characters to UART
// Called from DispatchIRQ when UART TX FIFO has space
//
//go:nosplit
//go:noinline
func UartIRQHandler(irqNum uint64) {
	// Suppress unused warning
	_ = irqNum

	// Fill the TX FIFO with characters from the ring buffer
	// Keep going until FIFO is full or ring buffer is empty
	for {
		// Check if TX FIFO is full (TXFF bit 5 in FR register)
		fr := mmioRead32(uintptr(QEMU_UART_FR))
		if fr&(1<<5) != 0 {
			// FIFO full - stop and wait for next interrupt
			break
		}

		// Try to read a character from ring buffer
		serial.UartOutput.Lock()
		c, ok := serial.UartOutput.Read()
		serial.UartOutput.Unlock()

		if !ok {
			// Ring buffer empty - disable TX interrupt
			mmioWrite(QEMU_UART_IMSC, 0)
			break
		}

		// Write character to UART data register
		mmioWrite(QEMU_UART_DR, uint32(c))
	}

	// Clear UART TX interrupt (bit 5)
	mmioWrite(QEMU_UART_ICR, 1<<5)
}

// mmioRead32 reads a 32-bit value from an MMIO address
//
//go:nosplit
func mmioRead32(addr uintptr) uint32 {
	return *(*uint32)(unsafe.Pointer(addr))
}

// mmioWrite writes a 32-bit value to an MMIO address
// This is a simple wrapper to avoid importing the asm package
//
//go:nosplit
func mmioWrite(addr uint64, value uint32) {
	*(*uint32)(unsafe.Pointer(uintptr(addr))) = value
}
