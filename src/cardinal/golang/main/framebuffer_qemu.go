//go:build qemuvirt && aarch64

package main

// QEMU framebuffer constants for VirtIO GPU device
// VirtIO GPU uses a fixed framebuffer address at 0x41000000 (8MB region)
const (
	QEMU_FB_WIDTH  = 1920
	QEMU_FB_HEIGHT = 1080
)

// Override BYTES_PER_PIXEL for QEMU - bochs-display uses XRGB8888 (32-bit, 4 bytes per pixel)
// This overrides the 3-byte value from framebuffer_common.go
// Note: bochs-display uses BGR byte order (Blue, Green, Red) for pixel data
const QEMU_BYTES_PER_PIXEL = 4

// framebufferInit initializes the framebuffer for QEMU using VirtIO GPU device
// VirtIO GPU initialization is now handled by initVirtIOGPUDriver()
// in kernelMainInternal() after MMU and device tree setup.
// This function now just verifies that fbinfo was populated.
// Returns 0 on success, non-zero on error
func framebufferInit() int32 {
	if fbinfo.Buf == nil {
		print("FATAL: framebuffer not initialized\r\n")
		return 1
	}

	return 0
}

// refreshFramebuffer refreshes the framebuffer to keep it visible
// Cardinal no longer handles GPU - this is now done in kmazarin
//
//go:nosplit
func refreshFramebuffer() {
	// No-op: GPU initialization and refresh are handled by kmazarin
}
