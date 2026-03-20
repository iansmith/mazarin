package mancini

import (
	"image/color"
)

// Sizer is implemented by interactors that know their preferred height.
type Sizer interface {
	PreferredHeight() float64
}

// Alignment controls cross-axis positioning of children within a container.
// For Column (vertical layout), this aligns children horizontally.
// For Row (horizontal layout), this aligns children vertically.
type Alignment int

const (
	AxisMinimum Alignment = 0 // left (Column) or top (Row)
	AxisMiddle  Alignment = 1 // centered
	AxisMaximum Alignment = 2 // right (Column) or bottom (Row)
)

// errPink is the background color drawn when an interactor has no children
// (almost always a programming error).
var errPink = color.NRGBA{255, 105, 180, 255}

// Column arranges children vertically. Gaps between children are provided
// by Spacer interactors — spacing is computed by the constraint system.
type Column struct {
	Theme             *Theme
	Name              string
	Children          []Drawer
	CrossAlign        Alignment // cross-axis (horizontal) alignment of children
	ClipChildOverflow bool      // true: clip last partial child; false: skip it
	Layout            *LayoutHandles
}

func (c *Column) GetLayout() *LayoutHandles { return c.Layout }

// SetSpacing sets the inter-child spacing value (in pixels).
func (c *Column) SetSpacing(v float64) {
	setLayoutSpacing(c.Layout, v)
}

// SetCrossAlign sets the cross-axis (horizontal) alignment for children.
func (c *Column) SetCrossAlign(a Alignment) {
	c.CrossAlign = a
	setLayoutCrossAlign(c.Layout, a)
}

// PreferredHeight returns the total height: sum of visible children heights + inter-child spacing.
func (c *Column) PreferredHeight() float64 {
	total := 0.0
	n := 0
	for _, child := range c.Children {
		if !isVisible(child) {
			continue
		}
		if s, ok := child.(Sizer); ok {
			total += s.PreferredHeight()
			n++
		}
	}
	if n > 1 {
		total += float64(n-1) * c.Layout.GetSpacing()
	}
	return total
}

// PreferredWidth returns the max width among visible children that implement WidthSizer.
func (c *Column) PreferredWidth() float64 {
	maxW := 0.0
	for _, child := range c.Children {
		if !isVisible(child) {
			continue
		}
		if s, ok := child.(WidthSizer); ok {
			if cw := s.PreferredWidth(); cw > maxW {
				maxW = cw
			}
		}
	}
	return maxW
}

// Draw implements the Drawer interface.
func (c *Column) Draw(dc DrawContext, x, y, w, h float64) {
	// No children: pink error indicator.
	if len(c.Children) == 0 {
		publishLayout(c.Layout, x, y, w, h)
		pc := errPink
		if c.Theme != nil {
			pc = c.Theme.C(pc)
		}
		dc.SetColor(pc)
		dc.DrawRectangle(x, y, w, h)
		dc.Fill()
		return
	}

	// Count visible children for fallback height division.
	visCount := 0
	for _, child := range c.Children {
		if isVisible(child) {
			visCount++
		}
	}
	if visCount == 0 {
		publishLayout(c.Layout, x, y, w, h)
		return
	}
	fallbackH := h / float64(visCount)

	// Layout top-to-bottom with inter-child spacing.
	// Invisible children are skipped entirely (no space, no draw).
	// Children past the bottom edge are skipped (parent's dc clip handles pixels).
	spacing := c.Layout.GetSpacing()
	curY := y
	drawnCount := 0
	for _, child := range c.Children {
		if !isVisible(child) {
			continue
		}
		childH := childLayoutHeight(child, fallbackH)

		// Add spacing before this child (but not before the first visible one).
		if drawnCount > 0 {
			curY += spacing
		}

		// Child completely outside the column's bottom edge: stop.
		if curY >= y+h {
			break
		}

		childW := childLayoutWidth(child, w)

		// Cross-axis (horizontal) alignment.
		childX := x
		switch c.CrossAlign {
		case AxisMiddle:
			childX = x + (w-childW)/2
		case AxisMaximum:
			childX = x + w - childW
		}

		// Child partially fits: use ClipChildOverflow to decide.
		if curY+childH > y+h {
			if c.ClipChildOverflow {
				// Pad must cover the full child overflow + max shadow spread.
				visibleH := (y + h) - curY
				overflowH := childH - visibleH
				shadowPad := 60.0
				pad := overflowH + shadowPad
				cc := WithClip(dc, childX, curY, childW, visibleH, pad, ClipBottom)
				child.Draw(dc, childX, curY, childW, childH)
				cc.Flush()
				curY += childH
				drawnCount++
			}
			break
		}

		child.Draw(dc, childX, curY, childW, childH)
		curY += childH
		drawnCount++
	}

	// Publish actual used height so inside-out constraints see content size.
	usedH := curY - y
	if usedH < h {
		publishLayout(c.Layout, x, y, w, usedH)
	} else {
		publishLayout(c.Layout, x, y, w, h)
	}
}

// isVisible returns true if an interactor's Visible handle is set (or has no handle).
func isVisible(d Drawer) bool {
	if l, ok := d.(Layouter); ok {
		lh := l.GetLayout()
		if lh != nil {
			return isVisibleHandle(lh)
		}
	}
	return true
}

// SetVisible sets the Visible layout handle on an interactor if it has one.
func SetVisible(d Drawer, visible bool) {
	if l, ok := d.(Layouter); ok {
		lh := l.GetLayout()
		if lh != nil {
			v := int64(0)
			if visible {
				v = 1
			}
			setVisibleHandle(lh, v)
		}
	}
}

// setChildrenVisible sets visibility on a slice of drawers.
func setChildrenVisible(children []Drawer, visible bool) {
	for _, child := range children {
		SetVisible(child, visible)
	}
}
