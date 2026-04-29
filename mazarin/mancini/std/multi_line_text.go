package std

import (
	"image/color"
	"io"
	"sync"
	"unicode"
	"unicode/utf8"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/vm"
	"mazzy/shared/hid"
)

// DefaultMaxLineCount is the default maximum number of logical lines.
const DefaultMaxLineCount = 200

// MultiLineText is a multi-line text editor interactor. It uses a gap buffer
// internally for fast cursor-local edits. Text is accessed directly via
// [MultiLineText.Text] rather than through the constraint system.
//
// The interactor publishes several attributes for scrollbar integration:
//   - LineCount (int64): total display lines after soft-wrap
//   - CursorLine (int64): display line containing the cursor
//   - CursorCol (int64): rune column of the cursor within its display line
//   - TopLine (int64): first visible display line (settable by scrollbar)
//   - MaxLineCount (int64): maximum logical lines allowed
//
// It implements [mancini.KeyPressable], [mancini.Clickable],
// [mancini.DoubleClickable], [mancini.TripleClickable], and
// [mancini.ClickDraggable].
type MultiLineText struct {
	impl.ThemedInteractor

	// FontSize is the rendered font size in pixels.
	FontSize int64
	// Radius is the corner radius for the editor's background rectangle.
	Radius float64
	// Padding is the interior pixel padding between border and text.
	Padding float64
	// BorderWidth is the pixel width of the editor border.
	BorderWidth float64
	// Focused controls cursor rendering: only a focused editor draws a caret.
	Focused bool
	// TextColor overrides the palette's BaseText() when non-zero.
	TextColor color.NRGBA
	BgColor   *color.NRGBA // if non-nil, overrides pal.Base() for background
	// Disabled disables keyboard and mouse input handling.
	Disabled bool
	// AppWindow is required for animation-driven alerts.
	AppWindow *AppWindow
	AfterEdit func() // optional; called after each text modification

	// Mu guards the gap buffer for concurrent access. The main goroutine
	// (keyboard handler, draw) and any background reader (e.g., markdown
	// render goroutine) must hold Mu while accessing the gap buffer.
	Mu sync.Mutex

	// Internal text storage.
	gap *gapBuffer

	// Constraint-published attributes.
	lineCountAttr    *attr.Attribute[int64]
	cursorLineAttr   *attr.Attribute[int64]
	cursorColAttr    *attr.Attribute[int64]
	topLineAttr      *attr.Attribute[int64]
	maxLineCountAttr *attr.Attribute[int64]

	// Cursor state — byte offset in logical content (= gap position).
	// Selection is anchor..cursor (unordered).
	selAnchor int // byte offset, -1 = no selection
	stickyCol int // desired rune column for vertical movement, -1 = unset

	// Soft-wrap state.
	logLines []logLine // one per logical line (newline-delimited)
	dispLineCount int  // total display lines across all logical lines
	wrapWidth float64  // last width used for wrapping (0 = needs rewrap)

	// Font state.
	regularFontID int32
	fontsOpened   bool

	// Alert state (same pattern as SingleLineText).
	isAlerting   bool
	alertAlpha   uint8
	alertLocalID uint64

	// Visible line metrics (set during Draw, used for mouse hit testing).
	lineHeight float64 // pixels per display line
	visibleLines int   // number of display lines that fit in the viewport

}

// logLine describes one logical line (terminated by '\n' or end-of-content).
type logLine struct {
	start     int   // byte offset in logical content
	length    int   // byte length (excluding the '\n')
	wrapBreaks []int // byte offsets within this line where display wraps occur
}

// displayLineCount returns the number of display lines for this logical line.
func (ll *logLine) displayLineCount() int {
	return 1 + len(ll.wrapBreaks)
}

// NewMultiLineText creates a MultiLineText wired to the constraint system.
func NewMultiLineText(layout *mancini.LayoutAttributes, theme mancini.Theme,
	text string, fontSize int64) *MultiLineText {

	myName := layout.Name()
	gap := newGapBuffer(text)

	m := &MultiLineText{
		FontSize:    fontSize,
		Radius:      6.0,
		Padding:     8.0,
		BorderWidth: 2.0,
		gap:         gap,
		selAnchor:   -1,
		stickyCol:   -1,
	}

	// Scrollbar integration attributes (all value attributes).
	lineCountURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutLineCount)
	m.lineCountAttr = attr.ValueI64(lineCountURI, 0)

	cursorLineURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutCursorLine)
	m.cursorLineAttr = attr.ValueI64(cursorLineURI, 0)

	cursorColURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutCursorCol)
	m.cursorColAttr = attr.ValueI64(cursorColURI, 0)

	topLineURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutTopLine)
	m.topLineAttr = attr.ValueI64(topLineURI, 0)

	maxLineCountURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutMaxLineCount)
	m.maxLineCountAttr = attr.ValueI64(maxLineCountURI, int64(DefaultMaxLineCount))

	m.ThemedInteractor.Initialize(m, layout, theme)

	// Build initial line index and update attributes.
	m.rebuildLineIndex()

	return m
}

// NewMultiLineTextNamed creates a MultiLineText with layout built from name + parent.
func NewMultiLineTextNamed(myName, parent string, theme mancini.Theme,
	text string, fontSize int64, width, height int64) *MultiLineText {

	if myName == "" {
		myName = mancini.DefaultName("multilinetext")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Width.Set(width)
	lh.Height.Set(height)
	return NewMultiLineText(lh, theme, text, fontSize)
}

// ──────────────────────────────────────────────────────────────────────
// Text access
// ──────────────────────────────────────────────────────────────────────

// Text returns the current text content directly from the gap buffer.
func (m *MultiLineText) Text() string {
	return m.gap.String()
}

// SetText replaces the entire text content, resets cursor to end, and rebuilds.
func (m *MultiLineText) SetText(s string) {
	m.gap = newGapBuffer(s)
	m.selAnchor = -1
	m.stickyCol = -1
	m.wrapWidth = 0 // force rewrap
	m.rebuildLineIndex()
	m.FullDamage()
}

// ──────────────────────────────────────────────────────────────────────
// Line index and soft-wrap
// ──────────────────────────────────────────────────────────────────────

// rebuildLineIndex scans the gap buffer for newlines and rebuilds logLines.
// Wrap breaks are cleared (recomputed in rewrap or during Draw when width is known).
func (m *MultiLineText) rebuildLineIndex() {
	m.logLines = m.logLines[:0]
	contentLen := m.gap.Len()
	lineStart := 0
	m.gap.ForEachByte(func(pos int, b byte) bool {
		if b == '\n' {
			m.logLines = append(m.logLines, logLine{
				start:  lineStart,
				length: pos - lineStart,
			})
			lineStart = pos + 1
		}
		return true
	})
	// Last line (may be empty if text ends with '\n').
	m.logLines = append(m.logLines, logLine{
		start:  lineStart,
		length: contentLen - lineStart,
	})

	// Clear wrap state — will be recomputed when width is known.
	for i := range m.logLines {
		m.logLines[i].wrapBreaks = nil
	}
	m.wrapWidth = 0
	m.updateDisplayLineCount()
	m.updateCursorAttributes()
}

// rewrap recomputes soft-wrap breaks for all logical lines at the given width.
// Uses the DrawContext to measure text.
func (m *MultiLineText) rewrap(dc mancini.DrawContext, contentWidth float64) {
	if contentWidth <= 0 || !m.fontsOpened {
		return
	}
	m.wrapWidth = contentWidth
	for i := range m.logLines {
		m.wrapLogicalLine(dc, i, contentWidth)
	}
	m.updateDisplayLineCount()
	m.updateCursorAttributes()
}

// wrapLogicalLine computes wrap breaks for a single logical line.
func (m *MultiLineText) wrapLogicalLine(dc mancini.DrawContext, idx int, maxWidth float64) {
	ll := &m.logLines[idx]
	ll.wrapBreaks = nil

	if ll.length == 0 {
		return
	}

	lineText := m.gap.Slice(ll.start, ll.start+ll.length)
	breakStart := 0 // byte offset within lineText

	for breakStart < len(lineText) {
		remaining := lineText[breakStart:]
		if dc.MeasureText(remaining, m.regularFontID) <= maxWidth {
			break // rest fits on one display line
		}
		// Find the last position that fits within maxWidth.
		// Binary-ish search by scanning runes forward.
		bestBreak := -1
		bestWordBreak := -1
		pos := 0
		for pos < len(remaining) {
			r, sz := utf8.DecodeRuneInString(remaining[pos:])
			next := pos + sz
			w := dc.MeasureText(remaining[:next], m.regularFontID)
			if w > maxWidth {
				break
			}
			bestBreak = next
			if unicode.IsSpace(r) {
				bestWordBreak = next
			}
			pos = next
		}

		// Prefer breaking at a word boundary.
		bp := bestWordBreak
		if bp <= 0 {
			bp = bestBreak
		}
		if bp <= 0 {
			// Single character too wide — force advance by one rune.
			_, sz := utf8.DecodeRuneInString(remaining)
			bp = sz
		}

		breakStart += bp
		if breakStart < len(lineText) {
			ll.wrapBreaks = append(ll.wrapBreaks, breakStart)
		}
	}
}

// updateDisplayLineCount recalculates total display lines and updates the attribute.
func (m *MultiLineText) updateDisplayLineCount() {
	total := 0
	for i := range m.logLines {
		total += m.logLines[i].displayLineCount()
	}
	m.dispLineCount = total
	m.lineCountAttr.Set(int64(total))
}

// ──────────────────────────────────────────────────────────────────────
// Cursor ↔ line/column conversion
// ──────────────────────────────────────────────────────────────────────

// cursorLogicalLine returns the logical line index containing byte offset pos.
func (m *MultiLineText) cursorLogicalLine(pos int) int {
	for i, ll := range m.logLines {
		end := ll.start + ll.length
		if i < len(m.logLines)-1 {
			// The '\n' at byte position 'end' terminates this line.
			// A cursor AT 'end' (on the '\n') belongs to this line,
			// but a cursor at 'end+1' is the start of the next line.
			if pos <= end {
				return i
			}
		} else {
			// Last line: cursor anywhere up to and including end.
			if pos <= end {
				return i
			}
		}
	}
	return len(m.logLines) - 1
}

// cursorDisplayPos returns (displayLine, runeCol) for the current gap position.
func (m *MultiLineText) cursorDisplayPos() (dispLine int, runeCol int) {
	pos := m.gap.GapPos()
	logIdx := m.cursorLogicalLine(pos)
	ll := m.logLines[logIdx]
	localByte := pos - ll.start // byte offset within this logical line

	// Which display sub-line?
	subLine := 0
	subStart := 0
	for _, wb := range ll.wrapBreaks {
		if localByte < wb {
			break
		}
		subLine++
		subStart = wb
	}

	// Count runes from subStart to localByte.
	lineSlice := m.gap.Slice(ll.start+subStart, ll.start+localByte)
	runeCol = utf8.RuneCountInString(lineSlice)

	// Convert logical line + sub-line to absolute display line.
	dispLine = 0
	for i := 0; i < logIdx; i++ {
		dispLine += m.logLines[i].displayLineCount()
	}
	dispLine += subLine

	return dispLine, runeCol
}

// displayLineToByteOffset converts a display line number and rune column to
// a logical byte offset. If runeCol exceeds the display line's length, clamps
// to end of that display line.
func (m *MultiLineText) displayLineToByteOffset(dispLine, runeCol int) int {
	// Find which logical line and sub-line.
	remaining := dispLine
	for i := range m.logLines {
		ll := m.logLines[i]
		dlc := ll.displayLineCount()
		if remaining < dlc {
			// This is the logical line. remaining = sub-line index.
			subStart := 0
			subEnd := ll.length
			if remaining > 0 {
				subStart = ll.wrapBreaks[remaining-1]
			}
			if remaining < len(ll.wrapBreaks) {
				subEnd = ll.wrapBreaks[remaining]
			}

			// Walk runes from subStart to find runeCol.
			byteOff := subStart
			runeCount := 0
			for byteOff < subEnd && runeCount < runeCol {
				_, sz := m.gap.RuneAt(ll.start + byteOff)
				byteOff += sz
				runeCount++
			}
			return ll.start + byteOff
		}
		remaining -= dlc
	}
	// Past end — return end of content.
	return m.gap.Len()
}

// updateCursorAttributes pushes current cursor display position to attributes.
func (m *MultiLineText) updateCursorAttributes() {
	dl, col := m.cursorDisplayPos()
	m.cursorLineAttr.Set(int64(dl))
	m.cursorColAttr.Set(int64(col))
}

// ──────────────────────────────────────────────────────────────────────
// Scroll management
// ──────────────────────────────────────────────────────────────────────

// topLine returns the current top visible display line.
func (m *MultiLineText) topLine() int {
	return int(m.topLineAttr.Get())
}

// setTopLine sets the top visible display line (clamped).
func (m *MultiLineText) setTopLine(line int) {
	if line < 0 {
		line = 0
	}
	max := m.dispLineCount - m.visibleLines
	if max < 0 {
		max = 0
	}
	if line > max {
		line = max
	}
	m.topLineAttr.Set(int64(line))
}

// scrollCursorIntoView adjusts TopLine so the cursor's display line is visible.
func (m *MultiLineText) scrollCursorIntoView() {
	if m.visibleLines <= 0 {
		return
	}
	dl, _ := m.cursorDisplayPos()
	top := m.topLine()
	if dl < top {
		m.setTopLine(dl)
	} else if dl >= top+m.visibleLines {
		m.setTopLine(dl - m.visibleLines + 1)
	}
}

// ──────────────────────────────────────────────────────────────────────
// Editing operations
// ──────────────────────────────────────────────────────────────────────

// afterEdit is called after any text modification. Rebuilds the line index, scrolls the cursor into view,
// and marks damage.
//
// Optimization: if the cursor is at the end of the text (the common
// append-typing case), only the cursor's display line is damaged.
// Otherwise the entire interactor is damaged (edits in the middle can
// reflow all subsequent lines).
func (m *MultiLineText) afterEdit() {
	m.afterEditNoCallback()
	if m.AfterEdit != nil {
		m.AfterEdit()
	}
}

// afterEditNoCallback does all post-edit bookkeeping (line rebuild, rewrap,
// scroll, damage) but does NOT invoke the AfterEdit callback. Use this when
// the caller needs to release Mu before the callback runs.
func (m *MultiLineText) afterEditNoCallback() {
	m.stickyCol = -1
	// Save wrapWidth before rebuildLineIndex clears it.
	savedWrapWidth := m.wrapWidth
	m.rebuildLineIndex()
	// Re-wrap if we had a known width before the rebuild.
	if savedWrapWidth > 0 && m.fontsOpened {
		dc := m.DC()
		if dc != nil {
			m.rewrap(dc, savedWrapWidth)
		}
	}
	m.scrollCursorIntoView()

	// Damage: restrict to cursor line if appending at end.
	if m.gap.GapPos() == m.gap.Len() && m.lineHeight > 0 {
		m.damageCursorLine()
	} else {
		m.FullDamage()
	}
}

// damageCursorLine sets the damage rectangle to cover only the display
// line containing the cursor. Used when appending at end-of-text to
// avoid redrawing the entire interactor.
func (m *MultiLineText) damageCursorLine() {
	lh := m.GetLayout()
	if lh == nil {
		m.FullDamage()
		return
	}
	cursorDisp, _ := m.cursorDisplayPos()
	topLine := m.topLine()
	visIdx := cursorDisp - topLine
	if visIdx < 0 || visIdx >= m.visibleLines {
		// Cursor scrolled off-screen — full damage (scroll happened).
		m.FullDamage()
		return
	}

	// Compute the pixel rect of the cursor's display line.
	bw := m.BorderWidth
	pad := m.Padding
	x := lh.X.Get()
	y := lh.Y.Get()
	w := lh.Width.Get()
	lineY := int64(float64(y) + bw + pad + float64(visIdx)*m.lineHeight)
	lineH := int64(m.lineHeight) + 1

	rect := vm.RectangleVal(x, lineY, x+w, lineY+lineH)
	if lh.Damage == nil {
		lh.Damage = &mancini.DamageAttributes{}
	}
	uri := mancini.LayoutURI(lh.Name(), mancini.DataTypeRect, mancini.LayoutDamageRect)
	if lh.Damage.DamageRect == nil {
		lh.Damage.DamageRect = attr.ValueRectangle(uri, rect)
	} else if !lh.Damage.DamageRect.IsConstraint() {
		lh.Damage.DamageRect.Set(rect)
	}
}

// WriteTo writes the editor's content into w via the gap buffer, with no
// intermediate string allocation.
func (m *MultiLineText) WriteTo(w io.Writer) (int64, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	return m.gap.WriteTo(w)
}

// InsertRune inserts a rune at the cursor, respecting max line count.
func (m *MultiLineText) InsertRune(ch rune) {
	if m.HasSelection() {
		m.deleteSelection()
	}
	if ch == '\n' {
		// Check max logical line count.
		maxLines := int(m.maxLineCountAttr.Get())
		if maxLines > 0 && len(m.logLines) >= maxLines {
			m.Alert()
			return
		}
	}
	m.gap.InsertRune(ch)
	m.afterEdit()
}

// InsertString inserts a string at the cursor.
func (m *MultiLineText) InsertString(s string) {
	if m.HasSelection() {
		m.deleteSelection()
	}
	m.gap.InsertString(s)
	m.afterEdit()
}

// DeleteBackward deletes one rune before the cursor.
func (m *MultiLineText) DeleteBackward() {
	if m.HasSelection() {
		m.deleteSelection()
		return
	}
	pos := m.gap.GapPos()
	if pos == 0 {
		m.Alert()
		return
	}
	// Find the byte length of the rune before the cursor.
	// Walk backward to find the start of the previous rune.
	n := 1
	for n <= 4 && n <= pos {
		b := m.gap.ByteAt(pos - n)
		if b < 0x80 || b >= 0xC0 {
			break // found the start byte
		}
		n++
	}
	m.gap.DeleteBackward(n)
	m.afterEdit()
}

// DeleteForward deletes one rune after the cursor.
func (m *MultiLineText) DeleteForward() {
	if m.HasSelection() {
		m.deleteSelection()
		return
	}
	pos := m.gap.GapPos()
	if pos >= m.gap.Len() {
		m.Alert()
		return
	}
	_, sz := m.gap.RuneAt(pos)
	m.gap.DeleteForward(sz)
	m.afterEdit()
}

// DeleteBackwardWord deletes from cursor back to previous word boundary.
func (m *MultiLineText) DeleteBackwardWord() {
	if m.HasSelection() {
		m.deleteSelection()
		return
	}
	pos := m.gap.GapPos()
	prev := m.previousWordBoundary(pos)
	if prev == pos {
		return
	}
	m.gap.DeleteBackward(pos - prev)
	m.afterEdit()
}

// ──────────────────────────────────────────────────────────────────────
// Selection
// ──────────────────────────────────────────────────────────────────────

// HasSelection returns true if text is selected.
func (m *MultiLineText) HasSelection() bool {
	return m.selAnchor >= 0
}

// SelectionRange returns the ordered byte range of the selection.
func (m *MultiLineText) SelectionRange() (start, end int) {
	if m.selAnchor < 0 {
		return 0, 0
	}
	a, b := m.selAnchor, m.gap.GapPos()
	if a > b {
		a, b = b, a
	}
	return a, b
}

// ClearSelection removes the selection without moving the cursor.
func (m *MultiLineText) ClearSelection() {
	m.selAnchor = -1
}

// SelectedText returns the currently selected text.
func (m *MultiLineText) SelectedText() string {
	if !m.HasSelection() {
		return ""
	}
	start, end := m.SelectionRange()
	return m.gap.Slice(start, end)
}

// deleteSelection removes selected text and positions cursor at selection start.
func (m *MultiLineText) deleteSelection() {
	start, end := m.SelectionRange()
	m.selAnchor = -1
	m.gap.MoveTo(end)
	m.gap.DeleteBackward(end - start)
	m.afterEdit()
}

// SelectAll selects all text.
func (m *MultiLineText) SelectAll() {
	contentLen := m.gap.Len()
	if contentLen == 0 {
		return
	}
	m.selAnchor = 0
	m.gap.MoveTo(contentLen)
	m.updateCursorAttributes()
	m.FullDamage()
}

// startSelection sets anchor at current cursor if not already selecting.
func (m *MultiLineText) startSelection() {
	if m.selAnchor < 0 {
		m.selAnchor = m.gap.GapPos()
	}
}

// ──────────────────────────────────────────────────────────────────────
// Cursor movement
// ──────────────────────────────────────────────────────────────────────

// moveCursorTo moves the gap to pos, optionally extending selection.
func (m *MultiLineText) moveCursorTo(pos int, extending bool) {
	if extending {
		m.startSelection()
	} else {
		m.selAnchor = -1
	}
	m.gap.MoveTo(pos)
	m.updateCursorAttributes()
	m.scrollCursorIntoView()
	m.FullDamage()
}

// CursorLeft moves cursor one rune left.
func (m *MultiLineText) CursorLeft(extend bool) {
	m.stickyCol = -1
	if !extend && m.HasSelection() {
		start, _ := m.SelectionRange()
		m.moveCursorTo(start, false)
		return
	}
	pos := m.gap.GapPos()
	if pos == 0 {
		m.Alert()
		return
	}
	n := 1
	for n <= 4 && n <= pos {
		b := m.gap.ByteAt(pos - n)
		if b < 0x80 || b >= 0xC0 {
			break
		}
		n++
	}
	m.moveCursorTo(pos-n, extend)
}

// CursorRight moves cursor one rune right.
func (m *MultiLineText) CursorRight(extend bool) {
	m.stickyCol = -1
	if !extend && m.HasSelection() {
		_, end := m.SelectionRange()
		m.moveCursorTo(end, false)
		return
	}
	pos := m.gap.GapPos()
	if pos >= m.gap.Len() {
		m.Alert()
		return
	}
	_, sz := m.gap.RuneAt(pos)
	m.moveCursorTo(pos+sz, extend)
}

// CursorUp moves cursor one display line up, preserving sticky column.
func (m *MultiLineText) CursorUp(extend bool) {
	dl, col := m.cursorDisplayPos()
	if dl == 0 {
		m.Alert()
		return
	}
	if m.stickyCol < 0 {
		m.stickyCol = col
	}
	pos := m.displayLineToByteOffset(dl-1, m.stickyCol)
	m.moveCursorToKeepSticky(pos, extend)
}

// CursorDown moves cursor one display line down, preserving sticky column.
func (m *MultiLineText) CursorDown(extend bool) {
	dl, col := m.cursorDisplayPos()
	if dl >= m.dispLineCount-1 {
		m.Alert()
		return
	}
	if m.stickyCol < 0 {
		m.stickyCol = col
	}
	pos := m.displayLineToByteOffset(dl+1, m.stickyCol)
	m.moveCursorToKeepSticky(pos, extend)
}

// moveCursorToKeepSticky is like moveCursorTo but preserves stickyCol.
func (m *MultiLineText) moveCursorToKeepSticky(pos int, extending bool) {
	saved := m.stickyCol
	m.moveCursorTo(pos, extending)
	m.stickyCol = saved
}

// CursorHome moves cursor to beginning of current display line.
func (m *MultiLineText) CursorHome(extend bool) {
	m.stickyCol = -1
	dl, _ := m.cursorDisplayPos()
	pos := m.displayLineToByteOffset(dl, 0)
	m.moveCursorTo(pos, extend)
}

// CursorEnd moves cursor to end of current display line.
func (m *MultiLineText) CursorEnd(extend bool) {
	m.stickyCol = -1
	dl, _ := m.cursorDisplayPos()
	// Move to column = very large number, displayLineToByteOffset clamps.
	pos := m.displayLineToByteOffset(dl, 1<<30)
	m.moveCursorTo(pos, extend)
}

// CursorDocStart moves cursor to beginning of document.
func (m *MultiLineText) CursorDocStart(extend bool) {
	m.stickyCol = -1
	m.moveCursorTo(0, extend)
}

// CursorDocEnd moves cursor to end of document.
func (m *MultiLineText) CursorDocEnd(extend bool) {
	m.stickyCol = -1
	m.moveCursorTo(m.gap.Len(), extend)
}

// CursorWordLeft moves cursor to previous word boundary.
func (m *MultiLineText) CursorWordLeft(extend bool) {
	m.stickyCol = -1
	if !extend && m.HasSelection() {
		start, _ := m.SelectionRange()
		m.moveCursorTo(start, false)
		return
	}
	pos := m.previousWordBoundary(m.gap.GapPos())
	m.moveCursorTo(pos, extend)
}

// CursorWordRight moves cursor to next word boundary.
func (m *MultiLineText) CursorWordRight(extend bool) {
	m.stickyCol = -1
	if !extend && m.HasSelection() {
		_, end := m.SelectionRange()
		m.moveCursorTo(end, false)
		return
	}
	pos := m.nextWordBoundary(m.gap.GapPos())
	m.moveCursorTo(pos, extend)
}

// previousWordBoundary finds the byte offset of the start of the previous word.
func (m *MultiLineText) previousWordBoundary(pos int) int {
	if pos == 0 {
		return 0
	}
	// Skip spaces/newlines backward.
	for pos > 0 {
		n := m.prevRuneWidth(pos)
		r, _ := m.gap.RuneAt(pos - n)
		if !unicode.IsSpace(r) {
			break
		}
		pos -= n
	}
	// Skip non-space backward.
	for pos > 0 {
		n := m.prevRuneWidth(pos)
		r, _ := m.gap.RuneAt(pos - n)
		if unicode.IsSpace(r) {
			break
		}
		pos -= n
	}
	return pos
}

// nextWordBoundary finds the byte offset past the end of the current word.
func (m *MultiLineText) nextWordBoundary(pos int) int {
	contentLen := m.gap.Len()
	if pos >= contentLen {
		return contentLen
	}
	// Skip non-space forward.
	for pos < contentLen {
		r, sz := m.gap.RuneAt(pos)
		if unicode.IsSpace(r) {
			break
		}
		pos += sz
	}
	// Skip spaces forward.
	for pos < contentLen {
		r, sz := m.gap.RuneAt(pos)
		if !unicode.IsSpace(r) {
			break
		}
		pos += sz
	}
	return pos
}

// prevRuneWidth returns the byte width of the rune ending at byte offset pos.
func (m *MultiLineText) prevRuneWidth(pos int) int {
	n := 1
	for n <= 4 && n <= pos {
		b := m.gap.ByteAt(pos - n)
		if b < 0x80 || b >= 0xC0 {
			break
		}
		n++
	}
	return n
}

// ──────────────────────────────────────────────────────────────────────
// Word/line selection helpers (for double/triple click)
// ──────────────────────────────────────────────────────────────────────

// wordBoundsAt returns the byte range [start, end) of the word at byte offset pos.
func (m *MultiLineText) wordBoundsAt(pos int) (int, int) {
	contentLen := m.gap.Len()
	if contentLen == 0 {
		return 0, 0
	}
	if pos >= contentLen {
		pos = contentLen - 1
	}

	start := pos
	end := pos

	// Expand backward.
	for start > 0 {
		n := m.prevRuneWidth(start)
		r, _ := m.gap.RuneAt(start - n)
		if unicode.IsSpace(r) {
			break
		}
		start -= n
	}
	// Expand forward.
	for end < contentLen {
		r, sz := m.gap.RuneAt(end)
		if unicode.IsSpace(r) {
			break
		}
		end += sz
	}
	return start, end
}

// logicalLineBoundsAt returns the byte range [start, end) of the logical line
// containing byte offset pos. The '\n' is not included.
func (m *MultiLineText) logicalLineBoundsAt(pos int) (int, int) {
	idx := m.cursorLogicalLine(pos)
	ll := m.logLines[idx]
	return ll.start, ll.start + ll.length
}

// ──────────────────────────────────────────────────────────────────────
// Alert (same pattern as SingleLineText)
// ──────────────────────────────────────────────────────────────────────

// Alert flashes a highlight border.
func (m *MultiLineText) Alert() {
	if m.isAlerting {
		return
	}
	m.isAlerting = true
	m.alertAlpha = 255
	m.FullDamage()

	if m.AppWindow == nil {
		return
	}
	ts, err := sys.GetTime()
	if err != nil {
		return
	}
	nowNanos := int64(ts.Seconds)*1_000_000_000 + int64(ts.Nanoseconds)
	startNanos := nowNanos + 500_000_000
	endNanos := nowNanos + 1_000_000_000
	m.alertLocalID = m.AppWindow.RegisterAnimation(m, startNanos, endNanos)
}

// ClearAlert removes the alert border.
func (m *MultiLineText) ClearAlert() {
	if !m.isAlerting {
		return
	}
	m.isAlerting = false
	m.alertAlpha = 0
	m.alertLocalID = 0
	m.FullDamage()
}

// AnimationStart implements [mancini.Animatable]; no-op for the alert fade.
func (m *MultiLineText) AnimationStart(localID uint64, startNanos int64) {}

// AnimationUpdate implements [mancini.Animatable]; fades the alert border alpha.
func (m *MultiLineText) AnimationUpdate(localID uint64,
	startNanos, endNanos int64, coveredStart, coveredEnd float64, nanosSinceStart int64) {
	m.alertAlpha = uint8(255.0 * (1.0 - coveredEnd))
	m.FullDamage()
}

// AnimationFinish implements [mancini.Animatable]; clears the alert state.
func (m *MultiLineText) AnimationFinish(localID uint64, endNanos int64) {
	m.isAlerting = false
	m.alertAlpha = 0
	m.alertLocalID = 0
	m.FullDamage()
}

// ──────────────────────────────────────────────────────────────────────
// Font helpers
// ──────────────────────────────────────────────────────────────────────

func (m *MultiLineText) ensureFonts(dc mancini.DrawContext) {
	if m.fontsOpened || dc == nil {
		return
	}
	m.fontsOpened = true
	m.regularFontID = mancini.OpenFont(dc, m.Theme(), mancini.None, m.FontSize)
}

func (m *MultiLineText) resolveTextColor(pal mancini.Palette) color.NRGBA {
	if m.TextColor != (color.NRGBA{}) {
		return m.TextColor
	}
	return pal.BaseText()
}

// ──────────────────────────────────────────────────────────────────────
// Hit testing — convert pixel (x, y) to byte offset
// ──────────────────────────────────────────────────────────────────────

// hitTestPosition converts local pixel coordinates to a byte offset
// in the gap buffer. Used by mouse click handlers.
func (m *MultiLineText) hitTestPosition(localX, localY int64) int {
	if m.lineHeight <= 0 || !m.fontsOpened {
		return 0
	}
	dc := m.DC()
	if dc == nil {
		return 0
	}

	bw := m.BorderWidth
	pad := m.Padding
	contentX := float64(localX) - bw - pad
	contentY := float64(localY) - bw - pad

	// Which display line?
	dispLine := m.topLine() + int(contentY/m.lineHeight)
	if dispLine < 0 {
		dispLine = 0
	}
	if dispLine >= m.dispLineCount {
		dispLine = m.dispLineCount - 1
	}

	// Get the text for that display line and find the rune column.
	dlStart, dlEnd := m.displayLineByteRange(dispLine)
	if contentX <= 0 {
		return dlStart
	}

	lineText := m.gap.Slice(dlStart, dlEnd)
	// Walk runes, measuring progressively, to find the closest position.
	bestPos := 0
	pos := 0
	for pos < len(lineText) {
		_, sz := utf8.DecodeRuneInString(lineText[pos:])
		mid := dc.MeasureText(lineText[:pos], m.regularFontID) +
			dc.MeasureText(lineText[pos:pos+sz], m.regularFontID)/2
		if contentX < mid {
			break
		}
		pos += sz
		bestPos = pos
	}
	return dlStart + bestPos
}

// displayLineByteRange returns the logical byte range [start, end) for a display line.
func (m *MultiLineText) displayLineByteRange(dispLine int) (int, int) {
	remaining := dispLine
	for i := range m.logLines {
		ll := m.logLines[i]
		dlc := ll.displayLineCount()
		if remaining < dlc {
			subStart := 0
			subEnd := ll.length
			if remaining > 0 {
				subStart = ll.wrapBreaks[remaining-1]
			}
			if remaining < len(ll.wrapBreaks) {
				subEnd = ll.wrapBreaks[remaining]
			}
			return ll.start + subStart, ll.start + subEnd
		}
		remaining -= dlc
	}
	// Past end.
	n := m.gap.Len()
	return n, n
}

// ──────────────────────────────────────────────────────────────────────
// KeyPress — keyboard input handling
// ──────────────────────────────────────────────────────────────────────

// KeyPress implements mancini.KeyPressable.
func (m *MultiLineText) KeyPress(ch rune, action string, ev *mancini.InputEvent) bool {
	if m.Disabled {
		return false
	}
	m.Mu.Lock()
	mods := int64(ev.Mods)
	shift := hid.Shift(mods)

	// Suppress the AfterEdit callback while we hold Mu to avoid
	// deadlock — checkCommand re-acquires Mu. We call it after unlock.
	savedAfterEdit := m.AfterEdit
	m.AfterEdit = nil

	handled := true
	edited := false
	switch action {
	case "backspace":
		if hid.Alt(mods) {
			m.DeleteBackwardWord()
		} else {
			m.DeleteBackward()
		}
		edited = true
	case "delete":
		m.DeleteForward()
		edited = true
	case "left":
		if hid.Meta(mods) {
			m.CursorHome(shift)
		} else if hid.Alt(mods) {
			m.CursorWordLeft(shift)
		} else {
			m.CursorLeft(shift)
		}
	case "right":
		if hid.Meta(mods) {
			m.CursorEnd(shift)
		} else if hid.Alt(mods) {
			m.CursorWordRight(shift)
		} else {
			m.CursorRight(shift)
		}
	case "up":
		if hid.Meta(mods) {
			m.CursorDocStart(shift)
		} else {
			m.CursorUp(shift)
		}
	case "down":
		if hid.Meta(mods) {
			m.CursorDocEnd(shift)
		} else {
			m.CursorDown(shift)
		}
	case "home":
		m.CursorHome(shift)
	case "end":
		m.CursorEnd(shift)
	case "enter":
		m.InsertRune('\n')
		edited = true
	case "escape":
		if m.HasSelection() {
			m.ClearSelection()
			m.FullDamage()
		}
	case "":
		handled = false
		if hid.Ctrl(mods) {
			switch ch {
			case 'a', 'A':
				if shift {
					m.SelectAll()
				} else {
					m.CursorHome(false)
				}
				handled = true
			case 'e', 'E':
				m.CursorEnd(false)
				handled = true
			case 'w', 'W':
				m.DeleteBackwardWord()
				handled = true
				edited = true
			}
		}
		if !handled && hid.Meta(mods) {
			switch ch {
			case 'a', 'A':
				m.SelectAll()
				handled = true
			}
		}
		if !handled && ch != 0 {
			m.InsertRune(ch)
			handled = true
			edited = true
		}
	default:
		handled = false
	}

	m.AfterEdit = savedAfterEdit
	m.Mu.Unlock()

	// Fire AfterEdit callback outside the lock to avoid deadlock
	// when the callback re-acquires Mu.
	if edited && savedAfterEdit != nil {
		savedAfterEdit()
	}

	return handled
}

// SetFocused sets the Focused flag, which controls cursor rendering.
func (m *MultiLineText) SetFocused(v bool) { m.Focused = v }

// ──────────────────────────────────────────────────────────────────────
// Mouse interaction
// ──────────────────────────────────────────────────────────────────────

// Click implements mancini.Clickable — positions cursor at click location.
func (m *MultiLineText) Click(ev *mancini.InputEvent) bool {
	if m.Disabled {
		return false
	}
	m.Mu.Lock()
	defer m.Mu.Unlock()
	pos := m.hitTestPosition(ev.X, ev.Y)
	m.stickyCol = -1
	m.moveCursorTo(pos, false)
	return true
}

// DoubleClick implements mancini.DoubleClickable — selects word at click.
func (m *MultiLineText) DoubleClick(ev *mancini.InputEvent) bool {
	if m.Disabled {
		return false
	}
	m.Mu.Lock()
	defer m.Mu.Unlock()
	pos := m.hitTestPosition(ev.X, ev.Y)
	start, end := m.wordBoundsAt(pos)
	m.selAnchor = start
	m.gap.MoveTo(end)
	m.stickyCol = -1
	m.updateCursorAttributes()
	m.scrollCursorIntoView()
	m.FullDamage()
	return true
}

// TripleClick implements mancini.TripleClickable — selects logical line.
func (m *MultiLineText) TripleClick(ev *mancini.InputEvent) bool {
	if m.Disabled {
		return false
	}
	m.Mu.Lock()
	defer m.Mu.Unlock()
	pos := m.hitTestPosition(ev.X, ev.Y)
	start, end := m.logicalLineBoundsAt(pos)
	m.selAnchor = start
	m.gap.MoveTo(end)
	m.stickyCol = -1
	m.updateCursorAttributes()
	m.scrollCursorIntoView()
	m.FullDamage()
	return true
}

// ClickDragStart implements mancini.ClickDraggable — begins drag selection.
func (m *MultiLineText) ClickDragStart(ev *mancini.InputEvent) bool {
	if m.Disabled {
		return false
	}
	m.Mu.Lock()
	defer m.Mu.Unlock()
	pos := m.hitTestPosition(ev.X, ev.Y)
	m.selAnchor = pos
	m.gap.MoveTo(pos)
	m.stickyCol = -1
	m.updateCursorAttributes()
	m.FullDamage()
	return true
}

// ClickDragMove implements mancini.ClickDraggable — extends drag selection.
func (m *MultiLineText) ClickDragMove(ev *mancini.InputEvent, startEv *mancini.InputEvent, outsideBounds bool) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	pos := m.hitTestPosition(ev.X, ev.Y)
	m.gap.MoveTo(pos)
	m.stickyCol = -1
	m.updateCursorAttributes()
	m.scrollCursorIntoView()
	m.FullDamage()
}

// ClickDragEnd implements mancini.ClickDraggable — finalizes drag selection.
func (m *MultiLineText) ClickDragEnd(ev *mancini.InputEvent, outsideBounds bool) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	pos := m.hitTestPosition(ev.X, ev.Y)
	m.gap.MoveTo(pos)
	m.stickyCol = -1
	m.updateCursorAttributes()
	// If anchor == cursor, clear selection.
	if m.selAnchor == m.gap.GapPos() {
		m.selAnchor = -1
	}
	m.FullDamage()
}

// HasAlert returns true if the interactor is showing an alert border.
func (m *MultiLineText) HasAlert() bool {
	return m.isAlerting
}
