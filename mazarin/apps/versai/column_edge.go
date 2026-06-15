package main

import (
	"image"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
	"mazzy/mazarin/mancini/std"
)

// ColumnEdgeToEdge lays out N children vertically, edge-to-edge,
// inside a ScrollerVertical. Each child's width is constrained to the
// scroller's width. Children are stacked top-to-bottom with 1px
// gaps: child[i].Y = child[i-1].Y + child[i-1].Height + 1.
//
// The ScrollerVertical handles all Scroller/Scrollbar constraint
// wiring internally.
type ColumnEdgeToEdge struct {
	impl.Interactor
	impl.Parent

	SV  *std.ScrollerVertical
	pal mancini.Palette
}

// NewColumnEdgeToEdge creates a ColumnEdgeToEdge with a ScrollerVertical
// inside it. Width and Height are constrained to the parent's dimensions.
//
// trackWidth is the pixel width reserved for the scrollbar.
func NewColumnEdgeToEdge(myName, parent string,
	theme mancini.Theme, pal mancini.Palette,
	parentWidthURI, parentHeightURI string,
	trackWidth int64,
	sbStyle std.ScrollbarStyle) *ColumnEdgeToEdge {

	if myName == "" {
		myName = mancini.DefaultName("coledge")
	}

	lh := mancini.NewLayoutAttributesBase(myName, parent)
	lh.Width = attr.ConstraintI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth),
		mancini.EqualI64(parentWidthURI))
	lh.Height = attr.ConstraintI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight),
		mancini.EqualI64(parentHeightURI))
	lh.InitBounds(myName)

	c := &ColumnEdgeToEdge{pal: pal}
	c.Interactor.Initialize(c, lh)
	c.Parent.Initialize(true, &c.Interactor)

	// Create ScrollerVertical as child.
	c.SV = std.NewScrollerVertical(myName+"_sv", myName,
		theme, pal,
		lh.Width.URI(), lh.Height.URI(),
		trackWidth, sbStyle)

	return c
}

// ScrollerName returns the constraint-system name of the Scroller,
// which children should use as their parent name.
func (c *ColumnEdgeToEdge) ScrollerName() string {
	return c.SV.ScrollerName()
}

// ScrollerWidthURI returns the URI of the Scroller's Width attribute.
// Children should constrain their width to this.
func (c *ColumnEdgeToEdge) ScrollerWidthURI() string {
	return c.SV.ScrollerWidthURI()
}

// LayoutChildren wires each Scroller child's Y as a live constraint so
// that height changes propagate automatically through the stack:
//
//	child[0].Y = 0  (value)
//	child[i].Y = child[i-1].Y + child[i-1].Height + 1  (constraint)
//	VirtualHeight  = last.Y + last.Height               (constraint)
//
// This replaces the old one-shot imperative assignment. Call this after
// adding children and again after removing one (DeleteUnitEntry).
// MaxScrollY and the scrollbar track VirtualHeight through existing
// constraint wiring in ScrollerVertical — no further updates needed.
//
// This method OWNS the Scroller's VirtualHeight (a live constraint while there
// are children, a plain 0 value when empty). Callers must not also drive it
// imperatively via Scroller.SetVirtualSize — Set panics on a constraint.
func (c *ColumnEdgeToEdge) LayoutChildren() {
	children := c.SV.Scroller.GetChildren()
	if len(children) == 0 {
		// No children: snap VirtualHeight back to a plain 0 value so the
		// scroller's fast path doesn't draw phantoms.
		attr.ResetToValue(c.SV.Scroller.VirtualHeight, int64(0))
		return
	}

	// child[0].X is always a plain value 0 — this layout never makes X a
	// constraint, so a bare Set is correct. child[0].Y is value 0 too, but may
	// currently be a constraint from a prior layout (a child that used to be at
	// index ≥1), so demote it; ResetToValue keeps the cheap Set when it is
	// already a value.
	if first, ok := children[0].(mancini.Layouter); ok {
		if flh := first.GetLayout(); flh != nil {
			flh.X.Set(0)
			attr.ResetToValue(flh.Y, int64(0))
		}
	}

	// For each subsequent child, wire Y as a live constraint derived from
	// the previous child's Y and Height. SwapToConstraint handles both the
	// first-time (value → constraint) and re-layout (old constraint →
	// new constraint) cases.
	for i := 1; i < len(children); i++ {
		prevL, prevOK := children[i-1].(mancini.Layouter)
		curL, curOK := children[i].(mancini.Layouter)
		if !prevOK || !curOK {
			continue
		}
		prevLH := prevL.GetLayout()
		curLH := curL.GetLayout()
		if prevLH == nil || curLH == nil {
			continue
		}
		curLH.X.Set(0)
		attr.SwapToConstraint(curLH.Y,
			mancini.ChildAfterI64(prevLH.Y.URI(), prevLH.Height.URI()))
	}

	// VirtualHeight = last.Y + last.Height — stays live as the last child
	// resizes (OnHeightChanged fires Height.Set, constraint re-evaluates,
	// MaxScrollY and the scrollbar Max update automatically).
	last := children[len(children)-1]
	if l, ok := last.(mancini.Layouter); ok {
		if lh := l.GetLayout(); lh != nil {
			attr.SwapToConstraint(c.SV.Scroller.VirtualHeight,
				mancini.AddI64(lh.Y.URI(), lh.Height.URI()))
		}
	}
}

// Draw implements mancini.NewDrawer. Delegates to DrawChildren.
func (c *ColumnEdgeToEdge) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	if !self.Visible() {
		return
	}
	if !c.Damaged(damage) {
		return
	}
	c.Parent.DrawChildren(self, x, y, w, h, damage)
	c.SnapshotDamage()
}
