package main

import (
	"fmt"
	"image"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// UnitContainer is the layout parent for a Unit's children: VersaiEditor
// at the top and BoxesAndGlueInteractor below it. It replaces the old
// VSplitter with a vertical arrangement:
//
//   VE at (0, 0), width = 2/3 of container, height = 3 text lines
//   BAG at (20% of container width, VE.bottom + 2), height = 400px
//
// The container's own height is BAG.bottom (the enclosing MarginParent
// adds bottom margin outside).
//
// VE height is computed at first draw when font metrics are available.
type UnitContainer struct {
	impl.Interactor
	impl.Parent

	theme          mancini.Theme
	veFontSize     int64 // font size used to compute VE line height
	veLines        int   // number of text lines for VE height
	heightComputed bool  // true after first draw computes VE height

	// VE is a direct reference to the VersaiEditor for height computation,
	// since the VE may be nested inside a Row.
	VE mancini.Interactor

	// OnHeightChanged is called after the container computes its final
	// height (at first draw). The Unit uses this to update the
	// MarginParent's height = containerHeight + Top + Bottom margins.
	OnHeightChanged func(containerHeight int64)
}

// NewUnitContainer creates a UnitContainer with value Width/Height.
// veFontSize is the VE's font size (for line height computation).
// veLines is how many text lines tall the VE should be.
func NewUnitContainer(myName, parent string, theme mancini.Theme, _ mancini.Palette, veFontSize int64, veLines int) *UnitContainer {
	if myName == "" {
		myName = mancini.DefaultName("unitcont")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)

	uc := &UnitContainer{
		theme:      theme,
		veFontSize: veFontSize,
		veLines:    veLines,
	}
	uc.Interactor.Initialize(uc, lh)
	uc.Parent.Initialize(true, &uc.Interactor)
	return uc
}

// HeightURI returns the URI of this container's Height attribute.
func (uc *UnitContainer) HeightURI() string {
	return uc.GetLayout().Height.URI()
}

// Draw implements mancini.NewDrawer. Positions VE and BAG, computes
// VE height from font metrics on first draw.
func (uc *UnitContainer) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	dc := self.DC()
	if dc == nil {
		return
	}

	children := uc.GetChildren()
	if len(children) < 2 {
		return
	}

	// Children are in registration order: VE row first, BAG second.
	// Additional children (Throbber) are drawn after BAG.
	veRow := children[0]
	bag := children[1]

	// Compute VE height from font metrics on first draw.
	// The VE may be nested inside a Row, so use uc.VE directly.
	if !uc.heightComputed && uc.VE != nil {
		fontID := mancini.OpenFont(dc, uc.theme, mancini.None, uc.veFontSize)
		metrics := dc.GetFontMetrics(fontID)
		asc := float64(metrics.Ascent) / 64.0
		desc := float64(metrics.Descent) / 64.0
		lineHeight := asc + desc + 2 // 2px inter-line spacing (matches MLT)
		// MLT adds Padding (8px) top and bottom + BorderWidth (2px) top and bottom.
		veH := int64(lineHeight*float64(uc.veLines) + 0.5) + 8 + 8 + 2 + 2
		if vl, ok := uc.VE.(mancini.Layouter); ok {
			if vlh := vl.GetLayout(); vlh != nil {
				vlh.Height.Set(veH)
			}
		}
		uc.heightComputed = true
		fmt.Printf("[unitcontainer] VE height computed: %d (lineHeight=%.1f, lines=%d)\n",
			veH, lineHeight, uc.veLines)
	}

	// Position VE row: (0, 0), width = 2/3 of container.
	veRowW := w * 2 / 3
	if vl, ok := veRow.(mancini.Layouter); ok {
		if vlh := vl.GetLayout(); vlh != nil {
			vlh.X.Set(x)
			vlh.Y.Set(y)
			if !vlh.Width.IsConstraint() {
				vlh.Width.Set(veRowW)
			}
		}
	}

	// Read VE row's actual height.
	var veRowH int64
	if vl, ok := veRow.(mancini.Layouter); ok {
		if vlh := vl.GetLayout(); vlh != nil {
			veRowH = vlh.Height.Get()
		}
	}

	// Position BAG: (20% of container width, VERow.bottom + 2).
	bagX := x + w*20/100
	bagY := y + veRowH + 2
	bagW := w - (w * 20 / 100)
	if bl, ok := bag.(mancini.Layouter); ok {
		if blh := bl.GetLayout(); blh != nil {
			blh.X.Set(bagX)
			blh.Y.Set(bagY)
			if !blh.Width.IsConstraint() {
				blh.Width.Set(bagW)
			}
		}
	}

	// Read BAG's actual height.
	var bagH int64
	if bl, ok := bag.(mancini.Layouter); ok {
		if blh := bl.GetLayout(); blh != nil {
			bagH = blh.Height.Get()
		}
	}

	// Update container height = BAG.bottom.
	containerH := (bagY - y) + bagH
	lh := uc.GetLayout()
	oldH := lh.Height.Get()
	lh.Height.Set(containerH)
	if containerH != oldH && uc.OnHeightChanged != nil {
		uc.OnHeightChanged(containerH)
	}

	// Draw VE row (Row handles drawing its own children: label + VE).
	if cs, ok := veRow.(interface{ SetDC(mancini.DrawContext) }); ok {
		cs.SetDC(dc)
	}
	if d, ok := veRow.(mancini.NewDrawer); ok {
		d.Draw(veRow, x, y, veRowW, veRowH, damage)
	}

	// Draw BAG.
	if cs, ok := bag.(interface{ SetDC(mancini.DrawContext) }); ok {
		cs.SetDC(dc)
	}
	if d, ok := bag.(mancini.NewDrawer); ok {
		d.Draw(bag, bagX, bagY, bagW, bagH, damage)
	}

	// Draw additional children (Throbber, etc.) pinned to lower-left of container.
	for i := 2; i < len(children); i++ {
		extra := children[i]
		if el, ok := extra.(mancini.Layouter); ok {
			if elh := el.GetLayout(); elh != nil {
				extraH := elh.Height.Get()
				ex := x + 4
				ey := y + containerH - extraH - 4
				elh.X.Set(ex)
				elh.Y.Set(ey)
			}
		}
		if cs, ok := extra.(interface{ SetDC(mancini.DrawContext) }); ok {
			cs.SetDC(dc)
		}
		if d, ok := extra.(mancini.NewDrawer); ok {
			eW := int64(0)
			eH := int64(0)
			if el, ok := extra.(mancini.Layouter); ok {
				if elh := el.GetLayout(); elh != nil {
					eW = elh.Width.Get()
					eH = elh.Height.Get()
				}
			}
			d.Draw(extra, extra.X(), extra.Y(), eW, eH, damage)
		}
	}
}
