//go:build riscv64 && !test_stubs

package console

import "unsafe"

// breadcrumb writes a byte directly to the 16550 UART via MMIO.
// This is the low-level implementation used by all console implementations.
// Safe to call from any context, including IRQ handlers.
//
// On RISC-V QEMU virt: 16550 UART at physical address 0x10000000.
// CRITICAL: When booting directly (not via diplomat), OpenSBI loads us with
// SATP=0 (bare mode, no paging). We must use physical addresses until page
// tables are set up. OpenSBI's PMP allows S-mode access to this range.
//
//go:nosplit
func breadcrumb(b byte) {
	const uartBase = uintptr(0x10000000)

	// Poll LSR (offset 5) bit 5 (THRE) until transmitter is ready
	for (*(*uint8)(unsafe.Pointer(uartBase + 5)) & 0x20) == 0 {
		// Busy wait
	}

	// Write byte to THR (offset 0)
	*(*uint8)(unsafe.Pointer(uartBase)) = b
}
