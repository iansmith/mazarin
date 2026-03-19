package mancini

import (
	"image"
	"image/color"

	"github.com/fogleman/gg"
)

// Sizer is implemented by interactors that know their preferred height.
type Sizer interface {
	PreferredHeight() float64
}

// errPink is the background color drawn when an interactor has no children
// (almost always a programming error).
var errPink = color.NRGBA{255, 105, 180, 255}

// Column arranges children vertically. Gaps between children are provided
// by Spacer interactors — spacing is computed by the constraint system.
type Column struct {
	Theme    *Theme
	Name     string
	Children []Drawer
	Layout   *LayoutHandles
}

func (c *Column) GetLayout() *LayoutHandles { return c.Layout }

// SetSpacing sets the inter-child spacing value (in pixels).
func (c *Column) SetSpacing(v float64) {
	setLayoutSpacing(c.Layout, v)
}

// PreferredHeight returns the total height: sum of children heights + inter-child spacing.
func (c *Column) PreferredHeight() float64 {
	total := 0.0
	n := 0
	for _, child := range c.Children {
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

// PreferredWidth returns the max width among children that implement WidthSizer.
func (c *Column) PreferredWidth() float64 {
	maxW := 0.0
	for _, child := range c.Children {
		if s, ok := child.(WidthSizer); ok {
			if cw := s.PreferredWidth(); cw > maxW {
				maxW = cw
			}
		}
	}
	return maxW
}

// Draw implements the Drawer interface.
func (c *Column) Draw(canvas *image.RGBA, x, y, w, h float64) {
	// Publish intrinsic dimensions for inside-out constraints.
	pw := c.PreferredWidth()
	ph := c.PreferredHeight()
	if pw > 0 {
		publishLayout(c.Layout, x, y, pw, ph)
	} else {
		publishLayout(c.Layout, x, y, w, h)
	}
	// No children: pink error indicator.
	if len(c.Children) == 0 {
		dc := gg.NewContextForRGBA(canvas)
		pc := errPink
		if c.Theme != nil {
			pc = c.Theme.C(pc)
		}
		dc.SetColor(pc)
		dc.DrawRectangle(x, y, w, h)
		dc.Fill()
		return
	}

	// One child: give it all available height.
	if len(c.Children) == 1 {
		child := c.Children[0]
		var childH float64
		if s, ok := child.(Sizer); ok {
			childH = s.PreferredHeight()
		} else {
			childH = h
		}
		child.Draw(canvas, x, y, w, childH)
		return
	}

	// Two+ children: layout top-to-bottom with inter-child spacing.
	spacing := c.Layout.GetSpacing()
	curY := y
	bottom := y + h
	for i, child := range c.Children {
		var childH float64
		if s, ok := child.(Sizer); ok {
			childH = s.PreferredHeight()
		} else {
			childH = h / float64(len(c.Children))
		}

		// Check if this child fits with spacing before the next.
		remaining := bottom - curY
		needsSpace := childH
		if i < len(c.Children)-1 {
			if spacing > 0 {
				needsSpace += spacing
			} else {
				needsSpace += 1 // at least 1px gap before next child
			}
		}
		if remaining < needsSpace {
			// Not enough room: hide this and all subsequent children.
			setChildrenVisible(c.Children[i:], false)
			break
		}

		setVisible(child, true)
		child.Draw(canvas, x, curY, w, childH)
		curY += childH + spacing
	}
}

// setVisible sets the Visible layout handle on an interactor if it has one.
func setVisible(d Drawer, visible bool) {
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
		setVisible(child, visible)
	}
}
