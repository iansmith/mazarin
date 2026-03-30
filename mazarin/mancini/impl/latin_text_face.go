package impl

import (
	"mazzy/mazarin/textshape"
	"mazzy/mazarin/mancini"

	"golang.org/x/image/font"
)

// LatinTextFaceImpl implements [mancini.LatinTextFace] for Latin
// left-to-right text. It holds a [textshape.TextLayout] and fontID
// for shaped rendering, with an optional [font.Face] for the unshaped
// fallback path.
type LatinTextFaceImpl struct {
	layout   *textshape.TextLayout
	fontID   int32
	face     font.Face
	fontSize int64
	text     string
	params   mancini.TextAlignmentParams
}

// NewLatinTextFace creates a LatinTextFaceImpl from a [mancini.FontConfig].
// The font.Face is resolved eagerly via fc.LoadFace; the shaped text
// layout and fontID are extracted from fc.Layout and fc.ShapedFontID.
func NewLatinTextFace(fc *mancini.FontConfig, bold bool, fontSize int64, params mancini.TextAlignmentParams) *LatinTextFaceImpl {
	var face font.Face
	var layout *textshape.TextLayout
	var fontID int32
	if fc != nil {
		if fc.LoadFace != nil {
			face = fc.LoadFace(bold, fontSize)
		}
		layout = fc.Layout
		fontID = fc.ShapedFontID
	}
	return &LatinTextFaceImpl{
		layout:   layout,
		fontID:   fontID,
		face:     face,
		fontSize: fontSize,
		params:   params,
	}
}

// NewLatinTextFaceFromLayout creates a LatinTextFaceImpl directly from
// a [textshape.TextLayout] and fontID. The optional [font.Face] provides
// the unshaped fallback; pass nil if only shaped rendering is needed.
//
// This constructor does not require [mancini.FontConfig] or a mancini
// theme, making it usable from darwin code that imports only textshape.
func NewLatinTextFaceFromLayout(layout *textshape.TextLayout, fontID int32,
	face font.Face, fontSize int64, params mancini.TextAlignmentParams,
) *LatinTextFaceImpl {
	return &LatinTextFaceImpl{
		layout:   layout,
		fontID:   fontID,
		face:     face,
		fontSize: fontSize,
		params:   params,
	}
}

// SetText updates the text that DrawFace will render.
func (f *LatinTextFaceImpl) SetText(text string) {
	f.text = text
}

// MeasureText returns the advance width of text in pixels.
func (f *LatinTextFaceImpl) MeasureText(text string) float64 {
	// Shaped path.
	if f.layout != nil {
		adv, err := f.layout.MeasureText(textshape.ShapingParams{
			Text:      text,
			FontID:    f.fontID,
			Direction: textshape.LTR,
		})
		if err == nil {
			return float64(adv) / 64.0
		}
	}

	// Unshaped path.
	if f.face != nil {
		advance := font.MeasureString(f.face, text)
		return float64(advance) / 64.0
	}

	// Fallback estimate.
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
	if f.layout != nil {
		run, err := f.layout.LayoutText(textshape.ShapingParams{
			Text:      f.text,
			FontID:    f.fontID,
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
		return x + (w - tw) / 2
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
