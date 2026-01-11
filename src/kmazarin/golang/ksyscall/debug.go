//go:build qemuvirt && aarch64

package ksyscall

import "unsafe"

// debugPrint outputs a single character to UART
//
//go:nosplit
func debugPrint(c byte) {
	uartBase := getUartBase()
	*(*byte)(unsafe.Pointer(uartBase)) = c
}

// debugPrintHex outputs a 64-bit value as 16 hex characters
//
//go:nosplit
func debugPrintHex(val uint64) {
	hexChars := "0123456789ABCDEF"
	uartBase := getUartBase()
	for i := 60; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		*(*byte)(unsafe.Pointer(uartBase)) = hexChars[nibble]
	}
}
