//go:build qemuvirt && aarch64

package main

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
	// Start cursor well above the bottom edge (not at the extreme edge)
	// CharsHeight = 135, so starting at row 100 gives good visible space
	fbinfo.CharsY = 100
	uartPuts("FB: CharsY set to row 100\r\n")

	// Calculate framebuffer size
	fbinfo.BufSize = fbinfo.Pitch * fbinfo.Height
	uartPuts("FB: BufSize calculated\r\n")

	// Initialize ramfb (allocates framebuffer memory)
	uartPuts("FB: Attempting ramfb initialization...\r\n")
	uartPuts("FB: About to call ramfbInit()...\r\n")
	ramfbSuccess := ramfbInit()
	uartPuts("FB: ramfbInit() returned\r\n")
	if ramfbSuccess {
		uartPuts("FB: ramfb reported success\r\n")
		uartPuts("FB: ramfb initialized\r\n")

		// Wait a moment for ramfb to fully initialize
		uartPuts("FB: Waiting for ramfb to initialize...\r\n")
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

		// Write test pattern
		// Note: XRGB8888 format is 32-bit (4 bytes per pixel)
		// Format: [X/Unused:8][Red:8][Green:8][Blue:8] = 0x00RRGGBB
		testPixels32 := (*[1 << 28]uint32)(fbinfo.Buf) // 32-bit pixels
		uartPuts("FB: Writing test pattern to ramfb (XRGB8888 format)...\r\n")

		// Test: Write a single pixel first
		// XRGB8888 format: [X:8][R:8][G:8][B:8] = 0x00RRGGBB
		// On little-endian AArch64, 0x00FFFFFF is stored as bytes: FF FF FF 00
		// Which QEMU reads as: Blue=FF, Green=FF, Red=FF, X=00 (white)
		// So we use 0x00FFFFFF directly (no byte-swapping needed)
		uartPuts("FB: Writing first pixel...\r\n")
		testPixels32[0] = 0x00FFFFFF // White in XRGB8888 (0x00RRGGBB format)
		uartPuts("FB: First pixel written\r\n")

		// Verify the write
		if testPixels32[0] == 0x00FFFFFF {
			uartPuts("FB: First pixel verified OK\r\n")
		} else {
			uartPuts("FB: First pixel verification FAILED\r\n")
		}

		// Fill top 100 rows with white
		uartPuts("FB: Filling top 100 rows...\r\n")
		pixelsWritten := 0
		for y := 0; y < 100; y++ {
			for x := 0; x < int(fbinfo.Width); x++ {
				offset := y*int(fbinfo.Width) + x
				testPixels32[offset] = 0x00FFFFFF // White in XRGB8888 (0x00RRGGBB)
				pixelsWritten++
			}
			if y%10 == 0 {
				uartPuts("FB: Row 0x")
				printHex32(uint32(y))
				uartPuts(" written\r\n")
			}
		}
		uartPuts("FB: Test pattern written (0x")
		printHex32(uint32(pixelsWritten))
		uartPuts(" pixels)\r\n")

		// Verify a few pixels
		uartPuts("FB: Verifying pixels...\r\n")
		verifyCount := 0
		for i := 0; i < 10; i++ {
			if testPixels32[i] == 0x00FFFFFF {
				verifyCount++
			}
		}
		uartPuts("FB: Verified 0x")
		printHex32(uint32(verifyCount))
		uartPuts("/0xA pixels\r\n")

		// Force memory barrier and ensure writes are visible
		dsb()
		uartPuts("FB: Memory barrier executed after pixel writes\r\n")
		uartPuts("FB: Pixels written - ramfb was already configured, display should update\r\n")

		// Give QEMU additional time to process the framebuffer update
		uartPuts("FB: Waiting for QEMU to process framebuffer...\r\n")
		for delay := 0; delay < 5000000; delay++ {
			// Additional delay to ensure QEMU has time to read framebuffer
		}
		uartPuts("FB: Delay complete\r\n")

		uartPuts("FB INIT DONE (ramfb)\r\n")
		return 0
	}

	// ramfb failed
	uartPuts("FB: ERROR - ramfb initialization failed\r\n")
	uartPuts("FB: No display device available\r\n")
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
