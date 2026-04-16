package impl

import (
	"mazzy/mazarin/mancini"
)

// LatinTextFaceImpl implements [mancini.LatinTextFace] for Latin
// left-to-right text. Font opening is deferred to the first DrawFace call,
// since a DrawContext is typically not available at construction time.
type LatinTextFaceImpl struct {
	dc       mancini.DrawContext
	fontID   int32
	fontSize int32
	text     string
	params   mancini.TextAlignmentParams
	fontPath string
}

// NewLatinTextFace creates a LatinTextFaceImpl from a [mancini.FontConfig].
// Font opening is deferred to the first DrawFace call when a DrawContext
// is available. If fc is nil or the font path is empty, fontID falls back
// to fc.ShapedFontID.
func NewLatinTextFace(fc *mancini.FontConfig, bold bool, fontSize int64, params mancini.TextAlignmentParams) *LatinTextFaceImpl {
	var fontPath string
	var fontID int32
	if fc != nil {
		fontPath = fc.FontRegular
		if bold {
			fontPath = fc.FontBold
		}
		fontID = fc.ShapedFontID // fallback until OpenFont succeeds
	}
	return &LatinTextFaceImpl{
		fontID:   fontID,
		fontSize: int32(fontSize),
		fontPath: fontPath,
		params:   params,
	}
}

// NewLatinTextFaceWithFontID creates a LatinTextFaceImpl that uses a
// pre-opened fontID. No OpenFont call is ever made — the caller is
// responsible for ensuring the fontID is valid on whatever DrawContext
// the face will be rendered into. This is useful when the font was
// already opened on a shared glyph provider (e.g., the app's main DC)
// and the face will be drawn on an overlay or child DC that shares the
// same provider.
func NewLatinTextFaceWithFontID(fontID int32, params mancini.TextAlignmentParams) *LatinTextFaceImpl {
	return &LatinTextFaceImpl{
		fontID:   fontID,
		fontSize: 0,
		fontPath: "", // empty prevents ensureFont from calling OpenFont
		params:   params,
	}
}

// ensureFont opens the font via dc.OpenFont. Re-opens when the DC changes
// (e.g., when rendering decorations into different temporary buffers).
func (f *LatinTextFaceImpl) ensureFont(dc mancini.DrawContext) {
	if dc == nil || f.dc == dc {
		return
	}
	f.dc = dc
	if f.fontPath != "" {
		m, err := dc.OpenFont(f.fontPath, 0, f.fontSize)
		if err != nil {
			println("[ensureFont] OpenFont FAILED:", err.Error())
		} else {
			f.fontID = m.FontID
		}
	}
}

// SetText updates the text that DrawFace will render.
func (f *LatinTextFaceImpl) SetText(text string) {
	f.text = text
}

// Text returns the current text set on the face.
func (f *LatinTextFaceImpl) Text() string {
	return f.text
}

// MeasureText returns the advance width of text in pixels.
func (f *LatinTextFaceImpl) MeasureText(text string) float64 {
	if f.dc == nil {
		return float64(len(text)) * float64(f.fontSize) * 0.6
	}
	return f.dc.MeasureText(text, f.fontID)
}

// DrawFace implements [mancini.Face]. It renders the current text into
// the rectangle (x, y, w, h) using the alignment from TextAlignmentParams.
// The caller must set the text color on dc before calling.
func (f *LatinTextFaceImpl) DrawFace(dc mancini.DrawContext, x, y, w, h float64) {
	if f.text == "" {
		return
	}
	f.ensureFont(dc)

	tw := dc.MeasureText(f.text, f.fontID)
	metrics := dc.GetFontMetrics(f.fontID)
	asc := float64(metrics.Ascent) / 64.0
	desc := float64(metrics.Descent) / 64.0

	ox := f.hAlignX(x, w, tw)
	oy := f.vAlignY(y, h, asc, desc)
	dc.DrawText(f.text, f.fontID, ox, oy)
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

