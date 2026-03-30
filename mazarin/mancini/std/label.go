package std

import (
	"image/color"

	"golang.org/x/image/font"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Label is a single-line text display interactor (not editable — see
// [SingleLineText] for an editable text field). It renders centered
// text using the [mancini.Theme]'s font family.
//
// Label embeds [impl.ThemedInteractor]. Its Draw method calls the
// super [impl.ThemedInteractor.Draw] to clear the background, then
// renders the text.
//
// Text can be static (set via Text field) or dynamic (set via TextFunc,
// which takes precedence). Multiple constructor variants handle bold,
// custom color, and named-layout creation.
type Label struct {
	impl.ThemedInteractor

	Text     string         // static text (used when TextFunc is nil)
	TextFunc func() string  // dynamic text source (takes precedence)
	FontSize int64
	Color    color.NRGBA
	Bold     bool
}

// NewLabel creates a Label wired to the constraint system and theme.
func NewLabel(layout *mancini.LayoutAttributes, theme mancini.Theme,
	text string, fontSize int64) *Label {
	l := &Label{
		Text:     text,
		FontSize: fontSize,
		Color:    theme.Palette().Text(),
	}
	l.ThemedInteractor.Initialize(l, layout, theme)
	return l
}

// NewLabelBold creates a bold Label.
func NewLabelBold(layout *mancini.LayoutAttributes, theme mancini.Theme,
	text string, fontSize int64) *Label {
	l := NewLabel(layout, theme, text, fontSize)
	l.Bold = true
	return l
}

// NewLabelColor creates a Label with a custom text color.
func NewLabelColor(layout *mancini.LayoutAttributes, theme mancini.Theme,
	text string, fontSize int64, col color.NRGBA) *Label {
	l := NewLabel(layout, theme, text, fontSize)
	l.Color = col
	return l
}

// NewLabelNamed creates a Label with layout built from name + parent strings.
// Publishes intrinsic dimensions (fontSize+4 height, text-measured width)
// so constraint programs can bootstrap.
func NewLabelNamed(myName, parent string, theme mancini.Theme,
	text string, fontSize int64) *Label {
	if myName == "" {
		myName = mancini.DefaultName("label")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Height.Set(fontSize + 4)

	// Measure width if the theme has a font resolver.
	fc := theme.Font(mancini.None, fontSize)
	if fc != nil && fc.LoadFace != nil {
		if face := fc.LoadFace(false, fontSize); face != nil {
			lh.Width.Set(mancini.MeasureTextWidth(face, text) + 8)
		}
	}

	return NewLabel(lh, theme, text, fontSize)
}

// NewLabelNamedBold creates a bold Label with layout from name + parent.
func NewLabelNamedBold(myName, parent string, theme mancini.Theme,
	text string, fontSize int64) *Label {
	if myName == "" {
		myName = mancini.DefaultName("label")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Height.Set(fontSize + 4)

	fc := theme.Font(mancini.Bold, fontSize)
	if fc != nil && fc.LoadFace != nil {
		if face := fc.LoadFace(true, fontSize); face != nil {
			lh.Width.Set(mancini.MeasureTextWidth(face, text) + 8)
		}
	}

	return NewLabelBold(lh, theme, text, fontSize)
}

// NewLabelNamedColor creates a colored Label with layout from name + parent.
func NewLabelNamedColor(myName, parent string, theme mancini.Theme,
	text string, fontSize int64, col color.NRGBA) *Label {
	if myName == "" {
		myName = mancini.DefaultName("label")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Height.Set(fontSize + 4)

	fc := theme.Font(mancini.None, fontSize)
	if fc != nil && fc.LoadFace != nil {
		if face := fc.LoadFace(false, fontSize); face != nil {
			lh.Width.Set(mancini.MeasureTextWidth(face, text) + 8)
		}
	}

	return NewLabelColor(lh, theme, text, fontSize, col)
}

// resolveText returns the current display text.
func (l *Label) resolveText() string {
	if l.TextFunc != nil {
		return l.TextFunc()
	}
	return l.Text
}

// Draw implements mancini.NewDrawer. Clears background via super,
// then renders centered text.
func (l *Label) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}

	// Super — clear background to theme BgColor.
	l.ThemedInteractor.Draw(self, x, y, w, h)

	dc := self.DC()

	// Load font from the theme using Feature + size.
	feature := mancini.None
	if l.Bold {
		feature = mancini.Bold
	}
	var face font.Face
	fc := l.Font(feature, l.FontSize)
	if fc != nil && fc.LoadFace != nil {
		face = fc.LoadFace(l.Bold, l.FontSize)
		if face != nil {
			dc.SetFontFace(face)
		}
	}

	dc.SetColor(l.Color)
	text := l.resolveText()
	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	// Compute baseline from font metrics so the visual text extent
	// (ascent above + descent below baseline) is centered in the label.
	// DrawStringAnchored's ay=0.5 shifts by fontHeight/2 which pushes
	// the baseline too low, causing descenders to overflow the bottom.
	if face != nil {
		m := face.Metrics()
		asc := float64(m.Ascent) / 64
		desc := float64(m.Descent) / 64
		baseline := fy + fh/2 + (asc-desc)/2
		dc.DrawStringAnchored(text, fx+fw/2, baseline, 0.5, 0)
	} else {
		dc.DrawStringAnchored(text, fx+fw/2, fy+fh/2, 0.5, 0.35)
	}
}
