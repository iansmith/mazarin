package main

import (
	"image"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// RowPacked arranges children horizontally. All children except one
// designated child receive their natural width; the designated child
// expands to fill the remaining space. Children are top-aligned.
//
// Width and Height are value attributes set imperatively in Draw:
// Width tracks the parent container's width; Height is max of children.
type RowPacked struct {
	impl.Interactor
	impl.Parent

	// DesignatedIndex is the index (in registration order) of the child
	// that receives the remaining width. Set before first Draw.
	DesignatedIndex int

	layoutDone bool // set after first draw positions children
}

// NewRowPacked creates a RowPacked with value Width/Height attributes.
// The parent sets Width in Draw; Height is computed as max of children.
func NewRowPacked(myName, parentName string, designatedIndex int) *RowPacked {
	if myName == "" {
		myName = mancini.DefaultName("rowpacked")
	}

	lh := mancini.NewLayoutAttributes(myName, parentName)

	rp := &RowPacked{
		DesignatedIndex: designatedIndex,
	}
	rp.Interactor.Initialize(rp, lh)
	rp.Parent.Initialize(true, &rp.Interactor)
	return rp
}

// Draw implements mancini.NewDrawer. Positions children left-to-right,
// giving non-designated children their natural width and the designated
// child the remaining space. Updates own Height to max of children.
func (rp *RowPacked) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	if !rp.Damaged(damage) {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}

	children := rp.GetChildren()
	if len(children) == 0 {
		rp.SnapshotDamage()
		return
	}

	// First pass: read natural sizes, compute max height and others' width.
	var othersW int64
	var maxH int64
	for i, child := range children {
		if cl, ok := child.(mancini.Layouter); ok {
			clh := cl.GetLayout()
			ch := clh.Height.Get()
			if ch > maxH {
				maxH = ch
			}
			if i != rp.DesignatedIndex {
				othersW += clh.Width.Get()
			}
		}
	}

	// Update own height to max of children.
	lh := rp.GetLayout()
	if maxH > 0 {
		lh.Height.Set(maxH)
		h = maxH
	}

	// Designated child gets the remainder.
	gaps := int64(len(children) - 1)
	designatedW := w - othersW - gaps
	if designatedW < 0 {
		designatedW = 0
	}

	// Second pass: position children left-to-right, top-aligned.
	curX := x
	for i, child := range children {
		if i > 0 {
			curX += 1 // 1px gap between children
		}

		var childW int64
		if i == rp.DesignatedIndex {
			childW = designatedW
		} else if cl, ok := child.(mancini.Layouter); ok {
			childW = cl.GetLayout().Width.Get()
		}

		// Compute child Y: non-designated children shorter than the
		// row are vertically centered; designated child is top-aligned.
		childY := y
		var childH int64
		if cl, ok := child.(mancini.Layouter); ok {
			clh := cl.GetLayout()
			if clh != nil {
				childH = clh.Height.Get()
				if i != rp.DesignatedIndex && childH > 0 && childH < maxH {
					childY = y + (maxH-childH)/2
				}
				clh.X.Set(curX)
				clh.Y.Set(childY)
				if i == rp.DesignatedIndex && !clh.Width.IsConstraint() {
					clh.Width.Set(childW)
				}
			}
		}
		if childH <= 0 {
			childH = h
		}
		// Propagate DC and draw.
		if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
			cs.SetDC(dc)
		}
		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, curX, childY, childW, childH, damage)
		}

		curX += childW
	}

	rp.layoutDone = true
	rp.SnapshotDamage()
}
