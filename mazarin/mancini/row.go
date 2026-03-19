package mancini

import (
	"image"

	"github.com/fogleman/gg"
)

// WidthSizer is implemented by interactors that know their preferred width.
type WidthSizer interface {
	PreferredWidth() float64
}

// Row arranges children horizontally. Gaps between children are provided
// by Spacer interactors — spacing is computed by the constraint system.
type Row struct {
	Theme    *Theme
	Name     string
	Children []Drawer
	Layout   *LayoutHandles
}

func (r *Row) GetLayout() *LayoutHandles { return r.Layout }

// SetSpacing sets the inter-child spacing value (in pixels).
func (r *Row) SetSpacing(v float64) {
	setLayoutSpacing(r.Layout, v)
}

// PreferredWidth returns the total width: sum of children widths + inter-child spacing.
func (r *Row) PreferredWidth() float64 {
	total := 0.0
	n := 0
	for _, child := range r.Children {
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

// PreferredHeight returns the max preferred height among children.
func (r *Row) PreferredHeight() float64 {
	maxH := 0.0
	for _, child := range r.Children {
		if s, ok := child.(Sizer); ok {
			if ch := s.PreferredHeight(); ch > maxH {
				maxH = ch
			}
		}
	}
	return maxH
}

// Draw implements the Drawer interface.
func (r *Row) Draw(canvas *image.RGBA, x, y, w, h float64) {
	// Publish intrinsic dimensions for inside-out constraints.
	pw := r.PreferredWidth()
	ph := r.PreferredHeight()
	if pw > 0 {
		publishLayout(r.Layout, x, y, pw, ph)
	} else {
		publishLayout(r.Layout, x, y, w, h)
	}
	// No children: pink error indicator.
	if len(r.Children) == 0 {
		dc := gg.NewContextForRGBA(canvas)
		pc := errPink
		if r.Theme != nil {
			pc = r.Theme.C(pc)
		}
		dc.SetColor(pc)
		dc.DrawRectangle(x, y, w, h)
		dc.Fill()
		return
	}

	// One child: give it all available width.
	if len(r.Children) == 1 {
		child := r.Children[0]
		var childW float64
		if s, ok := child.(WidthSizer); ok {
			childW = s.PreferredWidth()
		} else {
			childW = w
		}
		child.Draw(canvas, x, y, childW, h)
		return
	}

	// Two+ children: layout left-to-right with inter-child spacing.
	spacing := r.Layout.GetSpacing()
	curX := x
	right := x + w
	for i, child := range r.Children {
		var childW float64
		if s, ok := child.(WidthSizer); ok {
			childW = s.PreferredWidth()
		} else {
			childW = w / float64(len(r.Children))
		}

		// Check if this child fits with spacing before the next.
		remaining := right - curX
		needsSpace := childW
		if i < len(r.Children)-1 {
			if spacing > 0 {
				needsSpace += spacing
			} else {
				needsSpace += 1 // at least 1px gap before next child
			}
		}
		if remaining < needsSpace {
			setChildrenVisible(r.Children[i:], false)
			break
		}

		childH := h
		if s, ok := child.(Sizer); ok {
			childH = s.PreferredHeight()
		}

		setVisible(child, true)
		child.Draw(canvas, curX, y, childW, childH)
		curX += childW + spacing
	}
}
