# Framebuffer Text Output Design for Mazarin Kernel

**Status**: Design Document  
**Target**: QEMU aarch64-virt with RAMFB (Framebuffer)  
**Priority**: Visual debugging via character rendering  

---

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Font and Character Bitmaps](#font-and-character-bitmaps)
4. [Framebuffer State Management](#framebuffer-state-management)
5. [Character Rendering](#character-rendering)
6. [Scrolling Algorithm](#scrolling-algorithm)
7. [API Design](#api-design)
8. [Implementation Plan](#implementation-plan)
9. [Integration Points](#integration-points)

---

## Overview

The framebuffer text output system provides a visual debugging interface by rendering text characters directly to the QEMU framebuffer display. This complements the existing UART output and provides immediate visual feedback without needing serial connection monitoring.

**Key Design Decisions:**
- Fixed 8x8 pixel character bitmaps for simplicity
- Automatic scrolling when reaching bottom of screen
- Cursor tracking (character row/column, not pixel coordinates)
- Memory-efficient (no intermediate buffers)
- Independent of UART system (parallel output capability)

---

## Architecture

### High-Level Flow

```
Character to Display
    ↓
Validate character and position
    ↓
Check if needs scroll
    ↓
[YES] Scroll screen (copy rows up, clear last row)
    ↓
Render character bitmap to pixel location
    ↓
Update cursor position
    ↓
Check bounds and wrap/scroll as needed
```

### Component Interaction

```
┌─────────────────────────────────┐
│   Kernel Output Functions       │
│  (uartPuts, framebufferPuts)    │
└──────────────┬──────────────────┘
               │
       ┌───────┴───────┐
       ▼               ▼
   UART Output    Framebuffer Output
   (serial)       (visual)
   
   └───────┬───────┘
           │
           ▼
    Application Debug Output
    (visible in both UART and QEMU display)
```

---

## Font and Character Bitmaps

### Bitmap Format

Each character is represented as an 8x8 bitmap:
- **Width**: 8 pixels
- **Height**: 8 rows (bytes)
- **One byte per row**: Bits represent pixels left-to-right
- **Bit 0** = leftmost pixel, **Bit 7** = rightmost pixel

### Bitmap Table Structure

```go
// Each entry is one character (8 bytes)
// Index 0-127 = ASCII characters 0-127
// Total size: 128 characters × 8 bytes = 1024 bytes

var fontBitmaps = [128][8]uint8{
    // Character 0 (NUL) - empty
    {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
    // Character 1-31 - various control chars (mostly empty)
    // ...
    // Character 32 (space) - all zeros
    {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
    // Character 33 (!) - vertical line with dot
    {0x18, 0x18, 0x18, 0x18, 0x00, 0x00, 0x18, 0x00},
    // ... (A-Z, a-z, digits, punctuation)
    // Character 127 (DEL) - empty or special
}
```

**Character 'A' Example**:
```
Binary (each row):
Row 0: 0x7E = 01111110 = ██████
Row 1: 0x99 = 10011001 = █  ██  █
Row 2: 0x81 = 10000001 = █      █
Row 3: 0xFF = 11111111 = ████████
Row 4: 0x81 = 10000001 = █      █
Row 5: 0x81 = 10000001 = █      █
Row 6: 0x00 = 00000000 = 
Row 7: 0x00 = 00000000 = 
```

### Font Data Source

Based on [8x8 bitmap font from tutorial](https://jsandler18.github.io/tutorial/hdmi.html), with modification for simplicity.

### Implementation Storage

**Option A: Embedded in Code (128×8 = 1KB)**
```go
var fontBitmaps = [128][8]uint8{ ... }
```
Pros: Fast access, always available
Cons: Increases binary size slightly

**Option B: Generated/Loaded Dynamically**
Pros: Smaller binary
Cons: More complex initialization

**Decision**: Option A (embedded) - 1KB overhead is acceptable for convenience.

---

## Framebuffer State Management

### FramebufferInfo Structure

```go
type FramebufferInfo struct {
    // Pixel dimensions
    Width          uint32  // Pixels wide (e.g., 640)
    Height         uint32  // Pixels tall (e.g., 480)
    Pitch          uint32  // Bytes per scanline
    
    // Character grid
    CharWidth      uint32  // Characters per row (width / 8)
    CharHeight     uint32  // Character rows visible (height / 8)
    
    // Cursor position (in character coordinates)
    CursorX        uint32  // Current column (0 to CharWidth-1)
    CursorY        uint32  // Current row (0 to CharHeight-1)
    
    // Buffer info
    Buffer         unsafe.Pointer  // Pixel data start address
    BufferSize     uint32          // Total buffer size in bytes
    
    // Colors (future expansion)
    ForegroundColor uint32  // RGB888 or XRGB8888 format
    BackgroundColor uint32
}
```

### Global State

```go
var fbInfo FramebufferInfo
var fbInitialized bool
```

### Color Management

**Current**: White text (0xFFFFFFFF) on black background (0x00000000)

**Future**: Support for configurable colors via XRGB8888 format:
```
Bit 31-24: X (unused)
Bit 23-16: Red
Bit 15-8:  Green
Bit 7-0:   Blue
```

---

## Character Rendering

### Pixel-Level Rendering Function

```go
// WritePixel sets a single pixel at (x, y)
// x, y: pixel coordinates
// color: 32-bit color value (XRGB8888)
func WritePixel(x, y uint32, color uint32) {
    // Bounds check
    if x >= fbInfo.Width || y >= fbInfo.Height {
        return
    }
    
    // Calculate byte offset
    // Each row is fbInfo.Pitch bytes
    // Each pixel is 4 bytes (assuming 32-bit color depth)
    byteOffset := y*fbInfo.Pitch + x*4
    
    // Write pixel
    pixelPtr := (*[1]uint32)(unsafe.Pointer(
        uintptr(fbInfo.Buffer) + uintptr(byteOffset)))
    (*pixelPtr)[0] = color
}

// Alternative for byte-per-pixel (8-bit color)
func WritePixelByte(x, y uint32, colorByte uint8) {
    if x >= fbInfo.Width || y >= fbInfo.Height {
        return
    }
    byteOffset := y*fbInfo.Pitch + x
    bytePtr := (*[1]uint8)(unsafe.Pointer(
        uintptr(fbInfo.Buffer) + uintptr(byteOffset)))
    (*bytePtr)[0] = colorByte
}
```

### Character Rendering Function

```go
// RenderChar renders an 8x8 character at pixel position (pixelX, pixelY)
// This is the core rendering primitive
func RenderChar(char byte, pixelX, pixelY uint32) {
    const charPixelWidth = 8
    const charPixelHeight = 8
    
    // Get bitmap for this character
    if char >= 128 {
        return  // Out of range
    }
    bitmap := fontBitmaps[char]
    
    // Render each row
    for row := 0; row < charPixelHeight; row++ {
        rowByte := bitmap[row]
        
        // Render each bit in the row
        for col := 0; col < charPixelWidth; col++ {
            // Extract bit (from LSB = left to MSB = right)
            bitSet := (rowByte & (1 << uint(col))) != 0
            
            // Determine color
            var color uint32
            if bitSet {
                color = fbInfo.ForegroundColor  // 0xFFFFFFFF = white
            } else {
                color = fbInfo.BackgroundColor  // 0x00000000 = black
            }
            
            // Write pixel
            pixelAddr := pixelX + uint32(col)
            pixelAddrY := pixelY + uint32(row)
            WritePixel(pixelAddr, pixelAddrY, color)
        }
    }
}

// RenderCharAtCursor renders a character at current cursor position
func RenderCharAtCursor(char byte) {
    pixelX := fbInfo.CursorX * 8      // Each char is 8 pixels wide
    pixelY := fbInfo.CursorY * 8      // Each char is 8 pixels tall
    RenderChar(char, pixelX, pixelY)
}
```

---

## Scrolling Algorithm

### Scroll-Up Operation

When the cursor reaches the bottom of the screen, the entire display scrolls up one character row.

```
Before Scroll:
Row 0: [Line 1 content]
Row 1: [Line 2 content]
...
Row 59: [Line 60 content]
Cursor: (0, 60) <- INVALID! Cursor is off-screen

After Scroll:
Row 0: [Line 2 content] ← copied from old Row 1
Row 1: [Line 3 content] ← copied from old Row 2
...
Row 58: [Line 60 content] ← copied from old Row 59
Row 59: [Black/empty] ← cleared
Cursor: (0, 59) ← Now valid, at bottom of screen
```

### Implementation

```go
// ScrollScreenUp scrolls the entire screen up by one character row
// Clears the bottom row
func ScrollScreenUp() {
    const charPixelHeight = 8
    const charPixelWidth = 8
    
    // Number of pixels to scroll (one character = 8 pixels)
    scrollPixels := charPixelHeight
    
    // Copy each row up by one character height
    for row := 0; row < fbInfo.CharHeight-1; row++ {
        sourcePixelY := (row + 1) * charPixelHeight
        destPixelY := row * charPixelHeight
        
        // Copy entire row of pixels
        // Source: scanline at (row+1)*8
        // Dest: scanline at row*8
        MemoryCopy(
            fbInfo.Buffer + destPixelY*fbInfo.Pitch,
            fbInfo.Buffer + sourcePixelY*fbInfo.Pitch,
            charPixelHeight * fbInfo.Pitch)
    }
    
    // Clear the bottom row (fill with background color)
    lastRowPixelY := (fbInfo.CharHeight - 1) * charPixelHeight
    ClearPixelRect(0, lastRowPixelY, fbInfo.Width, charPixelHeight)
}

// ClearPixelRect clears a rectangular region with background color
func ClearPixelRect(x, y, width, height uint32) {
    for pixelY := y; pixelY < y+height; pixelY++ {
        for pixelX := x; pixelX < x+width; pixelX++ {
            WritePixel(pixelX, pixelY, fbInfo.BackgroundColor)
        }
    }
}

// MemoryCopy copies memory from src to dst (Pitch * Height bytes)
func MemoryCopy(dest, src, size uintptr) {
    // Use Go's copy or memmove equivalent
    // For Mazarin kernel, use assembly bzero pattern or Go builtin
    ...
}
```

### Cursor Management

```go
// AdvanceCursor moves cursor to next position, scrolling if necessary
func AdvanceCursor() {
    fbInfo.CursorX++
    
    // Check if need to wrap to next line
    if fbInfo.CursorX >= fbInfo.CharWidth {
        fbInfo.CursorX = 0
        fbInfo.CursorY++
        
        // Check if need to scroll
        if fbInfo.CursorY >= fbInfo.CharHeight {
            ScrollScreenUp()
            fbInfo.CursorY = fbInfo.CharHeight - 1
        }
    }
}

// HandleNewline moves cursor to start of next line
func HandleNewline() {
    fbInfo.CursorX = 0
    fbInfo.CursorY++
    
    if fbInfo.CursorY >= fbInfo.CharHeight {
        ScrollScreenUp()
        fbInfo.CursorY = fbInfo.CharHeight - 1
    }
}
```

---

## API Design

### Public Functions (Framebuffer Text Output)

```go
// InitFramebuffer initializes the framebuffer and clears the screen
func InitFramebuffer() error

// FramebufferPutc outputs a single character to the framebuffer
// Handles scrolling, wrapping, and special characters
func FramebufferPutc(c byte)

// FramebufferPuts outputs a string to the framebuffer
// Equivalent to repeated FramebufferPutc calls
func FramebufferPuts(str string)

// FramebufferPutHex64 outputs a 64-bit value as hex
func FramebufferPutHex64(val uint64)

// ClearScreen clears the entire framebuffer and resets cursor
func ClearScreen()
```

### Character-by-Character Output

```go
// FramebufferPutc handles all special characters and rendering
func FramebufferPutc(c byte) {
    switch c {
    case '\n':
        HandleNewline()
    case '\r':
        fbInfo.CursorX = 0
    case '\t':
        // Advance to next tab stop (4-char aligned)
        for i := 0; i < 4; i++ {
            FramebufferPutc(' ')
        }
    case '\b':
        // Backspace
        if fbInfo.CursorX > 0 {
            fbInfo.CursorX--
            // Render space to erase character
            RenderCharAtCursor(' ')
        }
    default:
        // Regular character
        if c >= 32 && c < 127 {
            RenderCharAtCursor(c)
            AdvanceCursor()
        }
    }
}
```

### String Output (puts equivalent)

```go
//go:nosplit
func FramebufferPuts(str string) {
    for i := 0; i < len(str); i++ {
        FramebufferPutc(str[i])
    }
}

// Hex output functions (similar to UART versions)
func FramebufferPutHex8(val uint8) {
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

func FramebufferPutHex64(val uint64) {
    for i := 0; i < 16; i++ {
        digit := (val >> (60 - i*4)) & 0xF
        if digit < 10 {
            FramebufferPutc(byte('0' + digit))
        } else {
            FramebufferPutc(byte('A' + digit - 10))
        }
    }
}
```

---

## Implementation Plan

### Phase 1: Font Data and Basic Rendering

**Files to create:**
- `src/go/mazarin/framebuffer_font.go` - Font bitmaps (8x8 characters)
- `src/go/mazarin/framebuffer_text.go` - Character and pixel rendering

**Deliverables:**
1. Font bitmap table (128 characters × 8 bytes)
2. `WritePixel()` function - single pixel rendering
3. `RenderChar()` function - character bitmap rendering
4. `RenderCharAtCursor()` wrapper
5. Build tag support for QEMU framebuffer-capable builds

**Testing:**
- Render test pattern (e.g., "ABCD" across multiple lines)
- Verify pixel colors and positions
- Check bitmap data integrity

### Phase 2: Cursor and Scrolling

**Deliverables:**
1. `FramebufferInfo` struct and global state
2. `AdvanceCursor()` - move cursor with wrapping
3. `ScrollScreenUp()` - scroll entire screen
4. Bounds checking and edge case handling
5. Clear screen initialization

**Testing:**
- Fill entire screen with characters (should trigger scroll)
- Verify scroll preserves existing content
- Check cursor positioning after scroll

### Phase 3: Public API (puts equivalent)

**Deliverables:**
1. `FramebufferPutc()` - single character with special handling
2. `FramebufferPuts()` - string output
3. `FramebufferPutHex64()` - hex output
4. Special character handling (\n, \r, \t, \b)
5. Integration with kernel output system

**Testing:**
- Output multiline text
- Verify newline behavior
- Test hex number formatting

### Phase 4: Integration and Debugging

**Deliverables:**
1. Dual output (UART + Framebuffer)
2. Updated exception handlers to use framebuffer output
3. Boot-time messages visible on display
4. Optional display of system state (CPU mode, memory usage, etc.)

**Testing:**
- Boot kernel with display connected
- Verify all startup messages appear on screen
- Compare UART output with framebuffer output

---

## Integration Points

### Initialization Sequence

```
KernelMain()
    ↓
uartInit()          ← Early serial output
    ↓
InitializeExceptions()
    ↓
InitFramebuffer()   ← NEW: Set up framebuffer text output
    ↓
ClearScreen()       ← NEW: Clear and prepare display
    ↓
FramebufferPuts("Mazarin Kernel Started\n")  ← NEW: Visual greeting
    ↓
... rest of kernel init ...
```

### Dual Output Pattern

```go
// New utility functions to output to both UART and framebuffer
func LogOutput(str string) {
    uartPuts(str)        // Serial output
    FramebufferPuts(str) // Visual output
}

func LogHex64(val uint64) {
    uartPutHex64(val)
    FramebufferPutHex64(val)
}
```

### Exception Handler Integration

```go
func ExceptionHandler(...) {
    // ... capture exception info ...
    
    // Output to both UART and display
    FramebufferPuts("EXCEPTION: ELR=0x")
    FramebufferPutHex64(excInfo.ELR)
    FramebufferPuts("\n")
    
    uartPuts("EXCEPTION: ELR=0x")
    uartPutHex64(excInfo.ELR)
    uartPuts("\r\n")
}
```

---

## Performance Considerations

### Scrolling Overhead

**Memory copy operation**: ~512KB per scroll (640×480 pixels × 4 bytes)
- At ~100ns per byte = ~50ms per scroll
- Acceptable for debug output (not real-time critical)

### Future Optimization

1. **Partial scrolling**: Only update changed regions
2. **Double buffering**: Reduce flicker (requires 1.2MB extra RAM)
3. **Ring buffer**: Circular buffer instead of physical scroll
4. **Hardware acceleration**: Use GPU/DMA if available

### Current Priority

Keep it simple and correct. Performance optimization deferred to Phase 4+.

---

## Color Scheme

### Current Design (Simple)

```
Foreground: 0xFFFFFFFF (White, all channels full)
Background: 0x00000000 (Black, all channels zero)
```

### Future Expansion

Support ANSI color codes:
```
\e[31m  - Red text
\e[32m  - Green text
\e[33m  - Yellow text
\e[0m   - Reset to default
```

---

## Testing Strategy

### Unit Tests (Pixel Level)

```go
// Test WritePixel
func TestWritePixel() {
    // Verify single pixel color
}

// Test RenderChar
func TestRenderChar() {
    // Render 'A', verify bitmap bits set correctly
}
```

### Integration Tests

```go
// Fill screen and trigger scroll
func TestScrolling() {
    for i := 0; i < 1000; i++ {
        FramebufferPutc('A' + (i % 26))
    }
}

// Visual verification
func TestVisualOutput() {
    ClearScreen()
    FramebufferPuts("Framebuffer Text Output Test\n")
    FramebufferPuts("Line 2\n")
    FramebufferPuts("Line 3 with hex: 0x")
    FramebufferPutHex64(0x1234567890ABCDEF)
    FramebufferPuts("\n")
}
```

---

## Error Handling

### Graceful Degradation

```go
// If framebuffer not initialized, silently skip
func FramebufferPutc(c byte) {
    if !fbInitialized {
        return  // No output, but no panic
    }
    // ... normal operation ...
}
```

### Sanity Checks

```go
func InitFramebuffer() error {
    // Verify framebuffer exists
    if fbInfo.Buffer == nil {
        fbInitialized = false
        return errors.New("framebuffer not available")
    }
    
    // Verify reasonable dimensions
    if fbInfo.Width == 0 || fbInfo.Height == 0 {
        return errors.New("invalid framebuffer dimensions")
    }
    
    fbInitialized = true
    return nil
}
```

---

## Summary

This design provides:

✅ **Simple and reliable**: Straightforward pixel-by-pixel rendering  
✅ **No dependencies**: Works standalone with just framebuffer address  
✅ **Scrolling support**: Automatic screen management  
✅ **Debug-friendly**: Equivalent to UART puts() but visual  
✅ **Extensible**: Foundation for colors, fonts, graphics later  

The implementation is straightforward and can be done incrementally:
- Phase 1: Rendering ✓
- Phase 2: Scrolling ✓  
- Phase 3: API ✓
- Phase 4: Integration ✓




