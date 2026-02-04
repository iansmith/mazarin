//go:build riscv64 && !test_stubs

package main

import "unsafe"

// ForceSerialCharacter writes a single byte directly to the serial port.
// No polling, no locks, no dependencies — the absolute minimum path to
// get a character onto the wire. Safe from any context including IRQ handlers.
//
// On RISC-V QEMU virt: 16550 UART at high-memory mapped address.
// Physical address 0x10000000, mapped with kernel MMIO offset.
//
//go:nosplit
func ForceSerialCharacter(b byte) {
	const uartBase = uintptr(0xFFFFFFFF10000000)
	*(*uint8)(unsafe.Pointer(uartBase)) = b
}
