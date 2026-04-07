package std

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// ColumnPercentage arranges children vertically, giving each child a
// fraction of the parent's height according to a percentage sequence.
// Children are centered horizontally.
//
// Percentage normalization:
//   - If percentages sum to more than 100, they are proportionally reduced.
//     Any percentage that reaches zero in this process is discarded.
//   - If percentages sum to less than 100, the remainder is added to the
//     last percentage.
//
// Child count vs percentage count:
//   - Extra children (more children than percentages) are hidden via
//     their Visible layout attribute.
//   - Fewer children than percentages: all remaining percentage values
//     are assigned to the last child.
type ColumnPercentage struct {
	impl.Interactor
	impl.Parent

	Pal      mancini.Palette
	HPadding int64     // horizontal padding applied at left and right edges
	Percents []float64 // percentage of height for each child position
}

// NewColumnPercentage creates a ColumnPercentage with the given percentage
// sequence. Percentages are normalized on construction (see type doc).
// Width and height are set as value attributes from the given dimensions.
func NewColumnPercentage(myName, parent string, pal mancini.Palette,
	w, h int64, percents []float64) *ColumnPercentage {

	if myName == "" {
		myName = mancini.DefaultName("colpct")
	}

	lh := mancini.NewLayoutAttributesBase(myName, parent)
	lh.Width = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth), w)
	lh.Height = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight), h)
	lh.InitBounds(myName)

	cp := &ColumnPercentage{
		Pal:      pal,
		Percents: normalizePercents(percents),
	}
	cp.Interactor.Initialize(cp, lh)
	cp.Parent.Initialize(true, &cp.Interactor)
	return cp
}

// Draw positions children vertically according to the percentage sequence.
// Each child is centered horizontally and given a height proportional to
// its percentage of the parent's total height.
func (cp *ColumnPercentage) Draw(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()
	if dc == nil {
		return
	}

	children := cp.GetChildren()
	pcts := assignPercents(cp.Percents, len(children))
	curY := y

	// Inset by horizontal padding.
	innerX := x + cp.HPadding
	innerW := w - 2*cp.HPadding
	if innerW < 0 {
		innerW = 0
	}

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

		// Ensure visible (may have been hidden on a previous draw with
		// a different child count).
		if l, ok := child.(mancini.Layouter); ok {
			lh := l.GetLayout()
			if lh != nil && lh.Visible != nil {
				lh.Visible.Set(true)
			}
		}

		pct := pcts[i]
		childH := int64(float64(h) * pct / 100.0)

		childL, hasLayout := child.(mancini.Layouter)
		childW := innerW
		if hasLayout {
			childW = int64(mancini.ChildWidth(childL, float64(innerW)))
		}

		// Children with constraint-computed width get the full
		// available width in Draw so they can distribute excess
		// space internally (e.g. Row grows inter-child spacing).
		// Value-width children are centered horizontally.
		drawW := childW
		childX := innerX + (innerW-childW)/2
		if hasLayout {
			clh := childL.GetLayout()
			if clh != nil {
				if clh.Width.IsConstraint() {
					drawW = innerW
					childX = innerX
				}
				clh.X.Set(childX)
				clh.Y.Set(curY)
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
			d.Draw(child, childX, curY, drawW, childH)
		}

		curY += childH
	}
}
