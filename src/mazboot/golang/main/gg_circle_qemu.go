//go:build qemuvirt && aarch64

package main

import (
	"image"
	"unsafe"

	gg "github.com/fogleman/gg"

	"mazboot/asm"
)

// ggCtx is a lazily-initialized gg drawing context that matches the
// current framebuffer dimensions. It renders into an in-memory RGBA
// backbuffer which we then flush into the Bochs BGRX framebuffer.
var ggCtx *gg.Context

// initGGContext initializes the gg context sized to the current
// framebuffer. It is safe to call multiple times.
func initGGContext() {
	uartPuts("DEBUG: initGGContext() - entry\r\n")
	print("TEST: Go print() function works!\n")

	// Debug: Print raw fbinfo values as hex
	uartPuts("DEBUG: fbinfo.Width = 0x")
	uartPutHex64(uint64(fbinfo.Width))
	uartPuts("\r\n")
	uartPuts("DEBUG: fbinfo.Height = 0x")
	uartPutHex64(uint64(fbinfo.Height))
	uartPuts("\r\n")
	uartPuts("DEBUG: fbinfo.Pitch = 0x")
	uartPutHex64(uint64(fbinfo.Pitch))
	uartPuts("\r\n")
	uartPuts("DEBUG: fbinfo addr = 0x")
	uartPutHex64(uint64(uintptr(unsafe.Pointer(&fbinfo))))
	uartPuts("\r\n")

	if ggCtx != nil {
		uartPuts("DEBUG: initGGContext() - already initialized\r\n")
		return
	}
	if fbinfo.Width == 0 || fbinfo.Height == 0 {
		uartPuts("DEBUG: initGGContext() - fbinfo not ready\r\n")
		return
	}

	w := int(fbinfo.Width)
	h := int(fbinfo.Height)

	uartPuts("DEBUG: initGGContext() - w = 0x")
	uartPutHex64(uint64(w))
	uartPuts(", h = 0x")
	uartPutHex64(uint64(h))
	uartPuts("\r\n")

	// Test: Try creating image.Rectangle directly to isolate the issue
	uartPuts("DEBUG: Creating image.Rect(0, 0, w, h)...\r\n")
	rect := image.Rect(0, 0, w, h)
	uartPuts("DEBUG: rect.Min.X = 0x")
	uartPutHex64(uint64(rect.Min.X))
	uartPuts(", rect.Min.Y = 0x")
	uartPutHex64(uint64(rect.Min.Y))
	uartPuts("\r\n")
	uartPuts("DEBUG: rect.Max.X = 0x")
	uartPutHex64(uint64(rect.Max.X))
	uartPuts(", rect.Max.Y = 0x")
	uartPutHex64(uint64(rect.Max.Y))
	uartPuts("\r\n")

	// Test: Try with hardcoded small values
	uartPuts("DEBUG: Creating image.Rect(0, 0, 100, 100)...\r\n")
	rect2 := image.Rect(0, 0, 100, 100)
	uartPuts("DEBUG: rect2.Max.X = 0x")
	uartPutHex64(uint64(rect2.Max.X))
	uartPuts(", rect2.Max.Y = 0x")
	uartPutHex64(uint64(rect2.Max.Y))
	uartPuts("\r\n")

	// Test: Create a small RGBA image first
	uartPuts("DEBUG: Creating small image.NewRGBA(image.Rect(0,0,10,10))...\r\n")
	print("PRINT: Before small image allocation\n")

	// DEBUG: Check mcache.alloc[47] right before allocation
	// NOTE: mcache struct is allocated at 0x41020000 (not the pointer variable at 0x40131408)
	mcacheStructAddr := uintptr(0x41020000)
	allocArrayStart := mcacheStructAddr + 0x30
	emptymspanAddr := uintptr(0x40108500)

	spanPtr47 := readMemory64(allocArrayStart + 47*8)
	uartPuts("DEBUG: mcache.alloc[47] = 0x")
	uartPutHex64(spanPtr47)
	uartPuts(" (expected 0x40108500)\r\n")

	// Check the span's offset 50 and 96 values
	uartPuts("DEBUG: span->offset50 (halfword) = 0x")
	uartPutHex64(uint64(readMemory16(uintptr(spanPtr47) + 50)))
	uartPuts("\r\n")
	uartPuts("DEBUG: span->offset96 (halfword) = 0x")
	uartPutHex64(uint64(readMemory16(uintptr(spanPtr47) + 96)))
	uartPuts("\r\n")

	// Also dump emptymspan for comparison
	uartPuts("DEBUG: emptymspan addr = 0x")
	uartPutHex64(uint64(emptymspanAddr))
	uartPuts("\r\n")
	uartPuts("DEBUG: emptymspan->offset50 = 0x")
	uartPutHex64(uint64(readMemory16(emptymspanAddr + 50)))
	uartPuts("\r\n")
	uartPuts("DEBUG: emptymspan->offset96 = 0x")
	uartPutHex64(uint64(readMemory16(emptymspanAddr + 96)))
	uartPuts("\r\n")

	smallImg := image.NewRGBA(image.Rect(0, 0, 10, 10))
	print("PRINT: After small image allocation\n")
	if smallImg != nil {
		uartPuts("DEBUG: Small image created successfully!\r\n")
		uartPuts("DEBUG: smallImg.Bounds().Max.X = 0x")
		uartPutHex64(uint64(smallImg.Bounds().Max.X))
		uartPuts(", smallImg.Bounds().Max.Y = 0x")
		uartPutHex64(uint64(smallImg.Bounds().Max.Y))
		uartPuts("\r\n")
		uartPuts("DEBUG: smallImg.Stride = 0x")
		uartPutHex64(uint64(smallImg.Stride))
		uartPuts("\r\n")
	} else {
		uartPuts("DEBUG: Small image creation returned nil!\r\n")
	}

	// Now try the actual size
	uartPuts("DEBUG: initGGContext() - calling gg.NewContext(w, h)\r\n")
	print("PRINT: About to call gg.NewContext\n")
	ggCtx = gg.NewContext(w, h)
	print("PRINT: gg.NewContext returned\n")
	uartPuts("DEBUG: initGGContext() - gg.NewContext returned\r\n")
}

// copyFramebufferToGG copies the current Bochs BGRX framebuffer contents
// into the gg RGBA backbuffer so we can draw on top of whatever is already
// on screen (e.g. scrolling text) before flushing back.
func copyFramebufferToGG() {
	if ggCtx == nil {
		return
	}
	if fbinfo.Buf == nil || fbinfo.Width == 0 || fbinfo.Height == 0 {
		return
	}

	im, ok := ggCtx.Image().(*image.RGBA)
	if !ok {
		return
	}

	width := int(fbinfo.Width)
	height := int(fbinfo.Height)
	pitch := int(fbinfo.Pitch)

	// Clamp to image bounds.
	if width > im.Bounds().Dx() {
		width = im.Bounds().Dx()
	}
	if height > im.Bounds().Dy() {
		height = im.Bounds().Dy()
	}

	// Clamp to framebuffer size in bytes to avoid overruns.
	maxBytes := int(fbinfo.BufSize)
	if pitch*height > maxBytes && pitch > 0 {
		height = maxBytes / pitch
	}

	if width <= 0 || height <= 0 || pitch <= 0 {
		return
	}

	src := unsafe.Slice((*uint8)(fbinfo.Buf), pitch*height)

	dstPix := im.Pix
	dstStride := im.Stride

	for y := 0; y < height; y++ {
		srcRow := src[y*pitch:]
		dstRow := dstPix[y*dstStride:]
		for x := 0; x < width; x++ {
			si := x * 4
			di := x * 4

			// Framebuffer is BGRX in memory (Bochs XRGB8888, little-endian).
			b := srcRow[si+0]
			g := srcRow[si+1]
			r := srcRow[si+2]

			// gg uses RGBA layout.
			dstRow[di+0] = r
			dstRow[di+1] = g
			dstRow[di+2] = b
			dstRow[di+3] = 0xFF
		}
	}
}

// flushGGToFramebuffer flushes the gg RGBA backbuffer into the Bochs
// BGRX framebuffer (XRGB8888, 4 bytes per pixel, little-endian).
//
//go:nosplit
func flushGGToFramebuffer() {
	if ggCtx == nil {
		return
	}
	if fbinfo.Buf == nil || fbinfo.Width == 0 || fbinfo.Height == 0 {
		return
	}

	im, ok := ggCtx.Image().(*image.RGBA)
	if !ok {
		return
	}

	width := int(fbinfo.Width)
	height := int(fbinfo.Height)
	pitch := int(fbinfo.Pitch)

	// Clamp to image bounds.
	if width > im.Bounds().Dx() {
		width = im.Bounds().Dx()
	}
	if height > im.Bounds().Dy() {
		height = im.Bounds().Dy()
	}

	// Clamp to framebuffer size in bytes.
	maxBytes := int(fbinfo.BufSize)
	if pitch*height > maxBytes && pitch > 0 {
		height = maxBytes / pitch
	}

	if width <= 0 || height <= 0 || pitch <= 0 {
		return
	}

	dst := unsafe.Slice((*uint8)(fbinfo.Buf), pitch*height)

	srcPix := im.Pix
	srcStride := im.Stride

	for y := 0; y < height; y++ {
		srcRow := srcPix[y*srcStride:]
		dstRow := dst[y*pitch:]
		for x := 0; x < width; x++ {
			si := x * 4
			di := x * 4

			// gg uses RGBA.
			r := srcRow[si+0]
			g := srcRow[si+1]
			b := srcRow[si+2]

			// Bochs framebuffer is BGRX in memory.
			dstRow[di+0] = b
			dstRow[di+1] = g
			dstRow[di+2] = r
			dstRow[di+3] = 0x00 // X / unused
		}
	}

	// Ensure writes are visible to the device.
	asm.Dsb()
}

// drawGGStartupCircle renders a simple circle using gg on top of the
// existing framebuffer contents (e.g. boot text & image) and flushes it
// back to the Bochs framebuffer.
func drawGGStartupCircle() {
	uartPuts("DEBUG: drawGGStartupCircle() starting\r\n")

	if fbinfo.Buf == nil || fbinfo.Width == 0 || fbinfo.Height == 0 {
		uartPuts("DEBUG: drawGGStartupCircle() - fbinfo not valid\r\n")
		return
	}
	uartPuts("DEBUG: drawGGStartupCircle() - fbinfo valid\r\n")

	uartPuts("DEBUG: drawGGStartupCircle() - calling initGGContext()\r\n")
	initGGContext()
	if ggCtx == nil {
		uartPuts("DEBUG: drawGGStartupCircle() - ggCtx is nil after init\r\n")
		return
	}
	uartPuts("DEBUG: drawGGStartupCircle() - ggCtx initialized\r\n")

	// Start by copying whatever is already on screen into the GG context
	// so we draw on top of the existing framebuffer contents.
	uartPuts("DEBUG: drawGGStartupCircle() - calling copyFramebufferToGG()\r\n")
	copyFramebufferToGG()
	uartPuts("DEBUG: drawGGStartupCircle() - copyFramebufferToGG() done\r\n")

	w := float64(int(fbinfo.Width))
	h := float64(int(fbinfo.Height))

	// Draw a red circle in the center of the screen.
	uartPuts("DEBUG: drawGGStartupCircle() - calling SetRGB()\r\n")
	ggCtx.SetRGB(1, 0, 0)
	uartPuts("DEBUG: drawGGStartupCircle() - calling SetLineWidth()\r\n")
	ggCtx.SetLineWidth(6)
	uartPuts("DEBUG: drawGGStartupCircle() - calling DrawCircle()\r\n")
	ggCtx.DrawCircle(w/2, h/2, h/4)
	uartPuts("DEBUG: drawGGStartupCircle() - calling Stroke()\r\n")
	ggCtx.Stroke()
	uartPuts("DEBUG: drawGGStartupCircle() - drawing complete\r\n")

	uartPuts("DEBUG: drawGGStartupCircle() - calling flushGGToFramebuffer()\r\n")
	flushGGToFramebuffer()
	uartPuts("DEBUG: drawGGStartupCircle() - done\r\n")
}
