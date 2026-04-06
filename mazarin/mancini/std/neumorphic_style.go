package std

import (
	"image"
	"image/color"

	"mazzy/mazarin/mancini"
)

var _ mancini.SurfaceStyle = (*NeumorphicStyle)(nil)

// NeumorphicStyle implements [mancini.SurfaceStyle] by delegating to the
// existing neumorphic rendering functions ([NeuBoxWith], [NeuCircleWith],
// [NeuGroove]). It holds two sets of [mancini.NeuParams] — heavy for
// window decorations, light for controls — and selects between them
// based on the [mancini.Weight] parameter.
type NeumorphicStyle struct {
	heavy *mancini.NeuParams
	light *mancini.NeuParams
}

// NewNeumorphicStyle creates a NeumorphicStyle from heavy (window-weight)
// and light (control-weight) parameter sets. Either may be nil to disable
// neumorphic rendering for that weight class.
func NewNeumorphicStyle(heavy, light *mancini.NeuParams) *NeumorphicStyle {
	return &NeumorphicStyle{heavy: heavy, light: light}
}

func (s *NeumorphicStyle) params(w mancini.Weight) *mancini.NeuParams {
	if w == mancini.HeavyWeight {
		return s.heavy
	}
	return s.light
}

func (s *NeumorphicStyle) DrawBox(pal mancini.Palette, dc mancini.DrawContext, depth mancini.NeuDepth, weight mancini.Weight,
	x1, y1, x2, y2, r float64, face color.NRGBA) {
	NeuBoxWith(pal, dc, depth, x1, y1, x2, y2, r, face, s.params(weight))
}

func (s *NeumorphicStyle) DrawCircle(pal mancini.Palette, dc mancini.DrawContext, depth mancini.NeuDepth, weight mancini.Weight,
	cx, cy, radius float64, face color.NRGBA) {
	NeuCircleWith(pal, dc, depth, cx, cy, radius, face, s.params(weight))
}

func (s *NeumorphicStyle) Groove(pal mancini.Palette, dc mancini.DrawContext, x1, y, x2 float64) {
	NeuGroove(pal, dc, x1, y, x2)
}

func (s *NeumorphicStyle) Pad(weight mancini.Weight) float64 {
	p := s.params(weight)
	if p == nil {
		return 0
	}
	return float64(mancini.NeuMaxPad(*p))
}

func (s *NeumorphicStyle) TintOverlay(dc mancini.DrawContext, _ mancini.NeuDepth,
	x1, y1, x2, y2, r float64, face color.NRGBA) {
	canvas := dc.Image().(*image.RGBA)
	applyTintOverlay(dc, canvas, x1, y1, x2, y2, r, face)
}
