package std

import (
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Checkbox is a neumorphic toggle interactor. When unchecked it renders
// as [mancini.Inset] (a recessed square); when checked it renders as
// [mancini.Raised] with a checkmark drawn on the face.
//
// Checkbox embeds [impl.ThemedInteractor] and delegates rendering to
// [NeuBoxWith]. When the theme's [mancini.NeumorphicParams.Light]
// returns nil, the checkbox falls back to flat rendering.
//
// Toggling is done externally by setting Checked and redrawing —
// Checkbox does not handle input events directly.
//
// See also [CheckboxWithLabel], which pairs a Checkbox with a text [Label].
type Checkbox struct {
	impl.ThemedInteractor

	Checked bool    // true = raised with checkmark, false = inset
	Size    float64 // side length of the square box in pixels
}

// NewCheckbox creates a Checkbox wired to the constraint system and theme.
// layout must already be created (e.g. via mancini.NewLayoutAttributes).
func NewCheckbox(layout *mancini.LayoutAttributes, theme mancini.Theme,
	size float64, checked bool) *Checkbox {

	c := &Checkbox{
		Checked: checked,
		Size:    size,
	}
	c.ThemedInteractor.Initialize(c, layout, theme)
	return c
}

// NewCheckboxNamed creates a Checkbox with layout built from name + parent
// strings. Width and height are both set to size (the checkbox is square)
// so the constraint system can bootstrap.
func NewCheckboxNamed(myName, parent string, theme mancini.Theme,
	size float64, checked bool) *Checkbox {

	if myName == "" {
		myName = mancini.DefaultName("checkbox")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Width.Set(int64(size))
	lh.Height.Set(int64(size))
	return NewCheckbox(lh, theme, size, checked)
}

// Draw implements [mancini.NewDrawer]. It renders a neumorphic square box
// centered within the given bounds. Unchecked = [mancini.Inset],
// Checked = [mancini.Raised] with a proportional checkmark.
func (c *Checkbox) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}

	pal := c.Theme().Palette()
	var params *mancini.NeuParams
	if neu := c.Theme().Neumorphic(); neu != nil {
		params = neu.Light()
	}
	size := c.Size

	// Center the square box within the allocated bounds.
	cx := float64(x) + float64(w)/2
	cy := float64(y) + float64(h)/2
	x1 := cx - size/2
	y1 := cy - size/2
	x2 := cx + size/2
	y2 := cy + size/2

	const radius = 4.0

	if !c.Checked {
		// Unchecked: inset (depressed) box, no face content.
		NeuBoxWith(pal, dc, mancini.Inset, x1, y1, x2, y2, radius, pal.Surface(), params, nil)
	} else {
		// Checked: raised box with a checkmark drawn on the face.
		checkmarkFace := mancini.FaceDrawer(func(fdc mancini.DrawContext, fx, fy, fw, fh float64) {
			fdc.SetColor(pal.Text())
			fdc.SetLineWidth(3)

			// Draw a standard checkmark: short leg down-right, long leg up-right.
			// Proportional to the face rectangle.
			startX := fx + fw*0.20
			startY := fy + fh*0.50
			midX := fx + fw*0.40
			midY := fy + fh*0.72
			endX := fx + fw*0.80
			endY := fy + fh*0.28

			fdc.MoveTo(startX, startY)
			fdc.LineTo(midX, midY)
			fdc.LineTo(endX, endY)
			fdc.Stroke()
		})
		NeuBoxWith(pal, dc, mancini.Raised, x1, y1, x2, y2, radius, pal.Surface(), params, checkmarkFace)
	}
}
