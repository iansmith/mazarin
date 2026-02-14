//go:build !riscv64

package main

//go:nosplit
func debugMmapEntry() {}

//go:nosplit
func debugSpanEntry() {}
