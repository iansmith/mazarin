package mancini

import (
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

// Theme provides visual styling for interactors.
type Theme interface {
	Palette() Palette
	Neumorphic() NeumorphicParams
	Font(feature Feature, size int64) *FontConfig
	DefaultFont() *FontConfig
	DefaultFontSize() int64
}
