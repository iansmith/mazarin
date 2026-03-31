package mancini

import "golang.org/x/image/font"

// resolveFont returns a font.Face for the given style and size.
func resolveFont(fc *FontConfig, bold bool, size int64) font.Face {
	if fc == nil || fc.LoadFace == nil {
		return nil
	}
	return fc.LoadFace(bold, size)
}

// openFont opens a font via DrawContext.OpenFont and returns the fontID.
// Uses FontConfig paths; returns -1 on failure.
func openFont(fc *FontConfig, dc DrawContext, bold bool, size int64) int32 {
	if fc == nil {
		return -1
	}
	// If a shaped fontID is already set and layout is available, use it.
	if fc.Layout != nil && fc.ShapedFontID >= 0 {
		return fc.ShapedFontID
	}
	path := fc.FontRegular
	if bold {
		path = fc.FontBold
	}
	if path == "" {
		return -1
	}
	variant := int32(0)
	if bold {
		variant = 1
	}
	m, err := dc.OpenFont(path, variant, int32(size))
	if err != nil {
		return -1
	}
	return m.FontID
}
