package std

import (
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// AppWindow is the root application window, implemented as an
// [impl.Decorator] whose decoration includes both neumorphic box
// shadows (via [NeuBoxWith]) and a title bar. The single child (content)
// is positioned below the title bar, clipped to the content area using
// [mancini.WithClip].
//
// There is one AppWindow per shepherd — its constraint name is always
// "AppWindow" so that rachel can locate it at a well-known attribute path.
//
// # Focus and Depth
//
// Focus state controls the neumorphic depth: [mancini.Raised] when
// focused, [mancini.Flush] when unfocused.
//
// # Title Bar
//
// The TitleDraw callback customizes the focused title bar appearance.
// Two standard implementations are provided:
//
//   - [GradientTitle] — animated horizontal gradient with oscillating peak
//   - [StripedTitle] — classic Mac OS horizontal pinstripes
//
// When TitleDraw is nil or the window is unfocused, plain centered text
// is drawn as a fallback.
//
// See also [FreeFloatingWindow] for non-root floating panels.
type AppWindow struct {
	impl.Decorator

	Pal       mancini.Palette
	NeuPrms   mancini.NeuParams
	Title     string
	Focused   bool
	Radius    float64

	// TitleDraw renders the title bar. It receives the focus state and
	// the title bar bounds. If nil, plain centered text is drawn.
	TitleDraw func(dc mancini.DrawContext, focused bool, x, y, w, h float64)

	shadowMargin int64
	tbHeight     int64
	textFace     mancini.LatinTextFace // pre-resolved for unfocused title rendering
}

const (
	appTBMargin = int64(8) // padding between NeuBox edge and content
	appTBGap    = int64(6) // gap between title bar and content
)

// NewAppWindow creates an AppWindow with inside-out sizing constraints.
// parent is nil for root windows. tbHeight is the title bar height
// (typically 26). maxWidth is the max content width (0 = default 740).
func NewAppWindow(parent mancini.Interactor, pal mancini.Palette,
	neuParams mancini.NeuParams,
	fonts *mancini.FontConfig, title string, tbHeight int64, maxWidth int64,
	titleDraw func(dc mancini.DrawContext, focused bool, x, y, w, h float64),
) *AppWindow {
	sm := mancini.NeuMaxPad(neuParams)

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

	// Pre-resolve unfocused title face (regular, 18pt, centered).
	var textFace mancini.LatinTextFace
	if fonts != nil {
		textFace = impl.NewLatinTextFace(fonts, false, 18, mancini.TextAlignmentParams{})
		textFace.SetText(title)
	}

	w := &AppWindow{
		Pal:          pal,
		NeuPrms:      neuParams,
		Title:        title,
		Radius:       14,
		TitleDraw:    titleDraw,
		shadowMargin: sm,
		tbHeight:     tbHeight,
		textFace:     textFace,
	}
	w.Decorator.Initialize(w, layout, top, side, bottom, side)
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
// content area inside the decoration insets, clipping to prevent overflow
// from escaping the window decoration.
func (w *AppWindow) Draw(self mancini.Interactor, x, y, ww, hh int64) {
	// 1. Decorate: NeuBox shadow + title bar (skipped if bounds unchanged).
	w.Decorator.DecorateIfNeeded(self, x, y, ww, hh)

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

	// 4. Clip child to content area (right edge, then bottom edge).
	ccR := mancini.WithClip(dc, float64(contentX), float64(contentY),
		float64(contentW), float64(contentH), 60, mancini.ClipRight)
	ccB := mancini.WithClip(dc, float64(contentX), float64(contentY),
		float64(contentW), float64(contentH), 60, mancini.ClipBottom)
	if d, ok := child.(mancini.NewDrawer); ok {
		d.Draw(child, contentX, contentY, contentW, contentH)
	}
	ccB.Flush()
	ccR.Flush()
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
		w.Radius, w.Pal.Surface(), &w.NeuPrms)

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
		if w.textFace != nil {
			w.textFace.SetText(w.Title)
			dc.SetColor(w.Pal.Text())
			w.textFace.DrawFace(dc, tbX, tbY, tbW, tbH)
		}
	}
}
