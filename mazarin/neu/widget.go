package neu

import (
	"image"
	"image/color"
	"math"
	"time"

	"github.com/fogleman/gg"
)

// loadFont sets the font on a gg context. Uses Theme.FontLoader if available,
// otherwise falls back to loading from FontRegular/FontBold file paths.
func loadFont(t *Theme, dc *gg.Context, bold bool, size float64) bool {
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
	Title    string
	Focused  bool
	TitleBar Drawer // used when Focused (StripedTitleFace, GradientTitleFace, or custom)
	Content  Drawer
	Floaters []*FreeFloatingWindow
}

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
func (w *AppWindow) Draw(canvas *image.RGBA, x, y, ww, hh float64) {
	t := w.Theme
	NeuBoxWith(t, canvas, w.Depth(), x, y, x+ww, y+hh, t.Px(14), t.Pal.Surface, WindowParams, nil)

	// Title bar
	tbMargin := t.Px(8)
	tbX, tbY := x+tbMargin, y+tbMargin
	tbW, tbH := ww-2*tbMargin, t.Px(26)
	tbR := t.Px(8)
	if w.Focused {
		NeuBoxWith(t, canvas, Raised, tbX, tbY, tbX+tbW, tbY+tbH, tbR,
			t.Pal.Surface, ButtonParams, asFaceDrawer(w.TitleBar))
	} else {
		NeuBoxWith(t, canvas, Flush, tbX, tbY, tbX+tbW, tbY+tbH, tbR,
			t.Pal.Surface, ButtonParams,
			TextFace(t, w.Title, t.Px(10), t.Pal.Text, false))
	}

	// Content area
	cY := tbY + tbH + t.Px(6)
	cH := (y + hh) - cY - tbMargin
	if w.Content != nil {
		w.Content.Draw(canvas, x+tbMargin, cY, ww-2*tbMargin, cH)
	}

	// Floaters
	for _, f := range w.Floaters {
		if f.Visible {
			f.Draw(canvas, f.X, f.Y, f.W, f.H)
		}
	}
}

// ── FreeFloatingWindow ───────────────────────────────────────────────────────

// FreeFloatingWindow is a neumorphic floating panel owned by an AppWindow.
// Always Flush when visible.
type FreeFloatingWindow struct {
	Theme   *Theme
	Title   string
	Visible bool
	Content Drawer
	X, Y    float64 // position (set by caller or constraints)
	W, H    float64 // size (set by caller or constraints)
}

func (w *FreeFloatingWindow) Depth() NeuDepth {
	return Flush
}

// Draw implements the Drawer interface.
func (w *FreeFloatingWindow) Draw(canvas *image.RGBA, x, y, ww, hh float64) {
	t := w.Theme
	NeuBoxWith(t, canvas, Flush, x, y, x+ww, y+hh, t.Px(14), t.Pal.Surface, WindowParams, nil)

	// Title
	titleY := y + t.Px(14)
	dc := gg.NewContextForRGBA(canvas)
	loadFont(t, dc, true, t.Px(10))
	dc.SetColor(t.C(t.Pal.Text))
	dc.DrawStringAnchored(w.Title, x+ww/2, titleY, 0.5, 0.5)

	// Groove separator
	grooveMargin := t.Px(18)
	grooveY := y + t.Px(26)
	NeuGroove(t, canvas, x+grooveMargin, grooveY, x+ww-grooveMargin)

	// Content area below groove
	cY := grooveY + t.Px(6)
	cH := (y + hh) - cY
	if w.Content != nil {
		w.Content.Draw(canvas, x, cY, ww, cH)
	}
}

// ── Button ───────────────────────────────────────────────────────────────────

// Button is a neumorphic button that delegates its face rendering to a Drawer.
type Button struct {
	Theme *Theme
	Depth NeuDepth
	Face  Drawer
}

// Draw implements the Drawer interface.
func (b *Button) Draw(canvas *image.RGBA, x, y, w, h float64) {
	NeuBox(b.Theme, canvas, b.Depth, x, y, x+w, y+h, b.Theme.Px(8),
		b.Theme.Pal.Surface, asFaceDrawer(b.Face))
}

// ── NeuLabel ─────────────────────────────────────────────────────────────────

// NeuLabel is a text label inside a neumorphic box at any depth.
type NeuLabel struct {
	Theme    *Theme
	Depth    NeuDepth
	Text     string         // static text (used when TextFunc is nil)
	TextFunc func() string  // dynamic text source (takes precedence over Text)
	FontSize float64
	Color    color.NRGBA
	Bold     bool
}

// Draw implements the Drawer interface.
func (l *NeuLabel) Draw(canvas *image.RGBA, x, y, w, h float64) {
	text := l.Text
	if l.TextFunc != nil {
		text = l.TextFunc()
	}
	NeuBox(l.Theme, canvas, l.Depth, x, y, x+w, y+h, l.Theme.Px(8),
		l.Theme.Pal.Surface, TextFace(l.Theme, text, l.FontSize, l.Color, l.Bold))
}

// ── Label ────────────────────────────────────────────────────────────────────

// Label is plain text with no neumorphic box.
type Label struct {
	Theme    *Theme
	Text     string         // static text (used when TextFunc is nil)
	TextFunc func() string  // dynamic text source (takes precedence over Text)
	FontSize float64
	Color    color.NRGBA
	Bold     bool
}

// Draw implements the Drawer interface.
func (l *Label) Draw(canvas *image.RGBA, x, y, w, h float64) {
	text := l.Text
	if l.TextFunc != nil {
		text = l.TextFunc()
	}
	TextFace(l.Theme, text, l.FontSize, l.Color, l.Bold)(canvas, x, y, w, h)
}

// ── Face factories ───────────────────────────────────────────────────────────

// TextFace returns a FaceDrawer that renders centered text.
func TextFace(t *Theme, text string, fontSize float64, col color.NRGBA, bold bool) FaceDrawer {
	return func(canvas *image.RGBA, x, y, w, h float64) {
		dc := gg.NewContextForRGBA(canvas)
		if !loadFont(t, dc, bold, fontSize) {
			return
		}
		dc.SetColor(t.C(col))
		dc.DrawStringAnchored(text, x+w/2, y+h/2, 0.5, 0.5)
	}
}

// CheckFace returns a FaceDrawer that renders a centered checkmark icon.
func CheckFace(t *Theme, sz, lw float64, col color.NRGBA) FaceDrawer {
	return func(canvas *image.RGBA, x, y, w, h float64) {
		dc := gg.NewContextForRGBA(canvas)
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
	return func(canvas *image.RGBA, x, y, w, h float64) {
		dc := gg.NewContextForRGBA(canvas)

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
	return func(canvas *image.RGBA, x, y, w, h float64) {
		elapsed := time.Since(start).Seconds()
		peak := (math.Sin(elapsed*math.Pi/6) + 1) / 2

		dc := gg.NewContextForRGBA(canvas)
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

// asFaceDrawer converts a Drawer to a FaceDrawer for use with NeuBox.
func asFaceDrawer(d Drawer) FaceDrawer {
	if d == nil {
		return nil
	}
	return func(canvas *image.RGBA, x, y, w, h float64) {
		d.Draw(canvas, x, y, w, h)
	}
}
