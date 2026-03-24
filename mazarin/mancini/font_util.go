package mancini

import "golang.org/x/image/font"

// resolveFont returns a font.Face for the given style and size.
func resolveFont(fc *FontConfig, bold bool, size int64) font.Face {
	if fc == nil || fc.LoadFace == nil {
		return nil
	}
	return fc.LoadFace(bold, size)
}

// loadFont sets the font on a gg context. Uses FontConfig.LoadFace if available,
// otherwise falls back to loading from FontRegular/FontBold file paths.
func loadFont(fc *FontConfig, dc DrawContext, bold bool, size int64) bool {
	if fc == nil {
		return false
	}
	if fc.LoadFace != nil {
		face := fc.LoadFace(bold, size)
		if face != nil {
			dc.SetFontFace(face)
			return true
		}
	}
	path := fc.FontRegular
	if bold {
		path = fc.FontBold
	}
	if path == "" {
		return false
	}
	return dc.LoadFontFace(path, size) == nil
}
