package std

import (
	"image"
	"image/color"

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

	Text        string         // static text (used when TextFunc is nil)
	TextFunc    func() string  // dynamic text source (takes precedence)
	FontSize    int64
	Color       color.NRGBA
	Bold        bool
	Transparent bool // skip background fill (parent paints background)
	textFace    mancini.LatinTextFace
}

// NewLabel creates a Label wired to the constraint system and theme.
func NewLabel(layout *mancini.LayoutAttributes, theme mancini.Theme,
	text string, fontSize int64) *Label {
	fc := theme.Font(mancini.None, fontSize)
	l := &Label{
		Text:     text,
		FontSize: fontSize,
		Color:    theme.Palette().Text(),
		textFace: impl.NewLatinTextFace(fc, false, fontSize, mancini.TextAlignmentParams{}),
	}
	l.ThemedInteractor.Initialize(l, layout, theme)
	return l
}

// NewLabelBold creates a bold Label.
func NewLabelBold(layout *mancini.LayoutAttributes, theme mancini.Theme,
	text string, fontSize int64) *Label {
	fc := theme.Font(mancini.Bold, fontSize)
	l := NewLabel(layout, theme, text, fontSize)
	l.Bold = true
	l.textFace = impl.NewLatinTextFace(fc, true, fontSize, mancini.TextAlignmentParams{})
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
// then renders centered text via LatinTextFace.
func (l *Label) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	if !self.Visible() {
		return
	}
	tl := nanotime()

	// Super — clear background to theme BgColor (skip if transparent).
	if !l.Transparent {
		l.ThemedInteractor.Draw(self, x, y, w, h, damage)
	}

	if l.textFace == nil {
		drawPerf.LabelNs.Add(nanotime() - tl)
		drawPerf.LabelCount.Add(1)
		return
	}

	dc := self.DC()
	dc.SetColor(l.Color)
	l.textFace.SetText(l.resolveText())
	l.textFace.DrawFace(dc, float64(x), float64(y), float64(w), float64(h))
	drawPerf.LabelNs.Add(nanotime() - tl)
	drawPerf.LabelCount.Add(1)
}
