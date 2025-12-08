//go:build qemuvirt && aarch64

package main

import (
	"unsafe"
)

// Note: FramebufferInfo is defined in framebuffer_common.go
// We use the shared fbinfo global variable and add text rendering functions

var (
	fbTextInitialized bool
	fbForegroundColor uint32 // Text color
	fbBackgroundColor uint32 // Background color
	fbScrollOffset    uint32 // Total number of pixels scrolled (for positioning overlays)
)

// ============================================================================
// Pixel Rendering Functions
// ============================================================================

// WritePixel sets a single pixel at (x, y) to the given color (opaque)
// x, y: pixel coordinates
// color: 32-bit ARGB8888 color value (alpha will be ignored, treated as opaque)
//
//go:nosplit
func WritePixel(x, y uint32, color uint32) {
	// Bounds check
	if x >= fbinfo.Width || y >= fbinfo.Height {
		return
	}

	// Calculate byte offset
	// Each row is fbinfo.Pitch bytes
	// Each pixel is 4 bytes (assuming 32-bit color depth)
	byteOffset := y*fbinfo.Pitch + x*4

	// Write pixel (opaque)
	pixelPtr := (*uint32)(unsafe.Pointer(
		uintptr(fbinfo.Buf) + uintptr(byteOffset)))
	*pixelPtr = color
}

// WritePixelAlpha sets a single pixel with alpha blending
// x, y: pixel coordinates
// color: 32-bit ARGB8888 color value (0xAARRGGBB)
// Blends with existing pixel using alpha channel
//
//go:nosplit
func WritePixelAlpha(x, y uint32, color uint32) {
	// Bounds check
	if x >= fbinfo.Width || y >= fbinfo.Height {
		return
	}

	// Extract alpha from source color
	alpha := uint32((color >> 24) & 0xFF)

	// If fully transparent, don't write
	if alpha == 0 {
		return
	}

	// If fully opaque, just write directly
	if alpha == 255 {
		WritePixel(x, y, color)
		return
	}

	// Calculate byte offset
	byteOffset := y*fbinfo.Pitch + x*4

	// Read existing pixel
	pixelPtr := (*uint32)(unsafe.Pointer(
		uintptr(fbinfo.Buf) + uintptr(byteOffset)))
	dest := *pixelPtr

	// Extract color components from source (ARGB)
	srcR := uint32((color >> 16) & 0xFF)
	srcG := uint32((color >> 8) & 0xFF)
	srcB := uint32(color & 0xFF)

	// Extract color components from destination (ARGB)
	destR := uint32((dest >> 16) & 0xFF)
	destG := uint32((dest >> 8) & 0xFF)
	destB := uint32(dest & 0xFF)

	// Blend using alpha: out = src * alpha + dst * (1 - alpha)
	// Divide by 256 instead of 255 for speed (close enough)
	invAlpha := 256 - alpha

	blendR := ((srcR * alpha) + (destR * invAlpha)) / 256
	blendG := ((srcG * alpha) + (destG * invAlpha)) / 256
	blendB := ((srcB * alpha) + (destB * invAlpha)) / 256

	// Write blended pixel (keep alpha as opaque for result)
	blended := (blendR << 16) | (blendG << 8) | blendB
	*pixelPtr = blended
}

// ============================================================================
// Character Rendering Functions
// ============================================================================

// RenderChar renders an 8x8 character bitmap at pixel position (pixelX, pixelY)
// char: ASCII character to render
// pixelX, pixelY: top-left pixel position for the character
//
//go:nosplit
func RenderChar(char byte, pixelX, pixelY uint32, color uint32) {
	const charPixelWidth = 8
	const charPixelHeight = 8

	// Get bitmap for this character
	if char >= 128 {
		return // Out of range
	}
	bitmap := fontBitmaps[char]

	// Render each row
	for row := 0; row < charPixelHeight; row++ {
		rowByte := bitmap[row]

		// Render each bit in the row
		// Font bitmap format: MSB (bit 7) = leftmost pixel, LSB (bit 0) = rightmost pixel
		for col := 0; col < charPixelWidth; col++ {
			// Extract bit (from MSB = left to LSB = right)
			bitSet := (rowByte & (1 << uint(7-col))) != 0

			// Determine pixel color
			var pixelColor uint32
			if bitSet {
				pixelColor = color // Foreground color (text)
			} else {
				pixelColor = fbBackgroundColor // Background color
			}

			// Write pixel
			pixelAddrX := pixelX + uint32(col)
			pixelAddrY := pixelY + uint32(row)
			WritePixel(pixelAddrX, pixelAddrY, pixelColor)
		}
	}
}

// DebugRenderChar same as RenderChar but with verbose debug output
func DebugRenderChar(char byte, pixelX, pixelY uint32, color uint32) {
	const charPixelWidth = 8
	const charPixelHeight = 8

	if char >= 128 {
		return
	}

	uartPuts("DRC: char=0x")
	uartPutc(byte('0' + (char>>4)%16))
	uartPutc(byte('0' + (char & 0xF)))
	uartPuts(" at (")
	uartPutc('P')
	uartPuts(",")
	uartPutc('Y')
	uartPuts(") color=0x")
	printHex32(color)
	uartPuts("\r\n")

	bitmap := fontBitmaps[char]

	// Just render first row for debug
	rowByte := bitmap[0]
	uartPuts("DRC: Row 0 byte=0x")
	uartPutc(byte('0' + (rowByte>>4)%16))
	uartPutc(byte('0' + (rowByte & 0xF)))
	uartPuts(" bits: ")

	for col := 0; col < charPixelWidth; col++ {
		bitSet := (rowByte & (1 << uint(col))) != 0
		if bitSet {
			uartPutc('1')
		} else {
			uartPutc('0')
		}

		var pixelColor uint32
		if bitSet {
			pixelColor = color
		} else {
			pixelColor = fbBackgroundColor
		}
		pixelAddrX := pixelX + uint32(col)
		pixelAddrY := pixelY
		WritePixel(pixelAddrX, pixelAddrY, pixelColor)
	}
	uartPuts("\r\n")
}

// RenderCharAtCursor renders a character at the current cursor position
//
//go:nosplit
func RenderCharAtCursor(char byte) {
	pixelX := fbinfo.CharsX * 8 // Each char is 8 pixels wide
	pixelY := fbinfo.CharsY * 8 // Each char is 8 pixels tall
	RenderChar(char, pixelX, pixelY, fbForegroundColor)
}

// ============================================================================
// Scrolling Functions
// ============================================================================

// ScrollScreenUp scrolls the entire screen up by one character row
// Copies all rows up, clears the bottom row
func ScrollScreenUp() {
	const charPixelHeight = 8

	// Copy each row up by one character height
	for row := uint32(0); row < fbinfo.CharsHeight-1; row++ {
		sourcePixelY := (row + 1) * charPixelHeight
		destPixelY := row * charPixelHeight

		// Copy entire row of pixels
		// We copy pixel row by pixel row (scanline by scanline)
		for scanline := uint32(0); scanline < charPixelHeight; scanline++ {
			srcOffset := (sourcePixelY + scanline) * fbinfo.Pitch
			dstOffset := (destPixelY + scanline) * fbinfo.Pitch

			// Use memmove to copy one scanline
			// Each scanline is fbinfo.Pitch bytes
			MemmoveBytes(
				uintptr(fbinfo.Buf)+uintptr(dstOffset),
				uintptr(fbinfo.Buf)+uintptr(srcOffset),
				fbinfo.Pitch)
		}
	}

	// Clear the bottom row (fill with background color)
	lastRowPixelY := (fbinfo.CharsHeight - 1) * charPixelHeight
	ClearPixelRect(0, lastRowPixelY, fbinfo.Width, charPixelHeight)

	// Track total scroll offset
	fbScrollOffset += charPixelHeight
}

// ClearPixelRect clears a rectangular region with background color
//
//go:nosplit
func ClearPixelRect(x, y, width, height uint32) {
	for pixelY := y; pixelY < y+height; pixelY++ {
		for pixelX := x; pixelX < x+width; pixelX++ {
			WritePixel(pixelX, pixelY, fbBackgroundColor)
		}
	}
}

// GetScrollOffset returns the total number of pixels the screen has scrolled
//
//go:nosplit
func GetScrollOffset() uint32 {
	return fbScrollOffset
}

// MemmoveBytes copies memory from src to dst
//
//go:linkname MemmoveBytes MemmoveBytes
//go:nosplit
func MemmoveBytes(dest, src uintptr, size uint32)

// ============================================================================
// Cursor Management
// ============================================================================

// AdvanceCursor moves cursor to next position, scrolling if necessary
//
//go:nosplit
func AdvanceCursor() {
	fbinfo.CharsX++

	// Check if need to wrap to next line
	if fbinfo.CharsX >= fbinfo.CharsWidth {
		fbinfo.CharsX = 0
		fbinfo.CharsY++

		// Check if need to scroll
		if fbinfo.CharsY >= fbinfo.CharsHeight {
			ScrollScreenUp()
			fbinfo.CharsY = fbinfo.CharsHeight - 1
		}
	}
}

// HandleNewline moves cursor to start of next line, scrolling if necessary
func HandleNewline() {
	fbinfo.CharsX = 0
	fbinfo.CharsY++

	if fbinfo.CharsY >= fbinfo.CharsHeight {
		ScrollScreenUp()
		fbinfo.CharsY = fbinfo.CharsHeight - 1
	}
}

// ============================================================================
// Public API - Character Output
// ============================================================================

// FramebufferPutc outputs a single character to the framebuffer
// Handles scrolling, wrapping, and special characters
//
//go:nosplit
func FramebufferPutc(c byte) {
	if !fbTextInitialized {
		return // Silently skip if not initialized
	}

	// For now, only handle printable ASCII characters
	if c >= 32 && c < 127 {
		RenderCharAtCursor(c)
		AdvanceCursor()
	} else if c == '\n' {
		HandleNewline()
	}
}

// FramebufferPuts outputs a string to the framebuffer
// Equivalent to repeated FramebufferPutc calls
//
//go:nosplit
func FramebufferPuts(str string) {
	if !fbTextInitialized {
		return
	}
	for i := 0; i < len(str); i++ {
		FramebufferPutc(str[i])
	}
}

// FramebufferPutHex8 outputs an 8-bit value as two hex digits
func FramebufferPutHex8(val uint8) {
	if !fbTextInitialized {
		return
	}
	writeHexDigit := func(digit uint32) {
		if digit < 10 {
			FramebufferPutc(byte('0' + digit))
		} else {
			FramebufferPutc(byte('A' + digit - 10))
		}
	}
	writeHexDigit(uint32((val >> 4) & 0xF))
	writeHexDigit(uint32(val & 0xF))
}

// FramebufferPutHex64 outputs a 64-bit value as 16 hex digits
func FramebufferPutHex64(val uint64) {
	if !fbTextInitialized {
		return
	}
	for i := 0; i < 16; i++ {
		digit := (val >> (60 - i*4)) & 0xF
		if digit < 10 {
			FramebufferPutc(byte('0' + digit))
		} else {
			FramebufferPutc(byte('A' + digit - 10))
		}
	}
}

// ============================================================================
// Initialization
// ============================================================================

// InitFramebufferText initializes the text rendering system on an already-initialized framebuffer
// This should be called after framebufferInit() has set up the hardware framebuffer
// Parameters: buffer address, width, height, pitch (all from the hardware framebuffer setup)
//
//go:nosplit
func InitFramebufferText(buffer unsafe.Pointer, width, height, pitch uint32) error {
	uartPuts("Init: 1\r\n")
	// Note: framebufferInit() has already set:
	// - fbinfo.Width, Height, Pitch
	// - fbinfo.CharsWidth, CharsHeight
	// - fbinfo.CharsX = 0, CharsY = 0
	// We only need to set the framebuffer pointer and the text rendering parameters

	// Store the framebuffer buffer pointer
	fbinfo.Buf = buffer
	uartPuts("Init: 2\r\n")

	// Set text rendering colors
	fbForegroundColor = FramebufferTextColor // AnsiBrightGreen
	uartPuts("Init: 3\r\n")
	fbBackgroundColor = FramebufferBackgroundColor // MidnightBlue
	uartPuts("Init: 4\r\n")

	// Mark text system as initialized
	fbTextInitialized = true
	uartPuts("Init: 5\r\n")

	// Clear the screen to midnight blue background
	uartPuts("Init: About to ClearScreen\r\n")
	ClearScreen()
	uartPuts("Init: ClearScreen returned\r\n")

	// Position cursor at bottom-left of screen for text to scroll upward
	// This way, when we print only a few lines, they appear near the bottom
	fbinfo.CharsX = 0
	fbinfo.CharsY = fbinfo.CharsHeight - 1
	uartPuts("Init: Cursor positioned at bottom\r\n")

	return nil
}

// ClearScreen clears the entire framebuffer and resets the cursor
//
//go:nosplit
func ClearScreen() {
	uartPuts("ClearScreen: ENTRY\r\n")
	if !fbTextInitialized {
		uartPuts("ClearScreen: Not initialized\r\n")
		return
	}

	// Fill entire framebuffer with background color
	uartPuts("ClearScreen: Before ClearPixelRect\r\n")
	ClearPixelRect(0, 0, fbinfo.Width, fbinfo.Height)
	uartPuts("ClearScreen: After ClearPixelRect\r\n")

	// Reset cursor (cursor should already be at 0,0 from framebufferInit, but reset anyway)
	// Note: Skip cursor reset for now to avoid potential memory corruption
	// fbinfo.CharsX = 0
	// fbinfo.CharsY = 0
	uartPuts("ClearScreen: EXIT\r\n")
}
