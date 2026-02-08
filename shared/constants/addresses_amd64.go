//go:build amd64

package constants

const (
	// x86_64 heap must use canonical addresses (bits 47-63 must all match).
	// 0xFFFF000100000000 (used on ARM64) is non-canonical on x86_64 with 48-bit
	// paging because bit 47 is 0 while bits 48-63 are 1.
	// Using the canonical negative half: 0xFFFF800000000000-0xFFFFFFFFFFFFFFFF.
	KernelHeapStart = 0xFFFF800100000000
	KernelHeapEnd   = 0xFFFF900000000000 // 1TB range
	KernelHeapSize  = KernelHeapEnd - KernelHeapStart
)
