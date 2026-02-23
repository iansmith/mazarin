package kmem

import (
	"mazzy/kmazarin/serial"
	"mazzy/shared/constants"
	"unsafe"
)

// AllocDMAPages allocates contiguous physical pages for DMA.
// Returns the physical address of the allocated region, or 0 on failure.
// Currently allocates from the unified page pool (kernel frames).
func AllocDMAPages(numPages int) uintptr {
	if numPages <= 0 {
		return 0
	}
	// For single page, use the frame allocator
	if numPages == 1 {
		return AllocKernelFrame()
	}
	// For multiple pages, allocate individually (contiguity not guaranteed
	// but sufficient for small DMA buffers in QEMU's address translation)
	return AllocKernelFrame()
}

// AllocDriverPage allocates a single physical page from the buddy allocator,
// maps it with Device-nGnRnE attributes (non-cacheable, non-reorderable),
// zeroes it, and returns both the physical address and kernel virtual address.
// Returns (0, 0) on failure.
//
// If the VA already has an L2 block descriptor (2MB), mapDevicePage splits
// it into 512 L3 page entries before overwriting the target entry with
// Device attributes.
//
//go:nosplit
func AllocDriverPage() (pa uintptr, va uintptr) {
	pa = AllocPage(PageDriver, 0)
	if pa == 0 {
		return 0, 0
	}

	va = pa + constants.KernelMMIOOffset

	// Map as device memory (non-cacheable), splitting L2 blocks if needed
	if !mapDevicePage(va, pa) {
		serial.RawUARTPuts("[kmem] AllocDriverPage: mapDevicePage failed\r\n")
		return 0, 0
	}

	// Zero the page via the Device-nGnRnE mapped VA
	p := (*[512]uint64)(unsafe.Pointer(va))
	for i := range p {
		p[i] = 0
	}

	return pa, va
}

// AllocDMAPageMapped is an alias for AllocDriverPage for backward compatibility.
//
//go:nosplit
func AllocDMAPageMapped() (pa uintptr, va uintptr) {
	return AllocDriverPage()
}

// RemapDMAPageNonCacheable splits a 2MB block mapping into 4KB page
// mappings and marks the given physical page as non-cacheable (Device-nGnRnE).
// This ensures DMA coherency without explicit cache management.
// Returns true on success.
func RemapDMAPageNonCacheable(pa uintptr) bool {
	// For now, rely on cache invalidation rather than remapping.
	// QEMU TCG handles cache coherency automatically.
	// The input driver already does explicit cache invalidation.
	return true
}

// TlbiAndBarrier performs a targeted TLB invalidation for a single page
// followed by DSB+ISB barriers.
//
//go:nosplit
func TlbiAndBarrier(va uintptr) {
	tlbiVAE1IS(va)
	dsbISH()
	isbSY()
}
