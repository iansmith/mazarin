//go:build amd64 && !test_stubs

package main

// ForceSerialCharacter writes a single byte directly to the serial port.
// No polling, no locks, no dependencies — the absolute minimum path to
// get a character onto the wire. Safe from any context including IRQ handlers.
//
// On x86_64 QEMU: COM1 at I/O port 0x3F8 via OUTB instruction.
// Implemented in serial_amd64.s.
//
//go:nosplit
func ForceSerialCharacter(b byte)
