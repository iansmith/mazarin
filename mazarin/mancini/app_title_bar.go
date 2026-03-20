package mancini

import (
	"image/color"
)

// AppTitleBar is a title bar interactor that centers a child Label
// and draws horizontal pinstripes on either side — classic Mac OS style.
// It is a special variant of a container with exactly one child.
type AppTitleBar struct {
	Theme  *Theme
	Name   string
	Child  Drawer // the label interactor (used for constraint sizing)
	Layout *LayoutHandles
}

func (tb *AppTitleBar) GetLayout() *LayoutHandles { return tb.Layout }

// PreferredHeight returns the child's preferred height plus vertical padding.
func (tb *AppTitleBar) PreferredHeight() float64 {
	childH := 0.0
	if s, ok := tb.Child.(Sizer); ok {
		childH = s.PreferredHeight()
	}
	return childH + tb.Theme.Px(4) // 2px padding top + 2px padding bottom
}

// PreferredWidth returns the child's preferred width plus horizontal padding.
func (tb *AppTitleBar) PreferredWidth() float64 {
	childW := 0.0
	if s, ok := tb.Child.(WidthSizer); ok {
		childW = s.PreferredWidth()
	}
	return childW + tb.Theme.Px(16) // 8px padding each side
}

// Draw implements the Drawer interface. It draws pinstripes on both sides
// of the centered title text. The child Label is used for constraint sizing
// but drawing is done directly here to avoid fillRect artifacts inside the
// parent AppWindow's neumorphic surface.
func (tb *AppTitleBar) Draw(dc DrawContext, x, y, w, h float64) {
	publishLayout(tb.Layout, x, y, w, h)

	// Publish child layout for constraint system (without calling Label.Draw).
	childW := w
	if s, ok := tb.Child.(WidthSizer); ok {
		childW = s.PreferredWidth()
	}
	childH := h
	if s, ok := tb.Child.(Sizer); ok {
		childH = s.PreferredHeight()
	}
	childX := x + (w-childW)/2
	childY := y + (h-childH)/2
	if l, ok := tb.Child.(Layouter); ok {
		if lh := l.GetLayout(); lh != nil {
			publishLayout(lh, childX, childY, childW, childH)
		}
	}

	// Get label properties for direct text rendering.
	t := tb.Theme
	text := ""
	fontSize := t.Px(18)
	textColor := t.Pal.Text
	bold := true
	if label, ok := tb.Child.(*Label); ok {
		text = label.Text
		if label.TextFunc != nil {
			text = label.TextFunc()
		}
		fontSize = label.FontSize
		textColor = label.Color
		bold = label.Bold
	}

	// Measure text width for pinstripe gap calculation.
	loadFont(t, dc, bold, fontSize)
	tw, _ := dc.MeasureString(text)
	pad := t.Px(8)
	cx := x + w/2
	gapL := cx - tw/2 - pad
	gapR := cx + tw/2 + pad

	// Draw pinstripes on both sides of the title.
	darkC := t.C(t.Pal.DarkSh)
	stripe := color.NRGBA{darkC.R, darkC.G, darkC.B, 120}
	dc.SetColor(stripe)
	dc.SetLineWidth(t.Px(1))
	spacing := t.Px(3)
	inset := t.Px(4)
	for sy := y + spacing; sy < y+h-spacing/2; sy += spacing {
		if gapL > x+inset {
			dc.DrawLine(x+inset, sy, gapL, sy)
		}
		if gapR < x+w-inset {
			dc.DrawLine(gapR, sy, x+w-inset, sy)
		}
	}
	dc.Stroke()

	// Draw title text centered, using font metrics for proper vertical centering.
	// DrawStringAnchored(0.5, 0.5) uses line height which doesn't account for
	// the ascent/descent imbalance. Compute baseline position manually.
	dc.SetColor(t.C(textColor))
	if t.FontLoader != nil {
		face := t.FontLoader(bold, fontSize)
		if face != nil {
			metrics := face.Metrics()
			ascent := float64(metrics.Ascent) / 64.0
			descent := float64(metrics.Descent) / 64.0
			// ascent+descent = total text height (they partition the font size,
			// not add to it). Center text box in bar, then place baseline.
			baseline := y + (h-ascent-descent)/2 + ascent
			dc.DrawString(text, cx-tw/2, baseline)
		} else {
			dc.DrawStringAnchored(text, cx, y+h/2, 0.5, 0.5)
		}
	} else {
		dc.DrawStringAnchored(text, cx, y+h/2, 0.5, 0.5)
	}
}
