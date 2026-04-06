package std

import (
	"math"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// LabelSide specifies which side of the checkbox the label is drawn on.
type LabelSide int

const (
	LabelTop    LabelSide = iota // label above checkbox
	LabelRight                   // label to the right of checkbox
	LabelBottom                  // label below checkbox
	LabelLeft                    // label to the left of checkbox
)

// CheckboxWithLabel combines a neumorphic [Checkbox] with a label drawn
// via a [mancini.Face]. The checkbox is rendered identically to [Checkbox]
// ([mancini.Inset] when unchecked, [mancini.Raised] + checkmark when
// checked), and the label is positioned on the specified Side with a
// configurable Gap.
//
// CheckboxWithLabel embeds [impl.ThemedInteractor] and renders both
// the checkbox and label in a single Draw call. It handles nil
// [mancini.NeuParams] by falling back to flat rendering for the
// checkbox portion.
type CheckboxWithLabel struct {
	impl.ThemedInteractor

	Checked   bool
	Side      LabelSide
	CheckSize float64      // side length of the checkbox square
	Gap       float64      // space between checkbox and label
	Label     mancini.Face // draws the label content (may be nil)
	LabelW    float64      // width of the label area
	LabelH    float64      // height of the label area
	Disabled  bool         // when true, renders dimmed and ignores input
}

// NewCheckboxWithLabel creates a CheckboxWithLabel wired to the constraint
// system and theme. layout must already be created.
func NewCheckboxWithLabel(layout *mancini.LayoutAttributes, theme mancini.Theme,
	checkSize float64, checked bool, side LabelSide,
	gap float64, label mancini.Face, labelW, labelH float64) *CheckboxWithLabel {

	c := &CheckboxWithLabel{
		Checked:   checked,
		Side:      side,
		CheckSize: checkSize,
		Gap:       gap,
		Label:     label,
		LabelW:    labelW,
		LabelH:    labelH,
	}
	c.ThemedInteractor.Initialize(c, layout, theme)
	return c
}

// NewCheckboxWithLabelNamed creates a CheckboxWithLabel with layout built
// from name + parent strings. Total width and height are computed from
// Side, CheckSize, Gap, and label dimensions, and published so the
// constraint system can bootstrap.
func NewCheckboxWithLabelNamed(myName, parent string, theme mancini.Theme,
	checkSize float64, checked bool, side LabelSide,
	gap float64, label mancini.Face, labelW, labelH float64) *CheckboxWithLabel {

	if myName == "" {
		myName = mancini.DefaultName("checkboxlabel")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)

	var totalW, totalH float64
	switch side {
	case LabelTop, LabelBottom:
		totalW = math.Max(checkSize, labelW)
		totalH = checkSize + gap + labelH
	case LabelLeft, LabelRight:
		totalW = checkSize + gap + labelW
		totalH = math.Max(checkSize, labelH)
	}
	lh.Width.Set(int64(totalW))
	lh.Height.Set(int64(totalH))

	return NewCheckboxWithLabel(lh, theme, checkSize, checked, side, gap, label, labelW, labelH)
}

// drawCheckmark draws a standard checkmark into the given face rectangle
// using the palette's text color. This matches the Checkbox checkmark.
func drawCheckmark(dc mancini.DrawContext, pal mancini.Palette, fx, fy, fw, fh float64) {
	dc.SetColor(pal.Text())
	dc.SetLineWidth(3)

	startX := fx + fw*0.20
	startY := fy + fh*0.50
	midX := fx + fw*0.40
	midY := fy + fh*0.72
	endX := fx + fw*0.80
	endY := fy + fh*0.28

	dc.MoveTo(startX, startY)
	dc.LineTo(midX, midY)
	dc.LineTo(endX, endY)
	dc.Stroke()
}

// Draw implements mancini.NewDrawer. It renders a neumorphic checkbox
// (Inset when unchecked, Raised + checkmark when checked) and a label
// positioned according to Side.
func (c *CheckboxWithLabel) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}

	pal := c.Theme().Palette()
	style := c.Theme().Style()

	ox := float64(x)
	oy := float64(y)
	size := c.CheckSize
	gap := c.Gap
	labelW := c.LabelW
	labelH := c.LabelH

	var checkCX, checkCY float64
	var labelX, labelY float64

	switch c.Side {
	case LabelTop:
		// Label above, checkbox below.
		labelX = ox + (float64(w)-labelW)/2
		labelY = oy
		checkCX = ox + float64(w)/2
		checkCY = oy + labelH + gap + size/2
	case LabelBottom:
		// Checkbox above, label below.
		checkCX = ox + float64(w)/2
		checkCY = oy + size/2
		labelX = ox + (float64(w)-labelW)/2
		labelY = oy + size + gap
	case LabelLeft:
		// Label left, checkbox right.
		labelX = ox
		labelY = oy + (float64(h)-labelH)/2
		checkCX = ox + labelW + gap + size/2
		checkCY = oy + float64(h)/2
	case LabelRight:
		// Checkbox left, label right.
		checkCX = ox + size/2
		checkCY = oy + float64(h)/2
		labelX = ox + size + gap
		labelY = oy + (float64(h)-labelH)/2
	}

	// Draw the checkbox.
	x1 := checkCX - size/2
	y1 := checkCY - size/2
	x2 := checkCX + size/2
	y2 := checkCY + size/2

	radius := c.Theme().ControlRadius() / 2

	if !c.Checked {
		style.DrawBox(pal, dc, mancini.Inset, mancini.LightWeight,
			x1, y1, x2, y2, radius, pal.Surface())
	} else {
		style.DrawBox(pal, dc, mancini.Raised, mancini.LightWeight,
			x1, y1, x2, y2, radius, pal.Surface())
		drawCheckmark(dc, pal, x1, y1, x2-x1, y2-y1)
	}

	// Draw the label.
	if c.Label != nil {
		c.Label.DrawFace(dc, labelX, labelY, labelW, labelH)
	}
	if c.Disabled {
		// Overlay covers the full allocated bounds (checkbox + label).
		ApplyDisabledOverlay(pal, dc, ox, oy, ox+float64(w), oy+float64(h), 0)
	}
}
