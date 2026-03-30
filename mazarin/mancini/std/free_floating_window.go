package std

import (
	"golang.org/x/image/font"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// FreeFloatingWindow is a neumorphic floating panel drawn as a sibling of
// AppWindow (not a child). It is always Flush depth when visible.
// Structurally it is a Decorator: NeuBox shadow → title + groove → content.
//
// Visibility is toggled externally (e.g., by the focus state of an associated
// AppWindow or by constraint programs).
type FreeFloatingWindow struct {
	impl.Decorator

	Pal     mancini.Palette
	Title   string
	Radius  float64
	Visible bool

	titleFace    font.Face
	grooveMargin float64
	grooveY      float64 // offset from top of inner area
	contentY     float64 // offset from top of inner area
}

const (
	ffwTitleY      = 14.0 // title text center Y offset from top
	ffwGrooveY     = 26.0 // groove Y offset from top
	ffwGrooveGap   = 6.0  // gap between groove and content
	ffwGrooveInset = 18.0 // groove horizontal inset
)

// NewFreeFloatingWindow creates a FreeFloatingWindow with inside-out sizing.
// parent is nil for top-level floaters. The window is initially not visible.
func NewFreeFloatingWindow(name string, parent mancini.Interactor,
	pal mancini.Palette, fonts *mancini.FontConfig, title string,
	radius float64,
) *FreeFloatingWindow {
	// Content starts below the groove.
	contentTop := int64(ffwGrooveY + ffwGrooveGap)
	margin := mancini.NeuMaxPad(mancini.NeuWindowParams)

	top := margin + contentTop
	side := margin
	bottom := margin

	hMargin := side
	vMargin := (top + bottom) / 2

	layout := mancini.NewDecoratorLayout(name, parent, hMargin, vMargin, 800)

	var titleFace font.Face
	if fonts != nil && fonts.LoadFace != nil {
		titleFace = fonts.LoadFace(true, 10) // bold, 10pt for floater titles
	}

	w := &FreeFloatingWindow{
		Pal:       pal,
		Title:     title,
		Radius:    radius,
		Visible:   false,
		titleFace: titleFace,
	}
	w.Decorator.Initialize(w, layout, top, side, bottom, side)
	return w
}

// Draw implements mancini.NewDrawer.
func (w *FreeFloatingWindow) Draw(self mancini.Interactor, x, y, ww, hh int64) {
	if !w.Visible {
		return
	}

	w.Decorate(self, x, y, ww, hh)

	contentX := x + w.Left
	contentY := y + w.Top
	contentW := ww - w.Left - w.Right
	contentH := hh - w.Top - w.Bottom

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

// Decorate implements mancini.Decoratable — draws the NeuBox shadow,
// title text, and groove separator.
func (w *FreeFloatingWindow) Decorate(self mancini.Interactor, x, y, ww, hh int64) {
	dc := self.DC()
	if dc == nil {
		return
	}

	fx, fy := float64(x), float64(y)
	fww, fhh := float64(ww), float64(hh)

	// NeuBox at Flush depth.
	NeuBoxWith(w.Pal, dc, mancini.Flush, fx, fy, fx+fww, fy+fhh,
		w.Radius, w.Pal.Surface, mancini.NeuWindowParams, nil)

	// Title text centered horizontally.
	if w.titleFace != nil {
		dc.SetFontFace(w.titleFace)
	}
	dc.SetColor(w.Pal.Text)
	dc.DrawStringAnchored(w.Title, fx+fww/2, fy+ffwTitleY, 0.5, 0.5)

	// Groove separator.
	NeuGroove(w.Pal, dc, fx+ffwGrooveInset, fy+ffwGrooveY, fx+fww-ffwGrooveInset)
}
