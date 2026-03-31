package mancini

import (
	"mazarin/textshape"

	"golang.org/x/image/font"
)

// FontConfig holds font loading and text measurement functions,
// independent of palette or rendering configuration. It is returned
// by [Theme.Font] and [Theme.DefaultFont].
//
// If LoadFace is nil (e.g., when no [FontResolver] was provided to the
// theme), text measurement falls back to a rough character-width
// estimate.
//
// If Layout is non-nil, [MeasureText] uses shaped text measurement
// instead of the simple face-based measurement. Interactors check
// Layout to decide whether to use the shaped text rendering path.
type FontConfig struct {
	// LoadFace creates a font.Face at the given size.
	LoadFace func(bold bool, size int64) font.Face

	// FontRegular is the path to the regular-weight font file.
	// Used when LoadFace is nil.
	FontRegular string

	// FontBold is the path to the bold-weight font file.
	// Used when LoadFace is nil.
	FontBold string

	// Layout is the text shaping+layout engine. When non-nil,
	// MeasureText uses shaped measurement and interactors use the
	// shaped rendering path via DrawShapedText.
	Layout textshape.TextLayout

	// ShapedFontID is the fontID registered with Layout for this config.
	// Only valid when Layout is non-nil.
	ShapedFontID int32
}

// MeasureText returns the advance width of text at the given font size.
func (fc *FontConfig) MeasureText(text string, bold bool, fontSize int64) float64 {
	if fc == nil {
		return float64(len(text)) * float64(fontSize) * 0.6
	}

	// Shaped path: use TextLayout.MeasureText.
	if fc.Layout != nil {
		adv, err := fc.Layout.MeasureText(textshape.ShapingParams{
			Text:      text,
			FontID:    fc.ShapedFontID,
			Direction: textshape.LTR,
		})
		if err == nil {
			return float64(adv) / 64.0
		}
		// fall through to unshaped path on error
	}

	if fc.LoadFace == nil {
		return float64(len(text)) * float64(fontSize) * 0.6 // rough estimate
	}
	face := fc.LoadFace(bold, fontSize)
	if face == nil {
		return float64(len(text)) * float64(fontSize) * 0.6
	}
	advance := font.MeasureString(face, text)
	return float64(advance) / 64.0
}

// MeasureTextWidth returns the advance width of text using a pre-resolved font.Face.
func MeasureTextWidth(face font.Face, text string) int64 {
	if face == nil {
		return int64(len(text) * 10) // rough estimate
	}
	advance := font.MeasureString(face, text)
	return int64(advance) / 64
}
