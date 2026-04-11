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

// LayoutChildren assigns Y positions to each child of the Scroller
// so they stack vertically with 1px gaps. Call this after all children
// have been added and before the first Draw. Sets VirtualHeight on
// the Scroller, which flows to MaxScrollY → Scrollbar.Max.
func (c *ColumnEdgeToEdge) LayoutChildren() {
	children := c.SV.Scroller.GetChildren()
	if len(children) == 0 {
		return
	}
	var y int64
	for i, child := range children {
		l, ok := child.(mancini.Layouter)
		if !ok {
			continue
		}
		clh := l.GetLayout()
		if clh == nil {
			continue
		}
		if i > 0 {
			prev := children[i-1]
			if pl, ok := prev.(mancini.Layouter); ok {
				if plh := pl.GetLayout(); plh != nil {
					y = plh.Y.Get() + plh.Height.Get() + 1
				}
			}
		}
		clh.X.Set(0)
		clh.Y.Set(y)
	}

	// Set virtual height to total content height.
	last := children[len(children)-1]
	if l, ok := last.(mancini.Layouter); ok {
		if lh := l.GetLayout(); lh != nil {
			totalH := lh.Y.Get() + lh.Height.Get()
			c.SV.Scroller.VirtualHeight.Set(totalH)
		}
	}
}

// Draw implements mancini.NewDrawer. Delegates to the ScrollerVertical.
func (c *ColumnEdgeToEdge) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}

	c.SV.SetDC(dc)
	c.SV.Draw(c.SV, x, y, w, h, damage)
}
