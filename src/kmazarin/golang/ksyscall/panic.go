//go:build qemuvirt && aarch64

package ksyscall

import "unsafe"

// Exit hangs the system in an infinite loop
// Implemented in panic_arm64.s
func Exit()

// KernelPanic prints an error message directly to UART and hangs the system
// This is used when syscalls fail critically or are not implemented
//
//go:nosplit
func KernelPanic(msg string) {
	const uartBase = uintptr(0xFFFFFFFF09000000)

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
