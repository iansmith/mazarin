package std

import (
	"math"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// MarginParent is a single-child container that enforces independent
// margins on each side around its child. It can optionally draw a
// rounded-rectangle border inside the margin area and a centered
// label in the top margin that interrupts the border.
//
// Width and Height can be value attributes (set by parent) or constrained.
type MarginParent struct {
	impl.Interactor
	impl.Parent

	Top, Right, Bottom, Left int64 // margins in pixels

	// BorderWidth draws a rounded-rectangle border inside the margin.
	// Must be ≤ all four margins. 0 = no border.
	BorderWidth int64

	// Label is drawn centered in the top margin using the theme's
	// default sans font at 75% of Top margin height. Empty = no label.
	Label string

	theme    mancini.Theme
	pal      mancini.Palette
	textFace mancini.LatinTextFace
}

// NewMarginParent creates a MarginParent with independent margins.
// Width and Height are value attributes (set by parent container).
// label may be empty for no label text.
func NewMarginParent(myName, parent string,
	top, right, bottom, left int64,
	borderWidth int64, label string,
	theme mancini.Theme, pal mancini.Palette) *MarginParent {

	if myName == "" {
		myName = mancini.DefaultName("marginparent")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	return newMarginParent(lh, top, right, bottom, left, borderWidth, label, theme, pal)
}

// NewMarginParentConstrained creates a MarginParent where Width and
// Height are provided by the caller as pre-built constraint attributes.
// label may be empty for no label text.
func NewMarginParentConstrained(myName, parent string,
	top, right, bottom, left int64,
	borderWidth int64, label string,
	width, height *attr.Attribute[int64],
	theme mancini.Theme, pal mancini.Palette) *MarginParent {

	if myName == "" {
		myName = mancini.DefaultName("marginparent")
	}
	lh := mancini.NewLayoutAttributesBase(myName, parent)
	lh.Width = width
	lh.Height = height
	lh.InitBounds(myName)
	return newMarginParent(lh, top, right, bottom, left, borderWidth, label, theme, pal)
}

func newMarginParent(lh *mancini.LayoutAttributes,
	top, right, bottom, left int64,
	borderWidth int64, label string,
	theme mancini.Theme, pal mancini.Palette) *MarginParent {

	// Label font: default sans at 75% of top margin height.
	var textFace mancini.LatinTextFace
	labelSize := top * 3 / 4
	if labelSize > 0 {
		fc := theme.Font(mancini.None, labelSize)
		textFace = impl.NewLatinTextFace(fc, false, labelSize, mancini.TextAlignmentParams{})
	}

	mp := &MarginParent{
		Top:         top,
		Right:       right,
		Bottom:      bottom,
		Left:        left,
		BorderWidth: borderWidth,
		Label:       label,
		theme:       theme,
		pal:         pal,
		textFace:    textFace,
	}
	mp.Interactor.Initialize(mp, lh)
	mp.Parent.Initialize(true, &mp.Interactor)
	return mp
}

// cornerRadius derives the corner radius from the margins.
func (mp *MarginParent) cornerRadius() float64 {
	m := mp.Top
	if mp.Right < m {
		m = mp.Right
	}
	if mp.Bottom < m {
		m = mp.Bottom
	}
	if mp.Left < m {
		m = mp.Left
	}
	return float64(m) / 2.0
}

// Draw implements mancini.NewDrawer. Draws the optional border and
// label, then positions and draws the single child inside the margins.
func (mp *MarginParent) Draw(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()
	if dc == nil {
		return
	}

	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	// Draw border if configured.
	if mp.BorderWidth > 0 {
		mp.drawBorder(dc, fx, fy, fw, fh)
	}

	// Child area inside the margins.
	children := mp.GetChildren()
	if len(children) == 0 {
		return
	}

	child := children[0]
	childX := x + mp.Left
	childY := y + mp.Top
	childW := w - mp.Left - mp.Right
	childH := h - mp.Top - mp.Bottom
	if childW < 0 {
		childW = 0
	}
	if childH < 0 {
		childH = 0
	}

	// Publish child position and dimensions.
	if l, ok := child.(mancini.Layouter); ok {
		clh := l.GetLayout()
		if clh != nil {
			clh.X.Set(childX)
			clh.Y.Set(childY)
			if !clh.Width.IsConstraint() {
				clh.Width.Set(childW)
			}
			if !clh.Height.IsConstraint() {
				clh.Height.Set(childH)
			}
		}
	}

	// Propagate DC and draw.
	if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
		cs.SetDC(dc)
	}
	if d, ok := child.(mancini.NewDrawer); ok {
		d.Draw(child, childX, childY, childW, childH)
	}
}

// drawBorder renders the rounded-rectangle border with an optional
// label gap in the top edge.
func (mp *MarginParent) drawBorder(dc mancini.DrawContext, x, y, w, h float64) {
	bw := float64(mp.BorderWidth)
	r := mp.cornerRadius()

	// Border center line: centered in each margin.
	topCenter := y + float64(mp.Top)/2.0
	bottomCenter := y + h - float64(mp.Bottom)/2.0
	leftCenter := x + float64(mp.Left)/2.0
	rightCenter := x + w - float64(mp.Right)/2.0

	rectX := leftCenter
	rectY := topCenter
	rectW := rightCenter - leftCenter
	rectH := bottomCenter - topCenter

	if rectW <= 0 || rectH <= 0 {
		return
	}

	// Clamp corner radius so arcs don't overlap.
	maxR := math.Min(rectW, rectH) / 2.0
	if r > maxR {
		r = maxR
	}

	dc.SetColor(mp.pal.Mid())
	dc.SetLineWidth(bw)

	// Measure label gap if we have a label.
	var hasLabel bool
	var labelLeft, labelRight float64
	var labelTextW float64

	if mp.Label != "" && mp.textFace != nil {
		labelTextW = mp.textFace.MeasureText(mp.Label)
		if labelTextW > 0 {
			hasLabel = true
			labelCenterX := x + w/2.0
			labelLeft = labelCenterX - labelTextW/2.0 - 4
			labelRight = labelCenterX + labelTextW/2.0 + 4
			// Clamp to border rect bounds.
			if labelLeft < rectX+r {
				labelLeft = rectX + r
			}
			if labelRight > rectX+rectW-r {
				labelRight = rectX + rectW - r
			}
		}
	}

	if !hasLabel {
		// Simple case: full rounded rectangle.
		dc.DrawRoundedRectangle(rectX, rectY, rectW, rectH, r)
		dc.Stroke()
	} else {
		// Draw the rounded rectangle with a gap in the top edge.
		// We draw the path manually: start after the gap, go clockwise.

		// Top-right corner arc center.
		trCx := rectX + rectW - r
		trCy := rectY + r
		// Bottom-right corner arc center.
		brCx := rectX + rectW - r
		brCy := rectY + rectH - r
		// Bottom-left corner arc center.
		blCx := rectX + r
		blCy := rectY + rectH - r
		// Top-left corner arc center.
		tlCx := rectX + r
		tlCy := rectY + r

		// Segment 1: from label gap right edge along top to top-right corner.
		dc.MoveTo(labelRight, rectY)
		dc.LineTo(trCx, rectY)

		// Top-right corner arc (quarter circle, -π/2 to 0).
		drawArc(dc, trCx, trCy, r, -math.Pi/2, 0)

		// Right edge.
		dc.LineTo(rectX+rectW, brCy)

		// Bottom-right corner arc (0 to π/2).
		drawArc(dc, brCx, brCy, r, 0, math.Pi/2)

		// Bottom edge.
		dc.LineTo(blCx, rectY+rectH)

		// Bottom-left corner arc (π/2 to π).
		drawArc(dc, blCx, blCy, r, math.Pi/2, math.Pi)

		// Left edge.
		dc.LineTo(rectX, tlCy)

		// Top-left corner arc (π to 3π/2).
		drawArc(dc, tlCx, tlCy, r, math.Pi, 3*math.Pi/2)

		// Segment 2: from top-left corner to label gap left edge.
		dc.LineTo(labelLeft, rectY)

		dc.Stroke()

		// Draw the label text.
		dc.SetColor(mp.pal.Mid())
		mp.textFace.SetText(mp.Label)
		labelX := x + (w-labelTextW)/2.0
		labelY := y
		mp.textFace.DrawFace(dc, labelX, labelY, labelTextW, float64(mp.Top))
	}
}

// drawArc approximates a circular arc from angle1 to angle2 using
// line segments. The arc is drawn at center (cx, cy) with radius r.
func drawArc(dc mancini.DrawContext, cx, cy, r, angle1, angle2 float64) {
	steps := 8
	for i := 1; i <= steps; i++ {
		t := angle1 + (angle2-angle1)*float64(i)/float64(steps)
		dc.LineTo(cx+r*math.Cos(t), cy+r*math.Sin(t))
	}
}
