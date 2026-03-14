//go:build arm64

package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/serial"
	"mazzy/kmazarin/uart"
	"mazzy/shared/hid"
)

// PL011 register offsets for direct MMIO access in the top-half.
const (
	pl011DR    = 0x000    // Data register
	pl011FR    = 0x018    // Flag register
	pl011IMSC  = 0x038    // Interrupt mask set/clear
	pl011MIS   = 0x040    // Masked interrupt status
	pl011ICR   = 0x044    // Interrupt clear register
	pl011RXFE  = 1 << 4   // RX FIFO empty flag
	pl011TXFF  = 1 << 5   // TX FIFO full flag
	pl011IRQRX = 1 << 4   // RX interrupt bit
	pl011IRQTX = 1 << 5   // TX interrupt bit
)

// uartTxDriver holds the PL011 instance for TX drain from the top-half.
// Set during device init via StoreUartTxDriver.
var uartTxDriver *uart.PL011

// StoreUartTxDriver stores the PL011 driver reference for top-half TX drain.
func StoreUartTxDriver(v interface{}) {
	if d, ok := v.(*uart.PL011); ok {
		uartTxDriver = d
	}
}

// uartTopHalf handles PL011 RX and TX interrupts directly via MMIO.
// RX: drains FIFO → topHalfUartRing. TX: drains txBuf → FIFO.
// Runs in nosplit assembly IRQ context.
//
//go:nosplit
func uartTopHalf(irqNum uint32) {
	base := uintptr(UartBase)

	status := asm.MmioRead32(base + pl011MIS)
	if status == 0 {
		return
	}

	pushed := false

	// Handle RX: drain PL011 FIFO into soft IRQ ring
	if status&pl011IRQRX != 0 {
		for asm.MmioRead32(base+pl011FR)&pl011RXFE == 0 {
			data := asm.MmioRead32(base + pl011DR)
			ev := hid.HIDEvent{Type: 0, Code: 0, Value: data & 0xFF}
			if ringPush(&topHalfUartRing, ev) {
				pushed = true
			} else {
				serial.PollWrite('X') // overflow
			}
		}
	}

	// Handle TX: drain driver's txBuf to PL011 FIFO
	if status&pl011IRQTX != 0 && uartTxDriver != nil {
		if uartTxDriver.TxTryLock() {
			for uartTxDriver.TxBufAvailable() > 0 && asm.MmioRead32(base+pl011FR)&pl011TXFF == 0 {
				b := uartTxDriver.TxBufReadByte()
				asm.MmioWrite32(base+pl011DR, uint32(b))
			}
			// If buffer empty, disable TX interrupt to prevent re-firing
			if uartTxDriver.TxBufAvailable() == 0 {
				asm.MmioWrite32(base+pl011IMSC, pl011IRQRX) // RX only
			}
			uartTxDriver.TxLockRelease()
		}
	}

	// Clear handled interrupts
	asm.MmioWrite32(base+pl011ICR, status)

	if pushed {
		WakeSlotForIRQ(irqNum)
	}
}
