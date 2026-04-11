package std

import (
	"image"
	"image/color"
	"os"
	"sync"
	"sync/atomic"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/stack"
	"mazzy/shared/wm"

	"fmt"
)

// BackingStoreEntry tracks a shared backing store mapping so it can be
// released via Munmap when superseded by a newer backing store.
type BackingStoreEntry struct {
	Addr uintptr
	Len  int
}

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

	// Animation bookkeeping — maps localID ↔ Animatable ↔ remoteID.
	nextLocalID       atomic.Uint64
	localToAnimatable sync.Map // map[uint64]mancini.Animatable
	remoteToLocal     sync.Map // map[uint64]uint64 (remoteID → localID)
	localToRemote     sync.Map // map[uint64]uint64 (localID → remoteID)
	RachelSID         int      // set by the shepherd before calling RegisterAnimation

	// bsStack tracks backing store pointers received from rachel.
	// When a new backing store arrives, old entries are popped and released.
	bsStack stack.Stack[BackingStoreEntry]

	// Track whether we've done an initial full background fill so
	// subsequent draws don't wipe undamaged children.
	hasDrawn       bool
	lastBoundsHash int64

	// --- Retained but unused: will move to rachel ---
	NeuPrms      mancini.NeuParams
	Radius       float64
	TitleDraw    func(dc mancini.DrawContext, focused bool, x, y, w, h float64)
	shadowMargin int64
	tbHeight     int64
	textFace     mancini.LatinTextFace
}

// NewAppWindow creates an AppWindow with outside-in sizing. Width and
// Height are value attributes set by rachel via BackingStoreReady /
// WindowResized messages.
func NewAppWindow(pal mancini.Palette, title string) *AppWindow {

	layout := mancini.NewAppWindowLayout()

	w := &AppWindow{
		Pal:   pal,
		Title: title,
	}
	// Publish title so rachel can read it.
	attr.ValueStr(attr.ShepherdURI("string", "AppWindow/Title"), title)

	// Publish minimum window size (0 = use rachel's default of 8x8).
	attr.ValueI64(attr.ShepherdURI("int64", "AppWindow/MinWidth"), 0)
	attr.ValueI64(attr.ShepherdURI("int64", "AppWindow/MinHeight"), 0)

	// Publish palette colors as packed NRGBA (R<<24 | G<<16 | B<<8 | A).
	publishColor := func(name string, c color.NRGBA) {
		attr.ValueI64(attr.ShepherdURI("int64", "Palette/"+name),
			int64(c.R)<<24|int64(c.G)<<16|int64(c.B)<<8|int64(c.A))
	}
	// Original 8 core colors.
	publishColor("Surface", pal.Surface())
	publishColor("SurfaceTint", pal.SurfaceTint())
	publishColor("DarkShadow", pal.DarkShadow())
	publishColor("LightShadow", pal.LightShadow())
	publishColor("Text", pal.Text())
	publishColor("Icon", pal.Icon())
	publishColor("Highlight", pal.Highlight())
	publishColor("HighlightText", pal.HighlightText())

	// Extended Qt-inspired color roles.
	publishColor("Base", pal.Base())
	publishColor("BaseText", pal.BaseText())
	publishColor("Midlight", pal.Midlight())
	publishColor("Mid", pal.Mid())
	publishColor("Shadow", pal.Shadow())
	publishColor("BrightText", pal.BrightText())
	publishColor("AlternateBase", pal.AlternateBase())
	publishColor("ToolTipBase", pal.ToolTipBase())
	publishColor("ToolTipText", pal.ToolTipText())
	publishColor("Link", pal.Link())
	publishColor("PlaceholderText", pal.PlaceholderText())
	publishColor("Accent", pal.Accent())
	publishColor("WindowText", pal.WindowText())
	publishColor("DesktopBG", pal.DesktopBG())

	// Cursor colors.
	publishColor("CursorColor", pal.CursorColor())
	publishColor("CursorTextColor", pal.CursorTextColor())

	// ANSI terminal colors (16 entries).
	for i := 0; i < 16; i++ {
		name := "Ansi/" + ansiIndexName(i)
		publishColor(name, pal.AnsiColor(i))
	}

	// DisabledAlpha as a fixed-point int64 (value * 1000).
	attr.ValueI64(attr.ShepherdURI("int64", "Palette/DisabledAlpha"),
		int64(pal.DisabledAlpha()*1000))

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

// SetSize updates the AppWindow's layout Width and Height to the given
// app content dimensions. Called when rachel sends BackingStoreReady or
// WindowResized with new dimensions.
func (w *AppWindow) SetSize(appW, appH int64) {
	lh := w.GetLayout()
	if lh != nil {
		lh.Width.Set(appW)
		lh.Height.Set(appH)
	}
}

// HandleBackingStoreReady processes a new backing store address from rachel.
// Pops and releases (via Munmap) any stale entries from the backing store
// stack. If the new address matches the current top, no action is taken.
// Otherwise all non-matching entries are released and the new entry is pushed.
func (w *AppWindow) HandleBackingStoreReady(addr uintptr, length int) {
	for !w.bsStack.IsEmpty() {
		top, _ := w.bsStack.Peek()
		if top.Addr == addr {
			return // already tracked
		}
		popped, _ := w.bsStack.Pop()
		if err := mem.Munmap(popped.Addr, popped.Len); err != nil {
			sys.UartWriteString(fmt.Sprintf("[AppWindow] Munmap old BS %x len=%d: %v\n",
				popped.Addr, popped.Len, err))
		}
	}
	w.bsStack.Push(BackingStoreEntry{Addr: addr, Len: length})
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

// UnregisterAnimation cancels an active animation by local ID. Sends
// AnimationUnregister to rachel, which responds with AnimationUnregistered.
// On receiving the response, DispatchAnimation calls AnimationFinish on
// the interactor and cleans up all bookkeeping.
func (w *AppWindow) UnregisterAnimation(localID uint64) {
	remoteID, ok := w.localToRemote.Load(localID)
	if !ok {
		return
	}
	msg := wm.EncodeAnimationUnregister(&wm.AnimationUnregister{
		AnimationID: remoteID.(uint64),
		Nonce:       localID,
	})
	_ = uring.Send(w.RachelSID, &msg)
}

// DispatchAnimation routes an animation WM message to the registered
// Animatable interactor. Returns true if the message was handled.
func (w *AppWindow) DispatchAnimation(wmMsg any) bool {
	switch m := wmMsg.(type) {
	case wm.AnimationRegistered:
		w.remoteToLocal.Store(m.AnimationID, m.Nonce)
		w.localToRemote.Store(m.Nonce, m.AnimationID)
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
			m.StartNanos, m.EndNanos, m.CoveredStart, m.CoveredEnd, m.NanosSinceStart)
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
		w.localToRemote.Delete(localID)
		return true
	case wm.AnimationUnregistered:
		localID := m.Nonce
		a, ok := w.localToAnimatable.Load(localID)
		if ok {
			a.(mancini.Animatable).AnimationFinish(localID, 0)
		}
		w.localToAnimatable.Delete(localID)
		w.remoteToLocal.Delete(m.AnimationID)
		w.localToRemote.Delete(localID)
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
func (w *AppWindow) Draw(self mancini.Interactor, x, y, ww, hh int64, damage image.Rectangle) {
	dc := self.DC()
	if dc == nil {
		return
	}

	lh := w.GetLayout()

	// Clip to damage rectangle if smaller than full bounds.
	clipped := false
	if lh != nil {
		clipped = lh.PushDamageClip(dc)
	}

	// Only fill background on first draw or when bounds changed (resize/move).
	// Otherwise, filling wipes children that have no damage and won't repaint.
	boundsHash := int64(0)
	if lh != nil && lh.BoundsHash != nil {
		boundsHash = lh.BoundsHash.Get()
	}
	if !w.hasDrawn || boundsHash != w.lastBoundsHash {
		dc.SetColor(w.Pal.SurfaceTint())
		dc.FillRectangle(float64(x), float64(y), float64(ww), float64(hh))
		w.lastBoundsHash = boundsHash
	}
	w.hasDrawn = true

	children := w.GetChildren()
	if len(children) == 0 {
		if clipped {
			mancini.PopDamageClip(dc)
		}
		return
	}
	child := children[0]

	// Read child dimensions from its layout constraints (bridge or inside-out).
	childW := ww
	childH := hh
	childL, hasLayout := child.(mancini.Layouter)
	if hasLayout {
		clh := childL.GetLayout()
		if clh != nil {
			clh.X.Set(x)
			clh.Y.Set(y)
			if !clh.Width.IsConstraint() {
				clh.Width.Set(ww)
			}
			if !clh.Height.IsConstraint() {
				clh.Height.Set(hh)
			}
		}
		childW = int64(mancini.ChildWidth(childL, float64(ww)))
		childH = int64(mancini.ChildHeight(childL, float64(hh)))
	}
	if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
		cs.SetDC(dc)
	}

	// Clip child to window bounds. Pad covers the rachel border zone
	// so save/restore reaches the backing store edge.
	const borderPad = 60
	tc0 := nanotime()
	ccR := mancini.WithClip(dc, float64(x), float64(y),
		float64(childW), float64(childH), borderPad, mancini.ClipRight)
	ccB := mancini.WithClip(dc, float64(x), float64(y),
		float64(childW), float64(childH), borderPad, mancini.ClipBottom)
	drawPerf.AppClipNs.Add(nanotime() - tc0)

	tc1 := nanotime()
	if d, ok := child.(mancini.NewDrawer); ok {
		d.Draw(child, x, y, childW, childH, damage)
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

// PublishThemeAttributes publishes theme geometry and font configuration
// as constraint attributes under the "Theme/" namespace. Call once during
// setup after creating the AppWindow.
func (w *AppWindow) PublishThemeAttributes(theme mancini.Theme) {
	publishI64 := func(name string, v int64) {
		attr.ValueI64(attr.ShepherdURI("int64", "Theme/"+name), v)
	}
	publishF64 := func(name string, v float64) {
		// Store float64 as fixed-point int64 (value * 1000).
		attr.ValueI64(attr.ShepherdURI("int64", "Theme/"+name), int64(v*1000))
	}

	attr.ValueStr(attr.ShepherdURI("string", "Theme/Name"), theme.Name())
	attr.ValueStr(attr.ShepherdURI("string", "Theme/FontFamily"), theme.FontFamily())
	attr.ValueStr(attr.ShepherdURI("string", "Theme/FontFamilyMono"), theme.FontFamilyMono())

	publishF64("ControlRadius", theme.ControlRadius())
	publishF64("FieldRadius", theme.FieldRadius())
	publishF64("FieldBorderWidth", theme.FieldBorderWidth())
	publishF64("FieldPadding", theme.FieldPadding())
	publishF64("ScrollbarThickness", theme.ScrollbarThickness())
	publishF64("ScrollbarMinThumb", theme.ScrollbarMinThumb())

	publishI64("TitleFontSize", theme.TitleFontSize())
	publishI64("ControlFontSize", theme.ControlFontSize())
	publishI64("BodyFontSize", theme.BodyFontSize())
	publishI64("SmallFontSize", theme.SmallFontSize())
}

// ansiIndexName returns a short name for ANSI color indices 0-15.
func ansiIndexName(i int) string {
	names := [16]string{
		"0", "1", "2", "3", "4", "5", "6", "7",
		"8", "9", "10", "11", "12", "13", "14", "15",
	}
	if i >= 0 && i < 16 {
		return names[i]
	}
	return "0"
}

// Decorate implements mancini.Decoratable — no-op. Window chrome is
// rachel's responsibility.
func (w *AppWindow) Decorate(self mancini.Interactor, x, y, ww, hh int64) {
	// Intentionally empty — decoration will move to rachel.
}

// SendBlit sends a Blit message to rachel reporting the app-area dimensions
// that were actually drawn. Rachel uses DrawnWidth/DrawnHeight to clip
// compositing during resize so undrawn pixels are not blitted.
func (w *AppWindow) SendBlit() {
	if w.RachelSID <= 0 {
		return
	}
	lh := w.GetLayout()
	var dw, dh int32
	if lh != nil {
		dw = int32(lh.Width.Get())
		dh = int32(lh.Height.Get())
	}
	msg := wm.EncodeBlit(&wm.Blit{
		SID:         int32(os.Getpid()),
		DrawnWidth:  dw,
		DrawnHeight: dh,
	})
	_ = uring.Send(w.RachelSID, &msg)
}

// AnnounceToWM sends an AppStart message to rachel with the desired
// initial window position and size.
func (w *AppWindow) AnnounceToWM(x, y, width, height int32) {
	w.AnnounceToWMWithPlacement(x, y, width, height, wm.PlacementDefault)
}

// AnnounceToWMWithPlacement sends an AppStart with an explicit placement hint.
func (w *AppWindow) AnnounceToWMWithPlacement(x, y, width, height, placement int32) {
	if w.RachelSID <= 0 {
		return
	}
	msg := wm.EncodeAppStart(&wm.AppStart{
		SID:       int32(os.Getpid()),
		X:         x,
		Y:         y,
		Width:     width,
		Height:    height,
		Placement: placement,
	})
	if err := uring.Send(w.RachelSID, &msg); err != nil {
		sys.UartWriteString(fmt.Sprintf("[AppWindow] AnnounceToWM failed: %s\n", err.Error()))
		return
	}
	sys.UartWriteString(fmt.Sprintf("[AppWindow] sent AppStart %dx%d at (%d,%d) placement=%d\n",
		width, height, x, y, placement))
}
