package std

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Row arranges children horizontally with inside-out sizing.
// Width, Height, and LastChildDrawn are constraints computed from children.
// Children declare this Row as their parent via the attribute namespace.
type Row struct {
	impl.Interactor // X(), Y(), W(), H(), Visible(), DC(), GetLayout()
	impl.Parent     // GetChildren() via constraint network

	Pal        mancini.Palette
	CrossAlign mancini.Alignment
	MaxWidth   int64
	HMargin    int64 // horizontal margin applied at left and right edges
}

// NewRow creates a Row wired to the constraint system.
// Children are not passed here — they declare this Row as their parent
// via their own InitLayout(parentName) call, and are discovered at draw
// time via the constraint network.
func NewRow(myName, parent string, pal mancini.Palette, maxWidth int64, crossAlign mancini.Alignment, hMargin int64) *Row {
	if hMargin <= 0 {
		hMargin = 1
	}
	r := &Row{
		Pal:        pal,
		CrossAlign: crossAlign,
		MaxWidth:   maxWidth,
		HMargin:    hMargin,
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

	// Horizontal margin attribute.
	hMarginURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHMargin)
	attr.ValueI64(hMarginURI, hMargin)

	// Row WIDTH constraint.
	widthProg := mancini.BindStringsChildren(ProgRowWidth,
		"_maxWidth_", maxWidthURI, "_spacing_", spacingURI, "_hMargin_", hMarginURI,
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
		"_maxWidth_", maxWidthURI, "_spacing_", spacingURI, "_hMargin_", hMarginURI,
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

	// Count visible children for fallback width division.
	visCount := int64(0)
	for _, child := range children {
		if child.Visible() {
			visCount++
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
	curX := x + r.HMargin
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

		// Cross-axis (vertical) alignment.
		childY := y
		switch r.CrossAlign {
		case mancini.AxisMiddle:
			childY = y + (h-childH)/2
		case mancini.AxisMaximum:
			childY = y + h - childH
		}

		// Publish child position to layout handles for constraint visibility.
		if hasLayout {
			clh := childL.GetLayout()
			if clh != nil {
				clh.X.Set(curX)
				clh.Y.Set(childY)
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
					d.Draw(child, curX, childY, childW, childH)
				}
				cc.Flush()
			}
			curX += childW
			visIndex++
			break
		}

		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, curX, childY, childW, childH)
		}
		curX += childW
		visIndex++
	}
}
