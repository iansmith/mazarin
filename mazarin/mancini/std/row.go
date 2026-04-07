package std

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Row arranges children horizontally with inside-out sizing. Width,
// Height, and LastChildDrawn are constraint-computed from children's
// published dimensions. Children declare this Row as their parent by
// naming it in their [mancini.LayoutAttributes]' Parent field.
//
// Row embeds [impl.Interactor] + [impl.Parent]. Children are discovered
// via the constraint network at draw time (see [impl.Parent.GetChildren]).
//
// # Layout Behavior
//
// Children are arranged left-to-right with configurable spacing.
// Cross-axis (vertical) alignment is controlled by CrossAlign
// ([mancini.AxisMinimum], [mancini.AxisMiddle], [mancini.AxisMaximum]).
// The last child that partially overflows MaxWidth is clipped using
// [mancini.WithClip].
//
// See also [Column] for vertical layout.
type Row struct {
	impl.Interactor // X(), Y(), W(), H(), Visible(), DC(), GetLayout()
	impl.Parent     // GetChildren() via constraint network

	Pal        mancini.Palette
	CrossAlign mancini.Alignment
	MaxWidth   int64
	HPadding    int64 // horizontal padding applied at left and right edges
}

// NewRow creates a Row wired to the constraint system.
// Children are not passed here — they declare this Row as their parent
// via their own InitLayout(parentName) call, and are discovered at draw
// time via the constraint network.
func NewRow(myName, parent string, pal mancini.Palette, maxWidth int64, crossAlign mancini.Alignment, hPadding int64) *Row {
	if hPadding <= 0 {
		hPadding = 1
	}
	r := &Row{
		Pal:        pal,
		CrossAlign: crossAlign,
		MaxWidth:   maxWidth,
		HPadding:    hPadding,
	}

	if myName == "" {
		myName = mancini.DefaultName("row")
	}

	lh := mancini.NewLayoutAttributesBase(myName, parent)

	// Inter-child spacing attribute.
	spacingURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutSpacing)
	lh.SpacingAttr = attr.ValueI64(spacingURI, 0)

	// Cross-axis alignment.
	alignURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutCrossAlign)
	lh.CrossAlignAttr = attr.ValueI64(alignURI, int64(crossAlign))

	// MaxWidth attribute.
	maxW := maxWidth
	if maxW <= 0 {
		maxW = 9999
	}
	maxWidthURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutMaxWidth)
	lh.MaxWidthAttr = attr.ValueI64(maxWidthURI, maxW)

	// Horizontal padding attribute.
	hPaddingURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHPadding)
	attr.ValueI64(hPaddingURI, hPadding)

	// Row WIDTH constraint.
	widthProg := mancini.BindStringsChildren(ProgRowWidth,
		"_maxWidth_", maxWidthURI, "_spacing_", spacingURI, "_hPadding_", hPaddingURI,
		"_myName_", myName)
	widthURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth)
	lh.Width = attr.ConstraintI64(widthURI, widthProg)

	// Row HEIGHT constraint.
	heightProg := mancini.BindStringsChildren(ProgRowHeight,
		"_myName_", myName)
	heightURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight)
	lh.Height = attr.ConstraintI64(heightURI, heightProg)

	// LastChildDrawn constraint.
	lastChildProg := mancini.BindStringsChildren(ProgRowLastChild,
		"_maxWidth_", maxWidthURI, "_spacing_", spacingURI, "_hPadding_", hPaddingURI,
		"_myName_", myName)
	lastChildURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutLastChildDrawn)
	lh.LastChildDrawnAttr = attr.ConstraintI64(lastChildURI, lastChildProg)

	lh.InitBounds(myName)

	r.Interactor.Initialize(r, lh)
	r.Parent.Initialize(true, &r.Interactor)
	return r
}

// SetSpacing sets the inter-child spacing value (in pixels).
func (r *Row) SetSpacing(v float64) {
	lh := r.GetLayout()
	if lh != nil && lh.SpacingAttr != nil {
		lh.SpacingAttr.Set(int64(v))
	}
}

// Draw implements mancini.NewDrawer. Arranges children left-to-right
// with spacing, cross-axis alignment, and overflow clipping.
func (r *Row) Draw(self mancini.Interactor, x, y, w, h int64) {
	lh := r.GetLayout()
	dc := self.DC()

	children := r.GetChildren()
	if len(children) == 0 {
		dc.SetColor(mancini.ErrPink)
		dc.DrawRectangle(float64(x), float64(y), float64(w), float64(h))
		dc.Fill()
		return
	}

	// Count visible children and compute natural width sum for
	// dynamic spacing: if the parent offers more width than the
	// children need, distribute the excess as inter-child spacing.
	visCount := int64(0)
	naturalWidth := int64(0)
	for _, child := range children {
		if !child.Visible() {
			continue
		}
		visCount++
		if childL, ok := child.(mancini.Layouter); ok {
			naturalWidth += int64(mancini.ChildWidth(childL, 0))
		}
	}
	if visCount == 0 {
		return
	}
	fallbackW := w / visCount

	// lastChildDrawn: 0-based index among visible children.
	lastChildDrawn := int64(-1)
	if lh.LastChildDrawnAttr != nil {
		lastChildDrawn = lh.LastChildDrawnAttr.Get()
	}

	spacing := int64(lh.GetSpacing())
	// If parent offers more width than children need, grow spacing
	// to distribute the excess evenly between children.
	availableForSpacing := w - 2*r.HPadding - naturalWidth
	if visCount > 1 && availableForSpacing > 0 {
		dynamicSpacing := availableForSpacing / (visCount - 1)
		if dynamicSpacing > spacing {
			spacing = dynamicSpacing
		}
	}

	curX := x + r.HPadding
	visIndex := 0
	for _, child := range children {
		if !child.Visible() {
			continue
		}
		if lastChildDrawn >= 0 && int64(visIndex) > lastChildDrawn {
			break
		}

		childL, hasLayout := child.(mancini.Layouter)
		childW := fallbackW
		if hasLayout {
			childW = int64(mancini.ChildWidth(childL, float64(fallbackW)))
		}

		if visIndex > 0 {
			curX += spacing
		}

		childH := h
		if hasLayout {
			childH = int64(mancini.ChildHeight(childL, float64(h)))
		}

		// Children with constraint-computed height get the full
		// available height in Draw so they can distribute excess
		// space internally (e.g. Column grows inter-child spacing).
		drawH := childH
		childY := y
		if hasLayout {
			clh := childL.GetLayout()
			if clh != nil && clh.Height.IsConstraint() {
				drawH = h
				childY = y // no centering — child fills
			} else {
				// Cross-axis (vertical) alignment for value-height children.
				switch r.CrossAlign {
				case mancini.AxisMiddle:
					childY = y + (h-childH)/2
				case mancini.AxisMaximum:
					childY = y + h - childH
				}
			}
		} else {
			switch r.CrossAlign {
			case mancini.AxisMiddle:
				childY = y + (h-childH)/2
			case mancini.AxisMaximum:
				childY = y + h - childH
			}
		}

		// Publish child position and dimensions to layout handles
		// so that pick/hit-testing uses correct bounds.
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

		// Last drawable child that overflows: clip it.
		if lastChildDrawn >= 0 && int64(visIndex) == lastChildDrawn && curX+childW > x+w {
			visibleW := (x + w) - curX
			if visibleW > 0 {
				overflowW := childW - visibleW
				shadowPad := int64(60)
				pad := overflowW + shadowPad
				cc := mancini.WithClip(dc, float64(curX), float64(childY), float64(visibleW), float64(h), float64(pad), mancini.ClipRight)
				if d, ok := child.(mancini.NewDrawer); ok {
					d.Draw(child, curX, childY, childW, drawH)
				}
				cc.Flush()
			}
			curX += childW
			visIndex++
			break
		}

		trc := nanotime()
		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, curX, childY, childW, drawH)
		}
		dt := nanotime() - trc
		idx := drawPerf.RowChildCount.Add(1) - 1
		if idx < int64(len(drawPerf.RowChildNs)) {
			drawPerf.RowChildNs[idx].Add(dt)
		}
		curX += childW
		visIndex++
	}
}
