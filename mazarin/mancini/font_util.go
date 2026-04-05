package mancini

import "golang.org/x/image/font"

// resolveFont returns a font.Face for the given style and size.
func resolveFont(fc *FontConfig, bold bool, size int64) font.Face {
	if fc == nil || fc.LoadFace == nil {
		return nil
	}
	return fc.LoadFace(bold, size)
}

// featureToVariant maps a Feature to the wire variant code used by fontsvc.
func featureToVariant(f Feature) int32 {
	switch f {
	case Bold:
		return 1
	case Italic:
		return 2
	case BoldItalic:
		return 3
	case Light:
		return 4
	case Condensed:
		return 5
	default:
		return 0
	}
}

// OpenFont opens a font via DrawContext.OpenFont for a given Theme, Feature,
// and size. Returns the fontID or -1 on failure.
func OpenFont(dc DrawContext, theme Theme, feature Feature, size int64) int32 {
	fc := theme.Font(feature, size)
	return openFont(fc, dc, feature, size)
}

// openFont opens a font via DrawContext.OpenFont and returns the fontID.
// Uses FontConfig paths; returns -1 on failure.
func openFont(fc *FontConfig, dc DrawContext, feature Feature, size int64) int32 {
	if fc == nil {
		return -1
	}
	// If a shaped fontID is already set and layout is available, use it.
	if fc.Layout != nil && fc.ShapedFontID >= 0 {
		return fc.ShapedFontID
	}
	bold := feature == Bold || feature == BoldItalic
	path := fc.FontRegular
	if bold {
		path = fc.FontBold
	}
	if path == "" {
		return -1
	}
	m, err := dc.OpenFont(path, featureToVariant(feature), int32(size))
	if err != nil {
		return -1
	}
	return m.FontID
}
