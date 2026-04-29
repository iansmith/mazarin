package std

import (
	"image"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Column arranges children vertically with inside-out sizing. Width and
// Height are constraint-computed from children's published dimensions.
// Children declare this Column as their parent by naming it in their
// [mancini.LayoutAttributes]' Parent field.
//
// Column embeds [impl.Interactor] + [impl.Parent]. It does not embed
// [impl.ThemedInteractor] because containers do not need theme access
// for their own rendering. Children are discovered via the constraint
// network at draw time (see [impl.Parent.GetChildren]).
//
// # Layout Behavior
//
// Children are arranged top-to-bottom with configurable spacing.
// Cross-axis (horizontal) alignment is controlled by CrossAlign
// ([mancini.AxisMinimum], [mancini.AxisMiddle], [mancini.AxisMaximum]).
// Children that overflow MaxHeight are either clipped (ClipChildOverflow)
// or skipped entirely.
//
// See also [Row] for horizontal layout and [ColumnOutsideIn] for a
// column with clamped height.
type Column struct {
	impl.Interactor // X(), Y(), W(), H(), Visible(), DC(), GetLayout()
	impl.Parent     // GetChildren() via constraint network

	// Pal is the palette used for the optional background fill.
	Pal mancini.Palette
	// CrossAlign controls horizontal alignment of children within the column.
	CrossAlign mancini.Alignment
	// MaxHeight is the maximum column height in pixels (0 means unbounded).
	MaxHeight         int64
	VPadding          int64 // vertical padding applied at top and bottom edges
	HPadding          int64 // horizontal padding applied at left and right edges
	ClipChildOverflow bool  // true: clip last partial child; false: skip it
	PaintBg           bool  // true: fill background with Pal.Surface() before children
}

// NewColumn creates a Column wired to the constraint system.
// Children are not passed here — they declare this Column as their parent
// via their own InitLayout(parentName) call, and are discovered at draw
// time via the constraint network.
func NewColumn(myName, parent string, pal mancini.Palette, maxHeight int64, crossAlign mancini.Alignment, vPadding, hPadding int64, clipOverflow bool) *Column {
	if vPadding <= 0 {
		vPadding = 1
	}
	c := &Column{
		Pal:               pal,
		CrossAlign:        crossAlign,
		MaxHeight:         maxHeight,
		VPadding:           vPadding,
		HPadding:           hPadding,
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

	// Vertical padding attribute.
	vPaddingURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutVPadding)
	attr.ValueI64(vPaddingURI, vPadding)

	// Horizontal padding attribute.
	hPaddingURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHPadding)
	attr.ValueI64(hPaddingURI, hPadding)

	// Column HEIGHT constraint.
	heightProg := mancini.BindStringsChildren(ProgColumnHeight,
		"_maxHeight_", maxHeightURI, "_spacing_", spacingURI, "_vPadding_", vPaddingURI,
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
		"_maxHeight_", maxHeightURI, "_spacing_", spacingURI, "_vPadding_", vPaddingURI,
		"_myName_", myName)
	lastChildURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutLastChildDrawn)
	lh.LastChildDrawnAttr = attr.ConstraintI64(lastChildURI, lastChildProg)

	lh.InitBounds(myName)

	c.Interactor.Initialize(c, lh)
	c.Parent.Initialize(true, &c.Interactor)
	return c
}

// NewColumnWithLayout creates a Column using a pre-built LayoutAttributes.
// Used when the caller provides bridge/custom layout (e.g., as AppWindow's
// direct child with inout-bridge constraints on Width/Height).
// The caller is responsible for setting Width, Height, and Bounds on lh.
func NewColumnWithLayout(lh *mancini.LayoutAttributes, pal mancini.Palette,
	crossAlign mancini.Alignment, hPadding, vPadding int64, clipOverflow bool) *Column {

	c := &Column{
		Pal:               pal,
		CrossAlign:        crossAlign,
		HPadding:           hPadding,
		VPadding:           vPadding,
		ClipChildOverflow: clipOverflow,
	}
	myName := lh.Name()

	// Inter-child spacing attribute.
	spacingURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutSpacing)
	lh.SpacingAttr = attr.ValueI64(spacingURI, 0)

	// Cross-axis alignment.
	alignURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutCrossAlign)
	lh.CrossAlignAttr = attr.ValueI64(alignURI, int64(crossAlign))

	// Horizontal padding attribute.
	hPaddingURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHPadding)
	attr.ValueI64(hPaddingURI, hPadding)

	// Vertical padding attribute.
	vPaddingURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutVPadding)
	attr.ValueI64(vPaddingURI, vPadding)

	// Use the layout's own Height as the max height for child clipping.
	// The bridge layout already constrains Height to
	// clamp(AppWindow.Height, minH, maxH), so this is the available space.
	maxHeightURI := lh.Height.URI()

	// LastChildDrawn constraint — determines which children fit.
	lastChildProg := mancini.BindStringsChildren(ProgColumnLastChild,
		"_maxHeight_", maxHeightURI, "_spacing_", spacingURI, "_vPadding_", vPaddingURI,
		"_myName_", myName)
	lastChildURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutLastChildDrawn)
	lh.LastChildDrawnAttr = attr.ConstraintI64(lastChildURI, lastChildProg)

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
// If PaintBg is true and Pal is set, the column fills its background
// with the theme's Surface color before drawing children.
func (c *Column) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	lh := c.GetLayout()
	dc := self.DC()

	// Optional themed background fill.
	if c.PaintBg && c.Pal != nil {
		bg := c.Pal.Surface()
		if bg.A > 0 {
			dc.SetColor(bg)
			dc.FillRectangle(float64(x), float64(y), float64(w), float64(h))
		}
	}

	children := c.GetChildren()
	if len(children) == 0 {
		dc.SetColor(mancini.ErrPink)
		dc.DrawRectangle(float64(x), float64(y), float64(w), float64(h))
		dc.Fill()
		return
	}

	// Count visible children and compute natural height sum for
	// dynamic spacing: if the parent offers more height than the
	// children need, distribute the excess as inter-child spacing.
	visCount := int64(0)
	naturalHeight := int64(0)
	for _, child := range children {
		if !child.Visible() {
			continue
		}
		visCount++
		if childL, ok := child.(mancini.Layouter); ok {
			naturalHeight += int64(mancini.ChildHeight(childL, 0))
		}
	}
	if visCount == 0 {
		return
	}
	fallbackH := h / visCount

	spacing := int64(lh.GetSpacing())
	// If parent offers more height than children need, grow spacing
	// to distribute the excess evenly between children.
	availableForSpacing := h - 2*c.VPadding - naturalHeight
	if visCount > 1 && availableForSpacing > 0 {
		dynamicSpacing := availableForSpacing / (visCount - 1)
		if dynamicSpacing > spacing {
			spacing = dynamicSpacing
		}
	}
	curY := y + c.VPadding
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

		// Publish child position and dimensions for pick/hit-testing.
		if hasLayout {
			clh := childL.GetLayout()
			if clh != nil {
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

		// Child partially fits: use ClipChildOverflow to decide.
		if curY+childH > y+h {
			if c.ClipChildOverflow {
				visibleH := (y + h) - curY
				overflowH := childH - visibleH
				shadowPad := int64(60)
				pad := overflowH + shadowPad
				cc := mancini.WithClip(dc, float64(childX), float64(curY), float64(childW), float64(visibleH), float64(pad), mancini.ClipBottom)
				if d, ok := child.(mancini.NewDrawer); ok {
					d.Draw(child, childX, curY, childW, childH, damage)
				}
				cc.Flush()
				curY += childH
				drawnCount++
			}
			break
		}

		tcc := nanotime()
		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, childX, curY, childW, childH, damage)
		}
		drawPerf.ColChildNs.Add(nanotime() - tcc)
		curY += childH
		drawnCount++
	}
}
