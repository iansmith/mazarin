package theme

import (
	"golang.org/x/image/font"

	"mazzy/mazarin/mancini"
)

var _ mancini.Theme = (*DefaultTheme)(nil)

// DefaultTheme implements [mancini.Theme], combining a [mancini.Palette],
// [mancini.NeumorphicParams], and [mancini.FontResolver] into one value.
// It is the standard theme used by all interactors in
// [mazzy/mazarin/mancini/std].
//
// Create with [NewDefaultTheme] (standard purple palette) or [NewTheme]
// (fully customized).
type DefaultTheme struct {
	pal     mancini.Palette
	neu     mancini.NeumorphicParams
	family  string
	defSize int64
	resolve mancini.FontResolver
}

func (t *DefaultTheme) Palette() mancini.Palette           { return t.pal }
func (t *DefaultTheme) Neumorphic() mancini.NeumorphicParams { return t.neu }
func (t *DefaultTheme) DefaultFontSize() int64             { return t.defSize }

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

// NewDefaultTheme creates a Theme with the standard purple palette and
// default neumorphic parameters.  resolve may be nil if font loading is
// not yet available.
func NewDefaultTheme(family string, defaultSize int64, resolve mancini.FontResolver) *DefaultTheme {
	return &DefaultTheme{
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
