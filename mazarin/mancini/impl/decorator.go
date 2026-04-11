package impl

import (
	"image"
	"image/color"

	"mazzy/mazarin/mancini"
)

// Decorator is a single-child parent interactor that draws visual
// decoration (shadows, title bars, etc.) around its child. It uses
// inside-out sizing: the Decorator's Width and Height are determined
// by the child's size plus the decoration insets (set up via constraint
// programs, not in Decorator itself).
//
// # Decoration Customization
//
// The decoration is drawn by the [mancini.Decoratable.Decorate] method.
// The default Decorator.Decorate draws a thick black box. Concrete
// types override it:
//
//   - [std.NeuBox] — rectangular neumorphic shadows via [std.NeuBoxWith]
//   - [std.NeuCircle] — circular neumorphic shadows via [std.NeuCircleWith]
//   - [std.AppWindow] — neumorphic shadows plus a title bar
//   - [std.FreeFloatingWindow] — neumorphic shadows, title, and groove
//
// # Inside-Out Sizing
//
// The child owns its Width and Height. The Decorator's dimensions come
// from constraint programs that add the insets:
// Decorator.Width = child.Width + Left + Right, etc.
//
// # Draw Sequence
//
// [Decorator.Draw] proceeds in three steps:
//
//  1. [DecorateIfNeeded] — calls Decorate via virtual dispatch if the
//     decorator's or child's BoundsHash has changed since the last frame.
//  2. Positions the child at (x+Left, y+Top).
//  3. Propagates [mancini.DrawContext] and calls the child's Draw.
//
// Decorator embeds [Parent] for GetChildren but does NOT use
// DrawChildren — it handles its single child directly in Draw.
//
// # Initialization
//
// Concrete types call [Decorator.Initialize], which internally calls
// [Interactor.Initialize] and [Parent.Initialize]:
//
//	n.Decorator.Initialize(n, layout, top, right, bottom, left)
type Decorator struct {
	Interactor
	Parent
	Top, Right, Bottom, Left int64
	lastOwnHash   int64
	lastChildHash int64
	hasDecorated  bool
}

// Initialize wires the backpointer, layout, and decoration insets.
// It calls [Interactor.Initialize] (registering in the global registry)
// and [Parent.Initialize] internally. The owner parameter must be the
// outermost concrete type (the backpointer).
//
// Constraint-based sizing (Width = child.Width + Left + Right, etc.)
// is set up separately by the concrete type's constructor via
// [mancini.NewDecoratorLayout] or [mancini.NewDecoratorLayoutByParentName].
func (d *Decorator) Initialize(owner any, layout *mancini.LayoutAttributes, top, right, bottom, left int64) {
	d.Interactor.Initialize(owner.(mancini.Interactor), layout)
	d.Parent.Initialize(true, &d.Interactor)
	d.Top = top
	d.Right = right
	d.Bottom = bottom
	d.Left = left
}

// DecorateIfNeeded checks whether the decoration needs to be redrawn by
// comparing the decorator's own BoundsHash and its child's BoundsHash
// against saved values. If either has changed (or this is the first frame),
// it calls Decorate via virtual dispatch and updates the saved hashes.
// If neither has changed, Decorate is skipped entirely — the previous
// frame's pixels are still in the framebuffer.
func (d *Decorator) DecorateIfNeeded(self mancini.Interactor, x, y, w, h int64) {
	ownHash := int64(0)
	if l, ok := self.(mancini.Layouter); ok {
		if lh := l.GetLayout(); lh != nil && lh.BoundsHash != nil {
			ownHash = lh.BoundsHash.Get()
		}
	}

	childHash := int64(0)
	children := d.GetChildren()
	if len(children) > 0 {
		if l, ok := children[0].(mancini.Layouter); ok {
			if lh := l.GetLayout(); lh != nil && lh.BoundsHash != nil {
				childHash = lh.BoundsHash.Get()
			}
		}
	}

	if d.hasDecorated && ownHash == d.lastOwnHash && childHash == d.lastChildHash {
		return
	}

	if dec, ok := d.Interactor.Owner().(mancini.Decoratable); ok {
		dec.Decorate(self, x, y, w, h)
	}
	d.lastOwnHash = ownHash
	d.lastChildHash = childHash
	d.hasDecorated = true
}

// Draw implements mancini.NewDrawer.
//
// 1. Calls DecorateIfNeeded — skips Decorate when the decorator's and
//    child's BoundsHash are unchanged from the previous frame.
// 2. Positions the child by setting its layout X/Y to (x+Left, y+Top) and
//    passes computed child bounds to the child's Draw.
// 3. Propagates the DrawContext to the child and calls its Draw.
//
// Note: the child's Width and Height are NOT set here. They are owned by
// the child (inside-out sizing). The Decorator's own Width/Height come
// from constraint programs that read child.Width + Left + Right, etc.
func (d *Decorator) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	// 1. Skip decoration if bounds unchanged.
	d.DecorateIfNeeded(self, x, y, w, h)

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

	// Publish child position and dimensions for pick/hit-testing.
	if l, ok := child.(mancini.Layouter); ok {
		lh := l.GetLayout()
		if lh != nil {
			lh.X.Set(childX)
			lh.Y.Set(childY)
			if !lh.Width.IsConstraint() {
				lh.Width.Set(childW)
			}
			if !lh.Height.IsConstraint() {
				lh.Height.Set(childH)
			}
		}
	}

	// 3. Propagate DrawContext and draw child.
	if cs, ok := child.(dcSetter); ok {
		cs.SetDC(self.DC())
	}
	if drawer, ok := child.(mancini.NewDrawer); ok {
		drawer.Draw(child, childX, childY, childW, childH, damage)
	}
}

// Decorate draws the default thick box decoration. Concrete types
// override this method to customize the visual decoration:
//
//   - [std.NeuBox] — rectangular neumorphic shadows
//   - [std.NeuCircle] — circular neumorphic shadows with bevel ring
//   - [std.AppWindow] — neumorphic shadows + title bar
//   - [std.FreeFloatingWindow] — neumorphic shadows + title + groove
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
