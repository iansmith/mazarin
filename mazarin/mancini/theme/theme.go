package theme

import (
	"golang.org/x/image/font"

	"mazzy/mazarin/mancini"
)

var _ mancini.Theme = (*DefaultTheme)(nil)

// DefaultTheme implements [mancini.Theme], combining a [mancini.Palette],
// [mancini.SurfaceStyle], [mancini.NeumorphicParams], geometry, and
// [mancini.FontResolver] into one value. It is the standard theme used
// by all interactors in [mazzy/mazarin/mancini/std].
//
// Create with [NewDefaultTheme] (standard purple palette) or [NewTheme]
// (fully customized).
type DefaultTheme struct {
	name    string
	pal     mancini.Palette
	style   mancini.SurfaceStyle
	neu     mancini.NeumorphicParams
	family  string
	mono    string
	defSize int64
	resolve mancini.FontResolver
}

// Name returns the theme's display name.
func (t *DefaultTheme) Name() string                         { return t.name }

// Palette returns the theme's [mancini.Palette].
func (t *DefaultTheme) Palette() mancini.Palette             { return t.pal }

// Style returns the theme's active [mancini.SurfaceStyle].
func (t *DefaultTheme) Style() mancini.SurfaceStyle          { return t.style }

// Neumorphic returns the theme's [mancini.NeumorphicParams].
func (t *DefaultTheme) Neumorphic() mancini.NeumorphicParams { return t.neu }

// DefaultFontSize returns the theme's default font size in pixels.
func (t *DefaultTheme) DefaultFontSize() int64               { return t.defSize }

// ── Geometry ────────────────────────────────────────────────────────────

// ControlRadius returns the corner radius used for buttons and similar controls.
func (t *DefaultTheme) ControlRadius() float64      { return 8.0 }

// FieldRadius returns the corner radius used for input fields.
func (t *DefaultTheme) FieldRadius() float64         { return 6.0 }

// FieldBorderWidth returns the border width used for input fields.
func (t *DefaultTheme) FieldBorderWidth() float64    { return 2.0 }

// FieldPadding returns the internal padding used for input fields.
func (t *DefaultTheme) FieldPadding() float64        { return 8.0 }

// ScrollbarThickness returns the scrollbar's cross-axis thickness in pixels.
func (t *DefaultTheme) ScrollbarThickness() float64  { return 22.5 }

// ScrollbarMinThumb returns the minimum scrollbar thumb length in pixels.
func (t *DefaultTheme) ScrollbarMinThumb() float64   { return 20.0 }

// ── Font configuration ──────────────────────────────────────────────────

// FontFamily returns the theme's primary font family name.
func (t *DefaultTheme) FontFamily() string     { return t.family }

// FontFamilyMono returns the theme's monospace font family name.
func (t *DefaultTheme) FontFamilyMono() string { return t.mono }

// TitleFontSize returns the font size used for titles.
func (t *DefaultTheme) TitleFontSize() int64   { return t.defSize + 2 }

// ControlFontSize returns the font size used for controls.
func (t *DefaultTheme) ControlFontSize() int64 { return t.defSize }

// BodyFontSize returns the font size used for body text.
func (t *DefaultTheme) BodyFontSize() int64    { return t.defSize }

// SmallFontSize returns the font size used for small/secondary text.
func (t *DefaultTheme) SmallFontSize() int64   { return t.defSize - 2 }

// DefaultFont returns a FontConfig for the theme's default family and size
// using the None (regular) feature.
func (t *DefaultTheme) DefaultFont() *mancini.FontConfig {
	return t.Font(mancini.None, t.defSize)
}

// Font returns a FontConfig whose LoadFace closure resolves the theme's
// font family with the given feature and size.  If no FontResolver was
// provided, LoadFace is nil and callers fall back to FontConfig's
// built-in estimate.
func (t *DefaultTheme) Font(feature mancini.Feature, size int64) *mancini.FontConfig {
	if t.resolve == nil {
		return &mancini.FontConfig{}
	}
	resolver := t.resolve
	family := t.family
	return &mancini.FontConfig{
		FontRegular: family,
		FontBold:    family,
		LoadFace: func(bold bool, sz int64) font.Face {
			f := feature
			if bold {
				switch f {
				case mancini.None:
					f = mancini.Bold
				case mancini.Italic:
					f = mancini.BoldItalic
				default:
					// already Bold or BoldItalic
				}
			}
			return resolver(family, f, sz)
		},
	}
}

// SetStyle sets the active SurfaceStyle. This is called by
// [std.NewDefaultTheme] after construction, since the theme package
// cannot import std (where style implementations live).
func (t *DefaultTheme) SetStyle(s mancini.SurfaceStyle) { t.style = s }

// SetName sets the theme's display name.
func (t *DefaultTheme) SetName(name string) { t.name = name }

// SetMonoFamily sets the monospace font family name.
func (t *DefaultTheme) SetMonoFamily(mono string) { t.mono = mono }

// NewDefaultTheme creates a Theme with the standard purple palette and
// default neumorphic parameters.  resolve may be nil if font loading is
// not yet available.
//
// The returned theme has no SurfaceStyle set — call [DefaultTheme.SetStyle]
// or use [std.NewDefaultTheme] which wires one up automatically.
func NewDefaultTheme(family string, defaultSize int64, resolve mancini.FontResolver) *DefaultTheme {
	return &DefaultTheme{
		name:    "Purple Neumorphic",
		pal:     NewDefaultPalette(),
		neu:     NewDefaultNeumorphicParams(),
		family:  family,
		defSize: defaultSize,
		resolve: resolve,
	}
}

// NewTheme creates a fully customized Theme.
func NewTheme(pal mancini.Palette, neu mancini.NeumorphicParams, family string, defaultSize int64, resolve mancini.FontResolver) *DefaultTheme {
	return &DefaultTheme{
		pal:     pal,
		neu:     neu,
		family:  family,
		defSize: defaultSize,
		resolve: resolve,
	}
}
