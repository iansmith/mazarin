//go:build arm64

package constants

const (
	KernelHeapStart = 0xFFFF000100000000 // Start in TTBR1 space
	KernelHeapEnd   = 0xFFFF100000000000 // End of heap VA space (16TB range)
	KernelHeapSize  = KernelHeapEnd - KernelHeapStart
)
