package std

import (
	"image/color"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// ColumnOutsideIn arranges children vertically with outside-in layout.
// Height is clamped between minH and maxH via ProgMinMaxColumnHeight;
// within that range it grows with the sum of children's heights + 1px spacing.
// During Draw, children that overflow the available height are hidden
// by setting their Visible handle to 0.
//
// Width is a constraint (ProgColumnWidth) — max of children's published widths.
type ColumnOutsideIn struct {
	impl.Interactor // X(), Y(), W(), H(), Visible(), DC(), GetLayout()
	impl.Parent     // GetChildren() via constraint network

	BgColor color.NRGBA
}

// NewColumnOutsideIn creates a ColumnOutsideIn wired to the constraint system.
// minH and maxH clamp the height constraint; within that range height equals
// sum(children heights) + (n-1) px spacing.
func NewColumnOutsideIn(myName, parent string, bgColor color.NRGBA, minH, maxH int64) *ColumnOutsideIn {
	if myName == "" {
		myName = mancini.DefaultName("col_oi")
	}

	lh := mancini.NewLayoutHandlesBase(myName, parent)

	// Min/max height value handles for the constraint program.
	minHURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutMinHeight)
	attr.ValueI64(minHURI, minH)
	maxHURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutMaxHeight)
	attr.ValueI64(maxHURI, maxH)

	// Height constraint: sum of children heights + 1px spacing, clamped to [minH, maxH].
	heightProg := mancini.BindStringsChildren(ProgMinmaxColumnHeight,
		"_myName_", myName, "_minH_", minHURI, "_maxH_", maxHURI)
	heightURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight)
	lh.Height = attr.ConstraintI64(heightURI, heightProg)

	// Width constraint: max of children widths.
	widthProg := mancini.BindStringsChildren(ProgColumnWidth,
		"_myName_", myName)
	widthURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth)
	lh.Width = attr.ConstraintI64(widthURI, widthProg)

	lh.InitBounds(myName)

	c := &ColumnOutsideIn{BgColor: bgColor}
	c.Interactor.Init(c, lh)
	c.Parent.InitParent(&c.Interactor)
	return c
}

// Draw implements mancini.NewDrawer. Fills background, lays out children
// top-to-bottom with 1px spacing, and hides any that overflow.
func (c *ColumnOutsideIn) Draw(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()

	// Fill background.
	dc.SetColor(c.BgColor)
	dc.FillRectangle(float64(x), float64(y), float64(w), float64(h))

	children := c.GetChildren()
	if len(children) == 0 {
		return
	}

	curY := y
	drawnCount := 0
	for _, child := range children {
		// Read child height from layout handles.
		childH := int64(20) // fallback
		if l, ok := child.(mancini.Layouter); ok {
			childH = int64(mancini.ChildHeight(l, 20))
		}

		// 1px spacing between children.
		if drawnCount > 0 {
			curY += 1
		}

		if curY+childH > y+h {
			// This child doesn't fit — hide it and all remaining.
			if l, ok := child.(mancini.Layouter); ok {
				lh := l.GetLayout()
				if lh != nil && lh.Visible != nil {
					lh.Visible.Set(false)
				}
			}
			continue
		}

		// Show this child.
		if l, ok := child.(mancini.Layouter); ok {
			lh := l.GetLayout()
			if lh != nil {
				if lh.Visible != nil {
					lh.Visible.Set(true)
				}
				lh.X.Set(x)
				lh.Y.Set(curY)
			}
		}

		// Propagate DC.
		if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
			cs.SetDC(dc)
		}

		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, x, curY, w, childH)
		}
		curY += childH
		drawnCount++
	}
}
