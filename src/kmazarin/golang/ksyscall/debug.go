//go:build qemuvirt && aarch64

package ksyscall

import "kmazarin/asm"

// debugPrint outputs a single character to UART
// Uses assembly volatile write to prevent compiler optimization
//
//go:nosplit
func debugPrint(c byte) {
	uartBase := getUartBase()
	asm.MmioWrite8(uartBase, c)
}

// debugPrintHex outputs a 64-bit value as 16 hex characters
// Uses assembly volatile write to prevent compiler optimization
//
//go:nosplit
func debugPrintHex(val uint64) {
	hexChars := "0123456789ABCDEF"
	uartBase := getUartBase()
	for i := 60; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		asm.MmioWrite8(uartBase, hexChars[nibble])
	}
}
