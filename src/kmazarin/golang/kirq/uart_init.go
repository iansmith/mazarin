//go:build qemuvirt && aarch64

package kirq

import (
	"shared/serial"
	"unsafe"
)

// GIC constants
const (
	GIC_DIST_BASE   = 0x08000000
	GICD_ISENABLER0 = GIC_DIST_BASE + 0x100
)

// InitUART initializes interrupt-driven UART
// Must be called after GIC is initialized
//
//go:nosplit
func InitUART() {
	// Enable UART interrupt (IRQ 33) in GIC
	enableIRQ(33)
}

// enableIRQ enables an interrupt in the GIC
//
//go:nosplit
func enableIRQ(irqNum uint32) {
	// Calculate which GICD_ISENABLER register and bit
	regNum := irqNum / 32
	bitNum := irqNum % 32

	// Read-modify-write the GICD_ISENABLER register
	regAddr := GICD_ISENABLER0 + (regNum * 4)
	current := mmioRead32(uintptr(regAddr))
	current |= (1 << bitNum)
	mmioWrite32(uintptr(regAddr), current)
}

// mmioWrite32 writes a 32-bit value to an MMIO address
//
//go:nosplit
func mmioWrite32(addr uintptr, value uint32) {
	*(*uint32)(unsafe.Pointer(addr)) = value
}

// UARTPutc writes a single character to the UART via ring buffer
// Primes the FIFO when buffer was empty to trigger TX interrupts
// Returns true if character was enqueued, false if buffer is full
//
//go:nosplit
func UARTPutc(c byte) bool {
	// Lock, check if empty, write to ring buffer, unlock
	serial.UartOutput.Lock()
	wasEmpty := (serial.UartOutput.Available() == 0)
	ok := serial.UartOutput.Write(c)
	serial.UartOutput.Unlock()

	if !ok {
		return false // Buffer full
	}

	// If buffer was empty, prime the FIFO to trigger interrupts
	if wasEmpty {
		// Enable TX interrupt BEFORE priming to ensure we catch the transition
		mmioWrite(QEMU_UART_IMSC, 1<<5)
		primeUartTxFifo()
	}

	return true
}

// primeUartTxFifo fills the TX FIFO from the ring buffer to trigger interrupts
// PL011 TX interrupt fires on FIFO level transitions, not while continuously empty
// By filling the FIFO, we ensure it drains and crosses the threshold, firing interrupts
//
//go:nosplit
func primeUartTxFifo() {
	// PL011 TX FIFO is 16 bytes deep
	// Fill it completely to ensure interrupt fires as it drains
	for i := 0; i < 16; i++ {
		// Check if TX FIFO is full (TXFF bit 5 in FR register)
		fr := mmioRead32(uintptr(QEMU_UART_FR))
		if fr&(1<<5) != 0 {
			// FIFO full, stop
			break
		}

		// Try to dequeue from ring buffer
		serial.UartOutput.Lock()
		c, ok := serial.UartOutput.Read()
		serial.UartOutput.Unlock()

		if !ok {
			// Ring buffer empty, stop
			break
		}

		// Write character directly to UART data register
		mmioWrite(QEMU_UART_DR, uint32(c))
	}
}

// UARTPuts writes a string to the UART via ring buffer
//
//go:nosplit
func UARTPuts(s string) {
	for i := 0; i < len(s); i++ {
		UARTPutc(s[i])
	}
}
