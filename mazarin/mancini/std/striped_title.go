package std

import (
	"image/color"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// StripedTitle returns a TitleDraw callback for [AppWindow] that draws
// horizontal pinstripes interrupted by centered title text — classic
// Mac OS style. The font face is resolved once at creation time.
//
// The returned callback only renders when focused=true. When unfocused,
// [AppWindow]'s own fallback renders plain centered text.
//
// See also [GradientTitle] for an animated gradient title bar.
func StripedTitle(pal mancini.Palette, fonts *mancini.FontConfig, title string, fontSize int64, bold bool) func(dc mancini.DrawContext, focused bool, x, y, w, h float64) {
	textFace := impl.NewLatinTextFace(fonts, bold, fontSize, mancini.TextAlignmentParams{})
	textFace.SetText(title)

	return func(dc mancini.DrawContext, focused bool, x, y, w, h float64) {
		if !focused {
			return
		}

		tw := textFace.MeasureText(title)
		pad := 8.0
		cx := x + w/2
		gapL := cx - tw/2 - pad
		gapR := cx + tw/2 + pad

		darkC := pal.DarkShadow()
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

		dc.SetColor(pal.Text())
		textFace.DrawFace(dc, x, y, w, h)
	}
}
