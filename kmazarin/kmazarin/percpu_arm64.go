//go:build arm64

package main

import "mazzy/shared/constants"

// readMPIDRAsm reads the MPIDR_EL1 register (implemented in assembly).
// Returns the full 64-bit MPIDR_EL1 value.
func readMPIDRAsm() uint64

// platformCPU0Stacks returns the boot CPU's stack addresses from
// platform-specific constants (already mapped by the bootloader).
//
//go:nosplit
func platformCPU0Stacks() (g0Top, g0Bottom, excTop, excBottom uint64) {
	return uint64(constants.KernelG0StackTop), uint64(constants.KernelG0StackBottom),
		uint64(constants.KernelExcStackTop), uint64(constants.KernelExcStackBottom)
}

// platformKernelVAOffset returns the offset to convert physical addresses
// to kernel virtual addresses (PA + offset = VA).
//
//go:nosplit
func platformKernelVAOffset() uint64 {
	return uint64(constants.KernelMMIOOffset)
}

// platformSaveKernelTLS is a no-op on ARM64 (uses TPIDR_EL1 instead of FS_BASE).
//
//go:nosplit
func platformSaveKernelTLS() {}
