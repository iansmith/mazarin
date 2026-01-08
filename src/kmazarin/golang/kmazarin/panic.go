//go:build qemuvirt && aarch64

package main

import "unsafe"

// Exit hangs the system in an infinite loop
// Implemented in panic_arm64.s
func Exit()

// KernelPanic prints an error message directly to UART and hangs the system
// This is used when something goes critically wrong and we cannot continue
//
//go:nosplit
func KernelPanic(msg string) {
	uartBase := GetUartBase()

	// Helper to write a string
	uartPuts := func(s string) {
		for i := 0; i < len(s); i++ {
			*(*byte)(unsafe.Pointer(uartBase)) = s[i]
		}
	}

	// Print the panic message
	uartPuts("\r\n*** KERNEL PANIC ***\r\n")
	uartPuts(msg)
	uartPuts("\r\n")

	// Halt the system
	Exit()
}
