# Color Palette and Framebuffer Text API

## Color Constants

All colors are defined in `src/go/mazarin/colors.go` in XRGB8888 format (0xAARRGGBB).

### ANSI Color Palette

```go
// Basic ANSI Colors
AnsiBlack       = #111111 (RGB: 17, 17, 17)
AnsiRed         = #FF9DA4 (RGB: 255, 157, 164)
AnsiGreen       = #D1F1A9 (RGB: 209, 241, 169)
AnsiYellow      = #FFEEADU (RGB: 255, 238, 173)
AnsiBlue        = #BBDAFF (RGB: 187, 218, 255)
AnsiMagenta     = #EBBBFF (RGB: 235, 187, 255)
AnsiCyan        = #99FFFF (RGB: 153, 255, 255)
AnsiWhite       = #CCCCCC (RGB: 204, 204, 204)

// Bright ANSI Colors (High intensity)
AnsiBrightBlack   = #333333 (RGB: 51, 51, 51)
AnsiBrightRed     = #FF7882 (RGB: 255, 120, 130)
AnsiBrightGreen   = #B8F171 (RGB: 184, 241, 113) ← DEFAULT TEXT COLOR
AnsiBrightYellow  = #FFE580 (RGB: 255, 229, 128)
AnsiBrightBlue    = #80BAFF (RGB: 128, 186, 255)
AnsiBrightMagenta = #D778FF (RGB: 215, 120, 255)
AnsiBrightCyan    = #78FFFF (RGB: 120, 255, 255)
AnsiBrightWhite   = #FFFFFF (RGB: 255, 255, 255)

// Background Colors
MidnightBlue = #191B70 (RGB: 25, 25, 112) ← DEFAULT BACKGROUND
```

### Default Framebuffer Configuration

```go
FramebufferBackgroundColor = MidnightBlue       // #191B70
FramebufferTextColor       = AnsiBrightGreen    // #B8F171
FramebufferErrorColor      = AnsiBrightRed      // #FF7882
FramebufferWarningColor    = AnsiBrightYellow   // #FFE580
FramebufferSuccessColor    = AnsiBrightGreen    // #B8F171
FramebufferInfoColor       = AnsiBrightBlue     // #80BAFF
```

## Framebuffer Text API

### Initialization

```go
// Initialize text rendering system on an already-initialized framebuffer
// Call after framebufferInit() hardware setup completes
// Parameters come from hardware framebuffer initialization
// buffer: pointer to framebuffer memory (from RAMFB device)
// width: framebuffer width in pixels (e.g., 640)
// height: framebuffer height in pixels (e.g., 480)
// pitch: bytes per scanline (from RAMFB configuration)
func InitFramebufferText(buffer unsafe.Pointer, width, height, pitch uint32) error
```

### Character Output

```go
// Output single character
// Handles \n (newline), \r (carriage return), \t (tab), \b (backspace)
// Automatically scrolls when reaching bottom of screen
func FramebufferPutc(c byte)

// Output string
// Equivalent to repeated FramebufferPutc calls
func FramebufferPuts(str string)

// Output 8-bit hex value (2 digits)
func FramebufferPutHex8(val uint8)

// Output 64-bit hex value (16 digits)
func FramebufferPutHex64(val uint64)

// Clear entire screen and reset cursor
func ClearScreen()
```

## Usage Examples

### Basic Text Output

```go
// Initialize during kernel boot
import "unsafe"

// Get framebuffer info (from RAMFB device)
fbBuffer := unsafe.Pointer(uintptr(0x40000000)) // Example address
InitFramebuffer(fbBuffer, 640, 480, 2560)  // pitch = 640 * 4

// Output text
FramebufferPuts("Mazarin Kernel Started\n")
FramebufferPuts("CPU Mode: EL1\n")
```

### Hex Output

```go
// Display memory address
FramebufferPuts("Stack pointer: 0x")
FramebufferPutHex64(stackPointer)
FramebufferPuts("\n")

// Display byte value
FramebufferPuts("Register B: 0x")
FramebufferPutHex8(uint8(registerB))
FramebufferPuts("\n")
```

### Special Characters

```go
// Newline - moves cursor to start of next line
FramebufferPutc('\n')

// Tab - advances to next 4-character tab stop
FramebufferPutc('\t')

// Backspace - deletes previous character
FramebufferPutc('\b')

// Carriage return - moves cursor to start of line
FramebufferPutc('\r')
```

### Exception Logging

```go
func ExceptionHandler(...) {
    // ... capture exception info ...
    
    // Display to framebuffer
    FramebufferPuts("EXCEPTION: ELR=0x")
    FramebufferPutHex64(excInfo.ELR)
    FramebufferPuts(" EC=0x")
    FramebufferPutHex8(uint8(ec))
    FramebufferPuts("\n")
}
```

## Screen Specifications

### Character Grid

```
Display Resolution: 640×480 pixels
Character Size: 8×8 pixels
Character Grid: 80 characters wide × 60 characters tall
```

### Scrolling Behavior

```
When cursor reaches bottom-right (79, 59):
  1. Move to next line: cursor moves to (0, 60)
  2. Screen full: Detect out-of-bounds
  3. ScrollScreenUp(): Copy all rows up one character height (8 pixels)
  4. Clear bottom row: Fill with background color (MidnightBlue)
  5. Reset cursor: Move to bottom-left (0, 59)
```

### Color Scheme

```
┌─ Midnight Blue (#191B70) Background ─┐
│                                       │
│  Bright Green (#B8F171) Text Here     │
│                                       │
└───────────────────────────────────────┘
```

## Implementation Details

### Pixel Rendering

Each character is rendered using an 8×8 bitmap:
- **Bitmap format**: 8 bytes per character, one byte per row
- **Bit layout**: LSB (bit 0) = leftmost pixel, MSB (bit 7) = rightmost pixel
- **Font table**: 128 characters (ASCII 0-127) × 8 bytes = 1KB

### Framebuffer State

The `FramebufferInfo` struct tracks:
- **Pixel dimensions**: Width, Height, Pitch
- **Character grid**: CharWidth, CharHeight
- **Cursor position**: CursorX, CursorY (in character coordinates)
- **Buffer address**: Points to framebuffer memory
- **Colors**: Foreground and background colors

### Memory Efficiency

```
Font table: 1 KB (fixed size)
FramebufferInfo struct: ~64 bytes
Scroll operation: ~500 KB memory copy (acceptable for debug output)
```

## Future Enhancements

### Color Messages (Phase 2)

```go
// Context-aware output with colors
func FramebufferPrintError(msg string)     // AnsiBrightRed
func FramebufferPrintWarning(msg string)   // AnsiBrightYellow
func FramebufferPrintSuccess(msg string)   // AnsiBrightGreen
func FramebufferPrintInfo(msg string)      // AnsiBrightBlue
```

### ANSI Escape Sequences (Phase 3)

```
\e[31m - Switch to red text
\e[32m - Switch to green text
\e[33m - Switch to yellow text
\e[0m  - Reset to default colors
```

### Additional Features (Future)

- Double buffering (reduce flicker)
- Cursor styles (block, underline, blink)
- Partial screen updates
- Text attributes (bold, reverse video, underline)
- Graphics rendering (lines, boxes, images)

## Separation from UART

The framebuffer output is **completely independent** from UART:

```go
// Two separate output channels
KernelPrint(str)        // → UART (serial console)
FramebufferPuts(str)    // → Framebuffer (visual display)

// Exception handlers can use either or both
ExceptionHandler(...) {
    // Just framebuffer (visual only)
    FramebufferPuts("ERROR\n")
    
    // Just UART (serial only)
    KernelPrint("ERROR\n")
    
    // Both (comprehensive logging)
    FramebufferPuts("ERROR\n")
    KernelPrint("ERROR\n")
}
```

This allows:
- **Remote debugging** via UART without display
- **Visual debugging** via display without serial
- **Comprehensive logging** to both channels when available

