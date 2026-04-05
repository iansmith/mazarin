package std

import (
	"image/color"
	"unicode/utf8"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
	"mazzy/mazarin/sys"
	"mazzy/shared/hid"
)

// SingleLineText is a single-line text input interactor. It renders an
// [mancini.Inset] neumorphic field with left-aligned text, an
// auto-scrolling viewport, a cursor indicator when focused, and
// hint text when the content is empty.
//
// SingleLineText publishes its text content as a string attribute and
// the text length as a constraint attribute (computed via a vgo program).
// It implements [mancini.KeyPressable] so it can receive matched
// key-press events when focused via a [mancini.KeyAgent].
//
// The Hint field is displayed in italic at 80% alpha when the text
// content is empty.
type SingleLineText struct {
	impl.ThemedInteractor

	Hint        string      // shown in italic at 80% alpha when text is empty
	FontSize    int64
	Radius      float64     // corner radius (default 6)
	Padding     float64     // horizontal padding inside field (default 8)
	BorderWidth float64     // border thickness in pixels (default 2)
	Focused     bool        // shows cursor when true
	TextColor   color.NRGBA // zero value uses palette Text()

	textAttr    *attr.Attribute[string]
	textLenAttr *attr.Attribute[int64]
	scrollOff   float64 // auto-computed horizontal scroll in pixels
	cursorPos   int     // cursor position in runes (0..runeCount)
	selAnchor   int     // rune position where shift was first pressed (-1 = no selection)
	selExtent   int     // rune position where selection extends to
	charLimit  int     // max runes (0 = visual-only limit)
	contentW     float64 // cached content width from last Draw (for visual limit)
	isAlerting   bool
	alertAlpha   uint8  // current border alpha (255 = opaque, 0 = gone)
	alertLocalID uint64 // local animation ID (0 = no active animation)
	AppWindow    *AppWindow

	// Font IDs for DrawContext text shaping (resolved lazily).
	regularFontID int32
	italicFontID  int32
	fontsOpened   bool
}

// NewSingleLineText creates a SingleLineText wired to the constraint
// system and theme. The layout must have a name set (used for attribute URIs).
func NewSingleLineText(layout *mancini.LayoutAttributes, theme mancini.Theme,
	text string, fontSize int64) *SingleLineText {

	myName := layout.Name()

	// Publish text as a string attribute.
	textURI := mancini.LayoutURI(myName, mancini.DataTypeStr, "Text")
	textAttr := attr.ValueStr(textURI, text)

	// Publish text length as a constraint computed from the text attribute.
	textLenURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, "TextLen")
	textLenAttr := attr.ConstraintI64(textLenURI,
		mancini.BindStrings(mancini.ProgStringLength, "_source_", textURI))

	t := &SingleLineText{
		FontSize:    fontSize,
		Radius:      6.0,
		Padding:     8.0,
		BorderWidth: 2.0,
		textAttr:    textAttr,
		textLenAttr: textLenAttr,
		cursorPos:   utf8.RuneCountInString(text),
		selAnchor:   -1,
		selExtent:   -1,
	}
	t.ThemedInteractor.Initialize(t, layout, theme)
	return t
}

// NewSingleLineTextNamed creates a SingleLineText with layout built from
// name + parent strings. Width sets the field width; height is derived from
// the font size plus padding and border.
func NewSingleLineTextNamed(myName, parent string, theme mancini.Theme,
	text string, fontSize int64, width int64) *SingleLineText {

	if myName == "" {
		myName = mancini.DefaultName("singlelinetext")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	bw := int64(2) // default BorderWidth
	pad := int64(8) // default Padding
	lh.Width.Set(width + 2*(bw+pad))
	lh.Height.Set(fontSize + 2*(bw+pad))
	return NewSingleLineText(lh, theme, text, fontSize)
}

// NewSingleLineTextWithLimit creates a SingleLineText with a maximum
// character count. The limit must be >= 1 or the call panics. When the
// limit is reached, further inserts replace the character under the cursor.
func NewSingleLineTextWithLimit(myName, parent string, theme mancini.Theme,
	text string, fontSize int64, width int64, charLimit int) *SingleLineText {

	if charLimit < 1 {
		panic("SingleLineText: charLimit must be >= 1")
	}
	t := NewSingleLineTextNamed(myName, parent, theme, text, fontSize, width)
	t.charLimit = charLimit
	return t
}

// Text returns the current text content.
func (t *SingleLineText) Text() string {
	return t.textAttr.Get()
}

// SetText replaces the text content, clamps the cursor, and triggers FullDamage.
func (t *SingleLineText) SetText(s string) {
	t.textAttr.Set(s)
	rc := utf8.RuneCountInString(s)
	if t.cursorPos > rc {
		t.cursorPos = rc
	}
	t.FullDamage()
}

// runeByteOffset returns the byte offset of rune position pos in s.
func runeByteOffset(s string, pos int) int {
	off := 0
	for i := 0; i < pos; i++ {
		_, sz := utf8.DecodeRuneInString(s[off:])
		off += sz
	}
	return off
}

// TextLen returns the current text length (from the constraint attribute).
func (t *SingleLineText) TextLen() int64 {
	return t.textLenAttr.Get()
}

// resolveTextColor returns the text color, falling back to the palette.
func (t *SingleLineText) resolveTextColor(pal mancini.Palette) color.NRGBA {
	if t.TextColor != (color.NRGBA{}) {
		return t.TextColor
	}
	return pal.Text()
}

// CursorMoveLeftChar moves the cursor one rune to the left.
// If there is a selection, collapses to the start of the selection.
// Alerts if already at the beginning.
func (t *SingleLineText) CursorMoveLeftChar() {
	if t.HasSelection() {
		start, _ := t.SelectionRange()
		t.cursorPos = start
		t.ClearSelection()
		t.FullDamage()
		return
	}
	if t.cursorPos > 0 {
		t.cursorPos--
		t.FullDamage()
	} else {
		t.Alert()
	}
}

// CursorMoveRightChar moves the cursor one rune to the right.
// If there is a selection, collapses to the end of the selection.
// Alerts if already at the end.
func (t *SingleLineText) CursorMoveRightChar() {
	if t.HasSelection() {
		_, end := t.SelectionRange()
		t.cursorPos = end
		t.ClearSelection()
		t.FullDamage()
		return
	}
	if t.cursorPos < utf8.RuneCountInString(t.Text()) {
		t.cursorPos++
		t.FullDamage()
	} else {
		t.Alert()
	}
}

// CursorMoveBeginningOfLine moves the cursor to the beginning of the text.
// Alerts if already at the beginning.
func (t *SingleLineText) CursorMoveBeginningOfLine() {
	t.ClearSelection()
	if t.cursorPos != 0 {
		t.cursorPos = 0
		t.FullDamage()
	} else {
		t.Alert()
	}
}

// CursorMoveEndOfLine moves the cursor to the end of the text.
// Alerts if already at the end.
func (t *SingleLineText) CursorMoveEndOfLine() {
	t.ClearSelection()
	rc := utf8.RuneCountInString(t.Text())
	if t.cursorPos != rc {
		t.cursorPos = rc
		t.FullDamage()
	} else {
		t.Alert()
	}
}

// PreviousWord returns the rune position of the beginning of the previous
// word relative to the current cursor position. A word is a group of
// non-space characters separated by spaces. If there is no previous word,
// returns 0 (beginning of line).
func (t *SingleLineText) PreviousWord() int {
	runes := []rune(t.Text())
	pos := t.cursorPos
	// Skip spaces backward.
	for pos > 0 && runes[pos-1] == ' ' {
		pos--
	}
	// Skip non-space characters backward.
	for pos > 0 && runes[pos-1] != ' ' {
		pos--
	}
	return pos
}

// NextWord returns the rune position of the beginning of the next word
// relative to the current cursor position. A word is a group of non-space
// characters separated by spaces. If there is no next word, returns the
// end of the text (end of line).
func (t *SingleLineText) NextWord() int {
	runes := []rune(t.Text())
	rc := len(runes)
	pos := t.cursorPos
	// Skip non-space characters forward.
	for pos < rc && runes[pos] != ' ' {
		pos++
	}
	// Skip spaces forward.
	for pos < rc && runes[pos] == ' ' {
		pos++
	}
	return pos
}

// CursorMoveBackwardWord moves the cursor to the beginning of the previous word.
// If there is a selection, collapses to the start of the selection first.
// Alerts if already at the beginning.
func (t *SingleLineText) CursorMoveBackwardWord() {
	if t.HasSelection() {
		start, _ := t.SelectionRange()
		t.cursorPos = start
		t.ClearSelection()
	}
	pos := t.PreviousWord()
	if pos != t.cursorPos {
		t.cursorPos = pos
		t.FullDamage()
	} else {
		t.Alert()
	}
}

// CursorMoveForwardWord moves the cursor to the beginning of the next word.
// If there is a selection, collapses to the end of the selection first.
// Alerts if already at the end.
func (t *SingleLineText) CursorMoveForwardWord() {
	if t.HasSelection() {
		_, end := t.SelectionRange()
		t.cursorPos = end
		t.ClearSelection()
	}
	pos := t.NextWord()
	if pos != t.cursorPos {
		t.cursorPos = pos
		t.FullDamage()
	} else {
		t.Alert()
	}
}

// DeleteBackwardWord deletes from the cursor back to the beginning of the
// previous word (Ctrl+W). After deletion the cursor is positioned so that
// typing extends the word before the deleted region.
func (t *SingleLineText) DeleteBackwardWord() {
	prev := t.PreviousWord()
	if prev == t.cursorPos {
		return
	}
	text := t.Text()
	bStart := runeByteOffset(text, prev)
	bEnd := runeByteOffset(text, t.cursorPos)
	t.cursorPos = prev
	t.textAttr.Set(text[:bStart] + text[bEnd:])
	t.FullDamage()
}

// DeleteForward deletes the character after the cursor.
// If there is a selection, deletes the selected range instead.
// Alerts if already at the end.
func (t *SingleLineText) DeleteForward() {
	if t.HasSelection() {
		t.deleteSelection()
		return
	}
	text := t.Text()
	if t.cursorPos < utf8.RuneCountInString(text) {
		bOff := runeByteOffset(text, t.cursorPos)
		_, sz := utf8.DecodeRuneInString(text[bOff:])
		t.textAttr.Set(text[:bOff] + text[bOff+sz:])
		t.FullDamage()
	} else {
		t.Alert()
	}
}

// DeleteBackward deletes the character before the cursor.
// If there is a selection, deletes the selected range instead.
// Alerts if already at the beginning.
func (t *SingleLineText) DeleteBackward() {
	if t.HasSelection() {
		t.deleteSelection()
		return
	}
	text := t.Text()
	if t.cursorPos > 0 {
		bOff := runeByteOffset(text, t.cursorPos-1)
		_, sz := utf8.DecodeRuneInString(text[bOff:])
		t.cursorPos--
		t.textAttr.Set(text[:bOff] + text[bOff+sz:])
		t.FullDamage()
	} else {
		t.Alert()
	}
}

// atLimit reports whether inserting a character would exceed the input
// limit. If charLimit > 0 the character count is checked. Otherwise
// the visual width is checked: the entire text plus the new character
// must fit in the cached content width (set during Draw). Before the
// first Draw (contentW == 0) input is always accepted.
func (t *SingleLineText) atLimit(ch rune) bool {
	text := t.Text()
	rc := utf8.RuneCountInString(text)
	if t.charLimit > 0 {
		return rc >= t.charLimit
	}
	if t.contentW <= 0 || !t.fontsOpened {
		return false
	}
	dc := t.DC()
	if dc == nil {
		return false
	}
	newText := text + string(ch)
	return dc.MeasureText(newText, t.regularFontID) > t.contentW
}

// InsertAtCursor inserts a rune at the current cursor position. If the
// input is at its limit, the character under the cursor is replaced
// instead and the cursor does not advance. Any insertion clears an
// active alert.
func (t *SingleLineText) InsertAtCursor(ch rune) {
	t.ClearAlert()
	if t.HasSelection() {
		t.deleteSelection()
	}
	text := t.Text()
	rc := utf8.RuneCountInString(text)
	if t.atLimit(ch) {
		// Replace the character under the cursor.
		if t.cursorPos < rc {
			bOff := runeByteOffset(text, t.cursorPos)
			_, sz := utf8.DecodeRuneInString(text[bOff:])
			t.textAttr.Set(text[:bOff] + string(ch) + text[bOff+sz:])
			t.FullDamage()
		}
		return
	}
	bOff := runeByteOffset(text, t.cursorPos)
	t.cursorPos++
	t.textAttr.Set(text[:bOff] + string(ch) + text[bOff:])
	t.FullDamage()
}

// HasAlert returns true if the interactor is currently showing an alert border.
func (t *SingleLineText) HasAlert() bool {
	return t.isAlerting
}

// Alert flashes a highlight border around the input. The border is solid
// for 500ms, then fades over the next 500ms via the animation protocol.
// Ignored if an alert is already active.
func (t *SingleLineText) Alert() {
	if t.isAlerting {
		return
	}
	t.isAlerting = true
	t.alertAlpha = 255
	t.FullDamage()

	if t.AppWindow == nil {
		return
	}
	ts, err := sys.GetTime()
	if err != nil {
		return
	}
	nowNanos := int64(ts.Seconds)*1_000_000_000 + int64(ts.Nanoseconds)
	startNanos := nowNanos + 500_000_000  // fade starts after 500ms
	endNanos := nowNanos + 1_000_000_000  // fade ends after 1000ms
	t.alertLocalID = t.AppWindow.RegisterAnimation(t, startNanos, endNanos)
}

// ClearAlert removes the alert border and forces a redraw.
func (t *SingleLineText) ClearAlert() {
	if !t.isAlerting {
		return
	}
	t.isAlerting = false
	t.alertAlpha = 0
	t.alertLocalID = 0
	t.FullDamage()
}

// AnimationStart implements mancini.Animatable. The alert border is
// already visible at full alpha during the solid phase — no-op.
func (t *SingleLineText) AnimationStart(localID uint64, startNanos int64) {
}

// AnimationUpdate implements mancini.Animatable. Fades the alert
// border alpha from 255 to 0 as coveredEnd progresses from 0 to 1.
func (t *SingleLineText) AnimationUpdate(localID uint64,
	startNanos, endNanos int64, coveredStart, coveredEnd float64, nanosSinceStart int64) {
	t.alertAlpha = uint8(255.0 * (1.0 - coveredEnd))
	t.FullDamage()
}

// AnimationFinish implements mancini.Animatable. Clears the alert.
func (t *SingleLineText) AnimationFinish(localID uint64, endNanos int64) {
	t.isAlerting = false
	t.alertAlpha = 0
	t.alertLocalID = 0
	t.FullDamage()
}

// HasSelection returns true if there is an active text selection.
func (t *SingleLineText) HasSelection() bool {
	return t.selAnchor >= 0
}

// SelectionRange returns the ordered start and end rune positions of
// the current selection. If there is no selection, returns (0, 0).
func (t *SingleLineText) SelectionRange() (start, end int) {
	if t.selAnchor < 0 {
		return 0, 0
	}
	a, b := t.selAnchor, t.selExtent
	if a > b {
		a, b = b, a
	}
	return a, b
}

// ClearSelection removes the selection without changing the cursor.
func (t *SingleLineText) ClearSelection() {
	t.selAnchor = -1
	t.selExtent = -1
}

// SelectedText returns the currently selected text, or "" if none.
func (t *SingleLineText) SelectedText() string {
	if !t.HasSelection() {
		return ""
	}
	start, end := t.SelectionRange()
	text := t.Text()
	bStart := runeByteOffset(text, start)
	bEnd := runeByteOffset(text, end)
	return text[bStart:bEnd]
}

// deleteSelection deletes the selected range, moves cursor to the
// start of the selection, and clears the selection state.
func (t *SingleLineText) deleteSelection() {
	start, end := t.SelectionRange()
	text := t.Text()
	bStart := runeByteOffset(text, start)
	bEnd := runeByteOffset(text, end)
	t.cursorPos = start
	t.selAnchor = -1
	t.selExtent = -1
	t.textAttr.Set(text[:bStart] + text[bEnd:])
	t.FullDamage()
}

// SelectionExtendLeftChar extends selection one rune left.
// If no selection, sets anchor = cursorPos first.
func (t *SingleLineText) SelectionExtendLeftChar() {
	if t.selAnchor < 0 {
		t.selAnchor = t.cursorPos
	}
	if t.cursorPos > 0 {
		t.cursorPos--
		t.selExtent = t.cursorPos
		t.FullDamage()
	}
}

// SelectionExtendRightChar extends selection one rune right.
func (t *SingleLineText) SelectionExtendRightChar() {
	if t.selAnchor < 0 {
		t.selAnchor = t.cursorPos
	}
	if t.cursorPos < utf8.RuneCountInString(t.Text()) {
		t.cursorPos++
		t.selExtent = t.cursorPos
		t.FullDamage()
	}
}

// SelectionExtendToBeginning extends selection to beginning of line.
func (t *SingleLineText) SelectionExtendToBeginning() {
	if t.selAnchor < 0 {
		t.selAnchor = t.cursorPos
	}
	t.cursorPos = 0
	t.selExtent = t.cursorPos
	t.FullDamage()
}

// SelectionExtendToEnd extends selection to end of line.
func (t *SingleLineText) SelectionExtendToEnd() {
	if t.selAnchor < 0 {
		t.selAnchor = t.cursorPos
	}
	t.cursorPos = utf8.RuneCountInString(t.Text())
	t.selExtent = t.cursorPos
	t.FullDamage()
}

// SelectionExtendBackwardWord extends selection to previous word boundary.
func (t *SingleLineText) SelectionExtendBackwardWord() {
	if t.selAnchor < 0 {
		t.selAnchor = t.cursorPos
	}
	pos := t.PreviousWord()
	if pos != t.cursorPos {
		t.cursorPos = pos
		t.selExtent = t.cursorPos
		t.FullDamage()
	}
}

// SelectionExtendForwardWord extends selection to next word boundary.
func (t *SingleLineText) SelectionExtendForwardWord() {
	if t.selAnchor < 0 {
		t.selAnchor = t.cursorPos
	}
	pos := t.NextWord()
	if pos != t.cursorPos {
		t.cursorPos = pos
		t.selExtent = t.cursorPos
		t.FullDamage()
	}
}

// SelectionAll selects all text.
func (t *SingleLineText) SelectionAll() {
	rc := utf8.RuneCountInString(t.Text())
	if rc == 0 {
		return
	}
	t.selAnchor = 0
	t.selExtent = rc
	t.cursorPos = rc
	t.FullDamage()
}

// KeyPress implements mancini.KeyPressable. The KeyAgent has already
// translated the evdev keycode to a character and action via the keymap.
//
// Modifier combinations (Meta = Cmd, Alt = Option on Mac):
//   - Cmd+Left / Ctrl+A: CursorMoveBeginningOfLine
//   - Cmd+Right / Ctrl+E: CursorMoveEndOfLine
//   - Opt+Left: CursorMoveBackwardWord
//   - Opt+Right: CursorMoveForwardWord
//   - Ctrl+W: DeleteBackwardWord
func (t *SingleLineText) KeyPress(ch rune, action string, ev *mancini.InputEvent) bool {
	mods := int64(ev.Mods)

	switch action {
	case "backspace":
		t.DeleteBackward()
		return true
	case "delete":
		t.DeleteForward()
		return true
	case "left":
		if hid.Shift(mods) {
			if hid.Meta(mods) {
				t.SelectionExtendToBeginning()
			} else if hid.Alt(mods) {
				t.SelectionExtendBackwardWord()
			} else {
				t.SelectionExtendLeftChar()
			}
		} else {
			if hid.Meta(mods) {
				t.CursorMoveBeginningOfLine()
			} else if hid.Alt(mods) {
				t.CursorMoveBackwardWord()
			} else {
				t.CursorMoveLeftChar()
			}
		}
		return true
	case "right":
		if hid.Shift(mods) {
			if hid.Meta(mods) {
				t.SelectionExtendToEnd()
			} else if hid.Alt(mods) {
				t.SelectionExtendForwardWord()
			} else {
				t.SelectionExtendRightChar()
			}
		} else {
			if hid.Meta(mods) {
				t.CursorMoveEndOfLine()
			} else if hid.Alt(mods) {
				t.CursorMoveForwardWord()
			} else {
				t.CursorMoveRightChar()
			}
		}
		return true
	case "home":
		if hid.Shift(mods) {
			t.SelectionExtendToBeginning()
		} else {
			t.CursorMoveBeginningOfLine()
		}
		return true
	case "end":
		if hid.Shift(mods) {
			t.SelectionExtendToEnd()
		} else {
			t.CursorMoveEndOfLine()
		}
		return true
	case "enter":
		return true
	case "escape":
		if t.HasSelection() {
			t.ClearSelection()
			t.FullDamage()
		}
		return true
	case "":
		// Check for Ctrl+letter bindings before character handling.
		if hid.Ctrl(mods) {
			switch ch {
			case 'a', 'A':
				if hid.Shift(mods) {
					t.SelectionAll()
				} else {
					t.CursorMoveBeginningOfLine()
				}
				return true
			case 'e', 'E':
				t.CursorMoveEndOfLine()
				return true
			case 'w', 'W':
				t.DeleteBackwardWord()
				return true
			}
		}
	default:
		return false
	}
	if ch != 0 {
		t.InsertAtCursor(ch)
		return true
	}
	return false
}

// ensureFonts lazily opens regular and italic fonts via the DrawContext.
func (t *SingleLineText) ensureFonts(dc mancini.DrawContext) {
	if t.fontsOpened || dc == nil {
		return
	}
	t.fontsOpened = true
	t.regularFontID = mancini.OpenFont(dc, t.Theme(), mancini.None, t.FontSize)
	t.italicFontID = mancini.OpenFont(dc, t.Theme(), mancini.Italic, t.FontSize)
	if t.italicFontID < 0 {
		t.italicFontID = t.regularFontID
	}
}

// Draw implements mancini.NewDrawer. It renders the inset field background,
// text (or hint), and cursor.
func (t *SingleLineText) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}
	t.ensureFonts(dc)

	pal := t.Theme().Palette()
	var params *mancini.NeuParams
	if neu := t.Theme().Neumorphic(); neu != nil {
		params = neu.Light()
	}

	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)
	bw := t.BorderWidth

	// 1. Draw alert border if active.
	if t.isAlerting && t.alertAlpha > 0 {
		hi := pal.Highlight()
		dc.SetColor(color.NRGBA{hi.R, hi.G, hi.B, t.alertAlpha})
		dc.FillRectangle(fx, fy, fw, fh)
	}

	// 2. Draw inset field background inside the border.
	ix, iy := fx+bw, fy+bw
	iw, ih := fw-2*bw, fh-2*bw
	NeuBoxWith(pal, dc, mancini.Inset, ix, iy, ix+iw, iy+ih, t.Radius,
		pal.Surface(), params)

	// 3. Content area (inside border + padding).
	pad := t.Padding
	cx := ix + pad
	cy := iy + pad
	cw := iw - 2*pad
	ch := ih - 2*pad
	if cw <= 0 || ch <= 0 {
		return
	}
	t.contentW = cw

	text := t.Text()
	textLen := int(t.TextLen())

	// 3. Resolve which font to use and compute baseline.
	fontID := t.regularFontID
	if textLen == 0 {
		fontID = t.italicFontID
	}
	metrics := dc.GetFontMetrics(fontID)
	asc := float64(metrics.Ascent) / 64.0
	desc := float64(metrics.Descent) / 64.0
	baseline := cy + ch/2 + (asc-desc)/2

	// 4. Measure text up to cursor position for scroll computation.
	var cursorPx float64
	if t.cursorPos > 0 && textLen > 0 {
		bOff := runeByteOffset(text, t.cursorPos)
		cursorPx = dc.MeasureText(text[:bOff], t.regularFontID)
	}

	// 5. Measure the character under the cursor for the box width.
	var cursorCharW float64
	if t.cursorPos < textLen {
		bOff := runeByteOffset(text, t.cursorPos)
		_, sz := utf8.DecodeRuneInString(text[bOff:])
		cursorCharW = dc.MeasureText(text[bOff:bOff+sz], t.regularFontID)
	}
	if cursorCharW < 2 {
		cursorCharW = float64(t.FontSize) * 0.6 // default box width at end of text
	}

	// 6. Auto-scroll so cursor box stays visible within the content area.
	cursorRight := cursorPx + cursorCharW
	if cursorRight-t.scrollOff > cw-2 {
		t.scrollOff = cursorRight - cw + 2
	}
	if cursorPx-t.scrollOff < 0 {
		t.scrollOff = cursorPx
	}
	if t.scrollOff < 0 {
		t.scrollOff = 0
	}

	// 6b. Render selection highlight.
	if t.HasSelection() && textLen > 0 {
		selStart, selEnd := t.SelectionRange()
		var selStartPx, selEndPx float64
		if selStart > 0 {
			selStartPx = dc.MeasureText(text[:runeByteOffset(text, selStart)], t.regularFontID)
		}
		selEndPx = dc.MeasureText(text[:runeByteOffset(text, selEnd)], t.regularFontID)
		sx := cx + selStartPx - t.scrollOff
		sw := selEndPx - selStartPx
		if sw > 0 {
			dc.SetColor(pal.SurfaceTint())
			selH := float64(t.FontSize) * 1.2
			selY := cy + (ch-selH)/2
			dc.FillRectangle(sx, selY, sw, selH)
		}
	}

	// 7. Render text or hint.
	textX := cx - t.scrollOff
	if textLen > 0 {
		dc.SetColor(t.resolveTextColor(pal))
		dc.DrawText(text, t.regularFontID, textX, baseline)
	} else if t.Hint != "" {
		tc := pal.Text()
		hintAlpha := uint8(float64(tc.A) * 0.8)
		dc.SetColor(color.NRGBA{tc.R, tc.G, tc.B, hintAlpha})
		dc.DrawText(t.Hint, t.italicFontID, cx, baseline)
	}

	// 8. Draw box cursor: filled when focused, outline when unfocused.
	cursorX := cx + cursorPx - t.scrollOff
	cursorH := float64(t.FontSize) * 1.2
	cursorY := cy + (ch-cursorH)/2
	tc := t.resolveTextColor(pal)
	if t.Focused {
		dc.SetColor(color.NRGBA{tc.R, tc.G, tc.B, 128})
		dc.FillRectangle(cursorX, cursorY, cursorCharW, cursorH)
	} else {
		dc.SetColor(color.NRGBA{tc.R, tc.G, tc.B, 96})
		dc.SetLineWidth(1.0)
		dc.DrawRectangle(cursorX, cursorY, cursorCharW, cursorH)
		dc.Stroke()
	}

	// Clear damage after drawing so parents see empty damage next frame.
	t.ClearDamage()
}
