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
	QEMU_FB_WIDTH  = 1280
	QEMU_FB_HEIGHT = 720
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
	// Try VirtIO GPU first (best support on ARM/AArch64)
	uartPuts("FB: Attempting VirtIO GPU initialization...\r\n")
	if findVirtIOGPU() {
		uartPuts("FB: VirtIO GPU device found\r\n")
		if virtioGPUInit() {
			uartPuts("FB: VirtIO GPU initialized\r\n")
			// Setup framebuffer (allocation happens inside setup to reduce stack usage)
			if virtioGPUSetupFramebuffer(QEMU_FB_WIDTH, QEMU_FB_HEIGHT) {
				uartPuts("FB: VirtIO GPU framebuffer setup complete\r\n")
				// Set framebuffer info
				fbinfo.Buf = virtioGPUDevice.Framebuffer
				fbinfo.BufSize = virtioGPUDevice.FramebufferSize

				uartPuts("FB: Framebuffer at 0x")
				for shift := 60; shift >= 0; shift -= 4 {
					digit := (uint64(pointerToUintptr(fbinfo.Buf)) >> shift) & 0xF
					if digit < 10 {
						uartPutc(byte('0' + digit))
					} else {
						uartPutc(byte('A' + digit - 10))
					}
				}
				uartPuts("\r\n")

				// Write test pattern
				testPixels32 := (*[1 << 28]uint32)(fbinfo.Buf)
				uartPuts("FB: Writing test pattern to VirtIO GPU framebuffer...\r\n")
				for y := 0; y < 100; y++ {
					for x := 0; x < int(fbinfo.Width); x++ {
						offset := y*int(fbinfo.Width) + x
						testPixels32[offset] = 0x00FFFFFF // White in BGRA8888
					}
				}
				dsb() // Ensure writes are visible

				// Transfer to host to update display
				virtioGPUTransferToHost(0, 0, fbinfo.Width, fbinfo.Height)

				uartPuts("FB INIT DONE (VirtIO GPU)\r\n")
				return 0
			}
		}
		uartPuts("FB: VirtIO GPU initialization failed, trying ramfb...\r\n")
	}

	// Fallback to ramfb (recommended for AArch64, works with all display backends)
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

		// Give QEMU additional time to process the framebuffer update
		uartPuts("FB: Waiting for QEMU to process framebuffer...\r\n")
		for delay := 0; delay < 5000000; delay++ {
			// Additional delay to ensure QEMU has time to read framebuffer
		}
		uartPuts("FB: Delay complete\r\n")

		uartPuts("FB INIT DONE (ramfb)\r\n")
		return 0
	}

	// Fallback to bochs-display if ramfb not available
	// Note: bochs-display has known PCI memory bar caching issues on ARM
	uartPuts("FB: ramfb failed, trying bochs-display...\r\n")
	uartPuts("FB: WARNING - bochs-display has known issues on AArch64\r\n")
	uartPuts("FB: Checking for bochs-display device...\r\n")
	if findBochsDisplayFull() {
		uartPuts("FB: bochs-display found, using it\r\n")
		// Initialize bochs-display
		uartPuts("FB: Initializing bochs-display...\r\n")
		vbeSuccess := initBochsDisplay(QEMU_FB_WIDTH, QEMU_FB_HEIGHT, 24)
		if !vbeSuccess {
			uartPuts("FB: VBE init failed, continuing anyway...\r\n")
		} else {
			uartPuts("FB: VBE init OK\r\n")
		}

		// Set framebuffer info from bochs-display
		fbAddr := bochsDisplayInfo.Framebuffer
		if fbAddr == 0 {
			uartPuts("FB: ERROR - Framebuffer address is 0\r\n")
			uartPuts("FB: Falling back to ramfb...\r\n")
		} else {
			fbinfo.Buf = unsafe.Pointer(fbAddr)
			fbinfo.BufSize = fbinfo.Pitch * fbinfo.Height

			uartPuts("FB: bochs-display initialized\r\n")
			uartPuts("FB: Framebuffer at 0x")
			for shift := 60; shift >= 0; shift -= 4 {
				digit := (uint64(fbAddr) >> shift) & 0xF
				if digit < 10 {
					uartPutc(byte('0' + digit))
				} else {
					uartPutc(byte('A' + digit - 10))
				}
			}
			uartPuts("\r\n")
			uartPuts("FB INIT DONE (bochs-display)\r\n")
			return 0
		}
	}

	// Both ramfb and bochs-display failed
	uartPuts("FB: ERROR - Both bochs-display and ramfb failed\r\n")
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
