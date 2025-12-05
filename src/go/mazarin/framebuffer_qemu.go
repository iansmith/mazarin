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
	uartPuts("FB1\r\n")

	// Set fixed dimensions for QEMU
	uartPuts("FB1a\r\n")
	fbinfo.Width = QEMU_FB_WIDTH
	uartPuts("FB1b\r\n")
	fbinfo.Height = QEMU_FB_HEIGHT
	uartPuts("FB1c\r\n")
	fbinfo.Pitch = fbinfo.Width * BYTES_PER_PIXEL
	uartPuts("FB1d\r\n")
	fbinfo.CharsWidth = fbinfo.Width / CHAR_WIDTH
	uartPuts("FB1e\r\n")
	fbinfo.CharsHeight = fbinfo.Height / CHAR_HEIGHT
	uartPuts("FB1f\r\n")
	fbinfo.CharsX = 0
	uartPuts("FB1g\r\n")
	fbinfo.CharsY = 0
	uartPuts("FB1h\r\n")

	// Calculate framebuffer size
	fbinfo.BufSize = fbinfo.Pitch * fbinfo.Height
	uartPuts("FB1i\r\n")

	uartPuts("FB2\r\n")
	// Try ramfb first (recommended for AArch64)
	// This is the simplest display device for AArch64
	uartPuts("FB: Attempting ramfb initialization...\r\n")
	ramfbSuccess := ramfbInit()
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
		uartPuts(" Width=")
		uartPutUint32(fbinfo.Width)
		uartPuts(" Height=")
		uartPutUint32(fbinfo.Height)
		uartPuts(" Pitch=")
		uartPutUint32(fbinfo.Pitch)
		uartPuts("\r\n")

		// Write test pattern
		// Note: XRGB8888 format is 32-bit (4 bytes per pixel)
		// Format: [X/Unused:8][Red:8][Green:8][Blue:8] = 0x00RRGGBB
		testPixels32 := (*[1 << 28]uint32)(fbinfo.Buf) // 32-bit pixels
		uartPuts("FB: Writing test pattern to ramfb (XRGB8888 format)...\r\n")

		// Test: Write a single pixel first (white = 0x00FFFFFF)
		uartPuts("FB: Writing first pixel...\r\n")
		testPixels32[0] = 0x00FFFFFF // White in XRGB8888
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
				testPixels32[offset] = 0x00FFFFFF // White in XRGB8888
				pixelsWritten++
			}
			if y%10 == 0 {
				uartPuts("FB: Row ")
				uartPutUint32(uint32(y))
				uartPuts(" written\r\n")
			}
		}
		uartPuts("FB: Test pattern written (")
		uartPutUint32(uint32(pixelsWritten))
		uartPuts(" pixels)\r\n")

		// Verify a few pixels
		uartPuts("FB: Verifying pixels...\r\n")
		verifyCount := 0
		for i := 0; i < 10; i++ {
			if testPixels32[i] == 0x00FFFFFF {
				verifyCount++
			}
		}
		uartPuts("FB: Verified ")
		uartPutUint32(uint32(verifyCount))
		uartPuts("/10 pixels\r\n")

		uartPuts("FB INIT DONE (ramfb)\r\n")
		return 0
	}

	// Fallback to bochs-display/VGA (simpler - just PCI BARs, no fw_cfg)
	uartPuts("FB: ramfb failed, trying bochs-display...\r\n")
	uartPuts("FB: bochs-display is simpler - just PCI BARs, no fw_cfg\r\n")

	// Find bochs-display device and get full info (BAR0=MMIO, BAR2=framebuffer)
	uartPuts("FB: Searching for bochs-display PCI device...\r\n")
	if !findBochsDisplayFull() {
		uartPuts("FB: ERROR - bochs-display not found\r\n")
		uartPuts("FB: No display device available\r\n")
		return 1
	}
	uartPuts("FB: bochs-display found successfully\r\n")
	uartPuts("FB3\r\n")

	uartPuts("FB4\r\n")
	// Try to initialize VBE registers to set video mode
	// NOTE: This may crash on AArch64 because bochs-display is designed for x86
	// If it crashes, we'll skip VBE and try writing directly to framebuffer
	uartPuts("FB4a: Attempting VBE init...\r\n")
	vbeSuccess := initBochsDisplay(QEMU_FB_WIDTH, QEMU_FB_HEIGHT, 24)
	if !vbeSuccess {
		uartPuts("FB: VBE init failed, continuing without VBE\r\n")
		// Continue anyway - maybe framebuffer works without VBE initialization
	} else {
		uartPuts("FB: VBE init OK\r\n")
	}
	uartPuts("FB5\r\n")

	// Use framebuffer address from BAR0 (correct mapping)
	fbAddr := bochsDisplayInfo.Framebuffer

	if fbAddr == 0 {
		uartPuts("ERROR: FB addr is 0\r\n")
		return 1
	}

	// Debug: Print framebuffer address
	uartPuts("FB addr: 0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(fbAddr) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")

	// Map directly to QEMU's framebuffer memory address
	fbinfo.Buf = unsafe.Pointer(fbAddr)

	// Test: Try writing a single byte first to see if address is accessible
	uartPuts("Testing FB write...\r\n")

	// Use mmio_write to test if address is accessible (safer than direct pointer)
	// Write a test value to first byte
	testAddr := fbAddr
	uartPuts("Writing to 0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(testAddr) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")

	// Try writing via pointer (this might crash if address is invalid)
	testPixels := (*[1 << 30]byte)(fbinfo.Buf)
	uartPuts("Pointer cast OK\r\n")

	testPixels[0] = 0xFF // Blue (BGR)
	uartPuts("Byte 0 written\r\n")
	testPixels[1] = 0xFF // Green
	uartPuts("Byte 1 written\r\n")
	testPixels[2] = 0xFF // Red
	uartPuts("Byte 2 written\r\n")
	uartPuts("Test pixel written\r\n")

	// Write a few more pixels to make it visible - but do it more carefully
	uartPuts("Writing 100 pixels...\r\n")
	for i := 0; i < 100; i++ {
		offset := i * 3
		testPixels[offset+0] = 0xFF // Blue
		testPixels[offset+1] = 0xFF // Green
		testPixels[offset+2] = 0xFF // Red
		// Add a small delay every 10 pixels to avoid overwhelming
		if i%10 == 9 {
			// Small delay
			for j := 0; j < 1000; j++ {
			}
		}
	}
	uartPuts("100 pixels written\r\n")

	// Try to verify the write worked by reading back
	uartPuts("Verifying write...\r\n")
	if testPixels[0] == 0xFF && testPixels[1] == 0xFF && testPixels[2] == 0xFF {
		uartPuts("Write verified OK\r\n")
	} else {
		uartPuts("Write verification FAILED\r\n")
	}

	// Write a large white rectangle to make it very visible
	// Fill the entire top 100 rows with white pixels
	uartPuts("Filling top 100 rows with white...\r\n")
	for y := 0; y < 100; y++ {
		for x := 0; x < int(fbinfo.Width); x++ {
			offset := (y * int(fbinfo.Pitch)) + (x * BYTES_PER_PIXEL)
			testPixels[offset+0] = 0xFF // Blue
			testPixels[offset+1] = 0xFF // Green
			testPixels[offset+2] = 0xFF // Red
		}
	}
	uartPuts("White rectangle written\r\n")

	// Also fill bottom 100 rows with red to make it even more visible
	uartPuts("Filling bottom 100 rows with red...\r\n")
	for y := int(fbinfo.Height) - 100; y < int(fbinfo.Height); y++ {
		for x := 0; x < int(fbinfo.Width); x++ {
			offset := (y * int(fbinfo.Pitch)) + (x * BYTES_PER_PIXEL)
			testPixels[offset+0] = 0x00 // Blue
			testPixels[offset+1] = 0x00 // Green
			testPixels[offset+2] = 0xFF // Red
		}
	}
	uartPuts("Red rectangle written\r\n")

	uartPuts("FB INIT DONE\r\n")
	return 0
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

	// Top 100 rows: white
	for y := 0; y < 100; y++ {
		for x := 0; x < int(fbinfo.Width); x++ {
			offset := y*int(fbinfo.Width) + x
			testPixels32[offset] = 0x00FFFFFF // White
		}
	}

	// Next 100 rows: red
	for y := 100; y < 200 && y < int(fbinfo.Height); y++ {
		for x := 0; x < int(fbinfo.Width); x++ {
			offset := y*int(fbinfo.Width) + x
			testPixels32[offset] = 0x00FF0000 // Red
		}
	}

	// Next 100 rows: green
	for y := 200; y < 300 && y < int(fbinfo.Height); y++ {
		for x := 0; x < int(fbinfo.Width); x++ {
			offset := y*int(fbinfo.Width) + x
			testPixels32[offset] = 0x0000FF00 // Green
		}
	}

	// Next 100 rows: blue
	for y := 300; y < 400 && y < int(fbinfo.Height); y++ {
		for x := 0; x < int(fbinfo.Width); x++ {
			offset := y*int(fbinfo.Width) + x
			testPixels32[offset] = 0x000000FF // Blue
		}
	}

	// Ensure all writes are visible to the device
	dsb()
}
