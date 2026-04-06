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
	Name() string
	Palette() Palette
	Style() SurfaceStyle

	// ── Decoration geometry ──────────────────────────────────────

	// Shadow margins outside the NeuBox face.
	ShadowTop() int
	ShadowBottom() int
	ShadowLeft() int
	ShadowRight() int

	// Title bar dimensions.
	TitleBarHeight() int
	TitleGap() int

	// Corner radius for the decoration NeuBox.
	CornerRadius() float64

	// ── Derived border totals (shadow + title + gap) ─────────────

	BorderTop() int    // ShadowTop + TitleBarHeight + TitleGap
	BorderRight() int  // ShadowRight
	BorderBottom() int // ShadowBottom
	BorderLeft() int   // ShadowLeft

	// ── Title bar styling ────────────────────────────────────────

	TitleStyle() TitleBarStyle
	TitleFont() string
	TitleFontSize() int64
	TitleActiveAlpha() uint8
	TitleInactiveAlpha() uint8

	// ── Window state colors ──────────────────────────────────────

	ActiveBorderColor() color.NRGBA
	InactiveBorderColor() color.NRGBA
	UrgentBorderColor() color.NRGBA

	// ── Unfocused window content ─────────────────────────────────

	UnfocusedContentMode() UnfocusedMode
	UnfocusedDimAlpha() uint8
}
