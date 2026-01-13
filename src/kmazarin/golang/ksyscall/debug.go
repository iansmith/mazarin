//go:build qemuvirt && aarch64

package ksyscall

// mmio_write8 is implemented in mmio_arm64.s
// Writes an 8-bit value to MMIO using volatile memory access
//
//go:nosplit
func mmio_write8(addr uintptr, val byte)

// debugPrint outputs a single character to UART
// Uses assembly volatile write to prevent compiler optimization
//
//go:nosplit
func debugPrint(c byte) {
	uartBase := getUartBase()
	mmio_write8(uartBase, c)
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
		mmio_write8(uartBase, hexChars[nibble])
	}
}
