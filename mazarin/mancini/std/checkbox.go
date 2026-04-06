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

	Checked  bool    // true = raised with checkmark, false = inset
	Size     float64 // side length of the square box in pixels
	Disabled bool    // when true, renders dimmed and ignores input
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
	style := c.Theme().Style()
	size := c.Size

	// Center the square box within the allocated bounds.
	cbcx := float64(x) + float64(w)/2
	cbcy := float64(y) + float64(h)/2
	x1 := cbcx - size/2
	y1 := cbcy - size/2
	x2 := cbcx + size/2
	y2 := cbcy + size/2

	radius := c.Theme().ControlRadius() / 2

	if !c.Checked {
		// Unchecked: inset (depressed) box, no face content.
		style.DrawBox(pal, dc, mancini.Inset, mancini.LightWeight,
			x1, y1, x2, y2, radius, pal.Surface())
	} else {
		// Checked: raised box with a checkmark drawn on the face.
		style.DrawBox(pal, dc, mancini.Raised, mancini.LightWeight,
			x1, y1, x2, y2, radius, pal.Surface())
		drawCheckmark(dc, pal, x1, y1, x2-x1, y2-y1)
	}
	if c.Disabled {
		ApplyDisabledOverlay(pal, dc, x1, y1, x2, y2, radius)
	}
}
