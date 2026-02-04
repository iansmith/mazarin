//go:build amd64 && !test_stubs

package main

// readMPIDRAsm is a compatibility function. On x86_64, it returns the APIC ID.
func readMPIDRAsm() uint64

// platformCPU0Stacks returns the boot CPU's stack addresses.
// Placeholder values until the x86_64 bootloader (diplomat) provides real addresses.
//
//go:nosplit
func platformCPU0Stacks() (g0Top, g0Bottom, excTop, excBottom uint64) {
	// TODO: Replace with actual addresses from diplomat boot info
	return 0xFFFF_0000, 0xFFFE_0000, 0xFFFD_0000, 0xFFFC_0000
}

// platformKernelVAOffset returns the offset to convert physical addresses
// to kernel virtual addresses. Placeholder until x86_64 memory map is defined.
//
//go:nosplit
func platformKernelVAOffset() uint64 {
	// TODO: Replace with actual offset when x86_64 memory layout is defined
	return 0xFFFFFFFF00000000
}
