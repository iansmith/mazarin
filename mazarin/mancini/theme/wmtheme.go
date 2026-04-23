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

func (t *DefaultWMTheme) Name() string              { return t.name }
func (t *DefaultWMTheme) Palette() mancini.Palette   { return t.pal }
func (t *DefaultWMTheme) Style() mancini.SurfaceStyle { return t.style }

// SetStyle sets the active SurfaceStyle. Called by std after construction
// to break the theme→std import cycle.
func (t *DefaultWMTheme) SetStyle(s mancini.SurfaceStyle) { t.style = s }

// ── Decoration geometry ─────────────────────────────────────────────────

// Shadow margins: left/right/bottom must be wide enough to contain the
// 12-pixel resize handle semi-circles (radius 12 + 2px groove margin = 14).
// Top is kept minimal since the title bar occupies that zone.
func (t *DefaultWMTheme) ShadowTop() int    { return 2 }
func (t *DefaultWMTheme) ShadowBottom() int { return 14 }
func (t *DefaultWMTheme) ShadowLeft() int   { return 14 }
func (t *DefaultWMTheme) ShadowRight() int  { return 14 }

func (t *DefaultWMTheme) TitleBarHeight() int { return 20 }
func (t *DefaultWMTheme) TitleGap() int       { return 2 }
func (t *DefaultWMTheme) CornerRadius() float64 { return 0.0 }

// ── Derived border totals ───────────────────────────────────────────────

func (t *DefaultWMTheme) BorderTop() int    { return t.ShadowTop() + t.TitleBarHeight() + t.TitleGap() }
func (t *DefaultWMTheme) BorderRight() int  { return t.ShadowRight() }
func (t *DefaultWMTheme) BorderBottom() int { return t.ShadowBottom() }
func (t *DefaultWMTheme) BorderLeft() int   { return t.ShadowLeft() }

// ── Title bar styling ───────────────────────────────────────────────────

func (t *DefaultWMTheme) TitleStyle() mancini.TitleBarStyle   { return mancini.TitleBarStriped }
func (t *DefaultWMTheme) TitleFont() string                    { return "" } // uses theme default
func (t *DefaultWMTheme) TitleFontSize() int64                 { return 10 }
func (t *DefaultWMTheme) TitleActiveAlpha() uint8              { return 255 }
func (t *DefaultWMTheme) TitleInactiveAlpha() uint8            { return 180 }

// ── Window state colors ─────────────────────────────────────────────────

func (t *DefaultWMTheme) ActiveBorderColor() color.NRGBA   { return t.pal.Surface() }
func (t *DefaultWMTheme) InactiveBorderColor() color.NRGBA { return t.pal.SurfaceTint() }
func (t *DefaultWMTheme) UrgentBorderColor() color.NRGBA   { return t.pal.Highlight() }

// ── Unfocused window content ────────────────────────────────────────────

func (t *DefaultWMTheme) UnfocusedContentMode() mancini.UnfocusedMode { return mancini.UnfocusedDesktopBG }
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
