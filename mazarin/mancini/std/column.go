package std

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Column arranges children vertically with inside-out sizing.
// Width and Height are constraints computed from children.
// Children declare this Column as their parent via the attribute namespace.
type Column struct {
	impl.Interactor // X(), Y(), W(), H(), Visible(), DC(), GetLayout()
	impl.Parent     // GetChildren() via constraint network

	Pal               mancini.Palette
	CrossAlign        mancini.Alignment
	MaxHeight         int64
	VMargin           int64 // vertical margin applied at top and bottom edges
	ClipChildOverflow bool  // true: clip last partial child; false: skip it
}

// NewColumn creates a Column wired to the constraint system.
// Children are not passed here — they declare this Column as their parent
// via their own InitLayout(parentName) call, and are discovered at draw
// time via the constraint network.
func NewColumn(myName, parent string, pal mancini.Palette, maxHeight int64, crossAlign mancini.Alignment, vMargin int64, clipOverflow bool) *Column {
	if vMargin <= 0 {
		vMargin = 1
	}
	c := &Column{
		Pal:               pal,
		CrossAlign:        crossAlign,
		MaxHeight:         maxHeight,
		VMargin:           vMargin,
		ClipChildOverflow: clipOverflow,
	}

	if myName == "" {
		myName = mancini.DefaultName("column")
	}

	lh := mancini.NewLayoutAttributesBase(myName, parent)

	// Inter-child spacing attribute.
	spacingURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutSpacing)
	lh.SpacingAttr = attr.ValueI64(spacingURI, 0)

	// Cross-axis alignment.
	alignURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutCrossAlign)
	lh.CrossAlignAttr = attr.ValueI64(alignURI, int64(crossAlign))

	// MaxHeight attribute.
	maxH := maxHeight
	if maxH <= 0 {
		maxH = 9999
	}
	maxHeightURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutMaxHeight)
	lh.MaxHeightAttr = attr.ValueI64(maxHeightURI, maxH)

	// Vertical margin attribute.
	vMarginURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutVMargin)
	attr.ValueI64(vMarginURI, vMargin)

	// Column HEIGHT constraint.
	heightProg := mancini.BindStringsChildren(ProgColumnHeight,
		"_maxHeight_", maxHeightURI, "_spacing_", spacingURI, "_vMargin_", vMarginURI,
		"_myName_", myName)
	heightURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight)
	lh.Height = attr.ConstraintI64(heightURI, heightProg)

	// Column WIDTH constraint: max of children widths.
	widthProg := mancini.BindStringsChildren(ProgColumnWidth,
		"_myName_", myName)
	widthURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth)
	lh.Width = attr.ConstraintI64(widthURI, widthProg)

	// LastChildDrawn constraint.
	lastChildProg := mancini.BindStringsChildren(ProgColumnLastChild,
		"_maxHeight_", maxHeightURI, "_spacing_", spacingURI, "_vMargin_", vMarginURI,
		"_myName_", myName)
	lastChildURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutLastChildDrawn)
	lh.LastChildDrawnAttr = attr.ConstraintI64(lastChildURI, lastChildProg)

	lh.InitBounds(myName)

	c.Interactor.Initialize(c, lh)
	c.Parent.Initialize(true, &c.Interactor)
	return c
}

// SetSpacing sets the inter-child spacing value (in pixels).
func (c *Column) SetSpacing(v float64) {
	lh := c.GetLayout()
	if lh != nil && lh.SpacingAttr != nil {
		lh.SpacingAttr.Set(int64(v))
	}
}

// Draw implements mancini.NewDrawer. Arranges children top-to-bottom
// with spacing, cross-axis alignment, and overflow clipping.
func (c *Column) Draw(self mancini.Interactor, x, y, w, h int64) {
	lh := c.GetLayout()
	dc := self.DC()

	children := c.GetChildren()
	if len(children) == 0 {
		dc.SetColor(mancini.ErrPink)
		dc.DrawRectangle(float64(x), float64(y), float64(w), float64(h))
		dc.Fill()
		return
	}

	// Count visible children for fallback height division.
	visCount := int64(0)
	for _, child := range children {
		if child.Visible() {
			visCount++
		}
	}
	if visCount == 0 {
		return
	}
	fallbackH := h / visCount

	spacing := int64(lh.GetSpacing())
	curY := y
	drawnCount := 0
	for _, child := range children {
		if !child.Visible() {
			continue
		}

		childL, hasLayout := child.(mancini.Layouter)
		childH := fallbackH
		if hasLayout {
			childH = int64(mancini.ChildHeight(childL, float64(fallbackH)))
		}

		// Add spacing before this child (but not before the first visible one).
		if drawnCount > 0 {
			curY += spacing
		}

		// Child completely outside the column's bottom edge: stop.
		if curY >= y+h {
			break
		}

		childW := w
		if hasLayout {
			childW = int64(mancini.ChildWidth(childL, float64(w)))
		}

		// Cross-axis (horizontal) alignment.
		childX := x
		switch c.CrossAlign {
		case mancini.AxisMiddle:
			childX = x + (w-childW)/2
		case mancini.AxisMaximum:
			childX = x + w - childW
		}

		// Publish child position to layout handles for constraint visibility.
		if hasLayout {
			clh := childL.GetLayout()
			if clh != nil {
				clh.X.Set(childX)
				clh.Y.Set(curY)
			}
		}

		// Propagate DC and draw.
		if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
			cs.SetDC(dc)
		}

		// Child partially fits: use ClipChildOverflow to decide.
		if curY+childH > y+h {
			if c.ClipChildOverflow {
				visibleH := (y + h) - curY
				overflowH := childH - visibleH
				shadowPad := int64(60)
				pad := overflowH + shadowPad
				cc := mancini.WithClip(dc, float64(childX), float64(curY), float64(childW), float64(visibleH), float64(pad), mancini.ClipBottom)
				if d, ok := child.(mancini.NewDrawer); ok {
					d.Draw(child, childX, curY, childW, childH)
				}
				cc.Flush()
				curY += childH
				drawnCount++
			}
			break
		}

		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, childX, curY, childW, childH)
		}
		curY += childH
		drawnCount++
	}
}
