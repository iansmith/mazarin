package mancini

import (
	"image/color"

	"golang.org/x/image/font"
)

// Feature selects a font variant within a family.
type Feature int

const (
	None       Feature = iota // No font modifiers — regular weight, upright
	Bold
	Italic
	BoldItalic
)

// String returns the style name used by the font index CSV.
func (f Feature) String() string {
	switch f {
	case None:
		return "Regular"
	case Bold:
		return "Bold"
	case Italic:
		return "Italic"
	case BoldItalic:
		return "BoldItalic"
	}
	return "Regular"
}

// FontResolver loads a font.Face for a given family, feature, and size.
// This is typically backed by fontcache.FontCache.OpenFaceByName.
type FontResolver func(family string, feature Feature, size int64) font.Face

// Theme provides visual styling for interactors: colors, font family,
// default size, and a resolver that talks to fontsvc.
type Theme struct {
	Bg          color.NRGBA
	Fg          color.NRGBA
	family      string
	defaultSize int64
	resolve     FontResolver
}

// NewTheme creates a Theme with the given colors, font family, default
// size, and resolver.
func NewTheme(bg, fg color.NRGBA, family string, defaultSize int64, resolve FontResolver) Theme {
	return Theme{
		Bg:          bg,
		Fg:          fg,
		family:      family,
		defaultSize: defaultSize,
		resolve:     resolve,
	}
}

// FontFamily returns the font family name (e.g., "AtkinsonHyperlegibleMono").
func (t Theme) FontFamily() string { return t.family }

// DefaultSize returns the default font size.
func (t Theme) DefaultSize() int64 { return t.defaultSize }

// Font returns a FontConfig for the given feature and size.
// The FontConfig's LoadFace closure calls the theme's resolver.
func (t Theme) Font(feature Feature, size int64) *FontConfig {
	family := t.family
	resolve := t.resolve
	return &FontConfig{
		LoadFace: func(bold bool, sz int64) font.Face {
			// The bold parameter from LoadFace is ignored — the Feature
			// already encodes the weight. sz overrides size.
			if resolve == nil {
				return nil
			}
			return resolve(family, feature, sz)
		},
	}
}

// DefaultFont returns a FontConfig for None (regular) at the default size.
// Equivalent to Font(None, DefaultSize()).
func (t Theme) DefaultFont() *FontConfig {
	return t.Font(None, t.defaultSize)
}

// SetupDC configures a DrawContext with this theme's defaults: foreground
// color and default font face. Returns the same DrawContext for chaining.
func (t Theme) SetupDC(dc DrawContext) DrawContext {
	dc.SetColor(t.Fg)
	fc := t.DefaultFont()
	if fc != nil && fc.LoadFace != nil {
		face := fc.LoadFace(false, t.defaultSize)
		if face != nil {
			dc.SetFontFace(face)
		}
	}
	return dc
}
