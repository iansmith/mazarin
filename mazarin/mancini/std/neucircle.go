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

// NewNeuCircleNamed creates a NeuCircle with inside-out sizing constraints.
// Builds the layout internally from name and parent strings.
func NewNeuCircleNamed(myName, parent string, pal mancini.Palette,
	depth mancini.NeuDepth, params mancini.NeuParams) *NeuCircle {

	margin := mancini.NeuMaxPad(params)
	layout := mancini.NewDecoratorLayoutByParentName(myName, parent, margin, margin, 300)
	return NewNeuCircle(layout, pal, depth, params)
}

// Decorate implements mancini.Decoratable — draws neumorphic circular
// shadows centered in the given bounds.
func (n *NeuCircle) Decorate(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()
	if dc == nil {
		return
	}
	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	cx := fx + fw/2
	cy := fy + fh/2
	rad := math.Min(fw, fh) / 2

	face := n.Face
	if face == (color.NRGBA{}) {
		face = n.Pal.Surface
	}
	NeuCircleWith(n.Pal, dc, n.Depth, cx, cy, rad, face, n.Params, nil)
}
