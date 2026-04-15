//go:build !riscv64

package main

// initHWProbeFromDTB is a no-op on non-RISC-V architectures.
func initHWProbeFromDTB() {}
