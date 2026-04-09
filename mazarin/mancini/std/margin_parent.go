package std

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// MarginParent is a single-child container that enforces horizontal and
// vertical margins around its child. Margins are set at construction time
// and maintained by constraints. The child's width = parent width - 2*hMargin,
// and the child's height = parent height - 2*vMargin.
//
// Width and Height can be value attributes (set by parent) or constrained.
type MarginParent struct {
	impl.Interactor
	impl.Parent

	HMargin int64 // horizontal margin in pixels (each side)
	VMargin int64 // vertical margin in pixels (each side)
}

// NewMarginParent creates a MarginParent. Width and Height are value
// attributes (outside-in, set by parent container).
func NewMarginParent(myName, parent string, hMargin, vMargin int64) *MarginParent {
	if myName == "" {
		myName = mancini.DefaultName("marginparent")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)

	mp := &MarginParent{
		HMargin: hMargin,
		VMargin: vMargin,
	}
	mp.Interactor.Initialize(mp, lh)
	mp.Parent.Initialize(true, &mp.Interactor)
	return mp
}

// NewMarginParentConstrained creates a MarginParent where Height is
// constrained to equal a source int64 attribute URI. Width is a value
// attribute (set by parent).
func NewMarginParentConstrained(myName, parent string, hMargin, vMargin int64,
	heightSourceURI string) *MarginParent {

	if myName == "" {
		myName = mancini.DefaultName("marginparent")
	}
	lh := mancini.NewLayoutAttributesBase(myName, parent)
	lh.Width = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth), 0)
	heightURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight)
	lh.Height = attr.ConstraintI64(heightURI, mancini.EqualI64(heightSourceURI))
	lh.InitBounds(myName)

	mp := &MarginParent{
		HMargin: hMargin,
		VMargin: vMargin,
	}
	mp.Interactor.Initialize(mp, lh)
	mp.Parent.Initialize(true, &mp.Interactor)
	return mp
}

// Draw implements mancini.NewDrawer. Positions the single child inside
// the margin area.
func (mp *MarginParent) Draw(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()
	if dc == nil {
		return
	}

	children := mp.GetChildren()
	if len(children) == 0 {
		return
	}

	child := children[0]
	childX := x + mp.HMargin
	childY := y + mp.VMargin
	childW := w - 2*mp.HMargin
	childH := h - 2*mp.VMargin
	if childW < 0 {
		childW = 0
	}
	if childH < 0 {
		childH = 0
	}

	// Publish child position and dimensions.
	if l, ok := child.(mancini.Layouter); ok {
		clh := l.GetLayout()
		if clh != nil {
			clh.X.Set(childX)
			clh.Y.Set(childY)
			if !clh.Width.IsConstraint() {
				clh.Width.Set(childW)
			}
			if !clh.Height.IsConstraint() {
				clh.Height.Set(childH)
			}
		}
	}

	// Propagate DC and draw.
	if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
		cs.SetDC(dc)
	}
	if d, ok := child.(mancini.NewDrawer); ok {
		d.Draw(child, childX, childY, childW, childH)
	}
}
