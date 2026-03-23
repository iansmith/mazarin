package std

import (
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// AppWindow is a neumorphic application window. It is a Decorator whose
// decoration includes both neumorphic box shadows and a title bar.
// The single child (content) is positioned below the title bar.
//
// Focus state controls the neumorphic depth: Raised when focused, Flush
// when unfocused. The TitleDraw callback varies the title bar appearance.
type AppWindow struct {
	impl.Decorator

	Pal     mancini.Palette
	Fonts   *mancini.FontConfig
	Title   string
	Focused bool
	Radius  float64

	// TitleDraw renders the title bar. It receives the focus state and
	// the title bar bounds. If nil, plain centered text is drawn.
	TitleDraw func(dc mancini.DrawContext, focused bool, x, y, w, h float64)

	shadowMargin int64
	tbHeight     int64
}

const (
	appTBMargin = int64(8) // padding between NeuBox edge and content
	appTBGap    = int64(6) // gap between title bar and content
)

// NewAppWindow creates an AppWindow with inside-out sizing constraints.
// parent is nil for root windows. tbHeight is the title bar height
// (typically 26). maxWidth is the max content width (0 = default 740).
func NewAppWindow(parent mancini.Interactor, pal mancini.Palette,
	fonts *mancini.FontConfig, title string, tbHeight int64, maxWidth int64,
	titleDraw func(dc mancini.DrawContext, focused bool, x, y, w, h float64),
) *AppWindow {
	sm := mancini.NeuMaxPad(mancini.WindowParams)

	top := sm + appTBMargin + tbHeight + appTBGap
	side := sm + appTBMargin
	bottom := sm + appTBMargin

	// Constraint half-margins: width uses side (symmetric), height uses
	// (top+bottom)/2 so the constraint program's 2*vMargin = top+bottom.
	hMargin := side
	vMargin := (top + bottom) / 2

	if maxWidth <= 0 {
		maxWidth = 740
	}
	maxW := maxWidth + 2*sm

	layout := mancini.NewDecoratorLayout("AppWindow", parent, hMargin, vMargin, maxW)

	w := &AppWindow{
		Pal:          pal,
		Fonts:        fonts,
		Title:        title,
		Radius:       14,
		TitleDraw:    titleDraw,
		shadowMargin: sm,
		tbHeight:     tbHeight,
	}
	w.Decorator.InitDecorator(w, layout, top, side, bottom, side)
	return w
}

// Depth returns the neumorphic depth based on focus state.
func (w *AppWindow) Depth() mancini.NeuDepth {
	if w.Focused {
		return mancini.Raised
	}
	return mancini.Flush
}

// Focus sets the window to focused state.
func (w *AppWindow) Focus() { w.Focused = true }

// Unfocus sets the window to unfocused state.
func (w *AppWindow) Unfocus() { w.Focused = false }

// Decorate implements mancini.Decoratable — draws the NeuBox shadow and
// the title bar inside it.
func (w *AppWindow) Decorate(self mancini.Interactor) {
	dc := self.DC()
	if dc == nil {
		return
	}

	sm := float64(w.shadowMargin)
	x, y := float64(self.X()), float64(self.Y())
	ww, hh := float64(self.W()), float64(self.H())

	// Inner area (excluding shadow margin).
	ix, iy := x+sm, y+sm
	iw, ih := ww-2*sm, hh-2*sm

	// NeuBox shadow.
	mancini.NeuBoxWith(w.Pal, dc, w.Depth(), ix, iy, ix+iw, iy+ih,
		w.Radius, w.Pal.Surface, mancini.WindowParams, nil)

	// Title bar inside the NeuBox.
	tbm := float64(appTBMargin)
	tbX, tbY := ix+tbm, iy+tbm
	tbW := iw - 2*tbm
	tbH := float64(w.tbHeight)

	if w.TitleDraw != nil {
		w.TitleDraw(dc, w.Focused, tbX, tbY, tbW, tbH)
	} else {
		// Default: plain centered text.
		if w.Fonts != nil && w.Fonts.LoadFace != nil {
			face := w.Fonts.LoadFace(false, 18)
			if face != nil {
				dc.SetFontFace(face)
			}
		}
		dc.SetColor(w.Pal.Text)
		dc.DrawStringAnchored(w.Title, tbX+tbW/2, tbY+tbH/2, 0.5, 0.5)
	}
}
