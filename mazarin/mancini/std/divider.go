package std

import (
	"image"
	"image/color"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Divider is a column-boundary indicator drawn as a black triangular
// marker on the bezel area above and below the parent grid. The
// triangle's tip points at the column boundary, with a black bezel
// rectangle covering the half of the triangle nearest the bezel edge
// (away from the grid). Together they look like a draggable thumb on
// a bezel "track" that points at the column split.
//
// Dividers implement [mancini.DetailedHit] for marker-shaped pick
// regions and [mancini.ClickDraggable] for drag-to-resize behavior.
// Dragging adjusts the split attribute, which propagates through the
// constraint network to update column widths. Both top and bottom
// markers move together since they share the same split position.
type Divider struct {
	impl.Interactor

	Pal      mancini.Palette
	ColIndex int // which column boundary (0 = after col 0, etc.)

	// Overhang is the height of the bezel area above and below the
	// parent grid where the marker is drawn. The marker's bezel-side
	// rectangle (TriHeight/2 tall) sits in this overhang area; the
	// triangle tip extends back across the grid edge by Dangle pixels.
	Overhang int64
	// TriHeight is the height of each triangle in pixels. The bezel
	// rectangle covers half of this height.
	TriHeight int64
	// TriHalfW is the half-width of each triangle's base in pixels.
	// The bezel rectangle has the same total width (2 * TriHalfW).
	TriHalfW int64
	// Dangle is how far the triangle tip extends INTO the grid past
	// the grid edge, so the marker visually "points to" a column
	// boundary inside the grid area.
	Dangle int64

	// MinPct, MaxPct clamp the column percentage during drag.
	MinPct, MaxPct int64

	// splitAttr is the column percentage attribute this divider controls.
	splitAttr *attr.Attribute[int64]

	// parentWidth is cached each frame by the GridTable for drag math.
	parentWidth int64

	// Drag state.
	dragging    bool
	dragStartX  int64
	dragStartPc int64 // split percent at drag start
}

// Compile-time interface checks.
var _ mancini.ClickDraggable = (*Divider)(nil)
var _ mancini.DetailedHit = (*Divider)(nil)

// NewDivider creates a Divider for the column boundary at colIndex.
// splitAttr is the attribute holding the column's percentage — the
// divider reads and writes this attribute during drag operations.
func NewDivider(myName, parent string, pal mancini.Palette,
	colIndex int, splitAttr *attr.Attribute[int64]) *Divider {

	if myName == "" {
		myName = mancini.DefaultName("divider")
	}

	lh := mancini.NewLayoutAttributesBase(myName, parent)
	lh.Width = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth), 0)
	lh.Height = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight), 0)
	lh.InitBounds(myName)

	d := &Divider{
		Pal:       pal,
		ColIndex:  colIndex,
		Overhang:  4,
		TriHeight: 5,
		TriHalfW:  7,
		Dangle:    2,
		MinPct:    10,
		MaxPct:    60,
		splitAttr: splitAttr,
	}
	d.Interactor.Initialize(d, lh)
	return d
}

// Draw renders the two markers. Each marker is a black triangle with
// its tip at the grid edge (pointing at the column boundary), with a
// black rectangle covering the bezel-side half of the triangle. The
// triangle plus rectangle together form a "thumb on a bezel" shape:
// the rectangle is the wide handle resting on the bezel surface, and
// the triangle protrudes from the rectangle pointing at the column.
//
// Geometry (in divider's local x,y,w,h):
//
//	Top bezel:     midX
//	  baseY ───►  ███████████   (rect: width 2*TriHalfW, height TriHeight/2)
//	              ███████████
//	              \         /
//	               \       /
//	                \     /
//	                 \   /
//	  tipY ────►      \ /     ← grid top edge, tip points down
//	  =================================  grid
//
//	Bottom bezel:                       grid
//	  =================================
//	  tipY ────►      / \     ← grid bottom edge, tip points up
//	                 /   \
//	                /     \
//	               /       \
//	              /         \
//	  baseY ───► ███████████   (rect)
//	              ███████████
func (d *Divider) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	dc := self.DC()
	if dc == nil {
		return
	}

	midX := float64(x) + float64(w)/2
	hw := float64(d.TriHalfW)
	th := float64(d.TriHeight)
	oh := float64(d.Overhang)
	dangle := float64(d.Dangle)
	rectH := th / 2
	black := color.NRGBA{R: 0, G: 0, B: 0, A: 255}

	// Grid edges in divider's local coords: divider extends Overhang
	// above and below the grid, so the grid spans [y+Overhang, y+h-Overhang].
	gridTopY := float64(y) + oh
	gridBotY := float64(y+h) - oh

	// ── Top marker ──
	// Triangle tip extends BELOW the grid top edge by `dangle` pixels
	// so it visually points into the grid. Base sits in the bezel area.
	topTipY := gridTopY + dangle
	topBaseY := topTipY - th
	dc.SetColor(black)
	dc.MoveTo(midX-hw, topBaseY)
	dc.LineTo(midX+hw, topBaseY)
	dc.LineTo(midX, topTipY)
	dc.ClosePath()
	dc.Fill()
	// Bezel rect over the BASE half (top half = bezel side, away from grid).
	dc.SetColor(black)
	dc.DrawRectangle(midX-hw, topBaseY, 2*hw, rectH)
	dc.Fill()

	// ── Bottom marker ──
	// Triangle tip extends ABOVE the grid bottom edge by `dangle` pixels
	// (Y decreases going up). Base sits in the bezel area below.
	botTipY := gridBotY - dangle
	botBaseY := botTipY + th
	dc.SetColor(black)
	dc.MoveTo(midX-hw, botBaseY)
	dc.LineTo(midX+hw, botBaseY)
	dc.LineTo(midX, botTipY)
	dc.ClosePath()
	dc.Fill()
	// Bezel rect over the BASE half (bottom half = bezel side, away from grid).
	dc.SetColor(black)
	dc.DrawRectangle(midX-hw, botBaseY-rectH, 2*hw, rectH)
	dc.Fill()
}

// DetailedHit implements mancini.DetailedHit. Returns true if the
// point is inside either marker (triangle + bezel rect). localX and
// localY are in the divider's own coordinate frame (0,0 = top-left
// of divider bounds).
//
// Top marker tip extends into the grid by Dangle pixels:
//   - [oh-th+dangle, oh-th/2+dangle]: bezel rect, full base width 2*hw
//   - [oh-th/2+dangle, oh+dangle]:    visible triangle tip half, narrows
//
// Bottom marker tip extends into the grid by Dangle pixels:
//   - [h-oh-dangle, h-oh+th/2-dangle]:    visible triangle tip half, widens
//   - [h-oh+th/2-dangle, h-oh+th-dangle]: bezel rect, full base width 2*hw
func (d *Divider) DetailedHit(localX, localY int64) bool {
	h := d.H()
	w := d.W()
	midX := w / 2
	th := d.TriHeight
	hw := d.TriHalfW
	oh := d.Overhang
	dangle := d.Dangle
	halfTH := th / 2

	// Top marker (tip dangling into grid by `dangle`).
	topTip := oh + dangle
	topBase := topTip - th
	topMid := topBase + halfTH
	if localY >= topBase && localY <= topTip {
		if localY <= topMid {
			// Bezel rect: full base width.
			if localX >= midX-hw && localX <= midX+hw {
				return true
			}
		} else {
			// Visible triangle tip: width tapers from hw at top to 0 at tip.
			distFromMid := localY - topMid                 // 0..halfTH
			halfSpan := hw * (halfTH - distFromMid) / halfTH // hw..0
			if localX >= midX-halfSpan && localX <= midX+halfSpan {
				return true
			}
		}
	}

	// Bottom marker (tip dangling into grid by `dangle`).
	botTip := h - oh - dangle
	botBase := botTip + th
	botMid := botTip + halfTH
	if localY >= botTip && localY <= botBase {
		if localY <= botMid {
			// Visible triangle tip: width widens from 0 at tip to hw at mid.
			distFromTip := localY - botTip                    // 0..halfTH
			halfSpan := hw * distFromTip / halfTH             // 0..hw
			if localX >= midX-halfSpan && localX <= midX+halfSpan {
				return true
			}
		} else {
			// Bezel rect: full base width.
			if localX >= midX-hw && localX <= midX+hw {
				return true
			}
		}
	}

	return false
}

// ClickDragStart implements mancini.ClickDraggable.
func (d *Divider) ClickDragStart(ev *mancini.InputEvent) bool {
	d.dragging = true
	d.dragStartX = ev.X
	d.dragStartPc = d.splitAttr.Get()
	return true
}

// ClickDragMove implements mancini.ClickDraggable.
func (d *Divider) ClickDragMove(ev *mancini.InputEvent, startEv *mancini.InputEvent, outsideBounds bool) bool {
	if !d.dragging || d.parentWidth <= 0 {
		return true
	}

	dx := ev.X - d.dragStartX
	dpct := int64(float64(dx) * 100.0 / float64(d.parentWidth))
	newPct := d.dragStartPc + dpct

	// Clamp to configured range.
	if newPct < d.MinPct {
		newPct = d.MinPct
	}
	if newPct > d.MaxPct {
		newPct = d.MaxPct
	}

	d.splitAttr.Set(newPct)
	return true
}

// ClickDragEnd implements mancini.ClickDraggable.
func (d *Divider) ClickDragEnd(ev *mancini.InputEvent, outsideBounds bool) bool {
	d.dragging = false
	return true
}
