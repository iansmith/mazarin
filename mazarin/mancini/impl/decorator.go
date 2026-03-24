package impl

import (
	"image/color"

	"mazzy/mazarin/mancini"
)

// Decorator is a single-child parent interactor that draws decoration
// around its child. It uses inside-out sizing: the Decorator's Width and
// Height are determined by the child's size plus the decoration insets
// (set up via constraint programs in InitLayout, not here).
//
// Embedding types override Decorate to customize the visual decoration.
// The default draws a thick black box. NeuBox draws neumorphic shadows,
// NeuCircle draws circular neumorphic shadows.
//
// At init time, Decorator takes top/right/bottom/left insets that reserve
// space for the decoration. During Draw, it positions the child at
// (self.X+Left, self.Y+Top). The child's Width and Height are owned by
// the child itself (inside-out) — the Decorator never sets them.
//
// Decorator embeds Parent for GetChildren but does NOT use
// DrawChildren — it handles its single child directly in Draw.
type Decorator struct {
	Interactor
	Parent
	Top, Right, Bottom, Left int64
}

// InitDecorator wires the back-pointer, layout, and decoration insets.
// Constraint-based sizing (Width = child.Width + Left + Right, etc.)
// is set up separately in InitLayout on the concrete type.
func (d *Decorator) InitDecorator(owner any, layout *mancini.LayoutHandles, top, right, bottom, left int64) {
	d.Interactor.Init(owner.(mancini.Interactor), layout)
	d.Parent.InitParent(&d.Interactor)
	d.Top = top
	d.Right = right
	d.Bottom = bottom
	d.Left = left
}

// Draw implements mancini.NewDrawer.
//
// 1. Calls Decorate through virtual dispatch on the owner — the concrete
//    type's Decorate draws the visual decoration at the Decorator's full
//    bounds.
// 2. Positions the child by setting its layout X/Y to (x+Left, y+Top) and
//    passes computed child bounds to the child's Draw.
// 3. Propagates the DrawContext to the child and calls its Draw.
//
// Note: the child's Width and Height are NOT set here. They are owned by
// the child (inside-out sizing). The Decorator's own Width/Height come
// from constraint programs that read child.Width + Left + Right, etc.
func (d *Decorator) Draw(self mancini.Interactor, x, y, w, h int64) {
	// 1. Virtual dispatch for Decorate.
	if dec, ok := d.Interactor.Owner().(mancini.Decoratable); ok {
		dec.Decorate(self, x, y, w, h)
	}

	// 2. Compute child bounds inside the decoration insets.
	children := d.GetChildren()
	if len(children) == 0 {
		return
	}
	child := children[0]
	childX := x + d.Left
	childY := y + d.Top
	childW := w - d.Left - d.Right
	childH := h - d.Top - d.Bottom

	// Publish child position to layout handles for constraint visibility.
	if l, ok := child.(mancini.Layouter); ok {
		lh := l.GetLayout()
		if lh != nil {
			lh.X.Set(childX)
			lh.Y.Set(childY)
		}
	}

	// 3. Propagate DrawContext and draw child.
	if cs, ok := child.(dcSetter); ok {
		cs.SetDC(self.DC())
	}
	if drawer, ok := child.(mancini.NewDrawer); ok {
		drawer.Draw(child, childX, childY, childW, childH)
	}
}

// Decorate draws the default thick box decoration.
// Embedding types (NeuBox, NeuCircle) override this method.
func (d *Decorator) Decorate(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()
	if dc == nil {
		return
	}
	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)
	dc.SetColor(color.NRGBA{0, 0, 0, 255})
	dc.SetLineWidth(3)
	dc.DrawRectangle(fx+1.5, fy+1.5, fw-3, fh-3)
	dc.Stroke()
}
