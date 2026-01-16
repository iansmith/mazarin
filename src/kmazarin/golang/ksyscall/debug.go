//go:build qemuvirt && aarch64

package ksyscall

import "kmazarin/console"

// debugPrint outputs a single character to console
// Uses console abstraction which provides spinlock protection
//
//go:nosplit
func debugPrint(c byte) {
	console.WriteByte(c)
}

// debugPrintHex outputs a 64-bit value as 16 hex characters
// Uses console abstraction which provides spinlock protection
//
//go:nosplit
func debugPrintHex(val uint64) {
	console.PrintHex64(val)
}
