
package main

import "mazzy/kmazarin/serial"

// KernelPanic prints an error message directly to console and hangs the system.
// This is used when something goes critically wrong and we cannot continue.
// Uses serial.RawUARTPuts for direct, nosplit-safe UART output.
//
//go:nosplit
func KernelPanic(msg string) {
	serial.RawUARTPuts("\r\n*** KERNEL PANIC ***\r\n")
	serial.RawUARTPuts(msg)
	serial.RawUARTPuts("\r\n")

	// Halt the system
	Exit()
}
