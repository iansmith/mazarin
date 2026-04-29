package std

import (
	"image/color"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// NeuBox is a single-child [impl.Decorator] that draws rectangular
// neumorphic shadows around its child. It overrides
// [impl.Decorator.Decorate] to render shadows via [NeuBoxWith] instead
// of the default thick black box.
//
// NeuBox computes its margin from [mancini.NeuMaxPad] to ensure
// shadows have room to render without clipping. The child is positioned
// inside the margin at (x+Left, y+Top).
//
// See also [NeuCircle] for circular neumorphic decoration, and
// [AppWindow] / [FreeFloatingWindow] for window-level decorators that
// add title bars.
type NeuBox struct {
	impl.Decorator

	// Pal is the [mancini.Palette] used for shadow and surface colors.
	Pal mancini.Palette
	// Depth controls whether the box appears Raised, Flush, or Inset.
	Depth mancini.NeuDepth
	// Weight selects the shadow strength (Light/Medium/Heavy).
	Weight mancini.Weight
	// Style supplies the [mancini.SurfaceStyle] that actually renders the box.
	Style mancini.SurfaceStyle
	// Face overrides the surface fill color when non-zero.
	Face color.NRGBA
	// Radius is the corner radius in pixels.
	Radius float64

	// Params is retained for backward compatibility with callers that
	// construct a NeuBox with explicit NeuParams (via NewNeuBox).
	// NewNeuBoxStyled does not use it.
	Params mancini.NeuParams
}

// NewNeuBox creates a NeuBox wired to the constraint system.
// The layout should be created with mancini.NewDecoratorLayout.
// Margin is computed from NeuParams (max shadow padding).
//
// Deprecated: prefer [NewNeuBoxStyled] which takes a Theme.
func NewNeuBox(layout *mancini.LayoutAttributes, pal mancini.Palette,
	depth mancini.NeuDepth, params mancini.NeuParams, radius float64) *NeuBox {

	margin := mancini.NeuMaxPad(params)
	n := &NeuBox{
		Pal:    pal,
		Depth:  depth,
		Weight: mancini.LightWeight,
		Style:  NewNeumorphicStyle(&params, &params),
		Params: params,
		Radius: radius,
	}
	n.Decorator.Initialize(n, layout, margin, margin, margin, margin)
	return n
}

// NewNeuBoxStyled creates a NeuBox that delegates rendering to the Theme's
// [mancini.SurfaceStyle]. Margin is derived from style.Pad(weight).
func NewNeuBoxStyled(layout *mancini.LayoutAttributes, theme mancini.Theme,
	depth mancini.NeuDepth, weight mancini.Weight, radius float64) *NeuBox {

	margin := int64(theme.Style().Pad(weight))
	n := &NeuBox{
		Pal:    theme.Palette(),
		Depth:  depth,
		Weight: weight,
		Style:  theme.Style(),
		Radius: radius,
	}
	n.Decorator.Initialize(n, layout, margin, margin, margin, margin)
	return n
}

// Decorate implements mancini.Decoratable — draws the box at the given bounds.
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
	if n.Style != nil {
		n.Style.DrawBox(n.Pal, dc, n.Depth, n.Weight,
			fx, fy, fx+fw, fy+fh, n.Radius, face)
	} else {
		NeuBoxWith(n.Pal, dc, n.Depth, fx, fy, fx+fw, fy+fh, n.Radius, face, &n.Params)
	}
}
