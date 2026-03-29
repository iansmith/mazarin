package std

import (
	"image/color"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// NeuBox is a single-child decorator that draws neumorphic box shadows.
// It embeds impl.Decorator for inside-out sizing and TRBL insets.
// Decorate overrides Decorator's thick-box default with neumorphic rendering.
type NeuBox struct {
	impl.Decorator

	Pal    mancini.Palette
	Depth  mancini.NeuDepth
	Params mancini.NeuParams
	Face   color.NRGBA
	Radius float64
}

// NewNeuBox creates a NeuBox wired to the constraint system.
// The layout should be created with mancini.NewDecoratorLayout.
// Margin is computed from NeuParams (max shadow padding).
func NewNeuBox(layout *mancini.LayoutAttributes, pal mancini.Palette,
	depth mancini.NeuDepth, params mancini.NeuParams, radius float64) *NeuBox {

	margin := mancini.NeuMaxPad(params)
	n := &NeuBox{
		Pal:    pal,
		Depth:  depth,
		Params: params,
		Radius: radius,
	}
	n.Decorator.InitDecorator(n, layout, margin, margin, margin, margin)
	return n
}

// Decorate implements mancini.Decoratable — draws neumorphic box shadows
// at the given bounds.
func (n *NeuBox) Decorate(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()
	if dc == nil {
		return
	}
	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	face := n.Face
	if face == (color.NRGBA{}) {
		face = n.Pal.Surface()
	}
	NeuBoxWith(n.Pal, dc, n.Depth, fx, fy, fx+fw, fy+fh, n.Radius, face, &n.Params, nil)
}
