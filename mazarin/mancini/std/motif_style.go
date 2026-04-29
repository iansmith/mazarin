package std

import (
	"image/color"

	"mazzy/mazarin/mancini"
)

var _ mancini.SurfaceStyle = (*MotifStyle)(nil)

// MotifStyle implements [mancini.SurfaceStyle] with hard-edged square
// bevels inspired by the Motif/CDE look. No blur, no anti-aliasing on
// bevel edges. The radius parameter is ignored — all geometry is square.
//
// Raised: LightShadow top+left, DarkShadow bottom+right.
// Flush:  uniform DarkShadow border.
// Inset:  DarkShadow top+left, LightShadow bottom+right. Face shifts to Accent.
type MotifStyle struct {
	bevelWidth float64 // bevel line width (default 2)
}

// NewMotifStyle creates a MotifStyle. bevelWidth controls the thickness
// of the hard-edged bevel lines.
func NewMotifStyle(bevelWidth float64) *MotifStyle {
	return &MotifStyle{bevelWidth: bevelWidth}
}

// DrawBox renders a rectangular surface with hard-edged Motif bevels
// for the given [mancini.NeuDepth]. The radius parameter is ignored.
func (s *MotifStyle) DrawBox(pal mancini.Palette, dc mancini.DrawContext, depth mancini.NeuDepth, weight mancini.Weight,
	x1, y1, x2, y2, _ float64, face color.NRGBA) {

	bw := s.bevelWidth

	switch depth {
	case mancini.Raised:
		dc.SetColor(face)
		dc.FillRectangle(x1, y1, x2-x1, y2-y1)
		drawHardBevel(dc, x1, y1, x2, y2, bw, pal.LightShadow(), pal.DarkShadow())
	case mancini.Flush:
		dc.SetColor(face)
		dc.FillRectangle(x1, y1, x2-x1, y2-y1)
		drawUniformEdge(dc, x1, y1, x2, y2, 0, pal.DarkShadow(), bw)
	case mancini.Inset:
		// Motif arm color: pressed buttons show accent background.
		dc.SetColor(pal.Accent())
		dc.FillRectangle(x1, y1, x2-x1, y2-y1)
		drawHardBevel(dc, x1, y1, x2, y2, bw, pal.DarkShadow(), pal.LightShadow())
	}
}

// DrawCircle renders a filled circle with a uniform Motif-style border.
// Motif doesn't have a natural circular bevel; the depth only affects
// the fill color.
func (s *MotifStyle) DrawCircle(pal mancini.Palette, dc mancini.DrawContext, depth mancini.NeuDepth, weight mancini.Weight,
	cx, cy, radius float64, face color.NRGBA) {

	// Motif doesn't do circles naturally — render as a filled circle
	// with a uniform border.
	switch depth {
	case mancini.Raised:
		dc.SetColor(face)
	case mancini.Flush:
		dc.SetColor(face)
	case mancini.Inset:
		dc.SetColor(pal.Accent())
	}
	dc.DrawCircle(cx, cy, radius)
	dc.Fill()

	dc.SetColor(pal.DarkShadow())
	dc.SetLineWidth(s.bevelWidth)
	dc.DrawCircle(cx, cy, radius)
	dc.Stroke()
}

// Groove draws a horizontal Motif-style groove (dark line above light
// line) from x1 to x2 at height y.
func (s *MotifStyle) Groove(pal mancini.Palette, dc mancini.DrawContext, x1, y, x2 float64) {
	bw := s.bevelWidth / 2
	if bw < 1 {
		bw = 1
	}
	dc.SetColor(pal.DarkShadow())
	dc.SetLineWidth(bw)
	dc.DrawLine(x1, y, x2, y)
	dc.Stroke()
	dc.SetColor(pal.LightShadow())
	dc.DrawLine(x1, y+bw, x2, y+bw)
	dc.Stroke()
}

// Pad returns the recommended interior padding in pixels for surfaces
// of the given weight.
func (s *MotifStyle) Pad(weight mancini.Weight) float64 {
	return 3
}

// TintOverlay draws a translucent face-colored rectangle on top of an
// existing surface to apply a hover/focus tint.
func (s *MotifStyle) TintOverlay(dc mancini.DrawContext, depth mancini.NeuDepth,
	x1, y1, x2, y2, _ float64, face color.NRGBA) {
	dc.SetColor(color.NRGBA{face.R, face.G, face.B, 60})
	dc.FillRectangle(x1, y1, x2-x1, y2-y1)
}

// drawHardBevel draws a Motif-style hard-edged bevel: hiColor on top+left,
// loColor on bottom+right. No rounded corners, no anti-aliasing.
func drawHardBevel(dc mancini.DrawContext, x1, y1, x2, y2, bw float64, hiColor, loColor color.NRGBA) {
	// Top-left highlight (drawn as filled trapezoids for crisp edges).
	dc.SetColor(hiColor)
	// Top edge.
	dc.MoveTo(x1, y1)
	dc.LineTo(x2, y1)
	dc.LineTo(x2-bw, y1+bw)
	dc.LineTo(x1+bw, y1+bw)
	dc.ClosePath()
	dc.Fill()
	// Left edge.
	dc.MoveTo(x1, y1)
	dc.LineTo(x1+bw, y1+bw)
	dc.LineTo(x1+bw, y2-bw)
	dc.LineTo(x1, y2)
	dc.ClosePath()
	dc.Fill()

	// Bottom-right shadow.
	dc.SetColor(loColor)
	// Bottom edge.
	dc.MoveTo(x1, y2)
	dc.LineTo(x1+bw, y2-bw)
	dc.LineTo(x2-bw, y2-bw)
	dc.LineTo(x2, y2)
	dc.ClosePath()
	dc.Fill()
	// Right edge.
	dc.MoveTo(x2, y1)
	dc.LineTo(x2, y2)
	dc.LineTo(x2-bw, y2-bw)
	dc.LineTo(x2-bw, y1+bw)
	dc.ClosePath()
	dc.Fill()
}
