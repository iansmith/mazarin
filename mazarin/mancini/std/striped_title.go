package std

import (
	"image/color"

	"golang.org/x/image/font"

	"mazzy/mazarin/mancini"
)

// StripedTitle returns a TitleDraw callback that draws horizontal pinstripes
// interrupted by centered title text — classic Mac OS style.
// The font face is resolved once at creation time.
func StripedTitle(pal mancini.Palette, fonts *mancini.FontConfig, title string, fontSize int64, bold bool) func(dc mancini.DrawContext, focused bool, x, y, w, h float64) {
	var face font.Face
	if fonts != nil && fonts.LoadFace != nil {
		face = fonts.LoadFace(bold, fontSize)
	}
	return func(dc mancini.DrawContext, focused bool, x, y, w, h float64) {
		if !focused {
			return
		}

		if face != nil {
			dc.SetFontFace(face)
		}
		tw, _ := dc.MeasureString(title)
		pad := 8.0
		cx := x + w/2
		gapL := cx - tw/2 - pad
		gapR := cx + tw/2 + pad

		darkC := pal.DarkSh
		stripe := color.NRGBA{darkC.R, darkC.G, darkC.B, 120}
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

		dc.SetColor(pal.Text)
		dc.DrawStringAnchored(title, cx, y+h/2, 0.5, 0.5)
	}
}
