//go:build riscv64

package main

// readMPIDRAsm is a compatibility function. On RISC-V, it returns the hart ID.
func readMPIDRAsm() uint64

// platformCPU0Stacks returns the boot hart's stack addresses.
// Placeholder values until the RISC-V bootloader provides real addresses.
//
//go:nosplit
func platformCPU0Stacks() (g0Top, g0Bottom, excTop, excBottom uint64) {
	// TODO: Replace with actual addresses from bootloader
	return 0xFFFF_0000, 0xFFFE_0000, 0xFFFD_0000, 0xFFFC_0000
}

// platformKernelVAOffset returns the offset to convert physical addresses
// to kernel virtual addresses. Placeholder until RISC-V memory map is defined.
//
//go:nosplit
func platformKernelVAOffset() uint64 {
	// TODO: Replace with actual offset when RISC-V memory layout is defined
	return 0xFFFFFFFF00000000
}

// platformSaveKernelTLS is a no-op on RISC-V (uses runtime.tls_g global instead of FS_BASE).
//
//go:nosplit
func platformSaveKernelTLS() {}
