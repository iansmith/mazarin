package std

import (
	"image"
	"image/color"
	"unicode/utf8"

	"mazzy/mazarin/mancini"
)

// Draw implements mancini.NewDrawer. Renders the multi-line text field:
// inset background, visible text lines, selection highlight, and cursor.
func (m *MultiLineText) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	if !self.Visible() {
		return
	}
	if !m.Damaged(damage) {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}
	m.Mu.Lock()
	defer m.Mu.Unlock()
	m.ensureFonts(dc)

	pal := m.Theme().Palette()
	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)
	bw := m.BorderWidth

	// 1. Alert border.
	if m.isAlerting && m.alertAlpha > 0 {
		hi := pal.Highlight()
		dc.SetColor(color.NRGBA{hi.R, hi.G, hi.B, m.alertAlpha})
		dc.FillRectangle(fx, fy, fw, fh)
	}

	// 2. Inset field background.
	ix, iy := fx+bw, fy+bw
	iw, ih := fw-2*bw, fh-2*bw
	bgCol := pal.Base()
	if m.BgColor != nil {
		bgCol = *m.BgColor
	}
	m.Theme().Style().DrawBox(pal, dc, mancini.Inset, mancini.LightWeight,
		ix, iy, ix+iw, iy+ih, m.Radius, bgCol)

	// 3. Content area.
	pad := m.Padding
	cx := ix + pad
	cy := iy + pad
	cw := iw - 2*pad
	ch := ih - 2*pad
	if cw <= 0 || ch <= 0 {
		return
	}

	// Compute font metrics.
	metrics := dc.GetFontMetrics(m.regularFontID)
	asc := float64(metrics.Ascent) / 64.0
	desc := float64(metrics.Descent) / 64.0
	m.lineHeight = asc + desc + 2 // 2px inter-line spacing
	m.visibleLines = int(ch / m.lineHeight)
	if m.visibleLines < 1 {
		m.visibleLines = 1
	}

	// Re-wrap if width changed.
	if m.wrapWidth != cw {
		m.rewrap(dc, cw)
	}

	// Ensure cursor is visible.
	m.scrollCursorIntoView()

	topLine := m.topLine()
	cursorDispLine, _ := m.cursorDisplayPos()

	// Selection range in bytes (if any).
	var selStart, selEnd int
	hasSel := m.HasSelection()
	if hasSel {
		selStart, selEnd = m.SelectionRange()
	}

	// 4. Draw visible display lines.
	tc := m.resolveTextColor(pal)
	for i := 0; i < m.visibleLines && topLine+i < m.dispLineCount; i++ {
		dispIdx := topLine + i
		dlStart, dlEnd := m.displayLineByteRange(dispIdx)
		lineY := cy + float64(i)*m.lineHeight
		baseline := lineY + asc

		lineText := m.gap.Slice(dlStart, dlEnd)

		// 4a. Selection highlight for this display line.
		if hasSel && selStart < dlEnd && selEnd > dlStart {
			hlStart := selStart
			if hlStart < dlStart {
				hlStart = dlStart
			}
			hlEnd := selEnd
			if hlEnd > dlEnd {
				hlEnd = dlEnd
			}

			var hlStartPx, hlEndPx float64
			if hlStart > dlStart {
				prefix := m.gap.Slice(dlStart, hlStart)
				hlStartPx = dc.MeasureText(prefix, m.regularFontID)
			}
			hlText := m.gap.Slice(dlStart, hlEnd)
			hlEndPx = dc.MeasureText(hlText, m.regularFontID)

			selW := hlEndPx - hlStartPx
			if selW > 0 {
				dc.SetColor(pal.SurfaceTint())
				dc.FillRectangle(cx+hlStartPx, lineY, selW, m.lineHeight)
			}
		}

		// 4b. Text.
		if len(lineText) > 0 {
			dc.SetColor(tc)
			dc.DrawText(lineText, m.regularFontID, cx, baseline)
		}

		// 4c. Cursor on this display line.
		if dispIdx == cursorDispLine {
			cursorBytePos := m.gap.GapPos()
			var cursorPx float64
			if cursorBytePos > dlStart {
				prefix := m.gap.Slice(dlStart, cursorBytePos)
				cursorPx = dc.MeasureText(prefix, m.regularFontID)
			}

			// Cursor character width.
			var cursorCharW float64
			if cursorBytePos < dlEnd {
				charText := m.gap.Slice(cursorBytePos, cursorBytePos+runeByteLen(lineText, cursorBytePos-dlStart))
				cursorCharW = dc.MeasureText(charText, m.regularFontID)
			}
			if cursorCharW < 2 {
				cursorCharW = float64(m.FontSize) * 0.6
			}

			cursorX := cx + cursorPx
			if m.Focused {
				dc.SetColor(color.NRGBA{tc.R, tc.G, tc.B, 128})
				dc.FillRectangle(cursorX, lineY, cursorCharW, m.lineHeight)
			} else {
				dc.SetColor(color.NRGBA{tc.R, tc.G, tc.B, 96})
				dc.SetLineWidth(1.0)
				dc.DrawRectangle(cursorX, lineY, cursorCharW, m.lineHeight)
				dc.Stroke()
			}
		}
	}

	// 5. Disabled overlay.
	if m.Disabled {
		ApplyDisabledOverlay(pal, dc, fx, fy, fx+fw, fy+fh, m.Radius)
	}

	m.ClearDamage()
}

// runeByteLen returns the byte length of the first rune at byte offset off
// within a string s. Used to measure the character under the cursor.
func runeByteLen(s string, off int) int {
	if off >= len(s) {
		return 0
	}
	_, sz := utf8.DecodeRuneInString(s[off:])
	return sz
}
