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
)

// ============================================================================
// Pixel Rendering Functions
// ============================================================================

// WritePixel sets a single pixel at (x, y) to the given color
// x, y: pixel coordinates
// color: 32-bit XRGB8888 color value
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

	// Write pixel
	pixelPtr := (*uint32)(unsafe.Pointer(
		uintptr(fbinfo.Buf) + uintptr(byteOffset)))
	*pixelPtr = color
}

// ============================================================================
// Character Rendering Functions
// ============================================================================

// RenderChar renders an 8x8 character bitmap at pixel position (pixelX, pixelY)
// char: ASCII character to render
// pixelX, pixelY: top-left pixel position for the character
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
		for col := 0; col < charPixelWidth; col++ {
			// Extract bit (from LSB = left to MSB = right)
			bitSet := (rowByte & (1 << uint(col))) != 0

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

// RenderCharAtCursor renders a character at the current cursor position
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
}

// ClearPixelRect clears a rectangular region with background color
func ClearPixelRect(x, y, width, height uint32) {
	for pixelY := y; pixelY < y+height; pixelY++ {
		for pixelX := x; pixelX < x+width; pixelX++ {
			WritePixel(pixelX, pixelY, fbBackgroundColor)
		}
	}
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
func FramebufferPutc(c byte) {
	if !fbTextInitialized {
		return // Silently skip if not initialized
	}

	switch c {
	case '\n':
		HandleNewline()
	case '\r':
		fbinfo.CharsX = 0
	case '\t':
		// Advance to next tab stop (4-char aligned)
		for i := 0; i < 4; i++ {
			FramebufferPutc(' ')
		}
	case '\b':
		// Backspace
		if fbinfo.CharsX > 0 {
			fbinfo.CharsX--
			// Render space to erase character
			RenderCharAtCursor(' ')
		}
	default:
		// Regular character - only render printable ASCII
		if c >= 32 && c < 127 {
			RenderCharAtCursor(c)
			AdvanceCursor()
		}
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
	uartPuts("InitFramebufferText: ENTRY\r\n")

	// Store framebuffer information in shared fbinfo
	fbinfo.Buf = buffer
	uartPuts("InitFramebufferText: Buf stored\r\n")

	fbinfo.Width = width
	uartPuts("InitFramebufferText: Width stored\r\n")

	fbinfo.Height = height
	uartPuts("InitFramebufferText: Height stored\r\n")

	fbinfo.Pitch = pitch
	uartPuts("InitFramebufferText: Pitch stored\r\n")

	uartPuts("InitFramebufferText: About to divide width\r\n")
	fbinfo.CharsWidth = width / 8 // 8 pixels per character
	uartPuts("InitFramebufferText: Width divided\r\n")

	uartPuts("InitFramebufferText: About to divide height\r\n")
	fbinfo.CharsHeight = height / 8 // 8 pixels per character
	uartPuts("InitFramebufferText: Height divided\r\n")

	uartPuts("InitFramebufferText: About to set cursor X\r\n")
	fbinfo.CharsX = 0
	uartPuts("InitFramebufferText: Cursor X set\r\n")

	uartPuts("InitFramebufferText: About to set cursor Y\r\n")
	fbinfo.CharsY = 0
	uartPuts("InitFramebufferText: Cursor Y set\r\n")

	uartPuts("InitFramebufferText: Grid values stored\r\n")

	// Set colors from constants
	fbForegroundColor = FramebufferTextColor       // AnsiBrightGreen
	fbBackgroundColor = FramebufferBackgroundColor // MidnightBlue
	uartPuts("InitFramebufferText: Colors set\r\n")

	fbTextInitialized = true
	uartPuts("InitFramebufferText: fbTextInitialized=true\r\n")

	// Clear the screen to midnight blue background
	uartPuts("InitFramebufferText: About to call ClearScreen\r\n")
	ClearScreen()
	uartPuts("InitFramebufferText: ClearScreen returned\r\n")

	uartPuts("InitFramebufferText: EXIT (success)\r\n")

	return nil
}

// ClearScreen clears the entire framebuffer and resets the cursor
func ClearScreen() {
	if !fbTextInitialized {
		return
	}

	uartPuts("ClearScreen: Starting clear\r\n")
	uartPuts("ClearScreen: Width=0x")
	uartPutHex64(uint64(fbinfo.Width))
	uartPuts(" Height=0x")
	uartPutHex64(uint64(fbinfo.Height))
	uartPuts("\r\n")

	// Fill entire framebuffer with background color
	ClearPixelRect(0, 0, fbinfo.Width, fbinfo.Height)

	uartPuts("ClearScreen: Clear complete\r\n")

	// Reset cursor
	fbinfo.CharsX = 0
	fbinfo.CharsY = 0
}
