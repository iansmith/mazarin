//go:build riscv64 && !test_stubs

package console

import "unsafe"

// breadcrumb writes a byte directly to the 16550 UART via MMIO.
// This is the low-level implementation used by all console implementations.
// Safe to call from any context, including IRQ handlers.
//
// On RISC-V QEMU virt: 16550 UART at high-memory mapped address.
// Physical address 0x10000000, mapped with kernel MMIO offset.
//
//go:nosplit
func breadcrumb(b byte) {
	const uartBase = uintptr(0xFFFFFFFF10000000)

	// Poll LSR (offset 5) bit 5 (THRE) until transmitter is ready
	for (*(*uint8)(unsafe.Pointer(uartBase + 5)) & 0x20) == 0 {
		// Busy wait
	}

	// Write byte to THR (offset 0)
	*(*uint8)(unsafe.Pointer(uartBase)) = b
}
