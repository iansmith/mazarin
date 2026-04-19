package std

import (
	"image"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Divider is a column-boundary resize handle drawn in the bezel strips
// above and below a GridFrame. Each bezel shows a thin vertical track
// line at the column boundary and a small rounded-rectangle pill
// (capsule) centred in the bezel. Three horizontal grip lines inside
// the pill hint at draggability.
//
// The pill colour is Pal.Mid() at rest and Pal.Accent() while dragging.
//
// Dividers implement [mancini.DetailedHit] for pill-shaped pick regions
// and [mancini.ClickDraggable] for drag-to-resize behaviour. Both top
// and bottom markers move together since they share the same split
// position.
type Divider struct {
	impl.Interactor

	Pal      mancini.Palette
	ColIndex int // which column boundary (0 = after col 0, etc.)

	// Overhang is the height of the bezel area above and below the
	// parent grid where the marker is drawn.
	Overhang  int64
	TriHeight int64
	TriHalfW  int64
	Dangle    int64

	// MinPct is the minimum percentage this divider's column may take.
	// MaxPct is a static upper bound; OtherSplitAttr provides a dynamic
	// upper bound that prevents the two dividers from crossing.
	MinPct, MaxPct int64

	// DragInverted reverses the drag direction. Use true for a divider
	// that controls the column to its RIGHT (e.g. the Date column): when
	// the user drags right the right column narrows, so its percentage
	// must decrease rather than increase.
	DragInverted bool

	// SplitAttr is the writable column-percentage attribute this divider
	// controls. Exported so GridFrame can cross-link paired dividers.
	SplitAttr *attr.Attribute[int64]

	// OtherSplitAttr, when set, is the paired divider's SplitAttr. Used
	// during drag to compute a dynamic upper bound that keeps the flex
	// column at least MinFlexPct wide and prevents the dividers from
	// crossing visually.
	OtherSplitAttr *attr.Attribute[int64]

	// MinFlexPct is the minimum percentage left for the flex column when
	// both dividers are constrained against each other. Defaults to 5.
	MinFlexPct int64

	// RawXAttr is a value attribute set by the parent GridFrame each draw
	// cycle with the unclamped pixel X of this divider's left edge (splitX - hw).
	// Used as the source for the X constraint program so other attrs can
	// subscribe without creating a cycle through the constrained lh.X.
	RawXAttr *attr.Attribute[int64]

	// RightXAttr is a value attribute updated by the parent GridFrame every
	// draw cycle with the raw pixel X of this divider's right marker edge
	// (splitX + hw). Set from the unclamped position so the paired divider's
	// floor constraint reads a stable value independent of clamping.
	RightXAttr *attr.Attribute[int64]

	// OnDamage is called after every drag event. GridFrame sets this to
	// damage all DynamicLabel leaf cells, expanding the blit region to
	// the full grid width so the revealed column area is sent to the display.
	OnDamage func()

	// ParentXAttr and ParentWidthAttr are value attributes set by GridFrame
	// each draw cycle. Exposed as attrs so ScaleI64-based constraints can
	// read them reactively (e.g. RawXAttr = pct * parentWidth / 100 + parentX - hw).
	ParentXAttr     *attr.Attribute[int64]
	ParentWidthAttr *attr.Attribute[int64]

	// Drag state. dragStartX is the mouse X at drag start; dragStartSplitX
	// is the column-boundary pixel at drag start (RawXAttr + hw). Using the
	// absolute split pixel as the canonical source avoids integer rounding
	// drift that accumulates when using percentage deltas.
	dragging        bool
	dragStartX      int64
	dragStartSplitX int64
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

	rawXURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutProp("rawX"))
	rightXURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutProp("rightX"))
	parentXURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutProp("parentX"))
	parentWURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutProp("parentW"))

	d := &Divider{
		Pal:             pal,
		ColIndex:        colIndex,
		Overhang:        32,
		TriHeight:       20,
		TriHalfW:        20,
		Dangle:          0,
		MinPct:          5,
		MaxPct:          90,
		MinFlexPct:      5,
		SplitAttr:       splitAttr,
		RawXAttr:        attr.ValueI64(rawXURI, 0),
		RightXAttr:      attr.ValueI64(rightXURI, 0),
		ParentXAttr:     attr.ValueI64(parentXURI, 0),
		ParentWidthAttr: attr.ValueI64(parentWURI, 0),
	}
	d.Interactor.Initialize(d, lh)
	// Eagerly register the DamageRect attribute so the parent's damage
	// constraint can subscribe to it on the first repaint.
	lh.FullDamage()
	return d
}

// SetupXConstraint replaces the divider's layout X value attribute with a
// constraint that prevents this divider from visually overlapping other.
//
// For a non-inverted (left) divider: X = min(rawX, other.RawX − (2*hw+1)).
// This ensures this divider's right edge stays strictly left of other's left edge.
//
// For an inverted (right) divider: X = max(rawX, other.RightX).
// This ensures this divider never moves left of other's right edge.
//
// Both constraints read only VALUE attributes from other (RawXAttr /
// RightXAttr), so there is no circular dependency between the two constraints.
// Call this once from the parent after both dividers exist.
func (d *Divider) SetupXConstraint(other *Divider) {
	lh := d.GetLayout()
	divW := d.TriHalfW * 2

	if d.DragInverted {
		// Right clip: X must be ≥ other's raw right edge.
		prog := mancini.BindStrings(mancini.ProgMaxDeref,
			"_source_", d.RawXAttr.URI(),
			"_floor_", other.RightXAttr.URI())
		attr.SwapToConstraint(lh.X, prog)
	} else {
		// Left clip: right edge (X + divW) must be < other's raw left edge.
		// Ceiling = other.RawXAttr - (divW + 1).
		// Build an intermediate constraint attr for the ceiling.
		offsetURI := mancini.LayoutURI(
			d.GetLayout().Name(), mancini.DataTypeInt64, mancini.LayoutProp("XCeilOff"))
		attr.ValueI64(offsetURI, divW+1) // constant; no finalizer — slot persists in store
		ceilURI := mancini.LayoutURI(
			d.GetLayout().Name(), mancini.DataTypeInt64, mancini.LayoutProp("XCeil"))
		attr.ConstraintI64(ceilURI, mancini.SubI64(other.RawXAttr.URI(), offsetURI))

		prog := mancini.BindStrings(mancini.ProgMinDeref,
			"_source_", d.RawXAttr.URI(),
			"_ceiling_", ceilURI)
		attr.SwapToConstraint(lh.X, prog)
	}
}

// SetupPositionConstraints wires RawXAttr and RightXAttr as ScaleI64-based
// constraints so the divider's pixel position reacts to both SplitAttr
// (percentage) and ParentXAttr / ParentWidthAttr (grid geometry) without
// any imperative position computation in GridFrame.Draw.
//
// Non-inverted (left): rawX = pct * parentW / 100 + parentX - hw
//
// Inverted (right):    rawX = parentX + parentW - pct * parentW / 100 - hw
//
// Call this once from NewGridFrame after all dividers are created, before
// SetupXConstraint (which references RawXAttr.URI()).
func (d *Divider) SetupPositionConstraints() {
	name := d.GetLayout().Name()
	hw := d.TriHalfW

	hundredURI := mancini.LayoutURI(name, mancini.DataTypeInt64, mancini.LayoutProp("const100"))
	attr.ValueI64(hundredURI, 100)
	hwURI := mancini.LayoutURI(name, mancini.DataTypeInt64, mancini.LayoutProp("constHW"))
	attr.ValueI64(hwURI, hw)
	negHWURI := mancini.LayoutURI(name, mancini.DataTypeInt64, mancini.LayoutProp("constNegHW"))
	attr.ValueI64(negHWURI, -hw)
	zeroURI := mancini.LayoutURI(name, mancini.DataTypeInt64, mancini.LayoutProp("constZero"))
	attr.ValueI64(zeroURI, 0)

	// scaledPct = SplitAttr * parentWidth / 100  (pixel offset of column boundary from gridX)
	scaledPctURI := mancini.LayoutURI(name, mancini.DataTypeInt64, mancini.LayoutProp("scaledPct"))
	attr.ConstraintI64(scaledPctURI, mancini.ScaleI64(
		d.SplitAttr.URI(), d.ParentWidthAttr.URI(), hundredURI))

	if !d.DragInverted {
		// rawX = scaledPct + parentX - hw
		attr.SwapToConstraint(d.RawXAttr,
			mancini.AddSubI64(scaledPctURI, d.ParentXAttr.URI(), hwURI))
		// rightX = scaledPct + parentX - (-hw) = scaledPct + parentX + hw
		attr.SwapToConstraint(d.RightXAttr,
			mancini.AddSubI64(scaledPctURI, d.ParentXAttr.URI(), negHWURI))
	} else {
		// hwPlusScale = scaledPct + hw  (used to compute rawX for inverted divider)
		hwPlusScaleURI := mancini.LayoutURI(name, mancini.DataTypeInt64, mancini.LayoutProp("hwPlusScale"))
		attr.ConstraintI64(hwPlusScaleURI, mancini.AddSubI64(scaledPctURI, hwURI, zeroURI))
		// scaleMinusHW = scaledPct - hw  (used to compute rightX for inverted divider)
		scaleMinusHWURI := mancini.LayoutURI(name, mancini.DataTypeInt64, mancini.LayoutProp("scaleMinusHW"))
		attr.ConstraintI64(scaleMinusHWURI, mancini.AddSubI64(scaledPctURI, zeroURI, hwURI))
		// rawX = parentX + parentW - (scaledPct + hw)
		attr.SwapToConstraint(d.RawXAttr,
			mancini.AddSubI64(d.ParentXAttr.URI(), d.ParentWidthAttr.URI(), hwPlusScaleURI))
		// rightX = parentX + parentW - (scaledPct - hw)
		attr.SwapToConstraint(d.RightXAttr,
			mancini.AddSubI64(d.ParentXAttr.URI(), d.ParentWidthAttr.URI(), scaleMinusHWURI))
	}
}

// Draw renders the two bezel markers. Each marker is a thin vertical track
// line spanning the full bezel height, with a small rounded-rectangle pill
// centred in the bezel. Three horizontal grip lines inside the pill hint at
// draggability. Pill colour is Pal.Mid() at rest and Pal.Accent() when dragging.
func (d *Divider) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	dc := self.DC()
	if dc == nil {
		return
	}

	midX := float64(x) + float64(w)/2
	oh := float64(d.Overhang)
	capH := float64(d.TriHeight) // capsule height (TriHeight reused)
	capHW := 5.0                 // visual capsule half-width → 10px total
	capR := 4.0                  // corner radius

	col := d.Pal.Mid()
	if d.dragging {
		col = d.Pal.Accent()
	}

	// Bezel vertical centres.
	topCY := float64(y) + oh/2
	botCY := float64(y+h) - oh/2

	// Thin track lines across each bezel.
	dc.SetLineWidth(1.0)
	dc.SetColor(col)
	dc.DrawLine(midX, float64(y), midX, float64(y)+oh)
	dc.DrawLine(midX, float64(y+h)-oh, midX, float64(y+h))
	dc.Stroke()

	// Rounded pills centred in each bezel.
	dc.SetColor(col)
	dc.DrawRoundedRectangle(midX-capHW, topCY-capH/2, capHW*2, capH, capR)
	dc.Fill()
	dc.DrawRoundedRectangle(midX-capHW, botCY-capH/2, capHW*2, capH, capR)
	dc.Fill()

	// Horizontal grip lines inside each pill.
	gripW := capHW - 2
	dc.SetLineWidth(1.0)
	dc.SetColor(d.Pal.Surface())
	for _, dy := range []float64{-4, 0, 4} {
		dc.DrawLine(midX-gripW, topCY+dy, midX+gripW, topCY+dy)
		dc.DrawLine(midX-gripW, botCY+dy, midX+gripW, botCY+dy)
	}
	dc.Stroke()
}

// DetailedHit implements mancini.DetailedHit. Returns true if the point
// falls inside either capsule pill. The hit area uses TriHalfW (wider
// than the 5px visual half-width) for easy grabbing.
func (d *Divider) DetailedHit(localX, localY int64) bool {
	h := d.H()
	w := d.W()
	midX := w / 2
	capH := d.TriHeight
	hw := d.TriHalfW
	oh := d.Overhang

	topCY := oh / 2
	botCY := h - oh/2

	if localX >= midX-hw && localX <= midX+hw {
		if localY >= topCY-capH/2 && localY <= topCY+capH/2 {
			return true
		}
		if localY >= botCY-capH/2 && localY <= botCY+capH/2 {
			return true
		}
	}
	return false
}

// notifyDamage calls FullDamage on the divider itself (so the bezel strips
// are included in the damage union) and then calls OnDamage if set. OnDamage
// is provided by the GridTable to damage all DynamicLabel cells, expanding
// the blit region to the full grid width so the revealed column area is
// sent to the display.
func (d *Divider) notifyDamage() {
	d.FullDamage()
	if d.OnDamage != nil {
		d.OnDamage()
	}
}

// ClickDragStart implements mancini.ClickDraggable.
func (d *Divider) ClickDragStart(ev *mancini.InputEvent) bool {
	d.dragging = true
	d.dragStartX = ev.X
	d.dragStartSplitX = d.RawXAttr.Get() + d.TriHalfW // column boundary pixel
	d.notifyDamage()
	return true
}

// ClickDragMove implements mancini.ClickDraggable.
//
// The handle's absolute pixel position (dragStartSplitX + mouse delta) is
// the input; the column percentage is derived from that position and
// parentWidth. This avoids accumulated integer-rounding drift from the
// delta-percentage approach.
func (d *Divider) ClickDragMove(ev *mancini.InputEvent, startEv *mancini.InputEvent, outsideBounds bool) bool {
	if !d.dragging || d.ParentWidthAttr.Get() <= 0 {
		return true
	}

	parentX := d.ParentXAttr.Get()
	parentWidth := d.ParentWidthAttr.Get()
	targetSplitX := d.dragStartSplitX + (ev.X - d.dragStartX)
	var newPct int64
	if d.DragInverted {
		newPct = int64(float64(parentX+parentWidth-targetSplitX) * 100.0 / float64(parentWidth))
	} else {
		newPct = int64(float64(targetSplitX-parentX) * 100.0 / float64(parentWidth))
	}

	// Static floor.
	if newPct < d.MinPct {
		newPct = d.MinPct
	}

	// Dynamic ceiling: prevent flex column from shrinking below MinFlexPct,
	// and prevent the two dividers from crossing.
	if d.OtherSplitAttr != nil {
		otherPct := d.OtherSplitAttr.Get()
		minFlex := d.MinFlexPct
		if minFlex <= 0 {
			minFlex = 5
		}
		maxAllowed := 100 - otherPct - minFlex
		if maxAllowed < d.MinPct {
			maxAllowed = d.MinPct
		}
		if newPct > maxAllowed {
			newPct = maxAllowed
		}
	} else if newPct > d.MaxPct {
		newPct = d.MaxPct
	}

	d.SplitAttr.Set(newPct)
	d.notifyDamage()
	return true
}

// ClickDragEnd implements mancini.ClickDraggable.
func (d *Divider) ClickDragEnd(ev *mancini.InputEvent, outsideBounds bool) bool {
	d.dragging = false
	d.notifyDamage()
	return true
}
