//go:build qemu

package main

import (
	"unsafe"
)

// QEMU framebuffer constants
// For QEMU, we write directly to QEMU's framebuffer memory at a fixed address
// This address is chosen to be well above kernel memory to avoid conflicts:
//   - Kernel: 0x200000 (2MB)
//   - Stack:  0x400000 (4MB)
//   - Heap:   0x500000 (5MB+)
//   - FB:     0x10000000 (256MB) - safe high address
//
// This is a "side channel" - we write directly to QEMU's framebuffer memory
// instead of allocating our own. The same gpuPutc/gpuPuts interface works.
//
// NOTE: The actual display device framebuffer address may differ. For PCI devices
// (VGA, bochs-display), the framebuffer is typically in PCI MMIO space (0x40000000+).
// If display doesn't work, you may need to:
//  1. Query PCI config space to find the actual framebuffer address
//  2. Or adjust QEMU_FB_BASE to match the device's framebuffer location
//  3. Or use a device that maps to a known address
const (
	// QEMU_FB_BASE: Framebuffer base address
	// For PCI devices (bochs-display, VGA) on 'virt' machine, the framebuffer
	// is typically in PCI MMIO space starting at 0x40000000.
	// Try 0x40000000 first (PCI MMIO base), fallback to 0x10000000 if needed
	QEMU_FB_BASE = 0x40000000 // PCI MMIO space (1GB) - typical location for display devices
	// Alternative: 0x10000000 (256MB) - safe but may not match device framebuffer
	QEMU_FB_WIDTH  = 640
	QEMU_FB_HEIGHT = 480
)

// framebufferInit initializes the framebuffer for QEMU by mapping to QEMU's framebuffer memory
// Returns 0 on success, non-zero on error
//
// This function does NOT allocate memory - it uses a fixed address that QEMU's
// VGA/virtio-gpu device should map its framebuffer to. This avoids conflicts
// with kernel memory (heap, stack, etc.).
//
//go:nosplit
func framebufferInit() int32 {
	uartPuts("framebufferInit (QEMU): Initializing framebuffer\r\n")

	// Set fixed dimensions for QEMU
	fbinfo.Width = QEMU_FB_WIDTH
	fbinfo.Height = QEMU_FB_HEIGHT
	fbinfo.Pitch = fbinfo.Width * BYTES_PER_PIXEL
	fbinfo.CharsWidth = fbinfo.Width / CHAR_WIDTH
	fbinfo.CharsHeight = fbinfo.Height / CHAR_HEIGHT
	fbinfo.CharsX = 0
	fbinfo.CharsY = 0

	// Calculate framebuffer size
	fbinfo.BufSize = fbinfo.Pitch * fbinfo.Height

	// Try to find bochs-display device via PCI enumeration
	fbAddr := findBochsDisplay()

	if fbAddr == 0 {
		// Fallback to fixed address if PCI enumeration fails
		uartPuts("framebufferInit (QEMU): Using fallback address 0x40000000\r\n")
		fbAddr = QEMU_FB_BASE
	}

	// Map directly to QEMU's framebuffer memory address
	// This is a "side channel" - we write directly to QEMU's framebuffer
	// instead of allocating our own memory. This avoids conflicts with
	// kernel memory space (heap, stack, etc.).
	fbinfo.Buf = unsafe.Pointer(fbAddr)

	// Clear framebuffer (black screen)
	bzero(fbinfo.Buf, fbinfo.BufSize)

	uartPuts("framebufferInit (QEMU): Framebuffer mapped successfully\r\n")
	uartPuts("  Address: 0x")
	// Print address in hex
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(fbAddr) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")
	uartPuts("  Size: ")
	uartPutUint32(fbinfo.BufSize)
	uartPuts(" bytes\r\n")
	uartPuts("  Dimensions: ")
	uartPutUint32(fbinfo.Width)
	uartPuts("x")
	uartPutUint32(fbinfo.Height)
	uartPuts("\r\n")
	uartPuts("  Note: Writing directly to QEMU's framebuffer memory\r\n")
	uartPuts("        (side channel, avoids kernel memory conflicts)\r\n")

	return 0
}
