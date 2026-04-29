package theme

import (
	"image/color"

	"mazzy/mazarin/mancini"
)

var _ mancini.WMTheme = (*DefaultWMTheme)(nil)

// DefaultWMTheme implements [mancini.WMTheme] with the default neumorphic
// decoration parameters matching rachel's current hardcoded constants.
type DefaultWMTheme struct {
	name    string
	pal     mancini.Palette
	style   mancini.SurfaceStyle
	raisedLightAlpha uint8
}

// Name returns the WM theme's display name.
func (t *DefaultWMTheme) Name() string              { return t.name }

// Palette returns the WM theme's [mancini.Palette].
func (t *DefaultWMTheme) Palette() mancini.Palette   { return t.pal }

// Style returns the WM theme's active [mancini.SurfaceStyle].
func (t *DefaultWMTheme) Style() mancini.SurfaceStyle { return t.style }

// SetStyle sets the active SurfaceStyle. Called by std after construction
// to break the theme→std import cycle.
func (t *DefaultWMTheme) SetStyle(s mancini.SurfaceStyle) { t.style = s }

// ── Decoration geometry ─────────────────────────────────────────────────

// Shadow margins: left/right/bottom must be wide enough to contain the
// 12-pixel resize handle semi-circles (radius 12 + 2px groove margin = 14).
// Top is kept minimal since the title bar occupies that zone.
// ShadowTop returns the top shadow margin in pixels.
func (t *DefaultWMTheme) ShadowTop() int    { return 2 }

// ShadowBottom returns the bottom shadow margin in pixels.
func (t *DefaultWMTheme) ShadowBottom() int { return 14 }

// ShadowLeft returns the left shadow margin in pixels.
func (t *DefaultWMTheme) ShadowLeft() int   { return 14 }

// ShadowRight returns the right shadow margin in pixels.
func (t *DefaultWMTheme) ShadowRight() int  { return 14 }

// TitleBarHeight returns the title bar height in pixels.
func (t *DefaultWMTheme) TitleBarHeight() int { return 20 }

// TitleGap returns the gap in pixels between the title bar and the window content.
func (t *DefaultWMTheme) TitleGap() int       { return 2 }

// CornerRadius returns the window corner radius in pixels.
func (t *DefaultWMTheme) CornerRadius() float64 { return 0.0 }

// ── Derived border totals ───────────────────────────────────────────────

// BorderTop returns the total top border width (shadow + title bar + gap).
func (t *DefaultWMTheme) BorderTop() int    { return t.ShadowTop() + t.TitleBarHeight() + t.TitleGap() }

// BorderRight returns the total right border width.
func (t *DefaultWMTheme) BorderRight() int  { return t.ShadowRight() }

// BorderBottom returns the total bottom border width.
func (t *DefaultWMTheme) BorderBottom() int { return t.ShadowBottom() }

// BorderLeft returns the total left border width.
func (t *DefaultWMTheme) BorderLeft() int   { return t.ShadowLeft() }

// ── Title bar styling ───────────────────────────────────────────────────

// TitleStyle returns the title bar style ([mancini.TitleBarStriped] by default).
func (t *DefaultWMTheme) TitleStyle() mancini.TitleBarStyle   { return mancini.TitleBarStriped }

// TitleFont returns the title bar font family name (empty = theme default).
func (t *DefaultWMTheme) TitleFont() string                    { return "" } // uses theme default

// TitleFontSize returns the title bar font size in pixels.
func (t *DefaultWMTheme) TitleFontSize() int64                 { return 10 }

// TitleActiveAlpha returns the title text alpha for focused windows.
func (t *DefaultWMTheme) TitleActiveAlpha() uint8              { return 255 }

// TitleInactiveAlpha returns the title text alpha for unfocused windows.
func (t *DefaultWMTheme) TitleInactiveAlpha() uint8            { return 180 }

// ── Window state colors ─────────────────────────────────────────────────

// ActiveBorderColor returns the border color for the focused window.
func (t *DefaultWMTheme) ActiveBorderColor() color.NRGBA   { return t.pal.Surface() }

// InactiveBorderColor returns the border color for unfocused windows.
func (t *DefaultWMTheme) InactiveBorderColor() color.NRGBA { return t.pal.SurfaceTint() }

// UrgentBorderColor returns the border color for windows requesting attention.
func (t *DefaultWMTheme) UrgentBorderColor() color.NRGBA   { return t.pal.Highlight() }

// ── Unfocused window content ────────────────────────────────────────────

// UnfocusedContentMode returns how unfocused window content should be rendered.
func (t *DefaultWMTheme) UnfocusedContentMode() mancini.UnfocusedMode { return mancini.UnfocusedDesktopBG }

// UnfocusedDimAlpha returns the dim alpha applied when unfocused content is dimmed.
func (t *DefaultWMTheme) UnfocusedDimAlpha() uint8                    { return 128 }

// RaisedLightAlpha returns the custom light shadow alpha for window
// decorations. Rachel uses 160 instead of the default 255 to avoid
// a visible white margin in the border zone.
func (t *DefaultWMTheme) RaisedLightAlpha() uint8 { return t.raisedLightAlpha }

// NewDefaultWMTheme creates a WMTheme with default decoration parameters.
// pal should be the RB-swapped palette used by rachel.
// style will be set via SetStyle after construction.
func NewDefaultWMTheme(pal mancini.Palette) *DefaultWMTheme {
	return &DefaultWMTheme{
		name:             "Default WM",
		pal:              pal,
		raisedLightAlpha: 160,
	}
}
