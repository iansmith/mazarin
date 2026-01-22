//go:build !test_stubs

package console

import "kmazarin/asm"

// breadcrumb writes a byte directly to UART MMIO.
// This is the low-level implementation used by all console implementations.
// Safe to call from any context, including IRQ handlers.
//
//go:nosplit
func breadcrumb(b byte) {
	// UART base address (high-memory mapped)
	const uartBase uintptr = 0xFFFFFFFF09000000

	// Wait for TX FIFO to have space (bit 5 = TXFF in FR register at offset 0x18)
	for (asm.MmioRead32(uartBase + 0x18) & 0x20) != 0 {
		// Busy wait
	}

	// Write byte to data register (offset 0x00)
	asm.MmioWrite32(uartBase, uint32(b))
}
