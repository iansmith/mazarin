package std

import (
	"image/color"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Label is a single-line text interactor. It embeds ThemedInteractor
// (which embeds Interactor), so it satisfies both mancini.Interactor
// and mancini.ThemedInteractor via promotion.
//
// ThemedInteractor.Draw clears the background. Label.Draw calls that
// as super, then renders centered text using the theme's font family.
type Label struct {
	impl.ThemedInteractor // X(), Y(), W(), H(), DC(), BgColor(), FgColor(), Font(), Draw()

	Text     string         // static text (used when TextFunc is nil)
	TextFunc func() string  // dynamic text source (takes precedence)
	FontSize int64
	Color    color.NRGBA
	Bold     bool
}

// NewLabel creates a Label wired to the constraint system and theme.
func NewLabel(layout *mancini.LayoutHandles, theme mancini.Theme,
	text string, fontSize int64) *Label {
	l := &Label{
		Text:     text,
		FontSize: fontSize,
		Color:    theme.Fg,
	}
	l.ThemedInteractor.Init(l, layout, theme)
	return l
}

// NewLabelBold creates a bold Label.
func NewLabelBold(layout *mancini.LayoutHandles, theme mancini.Theme,
	text string, fontSize int64) *Label {
	l := NewLabel(layout, theme, text, fontSize)
	l.Bold = true
	return l
}

// NewLabelColor creates a Label with a custom text color.
func NewLabelColor(layout *mancini.LayoutHandles, theme mancini.Theme,
	text string, fontSize int64, col color.NRGBA) *Label {
	l := NewLabel(layout, theme, text, fontSize)
	l.Color = col
	return l
}

// resolveText returns the current display text.
func (l *Label) resolveText() string {
	if l.TextFunc != nil {
		return l.TextFunc()
	}
	return l.Text
}

// Draw implements mancini.Drawer. Clears background via super,
// then renders centered text.
func (l *Label) Draw(self mancini.Interactor) {
	if !self.Visible() {
		return
	}

	// Super — clear background to theme BgColor.
	l.ThemedInteractor.Draw(self)

	dc := self.DC()

	// Load font from the theme using Feature + size.
	feature := mancini.None
	if l.Bold {
		feature = mancini.Bold
	}
	fc := l.Font(feature, l.FontSize)
	if fc != nil && fc.LoadFace != nil {
		face := fc.LoadFace(l.Bold, l.FontSize)
		if face != nil {
			dc.SetFontFace(face)
		}
	}

	dc.SetColor(l.Color)
	text := l.resolveText()
	x, y := float64(self.X()), float64(self.Y())
	w, h := float64(self.W()), float64(self.H())
	dc.DrawStringAnchored(text, x+w/2, y+h/2, 0.5, 0.5)
}
