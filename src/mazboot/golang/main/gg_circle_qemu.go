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

// initGGContext initializes the gg context sized to the current framebuffer.
func initGGContext() {
	if ggCtx != nil {
		return
	}
	if fbinfo.Width == 0 || fbinfo.Height == 0 {
		return
	}

	w := int(fbinfo.Width)
	h := int(fbinfo.Height)

	// Suppress unused variable warnings for image.Rect tests
	_ = image.Rect(0, 0, w, h)
	_ = image.Rect(0, 0, 100, 100)
	_ = image.NewRGBA(image.Rect(0, 0, 10, 10))

	ggCtx = gg.NewContext(w, h)
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
// existing framebuffer contents and flushes it back to the Bochs framebuffer.
func drawGGStartupCircle() {
	if fbinfo.Buf == nil || fbinfo.Width == 0 || fbinfo.Height == 0 {
		return
	}

	initGGContext()
	if ggCtx == nil {
		return
	}

	// Copy existing framebuffer contents
	copyFramebufferToGG()

	w := float64(int(fbinfo.Width))
	h := float64(int(fbinfo.Height))

	// Draw a red circle in the center
	ggCtx.SetRGB(1, 0, 0)
	ggCtx.SetLineWidth(6)
	ggCtx.DrawCircle(w/2, h/2, h/4)
	ggCtx.Stroke()

	flushGGToFramebuffer()
}
