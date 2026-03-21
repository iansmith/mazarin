package mancini

import (
	"image/color"
)

// ColumnOutsideIn arranges children vertically with outside-in layout:
// the parent dictates width and height, and children that don't fit
// vertically are hidden (Visible = false).
type ColumnOutsideIn struct {
	Pal      Palette
	Name     string
	Children []Drawer
	BgColor  color.NRGBA // content well background fill
	Layout   *LayoutHandles
}

func (c *ColumnOutsideIn) GetLayout() *LayoutHandles { return c.Layout }

// Draw implements the Drawer interface. The parent passes the available
// content area; children are laid out top-to-bottom within it.
func (c *ColumnOutsideIn) Draw(dc DrawContext, x, y, w, h float64) {
	publishLayout(c.Layout, x, y, w, h)

	// Fill background.
	dc.SetColor(c.BgColor)
	dc.FillRectangle(x, y, w, h)

	if len(c.Children) == 0 {
		return
	}

	fallbackH := 20.0
	curY := y
	for _, child := range c.Children {
		childH := childLayoutHeight(child, fallbackH)

		if curY+childH > y+h {
			// This child and all remaining don't fit — hide them.
			SetVisible(child, false)
			continue
		}

		SetVisible(child, true)
		child.Draw(dc, x, curY, w, childH)
		curY += childH
	}
}
