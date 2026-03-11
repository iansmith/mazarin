//go:build amd64

package main

import "mazzy/shared/constants"

// readMPIDRAsm is a compatibility function. On x86_64, it returns the APIC ID.
func readMPIDRAsm() uint64

// platformCPU0Stacks returns the boot CPU's stack addresses.
// These match the addresses set up by diplomat in the PML4 page tables.
//
//go:nosplit
func platformCPU0Stacks() (g0Top, g0Bottom, excTop, excBottom uint64) {
	return constants.KernelG0StackTop,
		constants.KernelG0StackBottom,
		constants.KernelExcStackTop,
		constants.KernelExcStackBottom
}

// platformKernelVAOffset returns the offset to convert physical addresses
// to kernel virtual addresses. This is the same offset used by ARM64.
//
//go:nosplit
func platformKernelVAOffset() uint64 {
	return constants.KernelMMIOOffset
}

// platformSaveKernelTLS saves the kernel's FS_BASE MSR into kmazarinFSBase.
// On x86_64, exception handlers need to WRMSR to the kernel FS_BASE before
// writing to the TLS slot, since FS_BASE may point to unmapped user memory
// when handling exceptions from userspace.
//
//go:nosplit
func platformSaveKernelTLS() {
	kmazarinFSBase = readMSR(0xC0000100)
}
