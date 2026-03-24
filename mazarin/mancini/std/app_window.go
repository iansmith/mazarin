package std

import (
	"golang.org/x/image/font"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// AppWindow is a neumorphic application window. It is a Decorator whose
// decoration includes both neumorphic box shadows and a title bar.
// The single child (content) is positioned below the title bar.
//
// There is one AppWindow per shepherd — its constraint name is always
// "AppWindow" so that rachel can locate it at a well-known attribute path.
//
// Focus state controls the neumorphic depth: Raised when focused, Flush
// when unfocused. The TitleDraw callback varies the title bar appearance.
type AppWindow struct {
	impl.Decorator

	Pal     mancini.Palette
	Title   string
	Focused bool
	Radius  float64

	// TitleDraw renders the title bar. It receives the focus state and
	// the title bar bounds. If nil, plain centered text is drawn.
	TitleDraw func(dc mancini.DrawContext, focused bool, x, y, w, h float64)

	shadowMargin  int64
	tbHeight      int64
	unfocusedFace font.Face // pre-resolved for unfocused title rendering
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

	// Pre-resolve unfocused title font (regular, 18pt).
	var unfocusedFace font.Face
	if fonts != nil && fonts.LoadFace != nil {
		unfocusedFace = fonts.LoadFace(false, 18)
	}

	w := &AppWindow{
		Pal:           pal,
		Title:         title,
		Radius:        14,
		TitleDraw:     titleDraw,
		shadowMargin:  sm,
		tbHeight:      tbHeight,
		unfocusedFace: unfocusedFace,
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

// Draw implements mancini.NewDrawer. Decorates and draws the single child
// content area inside the decoration insets.
func (w *AppWindow) Draw(self mancini.Interactor, x, y, ww, hh int64) {
	// 1. Decorate: NeuBox shadow + title bar.
	w.Decorate(self, x, y, ww, hh)

	// 2. Content area inside the decoration insets.
	contentX := x + w.Left
	contentY := y + w.Top
	contentW := ww - w.Left - w.Right
	contentH := hh - w.Top - w.Bottom

	// 3. Child via GetChildren.
	children := w.GetChildren()
	if len(children) == 0 {
		return
	}
	child := children[0]
	if l, ok := child.(mancini.Layouter); ok {
		lh := l.GetLayout()
		if lh != nil {
			lh.X.Set(contentX)
			lh.Y.Set(contentY)
		}
	}
	dc := self.DC()
	if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
		cs.SetDC(dc)
	}
	if d, ok := child.(mancini.NewDrawer); ok {
		d.Draw(child, contentX, contentY, contentW, contentH)
	}
}

// Decorate implements mancini.Decoratable — draws the NeuBox shadow and
// the title bar inside it.
func (w *AppWindow) Decorate(self mancini.Interactor, x, y, ww, hh int64) {
	dc := self.DC()
	if dc == nil {
		return
	}

	sm := w.shadowMargin

	// Inner area (excluding shadow margin).
	ix, iy := float64(x+sm), float64(y+sm)
	iw, ih := float64(ww-2*sm), float64(hh-2*sm)

	// NeuBox shadow.
	NeuBoxWith(w.Pal, dc, w.Depth(), ix, iy, ix+iw, iy+ih,
		w.Radius, w.Pal.Surface, mancini.WindowParams, nil)

	// Title bar inside the NeuBox.
	tbm := float64(appTBMargin)
	tbX, tbY := ix+tbm, iy+tbm
	tbW := iw - 2*tbm
	tbH := float64(w.tbHeight)

	if w.TitleDraw != nil {
		w.TitleDraw(dc, w.Focused, tbX, tbY, tbW, tbH)
	}

	// Unfocused fallback: plain centered text when TitleDraw is nil
	// or when unfocused (TitleDraw may choose to skip unfocused rendering).
	if w.TitleDraw == nil || !w.Focused {
		if w.unfocusedFace != nil {
			dc.SetFontFace(w.unfocusedFace)
		}
		dc.SetColor(w.Pal.Text)
		dc.DrawStringAnchored(w.Title, tbX+tbW/2, tbY+tbH/2, 0.5, 0.5)
	}
}
