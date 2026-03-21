package mancini

import (
	"image/color"
	"math"
	"time"

	"github.com/fogleman/gg"
)

// loadFont sets the font on a gg context. Uses FontConfig.LoadFace if available,
// otherwise falls back to loading from FontRegular/FontBold file paths.
func loadFont(fc *FontConfig, dc DrawContext, bold bool, size float64) bool {
	if fc == nil {
		return false
	}
	if fc.LoadFace != nil {
		face := fc.LoadFace(bold, size)
		if face != nil {
			dc.SetFontFace(face)
			return true
		}
	}
	path := fc.FontRegular
	if bold {
		path = fc.FontBold
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
	Pal      Palette
	Fonts    *FontConfig
	Name     string
	Title    string
	Focused  bool
	TitleBar Drawer // used when Focused (StripedTitleFace, GradientTitleFace, or custom)
	Content  Drawer
	Floaters []*FreeFloatingWindow
	Layout   *LayoutHandles
	MaxWidth int64 // maximum width in logical pixels (0 = default 740)

	shadowMargin   float64 // neumorphic shadow padding (set by InitLayout)
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
// The published bounds (x, y, ww, hh) include the neumorphic shadow padding.
// The NeuBox decoration and all content are offset inward by shadowMargin so
// that shadows stay within the published bounds.
func (w *AppWindow) Draw(dc DrawContext, x, y, ww, hh float64) {
	publishLayout(w.Layout, x, y, ww, hh)

	// The NeuBox inner area excludes the shadow margin on all sides.
	sm := w.shadowMargin
	ix := x + sm
	iy := y + sm
	iw := ww - 2*sm
	ih := hh - 2*sm

	// Skip expensive NeuBoxWith calls when bounds and focus are unchanged.
	needDecoration := true
	hash := w.Layout.boundsHashValue()
	if hash == w.lastBoundsHash && w.lastBoundsHash != 0 && w.Focused == w.lastFocused {
		needDecoration = false
	}
	w.lastBoundsHash = hash
	w.lastFocused = w.Focused

	if needDecoration {
		NeuBoxWith(w.Pal, dc, w.Depth(), ix, iy, ix+iw, iy+ih, 14, w.Pal.Surface, WindowParams, nil)
	}

	// Title bar — positioned inside the NeuBox inner area.
	tbMargin := 8.0
	tbX, tbY := ix+tbMargin, iy+tbMargin
	tbW := iw - 2*tbMargin
	tbH := 26.0 // default
	if s, ok := w.TitleBar.(Sizer); ok {
		tbH = s.PreferredHeight()
	}
	if needDecoration {
		if w.Focused && w.TitleBar != nil {
			w.TitleBar.Draw(dc, tbX, tbY, tbW, tbH)
		} else {
			// Unfocused: plain centered text, no pinstripes.
			TextFace(w.Fonts, w.Title, 18, w.Pal.Text, false)(dc, tbX, tbY, tbW, tbH)
		}
	}

	// Content area — clipped to the NeuBox inner area.
	cX := ix + tbMargin
	cY := tbY + tbH + 6
	cW := iw - 2*tbMargin
	cH := (iy + ih) - cY - tbMargin
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

// fillRect fills a rectangle with the theme's surface color using dc.FillRectangle.
// The dc already has SwapRB set on its underlying gg context.
func fillRect(dc DrawContext, pal Palette, x, y, w, h float64) {
	dc.SetColor(pal.Surface)
	dc.FillRectangle(x, y, w, h)
}

// ── FreeFloatingWindow ───────────────────────────────────────────────────────

// FreeFloatingWindow is a neumorphic floating panel owned by an AppWindow.
// Always Flush when visible.
type FreeFloatingWindow struct {
	Pal     Palette
	Fonts   *FontConfig
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

	// Skip expensive NeuBoxWith calls when bounds are unchanged.
	needDecoration := true
	hash := w.Layout.boundsHashValue()
	if hash == w.lastBoundsHash && w.lastBoundsHash != 0 {
		needDecoration = false
	}
	w.lastBoundsHash = hash

	if needDecoration {
		NeuBoxWith(w.Pal, dc, Flush, x, y, x+ww, y+hh, 14, w.Pal.Surface, WindowParams, nil)
	}

	// Title
	titleY := y + 14

	loadFont(w.Fonts, dc, true, 10)
	dc.SetColor(w.Pal.Text)
	dc.DrawStringAnchored(w.Title, x+ww/2, titleY, 0.5, 0.5)

	// Groove separator
	grooveMargin := 18.0
	grooveY := y + 26
	if needDecoration {
		NeuGroove(w.Pal, dc, x+grooveMargin, grooveY, x+ww-grooveMargin)
	}

	// Content area below groove
	cY := grooveY + 6
	cH := (y + hh) - cY
	if w.Content != nil {
		w.Content.Draw(dc, x, cY, ww, cH)
	}
}

// ── Button ───────────────────────────────────────────────────────────────────

// Button is a neumorphic button that delegates its face rendering to a Drawer.
type Button struct {
	Pal    Palette
	Name   string
	Depth  NeuDepth
	Face   Drawer
	Layout *LayoutHandles
}

func (b *Button) GetLayout() *LayoutHandles { return b.Layout }

// Draw implements the Drawer interface.
func (b *Button) Draw(dc DrawContext, x, y, w, h float64) {
	publishLayout(b.Layout, x, y, w, h)
	DrawNeuBox(b.Pal, dc, b.Depth, x, y, x+w, y+h, 8,
		b.Pal.Surface, asFaceDrawer(b.Face))
}

// ── NeuLabel ─────────────────────────────────────────────────────────────────

// NeuLabel is a text label inside a neumorphic box at any depth.
type NeuLabel struct {
	Pal      Palette
	Fonts    *FontConfig
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
	return l.FontSize + 16 // font + box padding
}

// Draw implements the Drawer interface.
func (l *NeuLabel) Draw(dc DrawContext, x, y, w, h float64) {
	publishLayout(l.Layout, x, y, w, h)
	text := l.Text
	if l.TextFunc != nil {
		text = l.TextFunc()
	}
	DrawNeuBox(l.Pal, dc, l.Depth, x, y, x+w, y+h, 8,
		l.Pal.Surface, TextFace(l.Fonts, text, l.FontSize, l.Color, l.Bold))
}

// ── Label ────────────────────────────────────────────────────────────────────

// Label is plain text with no neumorphic box.
type Label struct {
	Pal      Palette
	Fonts    *FontConfig
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

// resolveText returns the current text for this label, checking
// TextFunc and Text in priority order.
func (l *Label) resolveText() string {
	if l.TextFunc != nil {
		return l.TextFunc()
	}
	return l.Text
}

// PreferredHeight returns the preferred height for a Label.
func (l *Label) PreferredHeight() float64 {
	return l.FontSize + 4 // font + minimal padding
}

// PreferredWidth returns the preferred width for a Label based on text measurement.
func (l *Label) PreferredWidth() float64 {
	return l.Fonts.MeasureText(l.resolveText(), l.Bold, l.FontSize) + 8 // text + padding
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
	text := l.resolveText()
	hash := l.Layout.boundsHashValue()
	if text == l.lastDrawnText && hash == l.lastDrawnHash && l.lastDrawnHash != 0 {
		return // text and position unchanged — no damage
	}
	l.lastDrawnText = text
	l.lastDrawnHash = hash
	// Clear label area before drawing new text.
	fillRect(dc, l.Pal, x, y, pw, ph)
	loadFont(l.Fonts, dc, l.Bold, l.FontSize)
	dc.SetColor(l.Color)
	dc.DrawStringAnchored(text, x+pw/2, y+ph/2, 0.5, 0.5)
}

// ── Face factories ───────────────────────────────────────────────────────────

// TextFace returns a FaceDrawer that renders centered text.
func TextFace(fc *FontConfig, text string, fontSize float64, col color.NRGBA, bold bool) FaceDrawer {
	return func(dc DrawContext, x, y, w, h float64) {
		if !loadFont(fc, dc, bold, fontSize) {
			return
		}
		dc.SetColor(col)
		dc.DrawStringAnchored(text, x+w/2, y+h/2, 0.5, 0.5)
	}
}

// CheckFace returns a FaceDrawer that renders a centered checkmark icon.
func CheckFace(sz, lw float64, col color.NRGBA) FaceDrawer {
	return func(dc DrawContext, x, y, w, h float64) {
		dc.SetColor(col)
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
func StripedTitleFace(fc *FontConfig, pal Palette, title string, fontSize, r float64) FaceDrawer {
	return func(dc DrawContext, x, y, w, h float64) {
		loadFont(fc, dc, true, fontSize)
		tw, _ := dc.MeasureString(title)
		pad := 8.0
		cx := x + w/2
		gapL := cx - tw/2 - pad
		gapR := cx + tw/2 + pad

		darkC := pal.DarkSh
		stripe := color.NRGBA{darkC.R, darkC.G, darkC.B, 120}
		dc.SetColor(stripe)
		dc.SetLineWidth(1)
		spacing := 3.0
		inset := 4.0
		for sy := y + spacing; sy < y+h-spacing/2; sy += spacing {
			if gapL > x+inset {
				dc.DrawLine(x+inset, sy, gapL, sy)
			}
			if gapR < x+w-inset {
				dc.DrawLine(gapR, sy, x+w-inset, sy)
			}
		}
		dc.Stroke()

		dc.SetColor(pal.Text)
		dc.DrawStringAnchored(title, cx, y+h/2, 0.5, 0.5)
	}
}

// GradientTitleFace returns a FaceDrawer that fills the face with an animated
// horizontal gradient. The purple peak slowly sweeps back and forth.
func GradientTitleFace(fc *FontConfig, pal Palette, title string, fontSize, r float64) FaceDrawer {
	start := time.Now()
	return func(dc DrawContext, x, y, w, h float64) {
		elapsed := time.Since(start).Seconds()
		peak := (math.Sin(elapsed*math.Pi/6) + 1) / 2

		grad := gg.NewLinearGradient(x, y+h/2, x+w, y+h/2)
		grad.AddColorStop(0, pal.Surface)
		grad.AddColorStop(peak, pal.SurfaceTint)
		grad.AddColorStop(1, pal.Surface)
		dc.SetFillStyle(grad)
		dc.DrawRoundedRectangle(x, y, w, h, r)
		dc.Fill()

		loadFont(fc, dc, true, fontSize)
		dc.SetColor(pal.Text)
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
