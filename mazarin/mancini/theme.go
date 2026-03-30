package mancini

import (
	"mazzy/mazarin/extern/textshape"

	"golang.org/x/image/font"
)

// Feature selects a font variant within a family. Used by [Theme.Font]
// and [FontResolver] to request specific weights and styles.
type Feature int

const (
	None       Feature = iota // Regular weight, upright.
	Bold                      // Bold weight.
	Italic                    // Italic style.
	BoldItalic                // Bold weight, italic style.
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

// FontResolver loads a font.Face for a given family, [Feature], and point
// size. It is typically backed by a font cache (e.g., fontcache.FontCache).
// Passed to [theme.NewDefaultTheme] at application startup.
type FontResolver func(family string, feature Feature, size int64) font.Face

// ShapedFontResolver resolves a font for shaped text rendering.
// Returns (fontID, layout) — the caller uses layout.LayoutText with fontID.
// This parallels [FontResolver] for the shaped text path.
type ShapedFontResolver func(family string, feature Feature, size int64) (int32, *textshape.TextLayout)

// Theme provides the complete visual styling for all interactors:
// colors ([Palette]), shadow parameters ([NeumorphicParams]), and font
// resolution ([FontConfig]).
//
// The standard implementation is [theme.DefaultTheme], created by
// [theme.NewDefaultTheme] or [std.NewDefaultTheme].
//
// Theme is accessed by interactors through [impl.ThemedInteractor.Theme].
type Theme interface {
	// Palette returns the color scheme for neumorphic rendering.
	Palette() Palette

	// Neumorphic returns the shadow parameter provider. May return nil
	// to disable all neumorphic rendering.
	Neumorphic() NeumorphicParams

	// Font returns a [FontConfig] for the given feature and size.
	// The returned FontConfig's LoadFace closure resolves the theme's
	// font family with the requested variant.
	Font(feature Feature, size int64) *FontConfig

	// DefaultFont returns a [FontConfig] for the theme's default
	// family and size using the [None] (regular) feature.
	DefaultFont() *FontConfig

	// DefaultFontSize returns the theme's default font size in points.
	DefaultFontSize() int64
}
