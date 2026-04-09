package std

import (
	"fmt"
	"time"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// VSplitter arranges three children horizontally: a left primary child,
// a center VLine separator, and a right primary child. Widths are
// assigned as percentages of the VSplitter's own width (default 48/4/48).
//
// The VLine separator is click-draggable: dragging it adjusts the left
// child's percentage, clamped to [MinPercent, MaxPercent] (default 30–70).
//
// VSplitter constrains only the VLine's height to match its own height.
// The two primary children's heights are managed externally.
type VSplitter struct {
	impl.Interactor
	impl.Parent

	Pal      mancini.Palette
	Percents [3]float64 // left, center, right percentages

	// Drag limits for the left child percentage.
	MinPercent float64 // default 30
	MaxPercent float64 // default 70

	// heightURI is the URI of this VSplitter's Height attribute,
	// used to constrain the VLine child.
	heightURI string

	// Drag state.
	dragging    bool
	dragStartX  int64   // cursor X at drag start
	dragStartPc float64 // left percent at drag start

	// Draw performance tracking.
	drawCount   int64
	drawTotalNs int64
}

// Compile-time check.
var _ mancini.ClickDraggable = (*VSplitter)(nil)

// NewVSplitter creates a VSplitter. The width comes from the parent;
// height is a value attribute. The VLine child should be created with
// its height constrained to HeightURI().
func NewVSplitter(myName, parent string, pal mancini.Palette) *VSplitter {
	if myName == "" {
		myName = mancini.DefaultName("vsplit")
	}

	lh := mancini.NewLayoutAttributesBase(myName, parent)
	lh.Width = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth), 0)
	lh.Height = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight), 0)
	lh.InitBounds(myName)

	vs := &VSplitter{
		Pal:        pal,
		Percents:   [3]float64{48, 4, 48},
		MinPercent: 30,
		MaxPercent: 70,
		heightURI:  lh.Height.URI(),
	}
	vs.Interactor.Initialize(vs, lh)
	vs.Parent.Initialize(true, &vs.Interactor)
	return vs
}

// HeightURI returns the URI of the VSplitter's Height attribute.
// Use this to constrain the VLine child's height.
func (vs *VSplitter) HeightURI() string {
	return vs.heightURI
}

// Draw positions the three children horizontally according to the
// percentage split. Only three children are expected. Layout is always
// propagated to all children, but Draw is skipped for undamaged children.
func (vs *VSplitter) Draw(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()
	if dc == nil {
		return
	}

	children := vs.GetChildren()
	if len(children) < 3 {
		return
	}

	leftW := int64(float64(w) * vs.Percents[0] / 100.0)
	centerW := int64(float64(w) * vs.Percents[1] / 100.0)
	rightW := w - leftW - centerW

	// Phase 1: always propagate layout to all children.
	propagateChildLayout(children[0], dc, x, y, leftW)
	propagateChildLayout(children[1], dc, x+leftW+1, y, centerW)
	propagateChildLayout(children[2], dc, x+w-rightW, y, rightW)

	// Get parent's damage rect to detect children whose pixels were
	// overwritten by a parent background fill.
	var parentDmg [4]int64
	if l, ok := self.(mancini.Layouter); ok {
		if lh := l.GetLayout(); lh != nil {
			x0, y0, x1, y1 := lh.GetDamageRect()
			parentDmg = [4]int64{x0, y0, x1, y1}
		}
	}

	// Phase 2: only draw children with damage or parent overlap.
	t0 := time.Now()
	drewLeft := drawChildIfDamaged(children[0], x, y, leftW, parentDmg)
	t1 := time.Now()
	drewCenter := drawChildIfDamaged(children[1], x+leftW+1, y, centerW, parentDmg)
	t2 := time.Now()
	drewRight := drawChildIfDamaged(children[2], x+w-rightW, y, rightW, parentDmg)
	t3 := time.Now()

	// Snapshot damage so the parent constraint settles.
	if l, ok := self.(mancini.Layouter); ok {
		if slh := l.GetLayout(); slh != nil {
			slh.SnapshotDamage()
		}
	}

	// Track running average for periodic reporting.
	vs.drawCount++
	vs.drawTotalNs += t3.Sub(t0).Nanoseconds()
	if vs.drawCount%10 == 0 {
		avgUs := vs.drawTotalNs / vs.drawCount / 1000
		fmt.Printf("[vsplitter:perf] n=%d avg=%dµs last: left=%v(%t) center=%v(%t) right=%v(%t)\n",
			vs.drawCount, avgUs,
			t1.Sub(t0), drewLeft, t2.Sub(t1), drewCenter, t3.Sub(t2), drewRight)
	}
}

// ClickDragStart implements mancini.ClickDraggable.
// Accepts the drag if the press is in the center (VLine) region.
func (vs *VSplitter) ClickDragStart(ev *mancini.InputEvent) bool {
	w := vs.W()
	if w <= 0 {
		return false
	}

	leftW := int64(float64(w) * vs.Percents[0] / 100.0)
	centerW := int64(float64(w) * vs.Percents[1] / 100.0)

	// Accept if click is in the center child's X range.
	cx := leftW
	if ev.X < cx || ev.X >= cx+centerW {
		return false
	}

	vs.dragging = true
	vs.dragStartX = ev.X
	vs.dragStartPc = vs.Percents[0]
	fmt.Printf("[vsplitter] drag start at x=%d, left=%.1f%%\n", ev.X, vs.Percents[0])
	return true
}

// ClickDragMove implements mancini.ClickDraggable.
func (vs *VSplitter) ClickDragMove(ev *mancini.InputEvent, _ *mancini.InputEvent, outsideBounds bool) bool {
	if !vs.dragging {
		return false
	}

	w := vs.W()
	if w <= 0 {
		return true
	}

	// Convert pixel delta to percentage delta.
	dx := ev.X - vs.dragStartX
	dpct := float64(dx) * 100.0 / float64(w)
	newLeft := vs.dragStartPc + dpct

	// Clamp.
	if newLeft < vs.MinPercent {
		newLeft = vs.MinPercent
	}
	if newLeft > vs.MaxPercent {
		newLeft = vs.MaxPercent
	}

	vs.Percents[0] = newLeft
	vs.Percents[2] = 100.0 - newLeft - vs.Percents[1]
	fmt.Printf("[vsplitter] drag: evX=%d startX=%d dx=%d dpct=%.1f newLeft=%.1f%% w=%d\n",
		ev.X, vs.dragStartX, dx, dpct, newLeft, w)
	vs.FullDamage()
	return true
}

// ClickDragEnd implements mancini.ClickDraggable.
func (vs *VSplitter) ClickDragEnd(ev *mancini.InputEvent, outsideBounds bool) bool {
	if !vs.dragging {
		return false
	}
	vs.dragging = false
	fmt.Printf("[vsplitter] drag end, left=%.1f%%\n", vs.Percents[0])
	vs.FullDamage()
	return true
}

// propagateChildLayout sets position, width, and DrawContext on a child
// without drawing it. This always runs so layout attributes stay correct.
func propagateChildLayout(child mancini.Interactor, dc mancini.DrawContext, cx, cy, cw int64) {
	if l, ok := child.(mancini.Layouter); ok {
		clh := l.GetLayout()
		if clh != nil {
			clh.X.Set(cx)
			clh.Y.Set(cy)
			if !clh.Width.IsConstraint() {
				clh.Width.Set(cw)
			}
		}
	}
	if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
		cs.SetDC(dc)
	}
}

// drawChildIfDamaged calls Draw on the child only if it has pending damage
// or the parent's damage rect overlaps the child's area (meaning the parent's
// background fill may have overwritten the child's pixels).
func drawChildIfDamaged(child mancini.Interactor, cx, cy, cw int64, parentDamage [4]int64) bool {
	ch := int64(0)
	if l, ok := child.(mancini.Layouter); ok {
		clh := l.GetLayout()
		if clh != nil {
			ch = clh.Height.Get()
		}
	}
	if !mancini.ChildHasDamage(child) {
		// Even if the child has no damage, it must redraw if the parent's
		// damage rect overlaps this child (parent background fill wiped it).
		px0, py0, px1, py1 := parentDamage[0], parentDamage[1], parentDamage[2], parentDamage[3]
		if px0 == 0 && py0 == 0 && px1 == 0 && py1 == 0 {
			return false // no parent damage at all
		}
		// Check overlap: parent damage vs child rect.
		if cx+cw <= px0 || cx >= px1 || cy+ch <= py0 || cy >= py1 {
			return false // no overlap
		}
	}
	if d, ok := child.(mancini.NewDrawer); ok {
		d.Draw(child, cx, cy, cw, ch)
	}
	return true
}
