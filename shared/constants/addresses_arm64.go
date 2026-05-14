//go:build arm64

package constants

const (
	KernelHeapStart = 0xFFFF000100000000 // Start in TTBR1 space
	KernelHeapEnd   = 0xFFFF100000000000 // End of heap VA space (16TB range)
	KernelHeapSize  = KernelHeapEnd - KernelHeapStart

	// UART PL011 - physical 0x0900_0000 -> virtual 0xFFFF_FFFF_0900_0000
	KernelUartBase = KernelVAOffset + 0x09000000
)
