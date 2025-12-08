//go:build qemuvirt && aarch64

package main

import (
	"unsafe"
)

// QEMU framebuffer constants for ramfb device
// ramfb allocates framebuffer memory via kmalloc and configures QEMU via fw_cfg
const (
	QEMU_FB_WIDTH  = 1920
	QEMU_FB_HEIGHT = 1080
)

// Override BYTES_PER_PIXEL for QEMU - ramfb uses XRGB8888 (32-bit, 4 bytes per pixel)
// This overrides the 3-byte value from framebuffer_common.go
const QEMU_BYTES_PER_PIXEL = 4

// framebufferInit initializes the framebuffer for QEMU using ramfb device
// Returns 0 on success, non-zero on error
//
// ramfb allocates framebuffer memory via kmalloc and configures QEMU via fw_cfg
//
//go:nosplit
func framebufferInit() int32 {
	uartPuts("FB: framebufferInit() called\r\n")
	uartPuts("FB: About to access fbinfo...\r\n")
	// Set fixed dimensions for QEMU
	uartPuts("FB: Setting dimensions...\r\n")
	fbinfo.Width = QEMU_FB_WIDTH
	uartPuts("FB: Width set\r\n")
	fbinfo.Height = QEMU_FB_HEIGHT
	uartPuts("FB: Height set\r\n")
	// XRGB8888 format uses 4 bytes per pixel
	fbinfo.Pitch = fbinfo.Width * QEMU_BYTES_PER_PIXEL
	uartPuts("FB: Pitch set\r\n")
	fbinfo.CharsWidth = fbinfo.Width / CHAR_WIDTH
	uartPuts("FB: CharsWidth set\r\n")
	fbinfo.CharsHeight = fbinfo.Height / CHAR_HEIGHT
	uartPuts("FB: CharsHeight set\r\n")
	fbinfo.CharsX = 0
	uartPuts("FB: CharsX set\r\n")
	fbinfo.CharsY = fbinfo.CharsHeight - 1
	uartPuts("FB: CharsY set to bottom\r\n")

	// Calculate framebuffer size
	fbinfo.BufSize = fbinfo.Pitch * fbinfo.Height
	uartPuts("FB: BufSize calculated\r\n")

	// Try bochs-display via PCI enumeration first
	uartPuts("FB: Attempting bochs-display initialization via PCI...\r\n")
	if findBochsDisplayFull() {
		uartPuts("FB: bochs-display found!\r\n")
		// Initialize the bochs display with our resolution
		// Using 32 bits per pixel (XRGB8888)
		if initBochsDisplay(640, 480, 32) {
			uartPuts("FB: bochs-display initialized successfully\r\n")
			// Set the framebuffer address from the PCI device
			fbinfo.Buf = unsafe.Pointer(bochsDisplayInfo.Framebuffer)
			fbinfo.Width = 640
			fbinfo.Height = 480
			fbinfo.Pitch = 640 * 4 // 32-bit pixels
			fbinfo.CharsWidth = fbinfo.Width / CHAR_WIDTH
			fbinfo.CharsHeight = fbinfo.Height / CHAR_HEIGHT

			// Memory barrier to ensure all writes complete
			dsb()
			uartPuts("FB INIT DONE (bochs-display)\r\n")
			return 0
		}
	}

	// Fallback: Try ramfb
	uartPuts("FB: bochs-display not available, trying ramfb...\r\n")
	ramfbSuccess := ramfbInit()
	uartPuts("FB: ramfbInit() returned\r\n")
	if ramfbSuccess {
		uartPuts("FB: ramfb reported success\r\n")
		uartPuts("FB INIT DONE (ramfb)\r\n")
		return 0
	}

	// Both failed
	uartPuts("FB: ERROR - No display device available (bochs-display and ramfb both failed)\r\n")
	return 1
}

// refreshFramebuffer refreshes the framebuffer to keep it visible
// This prevents ramfb from clearing the display
// Uses XRGB8888 format (32-bit pixels)
//
//go:nosplit
func refreshFramebuffer() {
	if fbinfo.Buf == nil {
		return
	}

	// XRGB8888 format: 32-bit pixels, format 0x00RRGGBB
	testPixels32 := (*[1 << 28]uint32)(fbinfo.Buf)

	// Fill entire screen with a pattern to make it very visible
	// Top 100 rows: white (0x00FFFFFF)
	// Next 100 rows: red (0x00FF0000)
	// Next 100 rows: green (0x0000FF00)
	// Next 100 rows: blue (0x000000FF)

	// XRGB8888 format: [X:8][R:8][G:8][B:8] = 0x00RRGGBB
	// On little-endian AArch64, values are stored correctly in memory
	// Top 100 rows: white
	for y := 0; y < 100; y++ {
		for x := 0; x < int(fbinfo.Width); x++ {
			offset := y*int(fbinfo.Width) + x
			testPixels32[offset] = 0x00FFFFFF // White (R=FF, G=FF, B=FF)
		}
	}

	// Next 100 rows: red
	for y := 100; y < 200 && y < int(fbinfo.Height); y++ {
		for x := 0; x < int(fbinfo.Width); x++ {
			offset := y*int(fbinfo.Width) + x
			testPixels32[offset] = 0x00FF0000 // Red (R=FF, G=00, B=00)
		}
	}

	// Next 100 rows: green
	for y := 200; y < 300 && y < int(fbinfo.Height); y++ {
		for x := 0; x < int(fbinfo.Width); x++ {
			offset := y*int(fbinfo.Width) + x
			testPixels32[offset] = 0x0000FF00 // Green (R=00, G=FF, B=00)
		}
	}

	// Next 100 rows: blue
	for y := 300; y < 400 && y < int(fbinfo.Height); y++ {
		for x := 0; x < int(fbinfo.Width); x++ {
			offset := y*int(fbinfo.Width) + x
			testPixels32[offset] = 0x000000FF // Blue (R=00, G=00, B=FF)
		}
	}

	// Ensure all writes are visible to the device
	dsb()
}
