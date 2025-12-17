//go:build qemuvirt && aarch64

package main

import (
	"unsafe"

	"mazboot/asm"
)

// QEMU framebuffer constants for bochs-display device
// bochs-display uses a fixed framebuffer address from PCI BAR0 and configures via VBE MMIO registers
const (
	QEMU_FB_WIDTH  = 1280
	QEMU_FB_HEIGHT = 720
)

// Override BYTES_PER_PIXEL for QEMU - bochs-display uses XRGB8888 (32-bit, 4 bytes per pixel)
// This overrides the 3-byte value from framebuffer_common.go
// Note: bochs-display uses BGR byte order (Blue, Green, Red) for pixel data
const QEMU_BYTES_PER_PIXEL = 4

// framebufferInit initializes the framebuffer for QEMU using bochs-display device
// Returns 0 on success, non-zero on error
func framebufferInit() int32 {
	// Set fixed dimensions for QEMU
	fbinfo.Width = QEMU_FB_WIDTH
	fbinfo.Height = QEMU_FB_HEIGHT
	fbinfo.Pitch = fbinfo.Width * QEMU_BYTES_PER_PIXEL
	fbinfo.CharsWidth = fbinfo.Width / CHAR_WIDTH
	fbinfo.CharsHeight = fbinfo.Height / CHAR_HEIGHT
	fbinfo.CharsX = 0
	fbinfo.CharsY = fbinfo.CharsHeight - 1
	fbinfo.BufSize = fbinfo.Pitch * fbinfo.Height

	// Initialize bochs-display (finds PCI device and configures VBE registers)
	if !findBochsDisplayFull() {
		print("FATAL: bochs-display not found\r\n")
		return 1
	}

	if !initBochsDisplay(uint16(QEMU_FB_WIDTH), uint16(QEMU_FB_HEIGHT), 32) {
		print("FATAL: bochs-display init failed\r\n")
		return 1
	}

	// Set framebuffer address from bochs-display info
	fbinfo.Buf = unsafe.Pointer(bochsDisplayInfo.Framebuffer)

	// Short delay for bochs-display to initialize
	for delay := 0; delay < 2000000; delay++ {
	}

	// Fill entire framebuffer with midnight blue background
	testPixels32 := (*[1 << 28]uint32)(fbinfo.Buf)
	midnightBlue := uint32(0x00191B70)
	rowByteSize := fbinfo.Width * 4

	// Fill first row
	for x := uint32(0); x < fbinfo.Width; x++ {
		testPixels32[x] = midnightBlue
	}

	// Copy first row to all remaining rows
	firstRowAddr := uintptr(fbinfo.Buf)
	for y := uint32(1); y < fbinfo.Height; y++ {
		destAddr := uintptr(fbinfo.Buf) + uintptr(y*fbinfo.Pitch)
		asm.MemmoveBytes(unsafe.Pointer(destAddr), unsafe.Pointer(firstRowAddr), uint32(rowByteSize))
	}

	asm.Dsb()

	// Short delay for QEMU to process framebuffer
	for delay := 0; delay < 5000000; delay++ {
	}

	return 0
}

// refreshFramebuffer refreshes the framebuffer to keep it visible
// Uses XRGB8888 format (32-bit pixels)
// Note: bochs-display uses BGR byte order (Blue, Green, Red) for pixel data
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
	asm.Dsb()
}
