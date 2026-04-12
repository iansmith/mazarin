package main

import (
	"image/color"

	"mazzy/mazarin/mancini"
)

// ArrowRightFace draws a right-pointing triangle (play button) centered
// in the given rectangle.
type ArrowRightFace struct {
	Color color.Color
}

// DrawFace implements mancini.Face.
func (f *ArrowRightFace) DrawFace(dc mancini.DrawContext, x, y, w, h float64) {
	// Inset by 25% on each side for a nice centered arrow.
	insetX := w * 0.25
	insetY := h * 0.25

	left := x + insetX
	right := x + w - insetX
	top := y + insetY
	bottom := y + h - insetY
	midY := y + h/2

	dc.SetColor(f.Color)
	dc.MoveTo(left, top)
	dc.LineTo(right, midY)
	dc.LineTo(left, bottom)
	dc.ClosePath()
	dc.Fill()
}
