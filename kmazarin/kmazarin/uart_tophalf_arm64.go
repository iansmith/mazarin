//go:build arm64

package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/serial"
	"mazzy/shared/hid"
)

// PL011 register offsets for direct MMIO access in the top-half.
const (
	pl011DR    = 0x000    // Data register
	pl011FR    = 0x018    // Flag register
	pl011MIS   = 0x040    // Masked interrupt status
	pl011ICR   = 0x044    // Interrupt clear register
	pl011RXFE  = 1 << 4   // RX FIFO empty flag
	pl011IRQRX = 1 << 4   // RX interrupt bit
)

// uartTopHalf drains the PL011 RX FIFO directly via MMIO, pushes each
// byte into topHalfUartRing as an HIDEvent, clears the interrupt, and
// wakes any blocked userspace slot. Runs in nosplit assembly IRQ context.
//
//go:nosplit
func uartTopHalf(irqNum uint32) {
	base := uintptr(UartBase)

	// Check masked interrupt status — only handle RX
	status := asm.MmioRead32(base + pl011MIS)
	if status&pl011IRQRX == 0 {
		return
	}

	pushed := false
	for asm.MmioRead32(base+pl011FR)&pl011RXFE == 0 {
		data := asm.MmioRead32(base + pl011DR)
		ev := hid.HIDEvent{Type: 0, Code: 0, Value: data & 0xFF}
		if ringPush(&topHalfUartRing, ev) {
			pushed = true
		} else {
			serial.PollWrite('X') // overflow
		}
	}

	// Clear the RX interrupt
	asm.MmioWrite32(base+pl011ICR, status)

	if pushed {
		WakeSlotForIRQ(irqNum)
	}
}
