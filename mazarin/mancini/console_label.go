package mancini

import (
	"image/color"
)

// ConsoleLabel is a left-aligned monospaced text label with a configurable
// background color. Designed for console output lines (stdout/stderr).
type ConsoleLabel struct {
	Pal       Palette
	Fonts     *FontConfig
	Name      string
	FontSize  int64
	BgColor   color.NRGBA        // line background (content well color)
	TextFunc  func() string      // dynamic text source
	ColorFunc func() color.NRGBA // per-line color (stdout vs stderr)
	Layout    *LayoutHandles

	lastDrawnText string
	lastDrawnHash int64
}

func (l *ConsoleLabel) GetLayout() *LayoutHandles { return l.Layout }

// PreferredHeight returns the line height for console text.
func (l *ConsoleLabel) PreferredHeight() float64 {
	return float64(l.FontSize) + 2 // fontSize + LineSpacing
}

// Draw implements the Drawer interface.
func (l *ConsoleLabel) Draw(dc DrawContext, x, y, w, h float64) {
	if !isVisible(l) {
		return
	}
	ph := l.PreferredHeight()
	publishLayout(l.Layout, x, y, w, ph)

	text := ""
	if l.TextFunc != nil {
		text = l.TextFunc()
	}

	hash := l.Layout.boundsHashValue()
	if text == l.lastDrawnText && hash == l.lastDrawnHash && l.lastDrawnHash != 0 {
		return // unchanged
	}
	l.lastDrawnText = text
	l.lastDrawnHash = hash

	// Fill background.
	dc.SetColor(l.BgColor)
	dc.FillRectangle(x, y, w, ph)

	if len(text) == 0 {
		return
	}

	// Draw left-aligned text.
	col := l.Pal.Text
	if l.ColorFunc != nil {
		col = l.ColorFunc()
	}
	if !loadFont(l.Fonts, dc, false, l.FontSize) {
		return
	}
	dc.SetColor(col)
	// Anchor (0, 0.5): left edge, vertically centered.
	dc.DrawStringAnchored(text, x+4, y+ph/2, 0, 0.5)
}

// InitLayout creates layout handles for the ConsoleLabel.
func (l *ConsoleLabel) InitLayout(parent string) {
	if l.Name == "" {
		l.Name = DefaultName("clabel")
	}
	l.Layout = newLayoutHandles(l.Name, parent)
	l.Layout.Height.Set(int64(l.PreferredHeight()))
}
