package std

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// RowFillLastChild arranges children horizontally like [Row], but the
// last visible child receives all remaining width after the other
// children and spacing are subtracted. The row's own width is set
// externally (typically via a constraint), not computed from children.
//
// After construction, the caller must:
//  1. Set GetLayout().Width (e.g. via attr.ConstraintI64)
//  2. Call FinishLayout() to create Bounds and LastChildWidth constraints
//
// Children declare this interactor as their parent by naming it in
// their layout Parent field and are discovered at draw time via the
// constraint network.
type RowFillLastChild struct {
	impl.Interactor
	impl.Parent

	Pal     mancini.Palette
	HMargin int64

	spacingURI string
	hMarginURI string
	myName     string
}

// NewRowFillLastChild creates a RowFillLastChild. The row's width must
// be set by the caller on GetLayout().Width before calling FinishLayout().
func NewRowFillLastChild(myName, parent string, pal mancini.Palette, spacing int64) *RowFillLastChild {
	hMargin := int64(1)

	if myName == "" {
		myName = mancini.DefaultName("rowfilllastchild")
	}

	spacingURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutSpacing)
	hMarginURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHMargin)

	r := &RowFillLastChild{
		Pal:        pal,
		HMargin:    hMargin,
		spacingURI: spacingURI,
		hMarginURI: hMarginURI,
		myName:     myName,
	}

	lh := mancini.NewLayoutAttributesBase(myName, parent)

	// Height — max of visible children's heights (reuse row height program).
	heightProg := mancini.BindStringsChildren(ProgRowHeight,
		"_myName_", myName)
	heightURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight)
	lh.Height = attr.ConstraintI64(heightURI, heightProg)

	// Inter-child spacing attribute.
	lh.SpacingAttr = attr.ValueI64(spacingURI, spacing)

	// Horizontal margin attribute.
	attr.ValueI64(hMarginURI, hMargin)

	r.Interactor.Initialize(r, lh)
	// Parent.Initialize is deferred to FinishLayout because it needs
	// Bounds, which depends on Width (set by the caller).
	return r
}

// FinishLayout creates the Bounds, LastChildWidth, and parent damage
// constraints. Must be called after the caller has assigned GetLayout().Width.
func (r *RowFillLastChild) FinishLayout() {
	lh := r.GetLayout()
	widthURI := lh.Width.URI()

	// Last child width constraint — computed from row width minus other children.
	lastChildWidthProg := mancini.BindStringsChildren(ProgRowFillLastChildWidth,
		"_rowWidth_", widthURI, "_spacing_", r.spacingURI, "_hMargin_", r.hMarginURI,
		"_myName_", r.myName)
	lastChildWidthURI := mancini.LayoutURI(r.myName, mancini.DataTypeInt64, "LastChildWidth")
	attr.ConstraintI64(lastChildWidthURI, lastChildWidthProg)

	lh.InitBounds(r.myName)
	r.Parent.Initialize(true, &r.Interactor)
}

// SetSpacing sets the inter-child spacing value (in pixels).
func (r *RowFillLastChild) SetSpacing(v float64) {
	lh := r.GetLayout()
	if lh != nil && lh.SpacingAttr != nil {
		lh.SpacingAttr.Set(int64(v))
	}
}

// Draw implements mancini.NewDrawer. Arranges children left-to-right;
// the last visible child receives remaining width.
func (r *RowFillLastChild) Draw(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()

	children := r.GetChildren()
	if len(children) == 0 {
		dc.SetColor(mancini.ErrPink)
		dc.DrawRectangle(float64(x), float64(y), float64(w), float64(h))
		dc.Fill()
		return
	}

	// Count visible children.
	var visible []mancini.Interactor
	for _, child := range children {
		if child.Visible() {
			visible = append(visible, child)
		}
	}
	if len(visible) == 0 {
		return
	}

	lh := r.GetLayout()
	spacing := int64(lh.GetSpacing())
	curX := x + r.HMargin

	for i, child := range visible {
		childL, hasLayout := child.(mancini.Layouter)
		isLast := i == len(visible)-1

		var childW int64
		if isLast {
			// Last child gets remaining width.
			childW = (x + w - r.HMargin) - curX
			if childW < 0 {
				childW = 0
			}
		} else {
			childW = w / int64(len(visible)) // fallback
			if hasLayout {
				childW = int64(mancini.ChildWidth(childL, float64(childW)))
			}
		}

		if i > 0 {
			curX += spacing
		}

		childH := h
		if hasLayout {
			childH = int64(mancini.ChildHeight(childL, float64(h)))
		}

		// Vertically center child.
		childY := y + (h-childH)/2

		// Publish child position.
		if hasLayout {
			clh := childL.GetLayout()
			if clh != nil {
				clh.X.Set(curX)
				clh.Y.Set(childY)
			}
		}

		// Propagate DC.
		if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
			cs.SetDC(dc)
		}

		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, curX, childY, childW, childH)
		}
		curX += childW
	}
}
