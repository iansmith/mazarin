package mancini

import (
	"mazarin/textshape"

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
	Light                     // Light weight.
	Condensed                 // Condensed width.
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
	case Light:
		return "Light"
	case Condensed:
		return "Condensed"
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
type ShapedFontResolver func(family string, feature Feature, size int64) (int32, textshape.TextLayout)

// Theme provides the complete visual styling for all interactors:
// colors ([Palette]), surface style ([SurfaceStyle]), geometry, and font
// resolution ([FontConfig]).
//
// The standard implementation is [theme.DefaultTheme], created by
// [theme.NewDefaultTheme] or [std.NewDefaultTheme].
//
// Theme is accessed by interactors through [impl.ThemedInteractor.Theme].
type Theme interface {
	// Name returns a human-readable identifier for this theme (e.g. "Purple Neumorphic").
	Name() string

	// Palette returns the color scheme.
	Palette() Palette

	// Style returns the active [SurfaceStyle] used to render interactor
	// surfaces. All interactors should call Style().DrawBox() instead of
	// using [NeuBoxWith] directly.
	Style() SurfaceStyle

	// Neumorphic returns the shadow parameter provider. May return nil
	// to disable all neumorphic rendering.
	//
	// Deprecated: use [Theme.Style] instead. Will be removed after all
	// interactors are migrated.
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

	// ── Geometry ──────────────────────────────────────────────────

	// ControlRadius returns the corner radius for buttons and choosers.
	ControlRadius() float64

	// FieldRadius returns the corner radius for text input fields.
	FieldRadius() float64

	// FieldBorderWidth returns the border stroke width for text fields.
	FieldBorderWidth() float64

	// FieldPadding returns the horizontal padding inside text fields.
	FieldPadding() float64

	// ScrollbarThickness returns the cross-axis thickness of scrollbars.
	ScrollbarThickness() float64

	// ScrollbarMinThumb returns the minimum thumb length for scrollbars.
	ScrollbarMinThumb() float64

	// ── Font configuration ───────────────────────────────────────

	// FontFamily returns the primary font family name.
	FontFamily() string

	// FontFamilyMono returns the monospace font family name.
	FontFamilyMono() string

	// TitleFontSize returns the font size for window titles.
	TitleFontSize() int64

	// ControlFontSize returns the font size for buttons and controls.
	ControlFontSize() int64

	// BodyFontSize returns the font size for body text.
	BodyFontSize() int64

	// SmallFontSize returns the font size for small/caption text.
	SmallFontSize() int64
}
