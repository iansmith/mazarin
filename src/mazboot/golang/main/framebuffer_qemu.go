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
//
// bochs-display uses a fixed framebuffer address from PCI BAR0 and configures via VBE MMIO registers
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
	// Start cursor at the bottom of the screen
	// When text is printed, it will scroll upward from the bottom
	fbinfo.CharsY = fbinfo.CharsHeight - 1
	uartPuts("FB: CharsY set to bottom\r\n")

	// Calculate framebuffer size
	fbinfo.BufSize = fbinfo.Pitch * fbinfo.Height
	uartPuts("FB: BufSize calculated\r\n")

	// Initialize bochs-display (finds PCI device and configures VBE registers)
	uartPuts("FB: Attempting bochs-display initialization...\r\n")
	uartPuts("FB: About to call findBochsDisplayFull()...\r\n")
	bochsFound := findBochsDisplayFull()
	uartPuts("FB: findBochsDisplayFull() returned\r\n")
	if !bochsFound {
		uartPuts("FB: ERROR - bochs-display device not found\r\n")
		uartPuts("FB: No display device available\r\n")
		return 1
	}

	uartPuts("FB: bochs-display device found\r\n")
	uartPuts("FB: About to call initBochsDisplay()...\r\n")
	bochsInitSuccess := initBochsDisplay(uint16(QEMU_FB_WIDTH), uint16(QEMU_FB_HEIGHT), 32)
	uartPuts("FB: initBochsDisplay() returned\r\n")
	if !bochsInitSuccess {
		uartPuts("FB: ERROR - bochs-display initialization failed\r\n")
		uartPuts("FB: No display device available\r\n")
		return 1
	}

	uartPuts("FB: bochs-display initialized successfully\r\n")

	// Set framebuffer address from bochs-display info
	fbinfo.Buf = unsafe.Pointer(bochsDisplayInfo.Framebuffer)
	uartPuts("FB: Framebuffer address set from bochs-display BAR0\r\n")

	// Wait a moment for bochs-display to fully initialize
	uartPuts("FB: Waiting for bochs-display to initialize...\r\n")
	for delay := 0; delay < 2000000; delay++ {
	}
	uartPuts("FB: Initialization delay complete\r\n")

		// Debug: Verify framebuffer info
		uartPuts("FB: Buf=0x")
		for shift := 60; shift >= 0; shift -= 4 {
			digit := (uint64(uintptr(fbinfo.Buf)) >> shift) & 0xF
			if digit < 10 {
				uartPutc(byte('0' + digit))
			} else {
				uartPutc(byte('A' + digit - 10))
			}
		}
		uartPuts(" Width=0x")
		printHex32(fbinfo.Width)
		uartPuts(" Height=0x")
		printHex32(fbinfo.Height)
		uartPuts(" Pitch=0x")
		printHex32(fbinfo.Pitch)
		uartPuts("\r\n")

		// Fill entire framebuffer with midnight blue background
		// Note: XRGB8888 format is 32-bit (4 bytes per pixel)
		// Format: [X/Unused:8][Red:8][Green:8][Blue:8] = 0x00RRGGBB
		testPixels32 := (*[1 << 28]uint32)(fbinfo.Buf) // 32-bit pixels
		uartPuts("FB: Filling entire screen with midnight blue background...\r\n")

		// MidnightBlue = 0x00191B70 (RGB: 25, 27, 112)
		midnightBlue := uint32(0x00191B70)

		// Optimize: Fill first row pixel-by-pixel, then copy to remaining rows
		rowByteSize := fbinfo.Width * 4 // 4 bytes per pixel
		uartPuts("FB: Filling first row...\r\n")
		for x := uint32(0); x < fbinfo.Width; x++ {
			testPixels32[x] = midnightBlue
		}
		uartPuts("FB: Copying row to remaining rows...\r\n")

		// Copy first row to all remaining rows using MemmoveBytes
		firstRowAddr := uintptr(fbinfo.Buf)
		for y := uint32(1); y < fbinfo.Height; y++ {
			destAddr := uintptr(fbinfo.Buf) + uintptr(y*fbinfo.Pitch)
			asm.MemmoveBytes(unsafe.Pointer(destAddr), unsafe.Pointer(firstRowAddr), uint32(rowByteSize))
			if y%100 == 0 {
				uartPuts("FB: Row 0x")
				printHex32(y)
				uartPuts(" copied\r\n")
			}
		}
		uartPuts("FB: Screen filled\r\n")

		// Verify a few pixels
		uartPuts("FB: Verifying pixels...\r\n")
		verifyCount := 0
		for i := 0; i < 10; i++ {
			if testPixels32[i] == midnightBlue {
				verifyCount++
			}
		}
		uartPuts("FB: Verified 0x")
		printHex32(uint32(verifyCount))
		uartPuts("/0xA pixels\r\n")

		// Force memory barrier and ensure writes are visible
		asm.Dsb()
		uartPuts("FB: Memory barrier executed after pixel writes\r\n")
		uartPuts("FB: Pixels written - bochs-display configured, display should update\r\n")

		// Give QEMU additional time to process the framebuffer update
		uartPuts("FB: Waiting for QEMU to process framebuffer...\r\n")
		for delay := 0; delay < 5000000; delay++ {
			// Additional delay to ensure QEMU has time to read framebuffer
		}
		uartPuts("FB: Delay complete\r\n")

		uartPuts("FB INIT DONE (bochs-display)\r\n")
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
