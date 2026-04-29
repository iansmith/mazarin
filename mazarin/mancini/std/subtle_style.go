package std

import (
	"image/color"

	"mazzy/mazarin/mancini"
)

var _ mancini.SurfaceStyle = (*SubtleStyle)(nil)

// SubtleStyle implements [mancini.SurfaceStyle] with classic directional
// bevels: light from top-left, shadow on bottom-right. This style works
// well with both light and dark palettes.
//
// Raised: face brightened by delta, 1px Midlight top+left, 1px Mid bottom+right.
// Flush:  thin uniform edge strokes (dark + light).
// Inset:  face darkened by delta, 1px Mid top+left, 1px Midlight bottom+right.
type SubtleStyle struct {
	delta int // brightness adjustment (default 12)
}

// NewSubtleStyle creates a SubtleStyle. delta controls how much the face
// color is brightened (Raised) or darkened (Inset). Use 12 for a subtle
// effect, higher values for more contrast.
func NewSubtleStyle(delta int) *SubtleStyle {
	return &SubtleStyle{delta: delta}
}

// DrawBox renders a rounded rectangle with a directional bevel for the
// given [mancini.NeuDepth]. The face color is adjusted by delta for
// raised/inset depths.
func (s *SubtleStyle) DrawBox(pal mancini.Palette, dc mancini.DrawContext, depth mancini.NeuDepth, weight mancini.Weight,
	x1, y1, x2, y2, r float64, face color.NRGBA) {

	switch depth {
	case mancini.Raised:
		f := adjustBrightness(face, s.delta)
		dc.SetColor(f)
		dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
		dc.Fill()
		drawBevel(dc, x1, y1, x2, y2, r, pal.Midlight(), pal.Mid(), 1)
	case mancini.Flush:
		dc.SetColor(face)
		dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
		dc.Fill()
		drawUniformEdge(dc, x1, y1, x2, y2, r, pal.Mid(), 1)
	case mancini.Inset:
		f := adjustBrightness(face, -s.delta)
		dc.SetColor(f)
		dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
		dc.Fill()
		drawBevel(dc, x1, y1, x2, y2, r, pal.Mid(), pal.Midlight(), 1)
	}
}

// DrawCircle renders a filled circle with the face color brightness
// adjusted by depth.
func (s *SubtleStyle) DrawCircle(pal mancini.Palette, dc mancini.DrawContext, depth mancini.NeuDepth, weight mancini.Weight,
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
}

// Groove draws a thin two-line horizontal groove (Mid above Midlight)
// from x1 to x2 at height y.
func (s *SubtleStyle) Groove(pal mancini.Palette, dc mancini.DrawContext, x1, y, x2 float64) {
	dc.SetColor(pal.Mid())
	dc.SetLineWidth(1)
	dc.DrawLine(x1, y, x2, y)
	dc.Stroke()
	dc.SetColor(pal.Midlight())
	dc.DrawLine(x1, y+1, x2, y+1)
	dc.Stroke()
}

// Pad returns the recommended interior padding in pixels for the given
// surface weight.
func (s *SubtleStyle) Pad(weight mancini.Weight) float64 {
	if weight == mancini.HeavyWeight {
		return 4
	}
	return 2
}

// TintOverlay draws a translucent face-colored rounded rectangle on
// top of an existing surface for hover/focus highlighting.
func (s *SubtleStyle) TintOverlay(dc mancini.DrawContext, depth mancini.NeuDepth,
	x1, y1, x2, y2, r float64, face color.NRGBA) {
	dc.SetColor(color.NRGBA{face.R, face.G, face.B, 60})
	dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
	dc.Fill()
}

// ── Shared helpers for non-neumorphic styles ────────────────────────────

// adjustBrightness returns c with each RGB channel shifted by delta.
// Positive delta brightens, negative darkens. Channels are clamped to [0,255].
func adjustBrightness(c color.NRGBA, delta int) color.NRGBA {
	return color.NRGBA{
		R: clampChannel(int(c.R) + delta),
		G: clampChannel(int(c.G) + delta),
		B: clampChannel(int(c.B) + delta),
		A: c.A,
	}
}

func clampChannel(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// drawBevel draws a classic directional bevel: hiColor on top+left edges,
// loColor on bottom+right edges, with the given line width.
func drawBevel(dc mancini.DrawContext, x1, y1, x2, y2, r float64, hiColor, loColor color.NRGBA, lineW float64) {
	dc.SetLineWidth(lineW)
	hw := lineW / 2

	// Top-left highlight: top edge + left edge.
	dc.SetColor(hiColor)
	dc.MoveTo(x1+hw, y2-r)
	dc.LineTo(x1+hw, y1+r)
	if r > 0 {
		// Approximate 90° arc with quadratic Bezier through corner.
		dc.QuadraticTo(x1+hw, y1+hw, x1+r, y1+hw)
	}
	dc.LineTo(x2-r, y1+hw)
	dc.Stroke()

	// Bottom-right shadow: bottom edge + right edge.
	dc.SetColor(loColor)
	dc.MoveTo(x2-hw, y1+r)
	dc.LineTo(x2-hw, y2-r)
	if r > 0 {
		// Approximate 90° arc with quadratic Bezier through corner.
		dc.QuadraticTo(x2-hw, y2-hw, x2-r, y2-hw)
	}
	dc.LineTo(x1+r, y2-hw)
	dc.Stroke()
}

// drawUniformEdge draws a uniform edge on all sides. For r==0, uses four
// lines to avoid expensive stroke expansion on large rectangles.
func drawUniformEdge(dc mancini.DrawContext, x1, y1, x2, y2, r float64, c color.NRGBA, lineW float64) {
	dc.SetColor(c)
	dc.SetLineWidth(lineW)
	if r == 0 {
		hw := lineW / 2
		dc.DrawLine(x1+hw, y1+hw, x2-hw, y1+hw) // top
		dc.DrawLine(x2-hw, y1+hw, x2-hw, y2-hw) // right
		dc.DrawLine(x2-hw, y2-hw, x1+hw, y2-hw) // bottom
		dc.DrawLine(x1+hw, y2-hw, x1+hw, y1+hw) // left
		dc.Stroke()
	} else {
		dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
		dc.Stroke()
	}
}

