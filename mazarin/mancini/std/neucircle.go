package std

import (
	"image"
	"image/color"
	"math"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// neuCircleBevel is the pixel inset between the shadow radius and the face
// fill radius. This creates a visible ring where the neumorphic shadow
// gradient shows through, giving the raised/inset 3D effect.
const neuCircleBevel = 7

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

// Draw overrides Decorator.Draw. The child receives bounds inset by the
// bevel amount so the neumorphic shadow ring between the face edge and
// the shadow circle remains visible. The full decorator margins are for
// shadow overflow; the bevel is the visible 3D ring.
func (n *NeuCircle) Draw(self mancini.Interactor, x, y, w, h int64) {
	n.Decorator.DecorateIfNeeded(self, x, y, w, h)

	children := n.GetChildren()
	if len(children) == 0 {
		return
	}
	child := children[0]

	bevel := int64(neuCircleBevel)
	childX := x + bevel
	childY := y + bevel
	childW := w - 2*bevel
	childH := h - 2*bevel

	if l, ok := child.(mancini.Layouter); ok {
		lh := l.GetLayout()
		if lh != nil {
			lh.X.Set(childX)
			lh.Y.Set(childY)
		}
	}
	if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
		cs.SetDC(self.DC())
	}
	if drawer, ok := child.(mancini.NewDrawer); ok {
		drawer.Draw(child, childX, childY, childW, childH)
	}
}

// Decorate implements mancini.Decoratable — draws neumorphic circular
// shadows centered in the given bounds. Shadows are drawn at the full
// decorator radius; the face fill is drawn at radius - bevel so the
// shadow ring is visible between face edge and shadow fringe.
func (n *NeuCircle) Decorate(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()
	if dc == nil {
		return
	}
	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	cx := fx + fw/2
	cy := fy + fh/2
	shadowRad := math.Min(fw, fh) / 2
	faceRad := shadowRad - neuCircleBevel

	face := n.Face
	if face == (color.NRGBA{}) {
		face = n.Pal.Surface
	}

	switch n.Depth {
	case mancini.Raised:
		neuCircleRaisedShadows(n.Pal, dc, cx, cy, shadowRad, n.Params.Raised)
		dc.SetColor(face)
		dc.DrawCircle(cx, cy, faceRad)
		dc.Fill()
	case mancini.Flush:
		neuCircleFlush(n.Pal, dc, cx, cy, shadowRad, face, n.Params.Flush)
	case mancini.Inset:
		neuCircleInset(n.Pal, dc, cx, cy, shadowRad, face, n.Params.Inset)
	}

	if face != n.Pal.Surface && n.Depth != mancini.Raised {
		canvas := dc.Image().(*image.RGBA)
		applyCircleTintOverlay(n.Pal, canvas, cx, cy, faceRad, face)
	}
}
