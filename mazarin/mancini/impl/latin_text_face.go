package impl

import (
	"mazzy/mazarin/extern/textshape"
	"mazzy/mazarin/mancini"

	"golang.org/x/image/font"
)

// LatinTextFaceImpl implements [mancini.LatinTextFace] for Latin
// left-to-right text. It holds a [mancini.FontConfig] and eagerly
// resolves the font.Face at construction time.
type LatinTextFaceImpl struct {
	fc       *mancini.FontConfig
	bold     bool
	fontSize int64
	face     font.Face
	text     string
	params   mancini.TextAlignmentParams
}

// NewLatinTextFace creates a LatinTextFaceImpl with the given font
// configuration and alignment. The font.Face is resolved eagerly via
// fc.LoadFace so it is not re-resolved on every draw.
func NewLatinTextFace(fc *mancini.FontConfig, bold bool, fontSize int64, params mancini.TextAlignmentParams) *LatinTextFaceImpl {
	var face font.Face
	if fc != nil && fc.LoadFace != nil {
		face = fc.LoadFace(bold, fontSize)
	}
	return &LatinTextFaceImpl{
		fc:       fc,
		bold:     bold,
		fontSize: fontSize,
		face:     face,
		params:   params,
	}
}

// SetText updates the text that DrawFace will render.
func (f *LatinTextFaceImpl) SetText(text string) {
	f.text = text
}

// MeasureText returns the advance width of text in pixels.
func (f *LatinTextFaceImpl) MeasureText(text string) float64 {
	if f.fc != nil {
		return f.fc.MeasureText(text, f.bold, f.fontSize)
	}
	return float64(len(text)) * float64(f.fontSize) * 0.6
}

// DrawFace implements [mancini.Face]. It renders the current text into
// the rectangle (x, y, w, h) using the alignment from TextAlignmentParams.
// The caller must set the text color on dc before calling.
func (f *LatinTextFaceImpl) DrawFace(dc mancini.DrawContext, x, y, w, h float64) {
	if f.text == "" {
		return
	}

	// Set the font face on the DC.
	if f.face != nil {
		dc.SetFontFace(f.face)
	}

	// Try shaped text path first.
	if f.fc != nil && f.fc.Layout != nil {
		run, err := f.fc.Layout.LayoutText(textshape.ShapingParams{
			Text:      f.text,
			FontID:    f.fc.ShapedFontID,
			Direction: textshape.LTR,
			Script:    textshape.ScriptLatin,
		})
		if err == nil && run != nil {
			tw := float64(run.TotalAdvance) / 64
			asc := float64(run.Ascent) / 64
			desc := float64(run.Descent) / 64
			ox := f.hAlignX(x, w, tw)
			oy := f.vAlignY(y, h, asc, desc)
			dc.DrawShapedText(run, ox, oy)
			return
		}
	}

	// Unshaped path.
	if f.face != nil {
		m := f.face.Metrics()
		asc := float64(m.Ascent) / 64
		desc := float64(m.Descent) / 64
		tw, _ := dc.MeasureString(f.text)
		ox := f.hAlignX(x, w, tw)
		oy := f.vAlignY(y, h, asc, desc)
		dc.DrawStringAnchored(f.text, ox, oy, 0, 0)
	} else {
		// Last resort: no font face available. Use gg's default anchor.
		ox := f.hAlignX(x, w, 0)
		oy := y + h/2
		ax := 0.0
		switch f.params.HAlign {
		case mancini.HAlignCenter:
			ox = x + w/2
			ax = 0.5
		case mancini.HAlignRight:
			ox = x + w
			ax = 1.0
		}
		dc.DrawStringAnchored(f.text, ox, oy, ax, 0.5)
	}
}

// hAlignX computes the left edge of the text origin.
func (f *LatinTextFaceImpl) hAlignX(x, w, tw float64) float64 {
	switch f.params.HAlign {
	case mancini.HAlignLeft:
		return x
	case mancini.HAlignRight:
		return x + w - tw
	default: // HAlignCenter
		return x + (w-tw)/2
	}
}

// vAlignY computes the baseline Y of the text.
func (f *LatinTextFaceImpl) vAlignY(y, h, asc, desc float64) float64 {
	switch f.params.VAlign {
	case mancini.VAlignTop:
		return y + asc
	case mancini.VAlignBottom:
		return y + h - desc
	case mancini.VAlignBaseline:
		return y
	default: // VAlignCenter
		return y + h/2 + (asc-desc)/2
	}
}
