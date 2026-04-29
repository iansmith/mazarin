package std

import (
	"fmt"
	"image"
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

	// Pal is the [mancini.Palette] used for default child backgrounds.
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

	// In-app focus: set by the application after construction.
	// KeyFocusAgent is the KeyAgent that routes keyboard events.
	// KeyFocusTarget is the interactor that should receive keyboard input
	// when this VSplitter is clicked (e.g., the editor inside a unit).
	// FocusAttr is this VSplitter's focused attribute (true = has focus).
	// FocusPeers are all VSplitters in the same focus group (including self).
	KeyFocusAgent  *mancini.KeyAgent
	KeyFocusTarget mancini.Interactor
	FocusAttr      *attr.Attribute[bool]
	FocusPeers     []*VSplitter
}

// Compile-time checks.
var _ mancini.ClickDraggable = (*VSplitter)(nil)
var _ mancini.Clickable = (*VSplitter)(nil)

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
func (vs *VSplitter) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	dc := self.DC()
	if dc == nil {
		return
	}

	children := vs.GetChildren()

	// Get parent's damage rect to detect children whose pixels were
	// overwritten by a parent background fill.
	var parentDmg [4]int64
	if l, ok := self.(mancini.Layouter); ok {
		if lh := l.GetLayout(); lh != nil {
			x0, y0, x1, y1 := lh.GetDamageRect()
			parentDmg = [4]int64{x0, y0, x1, y1}
		}
	}

	// Phase 1: lay out and draw the three percentage-based panes if present.
	var t0, t1, t2, t3 time.Time
	var drewLeft, drewCenter, drewRight bool
	if len(children) >= 3 {
		leftW := int64(float64(w) * vs.Percents[0] / 100.0)
		centerW := int64(float64(w) * vs.Percents[1] / 100.0)
		rightW := w - leftW - centerW

		propagateChildLayout(children[0], dc, x, y, leftW)
		propagateChildLayout(children[1], dc, x+leftW+1, y, centerW)
		propagateChildLayout(children[2], dc, x+w-rightW, y, rightW)

		t0 = time.Now()
		drewLeft = drawChildIfDamaged(children[0], x, y, leftW, parentDmg, damage)
		t1 = time.Now()
		drewCenter = drawChildIfDamaged(children[1], x+leftW+1, y, centerW, parentDmg, damage)
		t2 = time.Now()
		drewRight = drawChildIfDamaged(children[2], x+w-rightW, y, rightW, parentDmg, damage)
		t3 = time.Now()
	}

	// Phase 2: draw all additional children (e.g., throbber).
	// Position each extra child at the lower-left corner of the
	// right (BAG) pane. X/Y are set to absolute screen coordinates
	// so Pick sees consistent values.
	rightX := x // fallback
	if len(children) >= 3 {
		rightW := w - int64(float64(w)*vs.Percents[0]/100.0) - int64(float64(w)*vs.Percents[1]/100.0)
		rightX = x + w - rightW
	}
	for i := 3; i < len(children); i++ {
		extra := children[i]
		if l, ok := extra.(mancini.Layouter); ok {
			elh := l.GetLayout()
			if elh != nil {
				eh := elh.Height.Get()
				// Pin to lower-left of right pane with 4px padding.
				ex := rightX + 4
				ey := y + h - eh - 4
				elh.X.Set(ex)
				elh.Y.Set(ey)
			}
		}
		if cs, ok := extra.(interface{ SetDC(mancini.DrawContext) }); ok {
			cs.SetDC(dc)
		}
		if d, ok := extra.(mancini.NewDrawer); ok {
			if l, ok := extra.(mancini.Layouter); ok {
				elh := l.GetLayout()
				if elh != nil {
					d.Draw(extra, elh.X.Get(), elh.Y.Get(),
						elh.Width.Get(), elh.Height.Get(), damage)
				}
			}
		}
	}

	// Snapshot damage so the parent constraint settles.
	if l, ok := self.(mancini.Layouter); ok {
		if slh := l.GetLayout(); slh != nil {
			slh.SnapshotDamage()
		}
	}

	// Track running average for periodic reporting.
	if !t0.IsZero() {
		vs.drawCount++
		vs.drawTotalNs += t3.Sub(t0).Nanoseconds()
		if vs.drawCount%10 == 0 {
			avgUs := vs.drawTotalNs / vs.drawCount / 1000
			fmt.Printf("[vsplitter:perf] n=%d avg=%dµs last: left=%v(%t) center=%v(%t) right=%v(%t)\n",
				vs.drawCount, avgUs,
				t1.Sub(t0), drewLeft, t2.Sub(t1), drewCenter, t3.Sub(t2), drewRight)
		}
	}
}

// ClickDragStart implements mancini.ClickDraggable.
// Accepts the drag if the press is in the center (VLine) region.
// Also claims in-app focus at the start of the drag.
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

	vs.SetFocusToSelf()
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

// SetFocusToSelf claims in-app keyboard focus for this VSplitter.
// It walks FocusPeers, setting each peer's FocusAttr to false except
// this one (true), and directs the KeyAgent to deliver keyboard events
// to KeyFocusTarget. Called from this VSplitter's own Click handler
// and from child interactors (VE, Throbber) at the start of their
// interactions.
func (vs *VSplitter) SetFocusToSelf() {
	if vs.KeyFocusAgent == nil || vs.KeyFocusTarget == nil {
		return
	}
	for _, peer := range vs.FocusPeers {
		isSelf := peer == vs
		if peer.FocusAttr != nil {
			peer.FocusAttr.Set(isSelf)
		}
		// Toggle the Focused bool on each peer's focus target (e.g.,
		// MultiLineText.Focused controls cursor rendering).
		if peer.KeyFocusTarget != nil {
			if f, ok := peer.KeyFocusTarget.(interface{ SetFocused(bool) }); ok {
				f.SetFocused(isSelf)
			}
		}
	}
	vs.KeyFocusAgent.SetFocus(vs.KeyFocusTarget)
	fmt.Printf("[vsplitter] SetFocusToSelf\n")
}

// Click implements mancini.Clickable. Catches clicks that fall through
// from non-interactive children (e.g., BAG) and claims focus.
func (vs *VSplitter) Click(ev *mancini.InputEvent) bool {
	vs.SetFocusToSelf()
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
func drawChildIfDamaged(child mancini.Interactor, cx, cy, cw int64, parentDamage [4]int64, damage image.Rectangle) bool {
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
		d.Draw(child, cx, cy, cw, ch, damage)
	}
	return true
}
