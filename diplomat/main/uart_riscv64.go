///go:build riscv64

// diplomat/main/uart_riscv64.go
// Direct UART support for RISC-V 64-bit (NS16550A at 0x10000000)

package main

// uartPutChar writes a single byte directly to the UART.
// Implemented in uart_riscv64.s
//
//go:noescape
func uartPutChar(c byte)

// uartDebugMarker writes a debug marker byte directly to UART (NOSPLIT|NOFRAME).
// Implemented in uart_riscv64.s
//
//go:noescape
func uartDebugMarker(c byte)

// printCharDirect prints a character directly to UART (bypasses UEFI).
// Used when systemTable is nil (running in -bios none mode).
//
//go:nosplit
func printCharDirect(c uint16) {
	// For ASCII range, convert directly
	// For Unicode, we'd need UTF-8 encoding
	if c < 128 {
		uartPutChar(byte(c))
	} else {
		// Non-ASCII: print '?' as placeholder
		uartPutChar('?')
	}
}

// printStringDirect prints a string directly to UART (bypasses UEFI).
//
//go:nosplit
func printStringDirect(s string) {
	for i := 0; i < len(s); i++ {
		uartPutChar(s[i])
	}
}
