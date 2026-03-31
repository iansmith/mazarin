package std

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// SingleLineText is a single-line text input interactor. It renders an
// [mancini.Inset] neumorphic field with left-aligned text, an
// auto-scrolling viewport, a cursor indicator when focused, and
// placeholder text when the content is empty.
//
// SingleLineText embeds [impl.ThemedInteractor] and uses Light-weight
// [mancini.NeuParams] for the inset field background (via [NeuBoxWith]).
// When [mancini.NeuParams] is nil, a flat surface-colored rectangle is
// drawn instead.
//
// CursorPos is in rune offsets (0 = before first character). The viewport
// scrolls automatically to keep the cursor visible within the field.
// Text and cursor are rendered into a clipped local buffer so they do
// not overflow the field's rounded corners.
//
// Compare with [Label], which is a non-editable text display interactor.
type SingleLineText struct {
	impl.ThemedInteractor

	Text        string      // current text content
	CursorPos   int         // rune offset (0..len([]rune(Text)))
	Placeholder string      // shown dimmed when Text is empty
	FontSize    int64
	Radius      float64     // corner radius (default 6)
	Padding     float64     // horizontal padding inside field (default 8)
	Focused     bool        // shows cursor when true
	TextColor   color.NRGBA // zero value uses palette Text()

	scrollOff float64 // auto-computed horizontal scroll in pixels
}

// NewSingleLineText creates a SingleLineText wired to the constraint
// system and theme.
func NewSingleLineText(layout *mancini.LayoutAttributes, theme mancini.Theme,
	text string, fontSize int64) *SingleLineText {

	t := &SingleLineText{
		Text:      text,
		CursorPos: len([]rune(text)),
		FontSize:  fontSize,
		Radius:    6.0,
		Padding:   8.0,
	}
	t.ThemedInteractor.Initialize(t, layout, theme)
	return t
}

// NewSingleLineTextNamed creates a SingleLineText with layout built from
// name + parent strings. Width sets the field width; height is derived from
// the font size.
func NewSingleLineTextNamed(myName, parent string, theme mancini.Theme,
	text string, fontSize int64, width int64) *SingleLineText {

	if myName == "" {
		myName = mancini.DefaultName("singlelinetext")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Width.Set(width)
	lh.Height.Set(fontSize + 16)
	return NewSingleLineText(lh, theme, text, fontSize)
}

// resolveTextColor returns the text color, falling back to the palette.
func (t *SingleLineText) resolveTextColor(pal mancini.Palette) color.NRGBA {
	if t.TextColor != (color.NRGBA{}) {
		return t.TextColor
	}
	return pal.Text()
}

// Draw implements mancini.NewDrawer. It renders the inset field background,
// text (or placeholder), and cursor.
func (t *SingleLineText) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}

	pal := t.Theme().Palette()
	var params *mancini.NeuParams
	if neu := t.Theme().Neumorphic(); neu != nil {
		params = neu.Light()
	}

	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	// 1. Draw inset field background.
	NeuBoxWith(pal, dc, mancini.Inset, fx, fy, fx+fw, fy+fh, t.Radius,
		pal.Surface(), params)

	canvas := dc.Image().(*image.RGBA)

	// 2. Resolve font.
	var face font.Face
	fc := t.Font(mancini.None, t.FontSize)
	if fc != nil && fc.LoadFace != nil {
		face = fc.LoadFace(false, t.FontSize)
	}

	// 3. Content area (inside padding, inset from field edges to avoid
	//    overlapping the rounded corners and inset shadows).
	pad := t.Padding
	innerV := 3.0
	contentRect := image.Rect(
		int(fx+pad), int(fy+innerV),
		int(fx+fw-pad), int(fy+fh-innerV),
	)
	contentRect = contentRect.Intersect(canvas.Bounds())
	cw, ch := contentRect.Dx(), contentRect.Dy()
	if cw <= 0 || ch <= 0 {
		return
	}

	// 4. Compute baseline in local buffer coordinates.
	var localBaseline float64
	if face != nil {
		m := face.Metrics()
		asc := float64(m.Ascent) / 64
		desc := float64(m.Descent) / 64
		localBaseline = float64(ch)/2 + (asc-desc)/2
	} else {
		localBaseline = float64(ch) * 0.6
	}

	// 5. Measure text to cursor position for scroll computation.
	runes := []rune(t.Text)
	cursorPos := t.CursorPos
	if cursorPos > len(runes) {
		cursorPos = len(runes)
	}
	if cursorPos < 0 {
		cursorPos = 0
	}

	var cursorPx float64
	if face != nil {
		cursorPx = float64(font.MeasureString(face, string(runes[:cursorPos]))) / 64.0
	} else {
		cursorPx = float64(cursorPos) * float64(t.FontSize) * 0.6
	}

	// 6. Auto-scroll so cursor stays visible within the content area.
	availW := float64(cw)
	if cursorPx-t.scrollOff > availW-2 {
		t.scrollOff = cursorPx - availW + 2
	}
	if cursorPx-t.scrollOff < 0 {
		t.scrollOff = cursorPx
	}
	if t.scrollOff < 0 {
		t.scrollOff = 0
	}

	// 7. Render text + cursor into a local buffer. The buffer size matches
	//    the content area, so text is naturally clipped at field boundaries.
	textBuf := image.NewRGBA(image.Rect(0, 0, cw, ch))
	tdc := gg.NewContextForRGBA(textBuf)
	if face != nil {
		tdc.SetFontFace(face)
	}

	if t.Text != "" {
		tdc.SetColor(t.resolveTextColor(pal))
		tdc.DrawString(t.Text, -t.scrollOff, localBaseline)
	} else if t.Placeholder != "" {
		tc := pal.Text()
		dimAlpha := uint8(float64(tc.A) * pal.DisabledAlpha())
		tdc.SetColor(color.NRGBA{tc.R, tc.G, tc.B, dimAlpha})
		tdc.DrawString(t.Placeholder, 0, localBaseline)
	}

	if t.Focused {
		cursorX := cursorPx - t.scrollOff
		cursorH := float64(t.FontSize) * 1.2
		cursorY := (float64(ch) - cursorH) / 2
		tdc.SetColor(t.resolveTextColor(pal))
		tdc.SetLineWidth(1.5)
		tdc.DrawLine(cursorX, cursorY, cursorX, cursorY+cursorH)
		tdc.Stroke()
	}

	// 8. Composite text buffer onto canvas.
	draw.Draw(canvas, contentRect, textBuf, image.Point{}, draw.Over)
}
