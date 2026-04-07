package std

import (
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// FreeFloatingWindow is a neumorphic floating panel drawn as a sibling
// of [AppWindow] (not a child). It is always [mancini.Flush] depth when
// visible. Structurally it is an [impl.Decorator]: [NeuBoxWith] shadow →
// title + [NeuGroove] separator → content.
//
// Visibility is toggled externally (e.g., by the focus state of an
// associated [AppWindow] or by constraint programs).
//
// See also [AppWindow] for the root application window.
type FreeFloatingWindow struct {
	impl.Decorator

	Pal     mancini.Palette
	NeuPrms mancini.NeuParams
	Style   mancini.SurfaceStyle
	Title   string
	Radius  float64
	Visible bool

	textFace     mancini.LatinTextFace
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
	pal mancini.Palette, neuParams mancini.NeuParams,
	fonts *mancini.FontConfig, title string,
	radius float64,
) *FreeFloatingWindow {
	// Content starts below the groove.
	contentTop := int64(ffwGrooveY + ffwGrooveGap)
	margin := mancini.NeuMaxPad(neuParams)

	top := margin + contentTop
	side := margin
	bottom := margin

	hPadding := side
	vPadding := (top + bottom) / 2

	layout := mancini.NewDecoratorLayout(name, parent, hPadding, vPadding, 800)

	var textFace mancini.LatinTextFace
	if fonts != nil {
		textFace = impl.NewLatinTextFace(fonts, true, 10, mancini.TextAlignmentParams{})
		textFace.SetText(title)
	}

	w := &FreeFloatingWindow{
		Pal:      pal,
		NeuPrms:  neuParams,
		Style:    NewNeumorphicStyle(&neuParams, &neuParams),
		Title:    title,
		Radius:   radius,
		Visible:  false,
		textFace: textFace,
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

	// Box at Flush depth.
	w.Style.DrawBox(w.Pal, dc, mancini.Flush, mancini.HeavyWeight,
		fx, fy, fx+fww, fy+fhh, w.Radius, w.Pal.Surface())

	// Title text centered horizontally.
	if w.textFace != nil {
		w.textFace.SetText(w.Title)
		dc.SetColor(w.Pal.Text())
		w.textFace.DrawFace(dc, fx, fy, fww, ffwTitleY*2)
	}

	// Groove separator.
	w.Style.Groove(w.Pal, dc, fx+ffwGrooveInset, fy+ffwGrooveY, fx+fww-ffwGrooveInset)
}
