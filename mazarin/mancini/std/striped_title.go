package std

import (
	"image/color"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// StripedTitleBar draws horizontal pinstripes interrupted by centered
// title text — classic Mac OS style. When inactive, only plain centered
// text is drawn (no stripes).
//
// See also [GradientTitleBar] for an animated gradient title bar.
type StripedTitleBar struct {
	pal      mancini.Palette
	textFace mancini.LatinTextFace
}

var _ mancini.TitleBar = (*StripedTitleBar)(nil)

// NewStripedTitleBar creates a StripedTitleBar. The font face is resolved
// once at creation time.
func NewStripedTitleBar(pal mancini.Palette, fonts *mancini.FontConfig, fontSize int64, bold bool) *StripedTitleBar {
	textFace := impl.NewLatinTextFace(fonts, bold, fontSize, mancini.TextAlignmentParams{})
	return &StripedTitleBar{
		pal:      pal,
		textFace: textFace,
	}
}

// DrawTitleBar implements mancini.TitleBar.
func (s *StripedTitleBar) DrawTitleBar(dc mancini.DrawContext, title string, state mancini.WindowState, _ mancini.WindowType, x, y, w, h float64) {
	s.textFace.SetText(title)

	// Stripes are always drawn; alpha varies by focus state.
	tw := s.textFace.MeasureText(title)
	pad := 8.0
	cx := x + w/2
	gapL := cx - tw/2 - pad
	gapR := cx + tw/2 + pad

	darkC := s.pal.DarkShadow()
	var alpha uint8 = 120
	if state == mancini.Inactive {
		alpha = 60
	}
	stripe := color.NRGBA{darkC.R, darkC.G, darkC.B, alpha}
	dc.SetColor(stripe)
	dc.SetLineWidth(1)
	spacing := 3.0
	inset := 4.0
	for sy := y + spacing; sy < y+h-spacing/2; sy += spacing {
		if gapL > x+inset {
			dc.DrawLine(x+inset, sy, gapL, sy)
		}
		if gapR < x+w-inset {
			dc.DrawLine(gapR, sy, x+w-inset, sy)
		}
	}
	dc.Stroke()

	dc.SetColor(s.pal.Text())
	s.textFace.DrawFace(dc, x, y, w, h)
}
