package std

import (
	"image/color"
	"math"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// NeuCircle is a single-child decorator that draws neumorphic circular
// shadows. It embeds impl.Decorator for inside-out sizing.
// The margin is symmetric on all sides (circle is square).
type NeuCircle struct {
	impl.Decorator

	Pal    mancini.Palette
	Depth  mancini.NeuDepth
	Params mancini.NeuParams
	Face   color.NRGBA
}

// NewNeuCircle creates a NeuCircle wired to the constraint system.
// The layout should be created with mancini.NewDecoratorLayout.
// Margin is computed from NeuParams (max shadow padding).
func NewNeuCircle(layout *mancini.LayoutHandles, pal mancini.Palette,
	depth mancini.NeuDepth, params mancini.NeuParams) *NeuCircle {

	margin := mancini.NeuMaxPad(params)
	n := &NeuCircle{
		Pal:    pal,
		Depth:  depth,
		Params: params,
	}
	n.Decorator.InitDecorator(n, layout, margin, margin, margin, margin)
	return n
}

// Decorate implements mancini.Decoratable — draws neumorphic circular
// shadows centered in the Decorator's bounds.
func (n *NeuCircle) Decorate(self mancini.Interactor) {
	dc := self.DC()
	if dc == nil {
		return
	}
	x, y := float64(self.X()), float64(self.Y())
	w, h := float64(self.W()), float64(self.H())

	cx := x + w/2
	cy := y + h/2
	rad := math.Min(w, h) / 2

	face := n.Face
	if face == (color.NRGBA{}) {
		face = n.Pal.Surface
	}
	mancini.NeuCircleWith(n.Pal, dc, n.Depth, cx, cy, rad, face, n.Params, nil)
}
