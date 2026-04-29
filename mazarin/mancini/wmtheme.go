package mancini

import "image/color"

// TitleBarStyle selects the visual treatment for the window title bar.
type TitleBarStyle int

const (
	TitleBarStriped  TitleBarStyle = iota // Alternating colored stripes.
	TitleBarGradient                      // Gradient fill.
	TitleBarFlat                          // Solid color fill.
)

// UnfocusedMode controls how unfocused window content is rendered.
type UnfocusedMode int

const (
	UnfocusedDesktopBG UnfocusedMode = iota // Replace content with desktop BG.
	UnfocusedDim                            // Dim content with a translucent overlay.
	UnfocusedNormal                         // Show content normally.
)

// WMTheme provides the complete visual styling for the window manager's
// decorations: shadows, title bar, border geometry, and focus state colors.
//
// WMTheme is separate from [Theme] because rachel (the window manager)
// renders decorations independently of the application's interactor theme.
// The standard implementation is [theme.DefaultWMTheme].
type WMTheme interface {
	// Name returns a human-readable identifier for this WM theme.
	Name() string
	// Palette returns the WM's color scheme.
	Palette() Palette
	// Style returns the [SurfaceStyle] used to render decorations.
	Style() SurfaceStyle

	// ── Decoration geometry ──────────────────────────────────────

	// ShadowTop returns the top shadow margin outside the NeuBox face.
	ShadowTop() int
	// ShadowBottom returns the bottom shadow margin outside the NeuBox face.
	ShadowBottom() int
	// ShadowLeft returns the left shadow margin outside the NeuBox face.
	ShadowLeft() int
	// ShadowRight returns the right shadow margin outside the NeuBox face.
	ShadowRight() int

	// TitleBarHeight returns the height of the window title bar.
	TitleBarHeight() int
	// TitleGap returns the gap between the title bar and the content area.
	TitleGap() int

	// CornerRadius returns the corner radius for the decoration NeuBox.
	CornerRadius() float64

	// ── Derived border totals (shadow + title + gap) ─────────────

	BorderTop() int    // ShadowTop + TitleBarHeight + TitleGap
	BorderRight() int  // ShadowRight
	BorderBottom() int // ShadowBottom
	BorderLeft() int   // ShadowLeft

	// ── Title bar styling ────────────────────────────────────────

	// TitleStyle returns the [TitleBarStyle] (striped, gradient, flat).
	TitleStyle() TitleBarStyle
	// TitleFont returns the font family used for window titles.
	TitleFont() string
	// TitleFontSize returns the font size for window titles.
	TitleFontSize() int64
	// TitleActiveAlpha returns the title text alpha for active windows.
	TitleActiveAlpha() uint8
	// TitleInactiveAlpha returns the title text alpha for inactive windows.
	TitleInactiveAlpha() uint8

	// ── Window state colors ──────────────────────────────────────

	// ActiveBorderColor returns the border color for the focused window.
	ActiveBorderColor() color.NRGBA
	// InactiveBorderColor returns the border color for unfocused windows.
	InactiveBorderColor() color.NRGBA
	// UrgentBorderColor returns the border color for windows demanding attention.
	UrgentBorderColor() color.NRGBA

	// ── Unfocused window content ─────────────────────────────────

	// UnfocusedContentMode returns how content is rendered when the window loses focus.
	UnfocusedContentMode() UnfocusedMode
	// UnfocusedDimAlpha returns the overlay alpha used when [UnfocusedDim] is active.
	UnfocusedDimAlpha() uint8
}
