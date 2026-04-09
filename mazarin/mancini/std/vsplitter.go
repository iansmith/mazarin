package std

import (
	"fmt"

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
// percentage split. Only three children are expected.
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

	// Left child: X=0, Y=0.
	setChildLayout(children[0], dc, x, y, leftW)
	// Center child (VLine): X = leftW + 1, Y=0.
	setChildLayout(children[1], dc, x+leftW+1, y, centerW)
	// Right child: X = w - rightW, Y=0.
	setChildLayout(children[2], dc, x+w-rightW, y, rightW)
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

func setChildLayout(child mancini.Interactor, dc mancini.DrawContext, cx, cy, cw int64) {
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
	if d, ok := child.(mancini.NewDrawer); ok {
		ch := int64(0)
		if l, ok := child.(mancini.Layouter); ok {
			clh := l.GetLayout()
			if clh != nil {
				ch = clh.Height.Get()
			}
		}
		d.Draw(child, cx, cy, cw, ch)
	}
}
