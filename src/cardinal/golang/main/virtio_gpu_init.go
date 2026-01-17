//go:build qemuvirt && aarch64

package main

// initVirtIOGPUDriver initializes the VirtIO GPU driver and framebuffer
// Returns true on success, false on failure
//
//go:nosplit
func initVirtIOGPUDriver() bool {
	uartPutsDirect("VirtIO GPU: Starting initialization...\r\n")

	// Step 1: Find VirtIO GPU device on PCI bus
	if !findVirtIOGPU() {
		uartPutsDirect("VirtIO GPU: Device not found on PCI bus\r\n")
		return false
	}
	uartPutsDirect("VirtIO GPU: Device found\r\n")

	// Step 2: Initialize device (VirtIO handshake)
	if !virtioGPUInit() {
		uartPutsDirect("VirtIO GPU: Device initialization failed\r\n")
		return false
	}
	uartPutsDirect("VirtIO GPU: Device initialized\r\n")

	// Step 3: Setup framebuffer (create resource, attach backing, set scanout)
	if !virtioGPUSetupFramebuffer(QEMU_FB_WIDTH, QEMU_FB_HEIGHT) {
		uartPutsDirect("VirtIO GPU: Framebuffer setup failed\r\n")
		return false
	}
	uartPutsDirect("VirtIO GPU: Framebuffer setup complete\r\n")

	// Step 4: Connect to framebuffer API (fbinfo global)
	fbinfo.Width = QEMU_FB_WIDTH
	fbinfo.Height = QEMU_FB_HEIGHT
	fbinfo.Pitch = QEMU_FB_WIDTH * QEMU_BYTES_PER_PIXEL
	fbinfo.CharsWidth = fbinfo.Width / CHAR_WIDTH
	fbinfo.CharsHeight = fbinfo.Height / CHAR_HEIGHT
	fbinfo.CharsX = 0
	fbinfo.CharsY = 0
	fbinfo.BufSize = fbinfo.Pitch * fbinfo.Height
	fbinfo.Buf = virtioGPUDevice.Framebuffer

	// Step 5: Clear screen to black background
	bzero4K(fbinfo.Buf, fbinfo.BufSize)

	// Step 6: TODO - Render boot Mazarin image (disabled until symbols are fixed)
	// imageData := GetBootMazarinImageData()
	// if imageData != nil {
	// 	// Center the image on 1920x1080 screen
	// 	// Assuming image is 512x512, center at (704, 284)
	// 	RenderImageData(imageData, 704, 284, false)
	// }

	// Step 7: Transfer framebuffer to host display
	virtioGPUTransferToHost(0, 0, QEMU_FB_WIDTH, QEMU_FB_HEIGHT)

	// Success message
	uartPutsDirect("VirtIO GPU initialized: 1920x1080 @ 32bpp\r\n")

	return true
}
