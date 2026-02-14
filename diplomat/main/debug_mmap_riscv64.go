//go:build riscv64

package main

import "unsafe"

// debugMmapEntry writes '$' to RISC-V UART to verify DiplomatMmap is called.
// On RISC-V the Go runtime may use ECALL instead, making DiplomatMmap dead code.
//
//go:nosplit
func debugMmapEntry() {
	*(*byte)(unsafe.Pointer(uintptr(0x10000000))) = '$'
}

// debugSpanEntry writes '%' to RISC-V UART to verify InitializeSpans is called.
//
//go:nosplit
func debugSpanEntry() {
	*(*byte)(unsafe.Pointer(uintptr(0x10000000))) = '%'
}
