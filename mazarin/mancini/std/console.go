// console.go — Console and consoleLine interactors for monospaced terminal
// text display. Console is a column of consoleLine children, sized by font
// metrics. It exposes a terminal-like API (HandleByte, AddLine) and manages
// all line content internally. consoleLine is unexported — external code
// interacts only with Console.
//
// Console is designed to be a child of a NeuBox (Heavy, Inset) so that the
// text area appears recessed into the neumorphic surface. It uses the
// standard palette colors (dark text on light surface) rather than the
// traditional white-on-black terminal look.
package std

import (
	"image/color"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// consoleLine displays a single line of monospaced text. It is internal
// to Console — external code uses Console's terminal API.
type consoleLine struct {
	text     string
	fg       color.NRGBA
	textFace mancini.LatinTextFace
}

// draw renders one line into the given rectangle. maxChars limits the
// number of characters rendered (text is truncated at the character
// boundary, never producing partial glyphs). The bg color fills the
// line's full rectangle before text is drawn on top.
func (cl *consoleLine) draw(dc mancini.DrawContext, x, y, w, h float64,
	bg color.NRGBA, maxChars int) {

	// Fill line background.
	dc.SetColor(bg)
	dc.FillRectangle(x, y, w, h)

	if cl.text == "" {
		return
	}

	// Truncate to maxChars to prevent any overflow past the line width.
	text := cl.text
	if len(text) > maxChars {
		text = text[:maxChars]
	}

	// Shape and render via harfbuzz text face.
	dc.SetColor(cl.fg)
	cl.textFace.SetText(text)
	cl.textFace.DrawFace(dc, x, y, w, h)
}

// lineData holds one line of console content.
type lineData struct {
	text  string
	color color.NRGBA
}

// Console is a terminal-like display of monospaced text lines. It arranges
// consoleLine children vertically edge-to-edge, computes line count from
// font metrics and allocated height, and provides AddLine / HandleByte
// methods for terminal-style content management.
//
// Console publishes LineHeight, CharsPerLine, and NumLines as constraint
// attributes for observability and future dynamic resizing.
//
// Usage: create a NeuBox (Heavy, Inset) and make Console its child:
//
//	box, console := std.NewConsoleWithBox("AppWindow", pal, neuParams, fonts, 16, 120, 20)
type Console struct {
	impl.Interactor

	// Font configuration.
	fonts    *mancini.FontConfig
	fontSize int64

	// Metrics (computed at creation from font probe).
	charW int64 // width of one monospace character in pixels
	lineH int64 // height of one line in pixels

	// Layout parameters (from constructor).
	cols int // requested columns
	rows int // requested rows (= number of consoleLine children)

	// Palette.
	pal     mancini.Palette
	bgColor color.NRGBA // console background (palette surface tint)
	fgColor color.NRGBA // default text color (palette text)

	// Children (managed internally, not in constraint network).
	lines []*consoleLine

	// Content ring buffer.
	content   []lineData
	lineCount int // number of lines with content (may exceed rows)
	maxBuf    int // max content lines to retain

	// Terminal state.
	inEscape bool
	lastFd   byte

	// Constraint attributes for observability.
	lineHeightAttr  *attr.Attribute[int64]
	charsPerLineAttr *attr.Attribute[int64]
	numLinesAttr    *attr.Attribute[int64]
}

// NewConsole creates a Console wired to the constraint system. It probes
// the font to compute charWidth and lineHeight, creates layout attributes
// with pixel dimensions (cols * charW, rows * lineH), and publishes
// LineHeight, CharsPerLine, NumLines as constraint attributes.
//
// Console's parent in the constraint network should be a NeuBox name.
// Use [NewConsoleWithBox] for a convenience constructor that creates
// both the NeuBox and Console.
func NewConsole(myName, parent string, pal mancini.Palette,
	fonts *mancini.FontConfig, fontSize int64, cols, rows int) *Console {

	if myName == "" {
		myName = mancini.DefaultName("console")
	}

	// Probe font for monospace character width.
	charW := fontSize * 6 / 10 // fallback: 0.6 em
	if fonts != nil && fonts.LoadFace != nil {
		if face := fonts.LoadFace(false, fontSize); face != nil {
			adv, ok := face.GlyphAdvance('M')
			if ok {
				charW = int64(adv.Ceil())
			}
		}
	}
	lineH := fontSize

	// Layout attributes with inside-out sizing.
	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Width.Set(charW * int64(cols))
	lh.Height.Set(lineH * int64(rows))

	// Publish console-specific attributes for constraint observability.
	lineHeightAttr := attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, "LineHeight"), lineH)
	charsPerLineAttr := attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, "CharsPerLine"), int64(cols))
	numLinesAttr := attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, "NumLines"), int64(rows))

	// Create consoleLine children (one-shot).
	lines := make([]*consoleLine, rows)
	for i := range lines {
		lines[i] = &consoleLine{
			fg: pal.Text(),
			textFace: impl.NewLatinTextFace(fonts, false, fontSize,
				mancini.TextAlignmentParams{
					HAlign: mancini.HAlignLeft,
					VAlign: mancini.VAlignCenter,
				}),
		}
	}

	maxBuf := rows * 10 // retain 10x visible lines
	if maxBuf < 200 {
		maxBuf = 200
	}

	c := &Console{
		fonts:    fonts,
		fontSize: fontSize,
		charW:    charW,
		lineH:    lineH,
		cols:     cols,
		rows:     rows,
		pal:      pal,
		bgColor:  pal.SurfaceTint(),
		fgColor:  pal.Text(),
		lines:    lines,
		content:  make([]lineData, 0, maxBuf),
		maxBuf:   maxBuf,

		lineHeightAttr:   lineHeightAttr,
		charsPerLineAttr: charsPerLineAttr,
		numLinesAttr:     numLinesAttr,
	}
	c.Interactor.Initialize(c, lh)
	return c
}

// NewConsoleWithBox creates a NeuBox (Heavy, Inset) containing a Console.
// Returns the NeuBox (add to parent layout) and the Console (use for
// content management). The NeuBox name is myName+"_box".
func NewConsoleWithBox(myName, parent string, pal mancini.Palette,
	neuParams mancini.NeuParams, fonts *mancini.FontConfig,
	fontSize int64, cols, rows int) (*NeuBox, *Console) {

	boxName := myName + "_box"
	margin := mancini.NeuMaxPad(neuParams)
	boxLayout := mancini.NewDecoratorLayoutByParentName(
		boxName, parent, margin, margin, 0)

	box := NewNeuBox(boxLayout, pal, mancini.Inset, neuParams, 8)

	console := NewConsole(myName, boxName, pal, fonts, fontSize, cols, rows)
	return box, console
}

// StderrColor returns a muted red suitable for stderr text on the
// neumorphic light surface. Pre-swapped for BGR framebuffer if the
// palette uses SwapRB.
func (c *Console) StderrColor() color.NRGBA {
	// Muted dark red that reads well on the light neumorphic surface.
	return mancini.SwapRB(color.NRGBA{R: 180, G: 50, B: 50, A: 255})
}

// Draw implements mancini.NewDrawer. Fills the console background and
// draws each consoleLine child at the correct Y position. The most
// recent content lines are shown (auto-scroll to bottom).
func (c *Console) Draw(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()
	if dc == nil {
		return
	}

	// Fill console background.
	dc.SetColor(c.bgColor)
	dc.FillRectangle(float64(x), float64(y), float64(w), float64(h))

	if len(c.lines) == 0 {
		return
	}

	// Determine which content lines are visible (most recent rows).
	visibleStart := len(c.content) - c.rows
	if visibleStart < 0 {
		visibleStart = 0
	}

	// Max characters that fit in the width.
	maxChars := c.cols
	if c.charW > 0 {
		maxChars = int(w / c.charW)
	}
	if maxChars < 0 {
		maxChars = 0
	}

	// Draw each consoleLine.
	lineH := float64(c.lineH)
	for i, cl := range c.lines {
		contentIdx := visibleStart + i
		if contentIdx < len(c.content) {
			cl.text = c.content[contentIdx].text
			cl.fg = c.content[contentIdx].color
			if cl.fg.A == 0 {
				cl.fg = c.fgColor
			}
		} else {
			cl.text = ""
			cl.fg = c.fgColor
		}

		lineY := float64(y) + float64(i)*lineH
		cl.draw(dc, float64(x), lineY, float64(w), lineH, c.bgColor, maxChars)
	}
}

// --- Terminal API ---

// AddLine appends a complete line with the given text and color.
func (c *Console) AddLine(text string, fg color.NRGBA) {
	c.content = append(c.content, lineData{text: text, color: fg})
	c.trimBuffer()
}

// HandleByte processes a single byte of terminal input, handling
// newlines, ANSI escape filtering, fd-change line breaks, and
// line length clamping.
func (c *Console) HandleByte(b byte, fd byte) {
	if b == '\r' {
		return
	}
	if b == '\n' {
		c.inEscape = false
		c.ensureCurrentLine()
		c.content = append(c.content, lineData{color: c.fgColor})
		c.trimBuffer()
		return
	}

	// Filter ANSI escape sequences: ESC [ ... letter
	if b == 0x1B {
		c.inEscape = true
		return
	}
	if c.inEscape {
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') {
			c.inEscape = false
		}
		return
	}

	// Drop other non-printable control characters.
	if b < 0x20 && b != '\t' {
		return
	}

	c.ensureCurrentLine()
	cur := &c.content[len(c.content)-1]

	// Force newline when fd changes mid-line so stdout/stderr
	// appear on separate lines.
	if c.lastFd != 0 && fd != c.lastFd && len(cur.text) > 0 {
		c.lastFd = fd
		c.HandleByte('\n', fd)
		c.ensureCurrentLine()
		cur = &c.content[len(c.content)-1]
	}
	c.lastFd = fd

	// Clamp line length.
	if len(cur.text) >= c.cols {
		return
	}

	cur.text += string(b)
	if fd == 2 {
		cur.color = c.StderrColor()
	} else if len(cur.text) == 1 {
		cur.color = c.fgColor
	}
}

// ensureCurrentLine makes sure there is at least one line in content.
func (c *Console) ensureCurrentLine() {
	if len(c.content) == 0 {
		c.content = append(c.content, lineData{color: c.fgColor})
	}
}

// trimBuffer drops old lines when the buffer exceeds maxBuf.
func (c *Console) trimBuffer() {
	if len(c.content) > c.maxBuf {
		drop := len(c.content) - c.maxBuf
		copy(c.content, c.content[drop:])
		c.content = c.content[:c.maxBuf]
	}
}
