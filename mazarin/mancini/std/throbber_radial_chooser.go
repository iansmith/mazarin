// throbber_radial_chooser.go — Pulsing indicator that opens a radial
// overlay menu on click.
//
// ThrobberRadialChooser is a small pulsing circle (throbber) that, when
// clicked, requests an overlay from rachel and draws a RadialNOfMChooser
// inside it. The overlay is dismissed on segment selection or on focus loss.
//
// The caller provides labels and an initial selection index. After the
// user selects a segment, OnSelect is called with the segment index.
//
// Usage:
//
//	trc := std.NewThrobberRadialChooser("trc", parentName, theme, pal,
//	    rachelSID, mainDC, []string{"Small","Large"}, 0)
//	// In the main loop, route OverlayReady/OverlayReleased/MouseMove/MouseRelease
//	// to trc.HandleOverlayReady, trc.HandleOverlayReleased, trc.HandleMouseMove,
//	// trc.HandleMouseRelease.
//	// Call trc.Tick() at 10Hz for the pulsing animation.
package std

import (
	"fmt"
	"image"
	"image/color"
	"sync"
	"unsafe"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
	"mazzy/mazarin/uring"
	"mazzy/shared/wm"
)

const (
	trcHalfTicks = 20 // 2 seconds × 10Hz
	trcFullTicks = 2 * trcHalfTicks
)

// Throbber orange endpoints (BGRA framebuffer order — SwapRB applied in Draw).
var (
	trcDimR, trcDimG, trcDimB       uint8 = 60, 30, 0
	trcBrightR, trcBrightG, trcBrightB uint8 = 255, 160, 0
)

// ThrobberRadialChooser is a pulsing circle that opens a radial overlay
// menu on click. It manages the full overlay lifecycle with rachel.
type ThrobberRadialChooser struct {
	impl.Interactor

	// OnSelect is called when the user selects a segment.
	// The argument is the segment index.
	OnSelect func(int)

	// FocusParent is the interactor that owns this throbber. When the
	// throbber is clicked, it calls FocusParent.SetFocusToSelf() to
	// claim in-app keyboard focus for the containing Unit.
	FocusParent mancini.FocusClaimer

	theme      mancini.Theme
	pal        mancini.Palette
	rachelSID  int
	mainDC     mancini.DrawContext // parent DC for creating overlay child DC
	labels     []string
	radialMenu *RadialNOfMChooser

	// Throbber animation state.
	tick int

	// Overlay state.
	mu                             sync.Mutex
	overlayID                      int32 // -1 = none
	overlayDC                      mancini.DrawContext
	overlayImg                     *image.RGBA
	overlayW, overlayH             int32
	overlayScreenX, overlayScreenY int32
}

// NewThrobberRadialChooser creates a ThrobberRadialChooser. The throbber
// is sized to size×size pixels. labels are the segment texts for the
// radial menu. initialSel is the initially selected segment index.
// rachelSID is the shepherd ID for rachel (overlay IPC).
// mainDC is the app's primary DrawContext, used to create overlay child DCs.
func NewThrobberRadialChooser(myName, parent string,
	theme mancini.Theme, pal mancini.Palette,
	rachelSID int, mainDC mancini.DrawContext,
	labels []string, initialSel int) *ThrobberRadialChooser {

	if myName == "" {
		myName = mancini.DefaultName("throbradial")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Width.Set(20)
	lh.Height.Set(20)

	trc := &ThrobberRadialChooser{
		theme:     theme,
		pal:       pal,
		rachelSID: rachelSID,
		mainDC:    mainDC,
		labels:    labels,
		overlayID: -1,
	}
	trc.Interactor.Initialize(trc, lh)

	// Create the radial menu (hidden until overlay is allocated).
	// Arc fans lower-right from the throbber's corner: 3.75° to 86.25°.
	segDeg := 82.5 / float64(len(labels))
	_ = segDeg // segment size is implicit from start/end + N labels
	trc.radialMenu = NewRadialNOfMChooserNamed(
		myName+"_radial", "", theme,
		5, 5, 60, 160, 3.75, 86.25,
		labels, 18, 1,
	)
	trc.radialMenu.Select(initialSel)
	trc.radialMenu.SetVisible(false)

	return trc
}

// SetMainDC updates the parent DrawContext used for overlay child DCs.
// Call this after the backing store is ready if mainDC was nil at construction.
func (trc *ThrobberRadialChooser) SetMainDC(dc mancini.DrawContext) {
	trc.mainDC = dc
}

// --- Throbber animation ---

// Tick advances the throbber by one 10Hz step. Always returns true
// since the color changes continuously.
func (trc *ThrobberRadialChooser) Tick() bool {
	trc.tick = (trc.tick + 1) % trcFullTicks
	return true
}

func (trc *ThrobberRadialChooser) fraction() float64 {
	if trc.tick < trcHalfTicks {
		return float64(trc.tick) / float64(trcHalfTicks)
	}
	return float64(trcFullTicks-trc.tick) / float64(trcHalfTicks)
}

func (trc *ThrobberRadialChooser) throbColor() color.NRGBA {
	f := trc.fraction()
	r := trcDimR + uint8(f*float64(trcBrightR-trcDimR))
	g := trcDimG + uint8(f*float64(trcBrightG-trcDimG))
	b := trcDimB + uint8(f*float64(trcBrightB-trcDimB))
	return mancini.SwapRB(color.NRGBA{R: r, G: g, B: b, A: 255})
}

// Draw renders the throbber as a filled circle at the current brightness.
func (trc *ThrobberRadialChooser) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	dc := self.DC()
	if dc == nil {
		return
	}
	if w <= 0 || h <= 0 {
		return
	}

	cx := float64(x) + float64(w)/2
	cy := float64(y) + float64(h)/2
	sz := w
	if h < sz {
		sz = h
	}
	maxRad := float64(sz) / 2
	f := trc.fraction()
	rad := maxRad * (0.7 + 0.3*f)

	dc.SetColor(trc.throbColor())
	dc.DrawCircle(cx, cy, rad)
	dc.Fill()
}

// --- Overlay lifecycle ---

// RequestOverlay sends an OverlayAllocate to rachel. If an overlay is
// already active, dismisses it instead. Call this when the throbber is clicked.
func (trc *ThrobberRadialChooser) RequestOverlay() {
	trc.mu.Lock()
	id := trc.overlayID
	trc.mu.Unlock()
	if id >= 0 {
		// Already showing — dismiss.
		msg := wm.EncodeOverlayRelease(&wm.OverlayRelease{OverlayID: id})
		_ = uring.Send(trc.rachelSID, &msg)
		return
	}

	// Arc center sits at the bottom-right corner of the throbber.
	lh := trc.GetLayout()
	throbX := lh.X.Get()
	throbY := lh.Y.Get()
	throbW := lh.Width.Get()
	throbH := lh.Height.Get()
	appCX := float64(throbX) + float64(throbW)
	appCY := float64(throbY) + float64(throbH)

	screenOX, screenOY := mancini.ScreenOrigin()
	screenCX := float64(screenOX) + appCX
	screenCY := float64(screenOY) + appCY

	outerR := 160.0
	pad := 5.0
	x1 := int32(screenCX - pad)
	y1 := int32(screenCY - pad)
	x2 := int32(screenCX + outerR + pad)
	y2 := int32(screenCY + outerR + pad)

	fmt.Printf("[throbber] requesting overlay: screen rect (%d,%d)-(%d,%d)\n",
		x1, y1, x2, y2)

	msg := wm.EncodeOverlayAllocate(&wm.OverlayAllocate{
		ScreenX1: x1, ScreenY1: y1, ScreenX2: x2, ScreenY2: y2,
	})
	_ = uring.Send(trc.rachelSID, &msg)
}

// HandleOverlayReady processes the OverlayReady message from rachel.
// Returns true if this ThrobberRadialChooser handled the message.
func (trc *ThrobberRadialChooser) HandleOverlayReady(m wm.OverlayReady) {
	trc.overlayW = m.Width
	trc.overlayH = m.Height
	trc.overlayScreenX = m.ScreenX
	trc.overlayScreenY = m.ScreenY

	stride := int(m.Stride)
	bufLen := stride * int(m.Height)
	ovSlice := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(m.OverlayAddr))), bufLen)
	trc.overlayImg = &image.RGBA{
		Pix:    ovSlice,
		Stride: stride,
		Rect:   image.Rect(0, 0, int(m.Width), int(m.Height)),
	}

	trc.overlayDC = trc.mainDC.NewChildContext(trc.overlayImg)

	trc.radialMenu.SetVisible(true)
	trc.radialMenu.SetDC(trc.overlayDC)
	trc.radialMenu.Draw(trc.radialMenu, 0, 0, int64(m.Width), int64(m.Height), image.Rect(0, 0, int(m.Width), int(m.Height)))

	trc.mu.Lock()
	trc.overlayID = m.OverlayID
	trc.mu.Unlock()

	blit := wm.EncodeOverlayBlit(&wm.OverlayBlit{OverlayID: m.OverlayID})
	_ = uring.Send(trc.rachelSID, &blit)
}

// HandleOverlayReleased cleans up overlay state after rachel confirms teardown.
func (trc *ThrobberRadialChooser) HandleOverlayReleased(m wm.OverlayReleased) {
	trc.mu.Lock()
	trc.overlayID = -1
	trc.mu.Unlock()
	trc.overlayDC = nil
	trc.overlayImg = nil
	trc.radialMenu.SetVisible(false)
}

// OverlayActive returns true if an overlay is currently allocated.
func (trc *ThrobberRadialChooser) OverlayActive() bool {
	trc.mu.Lock()
	id := trc.overlayID
	trc.mu.Unlock()
	return id >= 0
}

// --- Overlay input routing ---

// HandleMouseMove processes a MouseMove event for the overlay.
// Converts app-local coords to overlay-local and updates hover state.
// Returns true if the overlay was redrawn (caller should NOT redraw main window).
func (trc *ThrobberRadialChooser) HandleMouseMove(m wm.MouseMove, appScreenX, appScreenY int32) bool {
	if !trc.OverlayActive() {
		return false
	}
	olx := float64(m.X + appScreenX - trc.overlayScreenX)
	oly := float64(m.Y + appScreenY - trc.overlayScreenY)
	seg := trc.radialMenu.HitTestSegment(olx, oly)
	if trc.radialMenu.SetHovered(seg) {
		trc.radialMenu.SetDC(trc.overlayDC)
		trc.radialMenu.Draw(trc.radialMenu, 0, 0, int64(trc.overlayW), int64(trc.overlayH), image.Rect(0, 0, int(trc.overlayW), int(trc.overlayH)))
		trc.mu.Lock()
		id := trc.overlayID
		trc.mu.Unlock()
		if id >= 0 {
			blit := wm.EncodeOverlayBlit(&wm.OverlayBlit{OverlayID: id})
			_ = uring.Send(trc.rachelSID, &blit)
		}
	}
	return true
}

// HandleMouseRelease processes a MouseRelease event for the overlay.
// Selects the segment under the cursor (if any), calls OnSelect, and
// dismisses the overlay. Returns true if the overlay was active (caller
// should NOT redraw main window).
func (trc *ThrobberRadialChooser) HandleMouseRelease(m wm.MouseRelease, appScreenX, appScreenY int32) bool {
	if !trc.OverlayActive() {
		return false
	}
	olx := float64(m.X + appScreenX - trc.overlayScreenX)
	oly := float64(m.Y + appScreenY - trc.overlayScreenY)
	seg := trc.radialMenu.HitTestSegment(olx, oly)

	trc.mu.Lock()
	id := trc.overlayID
	trc.mu.Unlock()

	if seg >= 0 && id >= 0 {
		trc.radialMenu.Select(seg)
		if trc.OnSelect != nil {
			trc.OnSelect(seg)
		}

		// Redraw overlay with updated selection, then dismiss.
		trc.radialMenu.SetDC(trc.overlayDC)
		trc.radialMenu.Draw(trc.radialMenu, 0, 0, int64(trc.overlayW), int64(trc.overlayH), image.Rect(0, 0, int(trc.overlayW), int(trc.overlayH)))
		blit := wm.EncodeOverlayBlit(&wm.OverlayBlit{OverlayID: id})
		_ = uring.Send(trc.rachelSID, &blit)
	}

	// Dismiss the overlay.
	if id >= 0 {
		trc.mu.Lock()
		trc.overlayID = -1
		trc.mu.Unlock()
		rel := wm.EncodeOverlayRelease(&wm.OverlayRelease{OverlayID: id})
		_ = uring.Send(trc.rachelSID, &rel)
	}
	trc.radialMenu.SetHovered(-1)
	return true
}

// Click implements mancini.Clickable. Claims in-app focus for the
// containing Unit, then opens or dismisses the radial overlay.
func (trc *ThrobberRadialChooser) Click(ev *mancini.InputEvent) bool {
	fmt.Printf("[throbber] Click at (%d,%d)\n", ev.X, ev.Y)
	if trc.FocusParent != nil {
		trc.FocusParent.SetFocusToSelf()
	}
	trc.RequestOverlay()
	return true
}

// HitTest returns true if the point (lx, ly) in app-local coordinates
// is within the throbber's bounds (with 10px padding for easy clicking).
func (trc *ThrobberRadialChooser) HitTest(lx, ly int) bool {
	lh := trc.GetLayout()
	bx := int(lh.X.Get())
	by := int(lh.Y.Get())
	bw := int(lh.Width.Get())
	bh := int(lh.Height.Get())
	pad := 10
	return lx >= bx-pad && lx < bx+bw+pad && ly >= by-pad && ly < by+bh+pad
}

// Selected returns the currently selected segment index, or -1 if none.
func (trc *ThrobberRadialChooser) Selected() int {
	sel := trc.radialMenu.Selected()
	if len(sel) == 0 {
		return -1
	}
	return sel[0]
}
