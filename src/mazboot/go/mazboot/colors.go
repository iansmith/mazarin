package main

// ANSI Terminal Color Palette
// Color values in XRGB8888 format (0x00RRGGBB with 00=fully opaque)
// Based on Dracula theme for consistent visual styling

const (
	// Basic ANSI Colors
	AnsiBlack   uint32 = 0x00111111 // Dark gray/black
	AnsiRed     uint32 = 0x00FF9DA4 // Soft red
	AnsiGreen   uint32 = 0x00D1F1A9 // Soft green
	AnsiYellow  uint32 = 0x00FFEEAC // Soft yellow
	AnsiBlue    uint32 = 0x00BBDAFF // Soft blue
	AnsiMagenta uint32 = 0x00EBBBFF // Soft magenta
	AnsiCyan    uint32 = 0x0099FFFF // Soft cyan
	AnsiWhite   uint32 = 0x00CCCCCC // Light gray

	// Bright ANSI Colors (High intensity)
	AnsiBrightBlack   uint32 = 0x00333333 // Medium gray
	AnsiBrightRed     uint32 = 0x00FF7882 // Bright red
	AnsiBrightGreen   uint32 = 0x00B8F171 // Bright green
	AnsiBrightYellow  uint32 = 0x00FFE580 // Bright yellow
	AnsiBrightBlue    uint32 = 0x0080BAFF // Bright blue
	AnsiBrightMagenta uint32 = 0x00D778FF // Bright magenta
	AnsiBrightCyan    uint32 = 0x0078FFFF // Bright cyan
	AnsiBrightWhite   uint32 = 0x00FFFFFF // Pure white

	// Background Colors
	MidnightBlue uint32 = 0x00191B70 // RGB(25, 27, 112) - midnight blue background

	// Default Framebuffer Colors
	FramebufferBackgroundColor uint32 = MidnightBlue     // Midnight blue background
	FramebufferTextColor       uint32 = AnsiBrightGreen  // Bright green text
	FramebufferErrorColor      uint32 = AnsiBrightRed    // Bright red for errors
	FramebufferWarningColor    uint32 = AnsiBrightYellow // Bright yellow for warnings
	FramebufferSuccessColor    uint32 = AnsiBrightGreen  // Bright green for success
	FramebufferInfoColor       uint32 = AnsiBrightBlue   // Bright blue for info

	// Alternative color schemes for future use
	FramebufferSecondaryText uint32 = AnsiCyan  // Cyan for secondary text
	FramebufferDimmedText    uint32 = AnsiWhite // Light gray for less important text
)

// Color scheme helper function for future expansion
// Allows switching between different color themes
type ColorScheme struct {
	Background uint32
	Text       uint32
	Error      uint32
	Warning    uint32
	Success    uint32
	Info       uint32
}

// Default color scheme
var DefaultColorScheme = ColorScheme{
	Background: FramebufferBackgroundColor,
	Text:       FramebufferTextColor,
	Error:      FramebufferErrorColor,
	Warning:    FramebufferWarningColor,
	Success:    FramebufferSuccessColor,
	Info:       FramebufferInfoColor,
}

// Classic color scheme (for future reference)
var ClassicColorScheme = ColorScheme{
	Background: AnsiBlack,
	Text:       AnsiGreen,
	Error:      AnsiRed,
	Warning:    AnsiYellow,
	Success:    AnsiGreen,
	Info:       AnsiBlue,
}
