package mancini


// WidthSizer is implemented by interactors that know their preferred width.
type WidthSizer interface {
	PreferredWidth() float64
}

// Row arranges children horizontally. Gaps between children are provided
// by Spacer interactors — spacing is computed by the constraint system.
type Row struct {
	Pal               Palette
	Name              string
	Children          []Drawer
	CrossAlign        Alignment // cross-axis (vertical) alignment of children
	ClipChildOverflow bool      // true: clip last partial child; false: skip it
	Layout            *LayoutHandles
}

func (r *Row) GetLayout() *LayoutHandles { return r.Layout }

// SetSpacing sets the inter-child spacing value (in pixels).
func (r *Row) SetSpacing(v float64) {
	setLayoutSpacing(r.Layout, v)
}

// SetCrossAlign sets the cross-axis (vertical) alignment for children.
func (r *Row) SetCrossAlign(a Alignment) {
	r.CrossAlign = a
	setLayoutCrossAlign(r.Layout, a)
}

// PreferredWidth returns the total width: sum of visible children widths + inter-child spacing.
func (r *Row) PreferredWidth() float64 {
	total := 0.0
	n := 0
	for _, child := range r.Children {
		if !isVisible(child) {
			continue
		}
		if s, ok := child.(WidthSizer); ok {
			total += s.PreferredWidth()
			n++
		}
	}
	if n > 1 {
		total += float64(n-1) * r.Layout.GetSpacing()
	}
	return total
}

// PreferredHeight returns the max preferred height among visible children.
func (r *Row) PreferredHeight() float64 {
	maxH := 0.0
	for _, child := range r.Children {
		if !isVisible(child) {
			continue
		}
		if s, ok := child.(Sizer); ok {
			if ch := s.PreferredHeight(); ch > maxH {
				maxH = ch
			}
		}
	}
	return maxH
}

// Draw implements the Drawer interface.
func (r *Row) Draw(dc DrawContext, x, y, w, h float64) {
	// No children: pink error indicator.
	if len(r.Children) == 0 {
		publishLayout(r.Layout, x, y, w, h)
		dc.SetColor(errPink)
		dc.DrawRectangle(x, y, w, h)
		dc.Fill()
		return
	}

	// Count visible children for fallback width division.
	visCount := 0
	for _, child := range r.Children {
		if isVisible(child) {
			visCount++
		}
	}
	if visCount == 0 {
		publishLayout(r.Layout, x, y, w, h)
		return
	}
	fallbackW := w / float64(visCount)

	// Layout left-to-right with inter-child spacing.
	// Invisible children are skipped entirely (no space, no draw).
	// Children past the right edge are skipped (parent's dc clip handles pixels).
	spacing := r.Layout.GetSpacing()
	curX := x
	drawnCount := 0
	for _, child := range r.Children {
		if !isVisible(child) {
			continue
		}
		childW := childLayoutWidth(child, fallbackW)

		// Add spacing before this child (but not before the first visible one).
		if drawnCount > 0 {
			curX += spacing
		}

		// Child completely outside the row's right edge: stop.
		if curX >= x+w {
			break
		}

		childH := childLayoutHeight(child, h)

		// Cross-axis (vertical) alignment.
		childY := y
		switch r.CrossAlign {
		case AxisMiddle:
			childY = y + (h-childH)/2
		case AxisMaximum:
			childY = y + h - childH
		}

		// Child partially fits: use ClipChildOverflow to decide.
		if curX+childW > x+w {
			if r.ClipChildOverflow {
				// Draw clipped: save overflow pixels, draw, restore.
				// Pad must cover the full child overflow + max shadow spread.
				visibleW := (x + w) - curX
				overflowW := childW - visibleW
				shadowPad := 60.0 // worst-case neumorphic shadow extent
				pad := overflowW + shadowPad
				cc := WithClip(dc, curX, childY, visibleW, h, pad, ClipRight)
				child.Draw(dc, curX, childY, childW, childH)
				cc.Flush()
				curX += childW
				drawnCount++
			}
			// Whether we drew it clipped or skipped it, no more children fit.
			break
		}

		child.Draw(dc, curX, childY, childW, childH)
		curX += childW
		drawnCount++
	}

	// Publish actual used width so inside-out constraints see content size.
	usedW := curX - x
	if usedW < w {
		publishLayout(r.Layout, x, y, usedW, h)
	} else {
		publishLayout(r.Layout, x, y, w, h)
	}
}
