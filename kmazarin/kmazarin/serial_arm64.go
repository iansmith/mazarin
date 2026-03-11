//go:build arm64

package main

import "mazzy/kmazarin/serial"

// ForceSerialCharacter writes a single byte directly to the serial port.
// No polling, no locks, no dependencies — the absolute minimum path to
// get a character onto the wire. Safe from any context including IRQ handlers.
//
//go:nosplit
func ForceSerialCharacter(b byte) {
	serial.PollWrite(b)
}
