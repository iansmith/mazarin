package constants

const (
	// RISC-V Sv39 canonical kernel heap addresses.
	// Sv39 supports 39-bit VAs with canonical addressing:
	//   Lower half (user): 0x0000000000000000 - 0x0000003FFFFFFFFF
	//   Upper half (kernel): 0xFFFFFFC000000000 - 0xFFFFFFFFFFFFFFFF
	//
	// The linear map is at 0xFFFFFFFF00000000 (diplomat's KernelVAOffset).
	// Heap must be in canonical upper half, below the linear map.
	KernelHeapStart = 0xFFFFFFC100000000 // Start in canonical kernel space
	KernelHeapEnd   = 0xFFFFFFD000000000 // 60GB range
	KernelHeapSize  = KernelHeapEnd - KernelHeapStart
)
