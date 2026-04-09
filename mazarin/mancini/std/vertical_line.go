package std

import (
	"image/color"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// VerticalLine draws a single vertical line from (X, Y) to (X, Y+Height).
// X, Y, and Height can be constrained or set as value attributes.
// The line is 1px wide by default, drawn in the palette's text color.
type VerticalLine struct {
	impl.ThemedInteractor

	LineColor color.NRGBA // zero uses palette Text()
	LineWidth float64     // pixel width (default 1)
}

// NewVerticalLine creates a VerticalLine with all-value layout attributes.
func NewVerticalLine(myName, parent string, theme mancini.Theme) *VerticalLine {
	if myName == "" {
		myName = mancini.DefaultName("vline")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	v := &VerticalLine{
		LineWidth: 1.0,
	}
	v.ThemedInteractor.Initialize(v, lh, theme)
	return v
}

// NewVerticalLineConstrained creates a VerticalLine where Height is
// constrained to equal a source int64 attribute URI.
func NewVerticalLineConstrained(myName, parent string, theme mancini.Theme,
	heightSourceURI string) *VerticalLine {

	if myName == "" {
		myName = mancini.DefaultName("vline")
	}
	lh := mancini.NewVerticalLineLayout(myName, parent, heightSourceURI)
	v := &VerticalLine{
		LineWidth: 1.0,
	}
	v.ThemedInteractor.Initialize(v, lh, theme)
	return v
}

// Draw implements mancini.NewDrawer.
func (v *VerticalLine) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}

	c := v.LineColor
	if c == (color.NRGBA{}) {
		c = v.Theme().Palette().Text()
	}

	lw := v.LineWidth
	if lw < 1 {
		lw = 1
	}

	// Draw the line centered horizontally in the allocated width.
	fx := float64(x) + float64(w)/2.0
	fy := float64(y)
	fh := float64(h)

	dc.SetColor(c)
	dc.FillRectangle(fx-lw/2, fy, lw, fh)

	v.ClearDamage()
}
