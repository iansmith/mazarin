
package ksyscall

import "mazzy/kmazarin/console"

// debugPrint outputs a single character to console
// Uses console abstraction which provides spinlock protection
//
//go:nosplit
func debugPrint(c byte) {
	console.KWriteByte(c)
}

// debugPrintHex outputs a 64-bit value as 16 hex characters
// Uses console abstraction which provides spinlock protection
//
//go:nosplit
func debugPrintHex(val uint64) {
	console.KPrintHex64(val)
}
