package mancini

import (
	"image"
	"image/color"
	"math"
	"time"

	"github.com/fogleman/gg"
)

// loadFont sets the font on a gg context. Uses Theme.FontLoader if available,
// otherwise falls back to loading from FontRegular/FontBold file paths.
func loadFont(t *Theme, dc DrawContext, bold bool, size float64) bool {
	if t.FontLoader != nil {
		face := t.FontLoader(bold, size)
		if face != nil {
			dc.SetFontFace(face)
			return true
		}
	}
	path := t.FontRegular
	if bold {
		path = t.FontBold
	}
	if path == "" {
		return false
	}
	return dc.LoadFontFace(path, size) == nil
}

// ── AppWindow ────────────────────────────────────────────────────────────────

// AppWindow is a neumorphic application window.
// Resting: Flush, plain title, floaters hidden.
// Focused: Raised, decorated title bar, floaters visible.
type AppWindow struct {
	Theme    *Theme
	Name     string
	Title    string
	Focused  bool
	TitleBar Drawer // used when Focused (StripedTitleFace, GradientTitleFace, or custom)
	Content  Drawer
	Floaters []*FreeFloatingWindow
	Layout   *LayoutHandles

	lastBoundsHash int64
	lastFocused    bool
}

func (w *AppWindow) GetLayout() *LayoutHandles { return w.Layout }

func (w *AppWindow) Depth() NeuDepth {
	if w.Focused {
		return Raised
	}
	return Flush
}

func (w *AppWindow) Focus() {
	w.Focused = true
	for _, f := range w.Floaters {
		f.Visible = true
	}
}

func (w *AppWindow) Unfocus() {
	w.Focused = false
	for _, f := range w.Floaters {
		f.Visible = false
	}
}

// Draw implements the Drawer interface.
func (w *AppWindow) Draw(dc DrawContext, x, y, ww, hh float64) {
	publishLayout(w.Layout, x, y, ww, hh)
	t := w.Theme

	// Skip expensive NeuBoxWith calls when bounds and focus are unchanged.
	needDecoration := true
	hash := w.Layout.boundsHashValue()
	if hash == w.lastBoundsHash && w.lastBoundsHash != 0 && w.Focused == w.lastFocused {
		needDecoration = false
	}
	w.lastBoundsHash = hash
	w.lastFocused = w.Focused

	if needDecoration {
		NeuBoxWith(t, dc, w.Depth(), x, y, x+ww, y+hh, t.Px(14), t.Pal.Surface, WindowParams, nil)
	}

	// Title bar
	tbMargin := t.Px(8)
	tbX, tbY := x+tbMargin, y+tbMargin
	tbW := ww - 2*tbMargin
	tbH := t.Px(26) // default
	if s, ok := w.TitleBar.(Sizer); ok {
		tbH = s.PreferredHeight()
	}
	if needDecoration {
		if w.Focused && w.TitleBar != nil {
			w.TitleBar.Draw(dc, tbX, tbY, tbW, tbH)
		} else {
			// Unfocused: plain centered text, no pinstripes.
			TextFace(t, w.Title, t.Px(18), t.Pal.Text, false)(dc, tbX, tbY, tbW, tbH)
		}
	}

	// Content area — bounds enforcement is done by Row/Column (skip children
	// that don't fully fit). Shadow compositing bypasses gg's clip mask, so
	// pixel-level dc.Clip() alone cannot prevent overflow. Child-level bounds
	// checking is both correct and fast.
	cX := x + tbMargin
	cY := tbY + tbH + t.Px(6)
	cW := ww - 2*tbMargin
	cH := (y + hh) - cY - tbMargin
	if w.Content != nil {
		w.Content.Draw(dc, cX, cY, cW, cH)
	}

	// Floaters
	for _, f := range w.Floaters {
		if f.Visible {
			f.Draw(dc, f.X, f.Y, f.W, f.H)
		}
	}
}

// fillRect fills a rectangle with the theme's surface color (fast, no gg context).
func fillRect(canvas *image.RGBA, t *Theme, x, y, w, h float64) {
	sc := t.Pal.Surface
	if t.SwapRB {
		sc = color.NRGBA{R: sc.B, G: sc.G, B: sc.R, A: sc.A}
	}
	x0, y0 := int(x), int(y)
	x1, y1 := int(x+w), int(y+h)
	b := canvas.Bounds()
	if x0 < b.Min.X {
		x0 = b.Min.X
	}
	if y0 < b.Min.Y {
		y0 = b.Min.Y
	}
	if x1 > b.Max.X {
		x1 = b.Max.X
	}
	if y1 > b.Max.Y {
		y1 = b.Max.Y
	}
	for py := y0; py < y1; py++ {
		for px := x0; px < x1; px++ {
			off := canvas.PixOffset(px, py)
			canvas.Pix[off+0] = sc.R
			canvas.Pix[off+1] = sc.G
			canvas.Pix[off+2] = sc.B
			canvas.Pix[off+3] = sc.A
		}
	}
}

// ── FreeFloatingWindow ───────────────────────────────────────────────────────

// FreeFloatingWindow is a neumorphic floating panel owned by an AppWindow.
// Always Flush when visible.
type FreeFloatingWindow struct {
	Theme   *Theme
	Name    string
	Title   string
	Visible bool
	Content Drawer
	X, Y    float64 // position (set by caller or constraints)
	W, H    float64 // size (set by caller or constraints)
	Layout  *LayoutHandles

	lastBoundsHash int64
}

func (w *FreeFloatingWindow) GetLayout() *LayoutHandles { return w.Layout }

func (w *FreeFloatingWindow) Depth() NeuDepth {
	return Flush
}

// Draw implements the Drawer interface.
func (w *FreeFloatingWindow) Draw(dc DrawContext, x, y, ww, hh float64) {
	publishLayout(w.Layout, x, y, ww, hh)
	t := w.Theme

	// Skip expensive NeuBoxWith calls when bounds are unchanged.
	needDecoration := true
	hash := w.Layout.boundsHashValue()
	if hash == w.lastBoundsHash && w.lastBoundsHash != 0 {
		needDecoration = false
	}
	w.lastBoundsHash = hash

	if needDecoration {
		NeuBoxWith(t, dc, Flush, x, y, x+ww, y+hh, t.Px(14), t.Pal.Surface, WindowParams, nil)
	}

	// Title
	titleY := y + t.Px(14)

	loadFont(t, dc, true, t.Px(10))
	dc.SetColor(t.C(t.Pal.Text))
	dc.DrawStringAnchored(w.Title, x+ww/2, titleY, 0.5, 0.5)

	// Groove separator
	grooveMargin := t.Px(18)
	grooveY := y + t.Px(26)
	if needDecoration {
		NeuGroove(t, dc, x+grooveMargin, grooveY, x+ww-grooveMargin)
	}

	// Content area below groove
	cY := grooveY + t.Px(6)
	cH := (y + hh) - cY
	if w.Content != nil {
		w.Content.Draw(dc, x, cY, ww, cH)
	}
}

// ── Button ───────────────────────────────────────────────────────────────────

// Button is a neumorphic button that delegates its face rendering to a Drawer.
type Button struct {
	Theme  *Theme
	Name   string
	Depth  NeuDepth
	Face   Drawer
	Layout *LayoutHandles
}

func (b *Button) GetLayout() *LayoutHandles { return b.Layout }

// Draw implements the Drawer interface.
func (b *Button) Draw(dc DrawContext, x, y, w, h float64) {
	publishLayout(b.Layout, x, y, w, h)
	DrawNeuBox(b.Theme, dc, b.Depth, x, y, x+w, y+h, b.Theme.Px(8),
		b.Theme.Pal.Surface, asFaceDrawer(b.Face))
}

// ── NeuLabel ─────────────────────────────────────────────────────────────────

// NeuLabel is a text label inside a neumorphic box at any depth.
type NeuLabel struct {
	Theme    *Theme
	Name     string
	Depth    NeuDepth
	Text     string         // static text (used when TextFunc is nil)
	TextFunc func() string  // dynamic text source (takes precedence over Text)
	FontSize float64
	Color    color.NRGBA
	Bold     bool
	Layout   *LayoutHandles
}

func (l *NeuLabel) GetLayout() *LayoutHandles { return l.Layout }

// PreferredHeight returns the preferred height for a NeuLabel.
func (l *NeuLabel) PreferredHeight() float64 {
	return l.FontSize + l.Theme.Px(16) // font + box padding
}

// Draw implements the Drawer interface.
func (l *NeuLabel) Draw(dc DrawContext, x, y, w, h float64) {
	publishLayout(l.Layout, x, y, w, h)
	text := l.Text
	if l.TextFunc != nil {
		text = l.TextFunc()
	}
	DrawNeuBox(l.Theme, dc, l.Depth, x, y, x+w, y+h, l.Theme.Px(8),
		l.Theme.Pal.Surface, TextFace(l.Theme, text, l.FontSize, l.Color, l.Bold))
}

// ── Label ────────────────────────────────────────────────────────────────────

// Label is plain text with no neumorphic box.
type Label struct {
	Theme    *Theme
	Name     string
	Text     string         // static text (used when TextFunc is nil)
	TextFunc func() string  // dynamic text source (takes precedence over Text)
	FontSize float64
	Color    color.NRGBA
	Bold     bool
	Layout   *LayoutHandles

	lastDrawnText string
	lastDrawnHash int64
}

func (l *Label) GetLayout() *LayoutHandles { return l.Layout }

// PreferredHeight returns the preferred height for a Label.
func (l *Label) PreferredHeight() float64 {
	return l.FontSize + l.Theme.Px(4) // font + minimal padding
}

// PreferredWidth returns the preferred width for a Label based on text measurement.
func (l *Label) PreferredWidth() float64 {
	text := l.Text
	if l.TextFunc != nil {
		text = l.TextFunc()
	}
	return l.Theme.MeasureText(text, l.Bold, l.FontSize) + l.Theme.Px(8) // text + padding
}

// Draw implements the Drawer interface.
func (l *Label) Draw(dc DrawContext, x, y, w, h float64) {
	if !isVisible(l) {
		return
	}
	// Publish intrinsic dimensions so inside-out constraints see actual text size.
	pw := l.PreferredWidth()
	ph := l.PreferredHeight()
	publishLayout(l.Layout, x, y, pw, ph)
	text := l.Text
	if l.TextFunc != nil {
		text = l.TextFunc()
	}
	hash := l.Layout.boundsHashValue()
	if text == l.lastDrawnText && hash == l.lastDrawnHash && l.lastDrawnHash != 0 {
		return // text and position unchanged — no damage
	}
	l.lastDrawnText = text
	l.lastDrawnHash = hash
	// Clear label area before drawing new text.
	if l.Theme != nil {
		canvas := dc.Image().(*image.RGBA)
		fillRect(canvas, l.Theme, x, y, pw, ph)
	}
	TextFace(l.Theme, text, l.FontSize, l.Color, l.Bold)(dc, x, y, w, h)
}

// ── Face factories ───────────────────────────────────────────────────────────

// TextFace returns a FaceDrawer that renders centered text.
func TextFace(t *Theme, text string, fontSize float64, col color.NRGBA, bold bool) FaceDrawer {
	return func(dc DrawContext, x, y, w, h float64) {
		if !loadFont(t, dc, bold, fontSize) {
			return
		}
		dc.SetColor(t.C(col))
		dc.DrawStringAnchored(text, x+w/2, y+h/2, 0.5, 0.5)
	}
}

// CheckFace returns a FaceDrawer that renders a centered checkmark icon.
func CheckFace(t *Theme, sz, lw float64, col color.NRGBA) FaceDrawer {
	return func(dc DrawContext, x, y, w, h float64) {
		dc.SetColor(t.C(col))
		dc.SetLineWidth(lw)
		dc.SetLineCap(gg.LineCapRound)
		cx, cy := x+w/2, y+h/2
		dc.MoveTo(cx-sz, cy+1)
		dc.LineTo(cx-sz/3+1, cy+sz*2/3+1)
		dc.LineTo(cx+sz, cy-sz*2/3)
		dc.Stroke()
	}
}

// StripedTitleFace returns a FaceDrawer that draws horizontal pinstripes
// interrupted by a centered title — classic Mac OS style.
func StripedTitleFace(t *Theme, title string, fontSize, r float64) FaceDrawer {
	return func(dc DrawContext, x, y, w, h float64) {
		loadFont(t, dc, true, fontSize)
		tw, _ := dc.MeasureString(title)
		pad := t.Px(8)
		cx := x + w/2
		gapL := cx - tw/2 - pad
		gapR := cx + tw/2 + pad

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

		dc.SetColor(t.C(t.Pal.Text))
		dc.DrawStringAnchored(title, cx, y+h/2, 0.5, 0.5)
	}
}

// GradientTitleFace returns a FaceDrawer that fills the face with an animated
// horizontal gradient. The purple peak slowly sweeps back and forth.
func GradientTitleFace(t *Theme, title string, fontSize, r float64) FaceDrawer {
	start := time.Now()
	return func(dc DrawContext, x, y, w, h float64) {
		elapsed := time.Since(start).Seconds()
		peak := (math.Sin(elapsed*math.Pi/6) + 1) / 2

		grad := gg.NewLinearGradient(x, y+h/2, x+w, y+h/2)
		grad.AddColorStop(0, t.C(t.Pal.Surface))
		grad.AddColorStop(peak, t.C(t.Pal.SurfaceTint))
		grad.AddColorStop(1, t.C(t.Pal.Surface))
		dc.SetFillStyle(grad)
		dc.DrawRoundedRectangle(x, y, w, h, r)
		dc.Fill()

		loadFont(t, dc, true, fontSize)
		dc.SetColor(t.C(t.Pal.Text))
		dc.DrawStringAnchored(title, x+w/2, y+h/2, 0.5, 0.5)
	}
}

// asFaceDrawer converts a Drawer to a FaceDrawer for use with DrawNeuBox/NeuBoxWith.
func asFaceDrawer(d Drawer) FaceDrawer {
	if d == nil {
		return nil
	}
	return func(dc DrawContext, x, y, w, h float64) {
		d.Draw(dc, x, y, w, h)
	}
}
