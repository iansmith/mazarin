package gpu

import (
	"mazzy/kmazarin/ds"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/ksync"
	"unsafe"
)

// Display resolution constants. Change these to resize the QEMU display window.
const (
	DisplayWidth  = 1800
	DisplayHeight = 1200
)

// gpuLock protects all GPU command queue operations. Without this,
// concurrent FlushFramebuffer syscalls from different shepherds corrupt
// the virtio ring and hang the GPU.
var gpuLock uint32

// Init initializes the VirtIO GPU device
// This function:
// 1. Finds the VirtIO GPU PCI device
// 2. Initializes the device and virtqueues
// 3. Sets up the framebuffer
// 4. Returns true on success, false on failure
func Init() bool {
	// Find VirtIO GPU device on PCI bus
	if !findVirtIOGPU() {
		return false
	}

	// Initialize device (VirtIO handshake)
	if !virtioGPUInit() {
		return false
	}

	// Query host display dimensions. Use them if available, fall back to constants.
	// On macOS Cocoa, GET_DISPLAY_INFO may return the initial window size (e.g.
	// 640x414) rather than the requested xres/yres, so reject values smaller
	// than the compiled-in defaults.
	dispW, dispH, dispOK := virtioGPUGetDisplayInfo()
	if !dispOK || dispW < DisplayWidth || dispH < DisplayHeight {
		klog.Logf("[VirtIO GPU] Display info %dx%d too small or unavailable, using default %dx%d\n",
			dispW, dispH, DisplayWidth, DisplayHeight)
		dispW = DisplayWidth
		dispH = DisplayHeight
	}

	// Cap to what fits under framebuffer_max_mb (with 2x height for scrolling):
	// maxBytes / 4 bytes-per-pixel / 2 (resource height) pixels.
	maxPixels := framebufferMaxBytes / 4 / 2
	// 64-bit comparison: a hypervisor-reported dispW*dispH can itself overflow
	// uint32 before the cap loop runs, so widen both sides.
	for uint64(dispW)*uint64(dispH) > uint64(maxPixels) {
		// Scale down by 75% until it fits.
		dispW = dispW * 3 / 4
		dispH = dispH * 3 / 4
	}
	klog.Logf("[VirtIO GPU] Using display resolution: %dx%d\n", dispW, dispH)

	// Setup framebuffer at detected resolution @ 32bpp.
	// Resource is 2x display height to enable hardware scrolling via SET_SCANOUT offset.
	resourceHeight := dispH * 2
	if !virtioGPUSetupFramebuffer(dispW, dispH, resourceHeight) {
		return false
	}

	// Fill screen with neumorphic surface color — must match mancini.DefaultPalette().Surface
	// Surface = NRGBA{232, 230, 244, 255} → BGRA uint32 = 0xFFE8E6F4
	fillScreen(0xFFE8E6F4)

	// Initial transfer and flush to make display visible
	virtioGPUTransferToHost(0, 0, dispW, dispH)
	virtioGPUFlush(0, 0, dispW, dispH)

	// Initialize hardware cursor
	if !InitCursor() {
		klog.Errf("[VirtIO GPU] WARNING: Hardware cursor init failed, continuing without cursor\n")
	} else {
		InitCursorTopHalf()
	}

	return true
}

// TransferToHost transfers a region of the framebuffer to the host display
// This is a public wrapper for external callers
func TransferToHost(x, y, width, height uint32) {
	virtioGPUTransferToHost(x, y, width, height)
}

// Flush flushes a region of the framebuffer to the display
// This must be called after TransferToHost to make changes visible
func Flush(x, y, width, height uint32) {
	virtioGPUFlush(x, y, width, height)
}

// UpdateDisplay transfers and flushes a region of the framebuffer.
// Uses batched submission: both TRANSFER_TO_HOST_2D and RESOURCE_FLUSH are
// submitted to the virtqueue together with a single device notify, eliminating
// one round-trip compared to submitting them separately.
// Safe to call concurrently from multiple shepherds — serialized by gpuLock.
//
//go:nosplit
func UpdateDisplay(x, y, width, height uint32) {
	lockGPU()
	virtioGPUTransferAndFlush(x, y, width, height)
	unlockGPU()
}

// UpdateDisplayUnbatched transfers and flushes separately (original path).
// Kept for comparison and fallback.
//
//go:nosplit
func UpdateDisplayUnbatched(x, y, width, height uint32) {
	lockGPU()
	virtioGPUTransferToHost(x, y, width, height)
	virtioGPUFlush(x, y, width, height)
	unlockGPU()
}

// SetScanoutOffset changes the visible region's Y offset in the framebuffer.
// This enables hardware-accelerated scrolling without copying pixels.
// Safe to call concurrently — serialized by gpuLock.
//
//go:nosplit
func SetScanoutOffset(yOffset uint32) bool {
	lockGPU()
	ok := virtioGPUSetScanoutOffset(yOffset)
	unlockGPU()
	return ok
}

// saveAndDisableIRQs / restoreIRQs are the canonical ksync primitives
// (MAZ-167). Note the mask narrowed from all-of-DAIF to I-only — the recorded
// MAZ-167 decision; IRQs are the only exception source that can preempt into
// this driver's critical sections.
//
//go:nosplit
func saveAndDisableIRQs() uint64 {
	return ksync.SaveAndDisableIRQs()
}

//go:nosplit
func restoreIRQs(daif uint64) {
	ksync.RestoreIRQs(daif)
}

// lockGPU spins until the GPU command lock is acquired.
// No attempt limit — GPU commands always complete (internal timeout).
//
// MAZ-173: this lock is only ever taken from thread context — the tablet IRQ
// top-half (TopHalfMoveCursor) drives the cursor queue through its own MMIO
// state and never takes it — so IRQs stay enabled around it. A holder can be
// preempted; waiters spin with IRQs on and are themselves preemptible, so
// that costs latency, never progress. Cursor-queue state shared WITH the IRQ
// top-half is synchronized by masking at its one call site (SetActiveCursor),
// not by this lock.
//
//go:nosplit
func lockGPU() {
	for {
		if ds.CompareAndSwapUint32(&gpuLock, 0, 1) != 0 {
			return
		}
		// Brief spin delay
		for i := 0; i < 100; i++ {
		}
	}
}

// unlockGPU releases the GPU command lock.
//
//go:nosplit
func unlockGPU() {
	ds.StoreUint32(&gpuLock, 0)
}

// RenderBootImage renders the boot image to the framebuffer
// imageAddr: physical address of the image data
// imageSize: size of the image data in bytes
// The image format is: [4 bytes width][4 bytes height][width*height*4 bytes BGRA]
//
//go:nosplit
func RenderBootImage(imageAddr uintptr, imageSize uint64) bool {
	if imageAddr == 0 || imageSize < 8 {
		return false
	}

	// Read image dimensions from header (little-endian)
	imgWidth := *(*uint32)(unsafe.Pointer(imageAddr))
	imgHeight := *(*uint32)(unsafe.Pointer(imageAddr + 4))

	// Validate dimensions are non-zero
	if imgWidth == 0 || imgHeight == 0 {
		return false
	}

	// Calculate expected size using 64-bit math to avoid overflow
	// imgWidth * imgHeight * 4 could overflow uint32 for large images
	expectedSize := uint64(8) + uint64(imgWidth)*uint64(imgHeight)*4
	if imageSize < expectedSize {
		return false
	}

	// Get framebuffer info
	fbWidth := virtioGPUDevice.Width
	fbHeight := virtioGPUDevice.Height
	fbPitch := virtioGPUDevice.Pitch
	fb := virtioGPUDevice.Framebuffer

	if fb == nil {
		return false
	}

	// Validate image fits in framebuffer
	if imgWidth > fbWidth || imgHeight > fbHeight {
		return false
	}

	// Calculate centered position (safe now that we've validated dimensions)
	startX := (fbWidth - imgWidth) / 2
	startY := (fbHeight - imgHeight) / 2

	// Source pixel data starts after 8-byte header
	srcBase := imageAddr + 8

	// Ellipse parameters for fade effect
	// The ellipse is centered on the image with semi-axes equal to half the image dimensions
	centerX := float32(imgWidth) / 2.0
	centerY := float32(imgHeight) / 2.0
	radiusX := float32(imgWidth) / 2.0
	radiusY := float32(imgHeight) / 2.0

	// Fade parameters - start fading at 70% of the radius, fully transparent at 100%
	fadeStart := float32(0.70)
	fadeEnd := float32(1.0)

	// Background color: must match mancini.DefaultPalette().Surface
	// Surface = NRGBA{232, 230, 244, 255} → BGRA uint32 = 0xFFE8E6F4
	const bgColor uint32 = 0xFFE8E6F4
	bgB := uint8(bgColor & 0xFF)         // B=244
	bgG := uint8((bgColor >> 8) & 0xFF)  // G=230
	bgR := uint8((bgColor >> 16) & 0xFF) // R=232

	// Copy pixels to framebuffer (centered) with oval fade and background blending
	for y := uint32(0); y < imgHeight; y++ {
		dstY := startY + y
		if dstY >= fbHeight {
			break
		}

		// Calculate source and destination row addresses
		srcRow := srcBase + uintptr(y*imgWidth*4)
		dstRow := uintptr(fb) + uintptr(dstY)*uintptr(fbPitch) + uintptr(startX)*4

		for x := uint32(0); x < imgWidth; x++ {
			if startX+x >= fbWidth {
				break
			}

			// Calculate normalized elliptical distance from center (0 at center, 1 at edge)
			dx := (float32(x) - centerX) / radiusX
			dy := (float32(y) - centerY) / radiusY
			dist := sqrt32(dx*dx + dy*dy)

			// Calculate alpha multiplier based on distance
			var alphaMult float32
			if dist <= fadeStart {
				alphaMult = 1.0
			} else if dist >= fadeEnd {
				alphaMult = 0.0
			} else {
				// Smooth fade using cosine interpolation
				t := (dist - fadeStart) / (fadeEnd - fadeStart)
				alphaMult = (1.0 + cos32(t*3.14159)) / 2.0
			}

			// Read source pixel (BGRA from boot-image.bin)
			srcPixel := *(*uint32)(unsafe.Pointer(srcRow + uintptr(x)*4))

			// Extract source components (BGRA byte order in source file)
			srcB := uint8(srcPixel & 0xFF)
			srcG := uint8((srcPixel >> 8) & 0xFF)
			srcR := uint8((srcPixel >> 16) & 0xFF)

			// Blend source with background based on alpha
			// result = src * alpha + bg * (1 - alpha)
			outB := uint8(float32(srcB)*alphaMult + float32(bgB)*(1.0-alphaMult))
			outG := uint8(float32(srcG)*alphaMult + float32(bgG)*(1.0-alphaMult))
			outR := uint8(float32(srcR)*alphaMult + float32(bgR)*(1.0-alphaMult))

			// Reassemble pixel in BGRA format (framebuffer byte order)
			dstPixel := uint32(outB) | (uint32(outG) << 8) | (uint32(outR) << 16) | 0xFF000000

			// Write to framebuffer
			*(*uint32)(unsafe.Pointer(dstRow + uintptr(x)*4)) = dstPixel
		}
	}

	return true
}

// sqrt32 computes approximate square root using Newton-Raphson
//
//go:nosplit
func sqrt32(x float32) float32 {
	if x <= 0 {
		return 0
	}
	// Initial guess
	guess := x / 2.0
	// Newton-Raphson iterations
	for i := 0; i < 10; i++ {
		guess = (guess + x/guess) / 2.0
	}
	return guess
}

// cos32 computes approximate cosine using Taylor series
// Input: x in radians
//
//go:nosplit
func cos32(x float32) float32 {
	// Normalize to [0, 2*pi]
	const twoPi = 6.28318
	for x < 0 {
		x += twoPi
	}
	for x > twoPi {
		x -= twoPi
	}

	// Taylor series approximation for cosine
	// cos(x) = 1 - x^2/2! + x^4/4! - x^6/6! + ...
	x2 := x * x
	x4 := x2 * x2
	x6 := x4 * x2
	x8 := x4 * x4

	return 1.0 - x2/2.0 + x4/24.0 - x6/720.0 + x8/40320.0
}

// fillScreen fills the entire framebuffer with a solid color
func fillScreen(color uint32) {
	fb := virtioGPUDevice.Framebuffer
	if fb == nil {
		return
	}

	width := virtioGPUDevice.Width
	height := virtioGPUDevice.Height
	pitch := virtioGPUDevice.Pitch

	for y := uint32(0); y < height; y++ {
		rowPtr := uintptr(fb) + uintptr(y)*uintptr(pitch)
		for x := uint32(0); x < width; x++ {
			*(*uint32)(unsafe.Pointer(rowPtr + uintptr(x)*4)) = color
		}
	}
}

// GetFramebufferPA returns the physical address of the GPU framebuffer.
// Used by MapUserFramebuffer to map the correct physical pages into userspace.
func GetFramebufferPA() uintptr {
	return virtioGPUFramebufferAddr
}

// GetFramebuffer returns the framebuffer pointer
func GetFramebuffer() unsafe.Pointer {
	return virtioGPUDevice.Framebuffer
}

// GetFramebufferSize returns the framebuffer size in bytes
func GetFramebufferSize() uint32 {
	return virtioGPUDevice.FramebufferSize
}

// GetWidth returns the actual display width in pixels.
// Marked nosplit so it can be called from IRQ/nosplit context.
//
//go:nosplit
func GetWidth() uint32 {
	return virtioGPUDevice.Width
}

// GetHeight returns the actual display height in pixels.
// Marked nosplit so it can be called from IRQ/nosplit context.
//
//go:nosplit
func GetHeight() uint32 {
	return virtioGPUDevice.Height
}

// GetResourceHeight returns the total resource height in pixels.
// This may be larger than the display height for scrolling support.
func GetResourceHeight() uint32 {
	return virtioGPUDevice.ResourceHeight
}
