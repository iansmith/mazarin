package std

import (
	"image/color"

	"mazzy/mazarin/mancini"
)

var _ mancini.SurfaceStyle = (*FlatStyle)(nil)

// FlatStyle implements [mancini.SurfaceStyle] with no directional lighting
// at all. All borders are uniform weight on all four sides.
//
// Raised: face brightened by delta, thin uniform border.
// Flush:  fill Surface, thin uniform border.
// Inset:  face darkened by delta, thin uniform border.
type FlatStyle struct {
	delta       int     // brightness adjustment
	borderWidth float64 // border stroke width
}

// NewFlatStyle creates a FlatStyle. delta controls brightness shift for
// Raised/Inset. borderWidth sets the uniform border stroke width.
func NewFlatStyle(delta int, borderWidth float64) *FlatStyle {
	return &FlatStyle{delta: delta, borderWidth: borderWidth}
}

func (s *FlatStyle) DrawBox(pal mancini.Palette, dc mancini.DrawContext, depth mancini.NeuDepth, weight mancini.Weight,
	x1, y1, x2, y2, r float64, face color.NRGBA) {

	switch depth {
	case mancini.Raised:
		dc.SetColor(adjustBrightness(face, s.delta))
	case mancini.Flush:
		dc.SetColor(face)
	case mancini.Inset:
		dc.SetColor(adjustBrightness(face, -s.delta))
	}
	if r > 0 {
		dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
	} else {
		dc.DrawRectangle(x1, y1, x2-x1, y2-y1)
	}
	dc.Fill()

	drawUniformEdge(dc, x1, y1, x2, y2, 0, pal.Mid(), s.borderWidth)
}

func (s *FlatStyle) DrawCircle(pal mancini.Palette, dc mancini.DrawContext, depth mancini.NeuDepth, weight mancini.Weight,
	cx, cy, radius float64, face color.NRGBA) {

	switch depth {
	case mancini.Raised:
		dc.SetColor(adjustBrightness(face, s.delta))
	case mancini.Flush:
		dc.SetColor(face)
	case mancini.Inset:
		dc.SetColor(adjustBrightness(face, -s.delta))
	}
	dc.DrawCircle(cx, cy, radius)
	dc.Fill()

	dc.SetColor(pal.Mid())
	dc.SetLineWidth(s.borderWidth)
	dc.DrawCircle(cx, cy, radius)
	dc.Stroke()
}

func (s *FlatStyle) Groove(pal mancini.Palette, dc mancini.DrawContext, x1, y, x2 float64) {
	dc.SetColor(pal.Mid())
	dc.SetLineWidth(1)
	dc.DrawLine(x1, y, x2, y)
	dc.Stroke()
}

func (s *FlatStyle) Pad(weight mancini.Weight) float64 {
	return 2
}

func (s *FlatStyle) TintOverlay(dc mancini.DrawContext, depth mancini.NeuDepth,
	x1, y1, x2, y2, r float64, face color.NRGBA) {
	dc.SetColor(color.NRGBA{face.R, face.G, face.B, 60})
	dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
	dc.Fill()
}
