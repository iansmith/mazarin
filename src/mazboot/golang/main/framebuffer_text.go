//go:build qemuvirt && aarch64

package main

import (
	"unsafe"

	"mazboot/asm"
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

// RenderChar8x8 renders an 8x8 character bitmap at pixel position (pixelX, pixelY)
// char: ASCII character to render
// pixelX, pixelY: top-left pixel position for the character
//
// CRITICAL FIX: Like RenderChar16x16, this function avoids storing the bitmap array
// as a local variable to prevent unaligned stores when MMU is disabled.
//
//go:nosplit
func RenderChar8x8(char byte, pixelX, pixelY uint32, color uint32) {
	const bitmapWidth = 8
	const bitmapHeight = 8

	// Bounds check
	if char >= 128 {
		return // Out of range
	}

	// Render each row of the bitmap
	// Access fontBitmaps[char][row] directly to avoid storing array as local variable
	for row := 0; row < bitmapHeight; row++ {
		rowByte := fontBitmaps[char][row]

		// Render each bit in the row
		// Font bitmap format: MSB (bit 7) = leftmost pixel, LSB (bit 0) = rightmost pixel
		for col := 0; col < bitmapWidth; col++ {
			// Extract bit (from MSB = left to LSB = right)
			bitSet := (rowByte & (1 << uint(7-col))) != 0

			// Determine pixel color
			var pixelColor uint32
			if bitSet {
				pixelColor = color // Foreground color (text)
			} else {
				pixelColor = fbBackgroundColor // Background color
			}

			// Write single pixel (8x8 output)
			WritePixel(pixelX+uint32(col), pixelY+uint32(row), pixelColor)
		}
	}
}

// RenderChar16x16 renders an 8x8 character bitmap at pixel position (pixelX, pixelY)
// Each pixel from the bitmap is rendered as a 2x2 block, making the output 16x16 pixels
// char: ASCII character to render
// pixelX, pixelY: top-left pixel position for the character
//
// CRITICAL FIX: This function avoids storing pointers/arrays as local variables
// to prevent the Go compiler from generating unaligned stores (STUR instructions).
// When MMU is disabled, memory is Device-nGnRnE type which requires strict alignment.
// Even STUR cannot do unaligned access to Device memory!
//
//go:nosplit
func RenderChar16x16(char byte, pixelX, pixelY uint32, color uint32) {
	const bitmapWidth = 8  // Original bitmap width
	const bitmapHeight = 8 // Original bitmap height

	// Bounds check - do this first to avoid any array access on invalid char
	if char >= 128 {
		return // Out of range
	}

	// CRITICAL: Do NOT store the bitmap array as a local variable!
	// The Go compiler generates `stur x1, [sp, #34]` which stores an 8-byte pointer
	// to a 2-byte aligned address, causing an alignment fault on Device memory.
	//
	// Instead, access fontBitmaps[char][row] directly in the loop.
	// This avoids the problematic store entirely.

	// Render each row of the bitmap
	for row := 0; row < bitmapHeight; row++ {
		// Access the bitmap byte directly from the global array
		// This avoids storing a local copy that would require unaligned store
		rowByte := fontBitmaps[char][row]

		// Render each bit in the row
		// Font bitmap format: MSB (bit 7) = leftmost pixel, LSB (bit 0) = rightmost pixel
		for col := 0; col < bitmapWidth; col++ {
			// Extract bit (from MSB = left to LSB = right)
			bitSet := (rowByte & (1 << uint(7-col))) != 0

			// Determine pixel color
			var pixelColor uint32
			if bitSet {
				pixelColor = color // Foreground color (text)
			} else {
				pixelColor = fbBackgroundColor // Background color
			}

			// Render this bitmap pixel as a 2x2 block
			baseX := pixelX + uint32(col*2)
			baseY := pixelY + uint32(row*2)

			// Write 2x2 block
			WritePixel(baseX, baseY, pixelColor)
			WritePixel(baseX+1, baseY, pixelColor)
			WritePixel(baseX, baseY+1, pixelColor)
			WritePixel(baseX+1, baseY+1, pixelColor)
		}
	}
}

// RenderChar is an alias that defaults to RenderChar16x16 for backward compatibility
//
//go:nosplit
func RenderChar(char byte, pixelX, pixelY uint32, color uint32) {
	RenderChar16x16(char, pixelX, pixelY, color)
}

// DebugRenderChar same as RenderChar16x16 but with verbose debug output
// CRITICAL FIX: Avoids storing bitmap as local variable to prevent unaligned stores
func DebugRenderChar(char byte, pixelX, pixelY uint32, color uint32) {
	const bitmapWidth = 8

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

	// Access fontBitmaps directly instead of storing as local variable
	// Just render first row for debug
	rowByte := fontBitmaps[char][0]
	uartPuts("DRC: Row 0 byte=0x")
	uartPutc(byte('0' + (rowByte>>4)%16))
	uartPutc(byte('0' + (rowByte & 0xF)))
	uartPuts(" bits: ")

	for col := 0; col < bitmapWidth; col++ {
		bitSet := (rowByte & (1 << uint(7-col))) != 0
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
		// Render as 2x2 block (16x16 mode)
		baseX := pixelX + uint32(col*2)
		baseY := pixelY
		WritePixel(baseX, baseY, pixelColor)
		WritePixel(baseX+1, baseY, pixelColor)
		WritePixel(baseX, baseY+1, pixelColor)
		WritePixel(baseX+1, baseY+1, pixelColor)
	}
	uartPuts("\r\n")
}

// RenderCharAtCursor8x8 renders a character at the current cursor position using 8x8 rendering
//
//go:nosplit
func RenderCharAtCursor8x8(char byte) {
	pixelX := fbinfo.CharsX * 8 // Each char is 8 pixels wide
	pixelY := fbinfo.CharsY * 8 // Each char is 8 pixels tall
	RenderChar8x8(char, pixelX, pixelY, fbForegroundColor)
}

// RenderCharAtCursor16x16 renders a character at the current cursor position using 16x16 rendering
//
//go:nosplit
func RenderCharAtCursor16x16(char byte) {
	pixelX := fbinfo.CharsX * 16 // Each char is 16 pixels wide
	pixelY := fbinfo.CharsY * 16 // Each char is 16 pixels tall
	RenderChar16x16(char, pixelX, pixelY, fbForegroundColor)
}

// RenderCharAtCursor is an alias that defaults to RenderCharAtCursor16x16 for backward compatibility
//
//go:nosplit
func RenderCharAtCursor(char byte) {
	RenderCharAtCursor16x16(char)
}

// ============================================================================
// Scrolling Functions
// ============================================================================

// ScrollScreenUp scrolls the entire screen up by one character row
// Uses CHAR_HEIGHT to determine the character row height
// Optimized: Copy entire character rows at once instead of scanline-by-scanline
//
//go:nosplit
func ScrollScreenUp() {
	charPixelHeight := uint32(CHAR_HEIGHT)
	charRowByteSize := charPixelHeight * fbinfo.Pitch // Size of one character row in bytes

	// Copy each character row up by one character height
	// Copy entire character rows at once (much faster than scanline-by-scanline)
	for row := uint32(0); row < fbinfo.CharsHeight-1; row++ {
		sourcePixelY := (row + 1) * charPixelHeight
		destPixelY := row * charPixelHeight

		// Copy entire character row in one MemmoveBytes call
		srcAddr := uintptr(fbinfo.Buf) + uintptr(sourcePixelY*fbinfo.Pitch)
		dstAddr := uintptr(fbinfo.Buf) + uintptr(destPixelY*fbinfo.Pitch)

		asm.MemmoveBytes(unsafe.Pointer(dstAddr), unsafe.Pointer(srcAddr), uint32(charRowByteSize))
	}

	// Clear the bottom row (fill with background color)
	lastRowPixelY := (fbinfo.CharsHeight - 1) * charPixelHeight
	ClearPixelRect(0, lastRowPixelY, fbinfo.Width, charPixelHeight)

	// Track total scroll offset
	fbScrollOffset += charPixelHeight
}

// ClearPixelRect clears a rectangular region with background color
// Uses memmove to fill each row efficiently
//
//go:nosplit
func ClearPixelRect(x, y, width, height uint32) {
	const bytesPerPixel = 4 // XRGB8888 format

	// For full-width clears, we can optimize by copying first row to rest
	if x == 0 && width == fbinfo.Width {
		// Build one row of pixels in a temporary buffer
		rowByteSize := width * bytesPerPixel
		firstRowAddr := uintptr(fbinfo.Buf) + uintptr(y*fbinfo.Pitch)

		// Fill first row pixel by pixel (fast enough for one row)
		for px := uint32(0); px < width; px++ {
			pixelPtr := (*uint32)(unsafe.Pointer(firstRowAddr + uintptr(px*bytesPerPixel)))
			*pixelPtr = fbBackgroundColor
		}

		// Copy first row to remaining rows using memmove
		for pixelY := y + 1; pixelY < y+height; pixelY++ {
			destAddr := uintptr(fbinfo.Buf) + uintptr(pixelY*fbinfo.Pitch)
			asm.MemmoveBytes(unsafe.Pointer(destAddr), unsafe.Pointer(firstRowAddr), uint32(rowByteSize))
		}
	} else {
		// Partial-row clear - slower pixel-by-pixel
		for pixelY := y; pixelY < y+height; pixelY++ {
			byteOffset := pixelY*fbinfo.Pitch + x*bytesPerPixel
			for pixelX := x; pixelX < x+width; pixelX++ {
				pixelPtr := (*uint32)(unsafe.Pointer(
					uintptr(fbinfo.Buf) + uintptr(byteOffset)))
				*pixelPtr = fbBackgroundColor
				byteOffset += bytesPerPixel
			}
		}
	}
}

// GetScrollOffset returns the total number of pixels the screen has scrolled
//
//go:nosplit
func GetScrollOffset() uint32 {
	return fbScrollOffset
}

// MemmoveBytes is now in asm package - use asm.MemmoveBytes()

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
//
//go:nosplit
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

// FramebufferPutc8x8 outputs a single character to the framebuffer using 8x8 rendering
// Handles scrolling, wrapping, and special characters
//
//go:nosplit
func FramebufferPutc8x8(c byte) {
	if !fbTextInitialized {
		return // Silently skip if not initialized
	}

	// For now, only handle printable ASCII characters
	if c >= 32 && c < 127 {
		RenderCharAtCursor8x8(c)
		AdvanceCursor()
	} else if c == '\n' {
		HandleNewline()
	}
}

// FramebufferPutc16x16 outputs a single character to the framebuffer using 16x16 rendering
// Handles scrolling, wrapping, and special characters
//
//go:nosplit
func FramebufferPutc16x16(c byte) {
	if !fbTextInitialized {
		return // Silently skip if not initialized
	}

	// For now, only handle printable ASCII characters
	if c >= 32 && c < 127 {
		RenderCharAtCursor16x16(c)
		AdvanceCursor()
	} else if c == '\n' {
		HandleNewline()
	}
}

// FramebufferPutc outputs a single character to the framebuffer
// Defaults to 16x16 rendering for backward compatibility
// Handles scrolling, wrapping, and special characters
//
//go:nosplit
func FramebufferPutc(c byte) {
	FramebufferPutc16x16(c)
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
	// Force display refresh after text rendering
	asm.Dsb()
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
// Interrupt-Safe Framebuffer Output (called from assembly IRQ handlers)
// ============================================================================

// fb_putc_irq outputs a single character to the framebuffer from an interrupt handler
// This is called from assembly, so it must be interrupt-safe and use //go:nosplit
// Handles line wrapping automatically
//
//go:linkname fb_putc_irq fb_putc_irq
//go:nosplit
//go:noinline
func fb_putc_irq(c byte) {
	// Breadcrumb: Entered fb_putc_irq
	asm.UartPutcPl011('[')
	asm.UartPutcPl011('f')
	asm.UartPutcPl011('b')
	asm.UartPutcPl011(']') // [fb] = "fb_putc_irq"

	if !fbTextInitialized {
		asm.UartPutcPl011('!') // Print '!' if not initialized
		return                 // Silently skip if framebuffer not initialized
	}

	// Render the character at current cursor position (8x8 for compactness)
	pixelX := fbinfo.CharsX * 8
	pixelY := fbinfo.CharsY * 8
	RenderChar8x8(c, pixelX, pixelY, fbForegroundColor)

	// Advance cursor with line wrapping
	fbinfo.CharsX++
	if fbinfo.CharsX >= fbinfo.CharsWidth {
		fbinfo.CharsX = 0
		fbinfo.CharsY++
		if fbinfo.CharsY >= fbinfo.CharsHeight {
			// Scroll screen up
			ScrollScreenUp()
			fbinfo.CharsY = fbinfo.CharsHeight - 1
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

	// Note: Do NOT clear the screen here - framebufferInit() already set the background color
	// Clearing here would destroy any image or content already drawn
	// (e.g., test pattern in framebuffer_qemu.go fills top 100 rows with white)

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
