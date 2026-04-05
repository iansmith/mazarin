package std

import (
	"image/color"
	"sync"
	"sync/atomic"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
	"mazzy/mazarin/uring"
	"mazzy/shared/wm"
)

// AppWindow is the root application window — a thin, zero-inset decorator
// whose only job is to anchor the constraint tree at the well-known name
// "AppWindow", propagate DrawContext to its child, clip child rendering to
// window bounds, and track focus state for rachel (the window manager).
//
// AppWindow has no decoration of its own — window chrome (title bar,
// borders, shadows) is rachel's responsibility. The single child is
// "the app": whatever interactor tree the shepherd assembles.
//
// Width and height are inside-out from the child, clamped to [10%, 90%]
// of screen dimensions via constraint programs.
//
// There is one AppWindow per shepherd — its constraint name is always
// "AppWindow" so that rachel can locate it at a well-known attribute path.
//
// See also [FreeFloatingWindow] for non-root floating panels.
type AppWindow struct {
	impl.Decorator

	Pal     mancini.Palette
	Focused bool
	Title   string // informational — passed to rachel, not rendered here

	// Input is the Henry-Hudson dispatch pipeline for this app.
	// Nil until the app calls InitInput. Shepherd apps call
	// app.Input.DispatchWM(msg) from their message loop.
	Input *mancini.AppDispatcher

	// Animation bookkeeping — maps localID ↔ Animatable.
	nextLocalID       atomic.Uint64
	localToAnimatable sync.Map // map[uint64]mancini.Animatable
	remoteToLocal     sync.Map // map[uint64]uint64
	RachelSID         int      // set by the shepherd before calling RegisterAnimation

	// --- Retained but unused: will move to rachel ---
	NeuPrms      mancini.NeuParams
	Radius       float64
	TitleDraw    func(dc mancini.DrawContext, focused bool, x, y, w, h float64)
	shadowMargin int64
	tbHeight     int64
	textFace     mancini.LatinTextFace
}

// NewAppWindow creates an AppWindow with inside-out sizing constraints
// clamped to [10%, 90%] of screen dimensions. screenWURI and screenHURI
// are the kernel attribute URIs for screen width and height.
func NewAppWindow(pal mancini.Palette, title string,
	screenWURI, screenHURI string) *AppWindow {

	layout := mancini.NewAppWindowLayout(screenWURI, screenHURI)

	w := &AppWindow{
		Pal:   pal,
		Title: title,
	}
	// Publish title so rachel can read it.
	attr.ValueStr(attr.ShepherdURI("string", "AppWindow/Title"), title)

	// Publish palette colors as packed NRGBA (R<<24 | G<<16 | B<<8 | A).
	publishColor := func(name string, c color.NRGBA) {
		attr.ValueI64(attr.ShepherdURI("int64", "Palette/"+name),
			int64(c.R)<<24|int64(c.G)<<16|int64(c.B)<<8|int64(c.A))
	}
	publishColor("Surface", pal.Surface())
	publishColor("SurfaceTint", pal.SurfaceTint())
	publishColor("DarkShadow", pal.DarkShadow())
	publishColor("LightShadow", pal.LightShadow())
	publishColor("Text", pal.Text())
	publishColor("Icon", pal.Icon())
	publishColor("Highlight", pal.Highlight())
	publishColor("HighlightText", pal.HighlightText())

	// Zero insets on all sides — no decoration.
	w.Decorator.Initialize(w, layout, 0, 0, 0, 0)
	return w
}

// InitInput creates and returns the standard Henry-Hudson dispatch
// pipeline for this AppWindow. Call once during setup.
func (w *AppWindow) InitInput() (*mancini.AppDispatcher, *mancini.ClickAgent, *mancini.KeyAgent) {
	d, click, key := mancini.StandardPipeline(w)
	w.Input = d
	return d, click, key
}

// Focus sets the window to focused state.
func (w *AppWindow) Focus() { w.Focused = true }

// Unfocus sets the window to unfocused state.
func (w *AppWindow) Unfocus() { w.Focused = false }

// RegisterAnimation registers an Animatable for an animation and sends
// AnimationRegister to rachel. The local ID is sent as the Nonce field
// so rachel echoes it back. Returns the local animation ID.
func (w *AppWindow) RegisterAnimation(a mancini.Animatable, startNanos, endNanos int64) uint64 {
	localID := w.nextLocalID.Add(1)
	w.localToAnimatable.Store(localID, a)
	msg := wm.EncodeAnimationRegister(&wm.AnimationRegister{
		StartNanos: startNanos,
		EndNanos:   endNanos,
		Nonce:      localID,
	})
	_ = uring.Send(w.RachelSID, &msg)
	return localID
}

// DispatchAnimation routes an animation WM message to the registered
// Animatable interactor. Returns true if the message was handled.
func (w *AppWindow) DispatchAnimation(wmMsg any) bool {
	switch m := wmMsg.(type) {
	case wm.AnimationRegistered:
		w.remoteToLocal.Store(m.AnimationID, m.Nonce)
		return true
	case wm.AnimationStart:
		localID, ok := w.remoteToLocal.Load(m.AnimationID)
		if !ok {
			return false
		}
		a, ok := w.localToAnimatable.Load(localID)
		if !ok {
			return false
		}
		a.(mancini.Animatable).AnimationStart(localID.(uint64), m.StartNanos)
		return true
	case wm.AnimationUpdate:
		localID, ok := w.remoteToLocal.Load(m.AnimationID)
		if !ok {
			return false
		}
		a, ok := w.localToAnimatable.Load(localID)
		if !ok {
			return false
		}
		a.(mancini.Animatable).AnimationUpdate(localID.(uint64),
			m.StartNanos, m.EndNanos, m.CoveredStart, m.CoveredEnd)
		return true
	case wm.AnimationFinish:
		localID, ok := w.remoteToLocal.Load(m.AnimationID)
		if !ok {
			return false
		}
		a, ok := w.localToAnimatable.Load(localID)
		if ok {
			a.(mancini.Animatable).AnimationFinish(localID.(uint64), m.EndNanos)
		}
		w.localToAnimatable.Delete(localID)
		w.remoteToLocal.Delete(m.AnimationID)
		return true
	}
	return false
}

// Draw implements mancini.NewDrawer. Propagates DC to the child,
// clips child rendering to window bounds, and draws the child at
// the full window area (zero insets).
//
// If the AppWindow's DamageRect is smaller than its full bounds,
// drawing is clipped to the damaged region so unchanged pixels
// are not overwritten.
func (w *AppWindow) Draw(self mancini.Interactor, x, y, ww, hh int64) {
	dc := self.DC()
	if dc == nil {
		return
	}

	// Clip to damage rectangle if smaller than full bounds.
	lh := w.GetLayout()
	clipped := false
	if lh != nil {
		clipped = lh.PushDamageClip(dc)
	}

	// Fill window background with palette surface tint color.
	dc.SetColor(w.Pal.SurfaceTint())
	dc.FillRectangle(float64(x), float64(y), float64(ww), float64(hh))

	children := w.GetChildren()
	if len(children) == 0 {
		if clipped {
			mancini.PopDamageClip(dc)
		}
		return
	}
	child := children[0]

	// Propagate position and DC to child.
	if l, ok := child.(mancini.Layouter); ok {
		clh := l.GetLayout()
		if clh != nil {
			clh.X.Set(x)
			clh.Y.Set(y)
		}
	}
	if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
		cs.SetDC(dc)
	}

	// Clip child to window bounds. Pad covers the rachel border zone
	// so save/restore reaches the backing store edge.
	const borderPad = 60
	tc0 := nanotime()
	ccR := mancini.WithClip(dc, float64(x), float64(y),
		float64(ww), float64(hh), borderPad, mancini.ClipRight)
	ccB := mancini.WithClip(dc, float64(x), float64(y),
		float64(ww), float64(hh), borderPad, mancini.ClipBottom)
	drawPerf.AppClipNs.Add(nanotime() - tc0)

	tc1 := nanotime()
	if d, ok := child.(mancini.NewDrawer); ok {
		d.Draw(child, x, y, ww, hh)
	}
	drawPerf.AppChildNs.Add(nanotime() - tc1)

	tc2 := nanotime()
	ccB.Flush()
	ccR.Flush()
	drawPerf.AppClipNs.Add(nanotime() - tc2)

	if clipped {
		mancini.PopDamageClip(dc)
	}
}

// Decorate implements mancini.Decoratable — no-op. Window chrome is
// rachel's responsibility.
func (w *AppWindow) Decorate(self mancini.Interactor, x, y, ww, hh int64) {
	// Intentionally empty — decoration will move to rachel.
}
