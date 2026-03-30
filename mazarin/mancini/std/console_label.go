package std

import (
	"image/color"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// ConsoleLabel is a left-aligned monospaced text label with a per-instance
// background color and dynamic text color. Designed for console output lines
// where stdout and stderr need different colors.
//
// It embeds ThemedInteractor for font resolution but does NOT use the theme's
// background color — BgColor is set per-instance. Draw does not call super.
type ConsoleLabel struct {
	impl.ThemedInteractor // Font(), GetLayout(), FgColor()

	FontSize  int64
	BgColor   color.NRGBA          // per-instance background
	TextFunc  func() string        // dynamic text source
	ColorFunc func() color.NRGBA   // dynamic text color (stdout vs stderr)
	MaxCols   int                  // max characters per line

	lastDrawnText string
	lastDrawnHash int64
}

// NewConsoleLabel creates a ConsoleLabel wired to the constraint system.
// Width is probed from the theme's monospaced font: charW * maxCols + 8.
// Height is fontSize (1px inter-line spacing is in the parent column).
func NewConsoleLabel(myName, parent string, theme mancini.Theme,
	fontSize int64, bgColor color.NRGBA, maxCols int) *ConsoleLabel {

	if myName == "" {
		myName = mancini.DefaultName("clabel")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Height.Set(fontSize)

	// Probe font to compute intrinsic width for constraint programs.
	charW := int64(fontSize * 6 / 10) // fallback: 0.6 em
	fc := theme.Font(mancini.None, fontSize)
	if fc != nil && fc.LoadFace != nil {
		if face := fc.LoadFace(false, fontSize); face != nil {
			adv, ok := face.GlyphAdvance('M')
			if ok {
				charW = int64(adv.Ceil())
			}
		}
	}
	lh.Width.Set(charW*int64(maxCols) + 8)

	l := &ConsoleLabel{
		FontSize: fontSize,
		BgColor:  bgColor,
		MaxCols:  maxCols,
	}
	l.ThemedInteractor.Initialize(l, lh, theme)
	return l
}

// Draw implements mancini.NewDrawer. Fills BgColor background, then
// renders left-aligned text using ColorFunc for the text color.
func (l *ConsoleLabel) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}

	text := ""
	if l.TextFunc != nil {
		text = l.TextFunc()
	}

	// Hash-based caching: skip redraw if text and bounds are unchanged.
	lh := l.GetLayout()
	hash := int64(0)
	if lh != nil && lh.BoundsHash != nil {
		hash = lh.BoundsHash.Get()
	}
	if text == l.lastDrawnText && hash == l.lastDrawnHash && l.lastDrawnHash != 0 {
		return
	}
	l.lastDrawnText = text
	l.lastDrawnHash = hash

	dc := self.DC()

	// Fill background with per-instance BgColor (not theme.Bg).
	dc.SetColor(l.BgColor)
	dc.FillRectangle(float64(x), float64(y), float64(w), float64(h))

	if len(text) == 0 {
		return
	}

	// Truncate to MaxCols.
	if l.MaxCols > 0 && len(text) > l.MaxCols {
		text = text[:l.MaxCols]
	}

	// Load monospaced font via theme.
	fc := l.Font(mancini.None, l.FontSize)
	if fc != nil && fc.LoadFace != nil {
		if face := fc.LoadFace(false, l.FontSize); face != nil {
			dc.SetFontFace(face)
		}
	}

	// Text color: dynamic ColorFunc or theme foreground.
	col := l.FgColor()
	if l.ColorFunc != nil {
		col = l.ColorFunc()
	}
	dc.SetColor(col)

	// Left-aligned, vertically centered.
	dc.DrawStringAnchored(text, float64(x)+4, float64(y)+float64(h)/2, 0, 0.5)
}
