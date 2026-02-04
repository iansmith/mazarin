//go:build arm64 && !test_stubs

package main

import "unsafe"

// ForceSerialCharacter writes a single byte directly to the serial port.
// No polling, no locks, no dependencies — the absolute minimum path to
// get a character onto the wire. Safe from any context including IRQ handlers.
//
// On ARM64 QEMU virt: PL011 UART at high-memory mapped address.
//
//go:nosplit
func ForceSerialCharacter(b byte) {
	const uartBase = uintptr(0xFFFFFFFF09000000)
	*(*uint32)(unsafe.Pointer(uartBase)) = uint32(b)
}
