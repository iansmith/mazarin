package std

import (
	"image"
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

// ControlsInteractor returns the interactor to draw in the top-right
// margin area. The default implementation returns nil (no controls).
// Subclasses that embed *MarginParent can override this by defining
// their own ControlsInteractor method on the concrete type; the Draw
// method resolves the override through the self parameter.
func (mp *MarginParent) ControlsInteractor() mancini.Interactor {
	return nil
}

// labelMetrics computes the label gap boundaries for the top border edge.
// Returns hasLabel=false if no label or no text face.
func (mp *MarginParent) labelMetrics(x, w, rectX, rectW, r float64) (hasLabel bool, labelLeft, labelRight, labelTextW float64) {
	if mp.Label == "" || mp.textFace == nil {
		return false, 0, 0, 0
	}
	labelTextW = mp.textFace.MeasureText(mp.Label)
	if labelTextW <= 0 {
		return false, 0, 0, 0
	}
	hasLabel = true
	labelCenterX := x + w/2.0
	labelLeft = labelCenterX - labelTextW/2.0 - 4
	labelRight = labelCenterX + labelTextW/2.0 + 4
	if labelLeft < rectX+r {
		labelLeft = rectX + r
	}
	if labelRight > rectX+rectW-r {
		labelRight = rectX + rectW - r
	}
	return
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
// If self implements ControlsInteractor() returning non-nil, the
// controls interactor is positioned in the top margin and the border
// draws a hump around it.
func (mp *MarginParent) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	dc := self.DC()
	if dc == nil {
		return
	}

	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	// Resolve controls interactor through self (virtual dispatch).
	type controlsProvider interface {
		ControlsInteractor() mancini.Interactor
	}
	var ctrl mancini.Interactor
	if cp, ok := self.(controlsProvider); ok {
		ctrl = cp.ControlsInteractor()
	}

	// Position controls interactor in the top margin if present.
	var ctrlBounds [4]float64 // x, y, w, h — only valid when ctrl != nil
	if ctrl != nil {
		r := mp.cornerRadius()
		leftCenter := fx + float64(mp.Left)/2.0
		rightCenter := fx + fw - float64(mp.Right)/2.0
		rectX := leftCenter
		rectW := rightCenter - leftCenter

		// Measure label to find labelRight.
		_, _, labelRight, _ := mp.labelMetrics(fx, fw, rectX, rectW, r)

		// Read the control's current width.
		var ctrlW float64
		if cl, ok := ctrl.(mancini.Layouter); ok {
			if clh := cl.GetLayout(); clh != nil {
				ctrlW = float64(clh.Width.Get())
			}
		}

		// Center between labelRight (or left of available area) and the
		// right edge of the border rect.
		availLeft := labelRight
		if availLeft < rectX+r {
			availLeft = rectX + r
		}
		availRight := rectX + rectW - r
		ctrlX := (availLeft+availRight)/2.0 - ctrlW/2.0
		ctrlY := fy
		ctrlH := float64(mp.Top)

		// Clamp so the control stays inside the border rect.
		if ctrlX < availLeft {
			ctrlX = availLeft
		}
		if ctrlX+ctrlW > availRight {
			ctrlX = availRight - ctrlW
		}

		ctrlBounds = [4]float64{ctrlX, ctrlY, ctrlW, ctrlH}

		// Publish position to the controls interactor's layout.
		if cl, ok := ctrl.(mancini.Layouter); ok {
			if clh := cl.GetLayout(); clh != nil {
				clh.X.Set(int64(ctrlX))
				clh.Y.Set(int64(ctrlY))
				if !clh.Height.IsConstraint() {
					clh.Height.Set(int64(ctrlH))
				}
			}
		}
	}

	// Draw border if configured.
	if mp.BorderWidth > 0 {
		if ctrl != nil {
			mp.drawBorder(dc, fx, fy, fw, fh, ctrlBounds)
		} else {
			mp.drawBorder(dc, fx, fy, fw, fh, [4]float64{})
		}
	}

	// Draw controls interactor after border (on top).
	if ctrl != nil {
		if cs, ok := ctrl.(interface{ SetDC(mancini.DrawContext) }); ok {
			cs.SetDC(dc)
		}
		if d, ok := ctrl.(mancini.NewDrawer); ok {
			d.Draw(ctrl, int64(ctrlBounds[0]), int64(ctrlBounds[1]),
				int64(ctrlBounds[2]), int64(ctrlBounds[3]), damage)
		}
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
		d.Draw(child, childX, childY, childW, childH, damage)
	}
}

// drawBorder renders the rounded-rectangle border with an optional
// label gap in the top edge and an optional hump around a controls
// interactor. ctrlBounds is [x,y,w,h] of the controls interactor;
// if ctrlBounds[2] (width) is 0, no hump is drawn.
func (mp *MarginParent) drawBorder(dc mancini.DrawContext, x, y, w, h float64, ctrlBounds [4]float64) {
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

	// Measure label gap.
	hasLabel, labelLeft, labelRight, labelTextW := mp.labelMetrics(x, w, rectX, rectW, r)

	// Hump geometry for controls interactor.
	hasHump := ctrlBounds[2] > 0
	var humpLeft, humpRight, humpTop float64
	if hasHump {
		humpLeft = ctrlBounds[0] - 1
		humpRight = ctrlBounds[0] + ctrlBounds[2] + 1
		humpTop = rectY - ctrlBounds[3]
		if humpTop < y {
			humpTop = y
		}
	}

	// We need a manual path if we have a label gap or a hump.
	needsManualPath := hasLabel || hasHump

	if !needsManualPath {
		// Simple case: full rounded rectangle, no gaps.
		dc.DrawRoundedRectangle(rectX, rectY, rectW, rectH, r)
		dc.Stroke()
		return
	}

	// Corner arc centers.
	trCx := rectX + rectW - r
	trCy := rectY + r
	brCx := rectX + rectW - r
	brCy := rectY + rectH - r
	blCx := rectX + r
	blCy := rectY + rectH - r
	tlCx := rectX + r
	tlCy := rectY + r

	// drawTopSegment draws the top edge from startX to endX at rectY,
	// inserting the hump if it falls within this segment.
	drawTopSegment := func(startX, endX float64) {
		if !hasHump || humpRight <= startX || humpLeft >= endX {
			// No hump in this segment — straight line.
			dc.LineTo(endX, rectY)
			return
		}
		// Draw to hump start, up, across, down, then continue.
		if humpLeft > startX {
			dc.LineTo(humpLeft, rectY)
		}
		dc.LineTo(humpLeft, humpTop)
		dc.LineTo(humpRight, humpTop)
		dc.LineTo(humpRight, rectY)
		if humpRight < endX {
			dc.LineTo(endX, rectY)
		}
	}

	if hasLabel {
		// Top edge with label gap: two segments separated by the gap.
		// Segment 1: labelRight → top-right corner.
		dc.MoveTo(labelRight, rectY)
		drawTopSegment(labelRight, trCx)
	} else {
		// Top edge without label gap: single segment from top-left to top-right.
		dc.MoveTo(rectX+r, rectY)
		drawTopSegment(rectX+r, trCx)
	}

	// Top-right corner arc (-π/2 to 0).
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

	if hasLabel {
		// Segment 2: top-left corner → labelLeft.
		dc.LineTo(labelLeft, rectY)
	}
	// If no label, path closes back to the MoveTo point.

	dc.Stroke()

	// Draw the label text if present.
	if hasLabel {
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
