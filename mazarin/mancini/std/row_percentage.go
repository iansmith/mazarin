package std

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// RowPercentage arranges children horizontally, giving each child a
// fraction of the parent's width according to a percentage sequence.
// Children are centered vertically.
//
// Percentage normalization and child-count handling follow the same
// rules as [ColumnPercentage] — see its documentation.
type RowPercentage struct {
	impl.Interactor
	impl.Parent

	Pal      mancini.Palette
	Percents []float64 // percentage of width for each child position
}

// NewRowPercentage creates a RowPercentage with the given percentage
// sequence. Percentages are normalized on construction.
// Width and height are set as value attributes from the given dimensions.
func NewRowPercentage(myName, parent string, pal mancini.Palette,
	w, h int64, percents []float64) *RowPercentage {

	if myName == "" {
		myName = mancini.DefaultName("rowpct")
	}

	lh := mancini.NewLayoutAttributesBase(myName, parent)
	lh.Width = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth), w)
	lh.Height = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight), h)
	lh.InitBounds(myName)

	rp := &RowPercentage{
		Pal:      pal,
		Percents: normalizePercents(percents),
	}
	rp.Interactor.Initialize(rp, lh)
	rp.Parent.Initialize(true, &rp.Interactor)
	return rp
}

// Draw positions children horizontally according to the percentage sequence.
// Each child is centered vertically and given a width proportional to
// its percentage of the parent's total width.
func (rp *RowPercentage) Draw(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()
	if dc == nil {
		return
	}

	children := rp.GetChildren()
	pcts := assignPercents(rp.Percents, len(children))
	curX := x

	for i, child := range children {
		if i >= len(pcts) {
			// Extra child — hide it.
			if l, ok := child.(mancini.Layouter); ok {
				lh := l.GetLayout()
				if lh != nil && lh.Visible != nil {
					lh.Visible.Set(false)
				}
			}
			continue
		}

		// Ensure visible.
		if l, ok := child.(mancini.Layouter); ok {
			lh := l.GetLayout()
			if lh != nil && lh.Visible != nil {
				lh.Visible.Set(true)
			}
		}

		pct := pcts[i]
		childW := int64(float64(w) * pct / 100.0)

		childL, hasLayout := child.(mancini.Layouter)
		childH := h
		if hasLayout {
			childH = int64(mancini.ChildHeight(childL, float64(h)))
		}

		// Center vertically.
		childY := y + (h-childH)/2

		// Publish position and dimensions for pick/hit-testing.
		if hasLayout {
			clh := childL.GetLayout()
			if clh != nil {
				clh.X.Set(curX)
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
			d.Draw(child, curX, childY, childW, childH)
		}

		curX += childW
	}
}
