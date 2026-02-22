//go:build riscv64

package main

// debugMmapEntry is a no-op (was writing '$' to UART for debugging).
//
//go:nosplit
func debugMmapEntry() {
}

// debugSpanEntry is a no-op (was writing '%' to UART for debugging).
//
//go:nosplit
func debugSpanEntry() {
}
