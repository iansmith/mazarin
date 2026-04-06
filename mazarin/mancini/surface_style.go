package mancini

import "image/color"

// SurfaceStyle abstracts the visual treatment applied to interactor
// surfaces. Implementations include neumorphic shadows, classic
// directional bevels, and flat color fills.
//
// All interactors in [mazzy/mazarin/mancini/std] call SurfaceStyle
// methods (via [Theme.Style]) instead of directly invoking neumorphic
// rendering functions. This allows the entire desktop to switch visual
// styles by changing the Theme's SurfaceStyle.
type SurfaceStyle interface {
	// DrawBox renders a rounded rectangle at the given depth and weight.
	// face is the fill color; r is the corner radius.
	DrawBox(pal Palette, dc DrawContext, depth NeuDepth, weight Weight,
		x1, y1, x2, y2, r float64, face color.NRGBA)

	// DrawCircle renders a circle at the given depth and weight.
	// face is the fill color.
	DrawCircle(pal Palette, dc DrawContext, depth NeuDepth, weight Weight,
		cx, cy, radius float64, face color.NRGBA)

	// Groove renders a thin horizontal inset separator line from x1 to x2
	// at vertical position y.
	Groove(pal Palette, dc DrawContext, x1, y, x2 float64)

	// Pad returns the padding (in pixels) that this style's shadows or
	// bevels consume around a surface of the given weight.
	Pad(weight Weight) float64

	// TintOverlay draws a semi-transparent tinted overlay on top of an
	// already-rendered surface. Used for hover/focus highlights and
	// disabled dimming.
	TintOverlay(dc DrawContext, depth NeuDepth,
		x1, y1, x2, y2, r float64, face color.NRGBA)
}
