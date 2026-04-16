// corner_radial_chooser.go — Self-contained pulsing indicator + radial
// overlay menu.
//
// CornerRadialChooser is a drop-in interactor: create it as a child of
// any container, and it handles its own throbber animation, overlay
// lifecycle, and mouse input. The caller only needs to route WM events
// and call Tick() at 10Hz.
//
// Unlike ThrobberRadialChooser, CornerRadialChooser tracks its own
// drawn position and app screen origin — no manual coordinate wiring.
//
// Usage:
//
//	crc := std.NewCornerRadialChooser("crc", parentName, theme, pal,
//	    rachelSID, []string{"Small","Medium","Large"}, 0)
//	crc.OnSelect = func(idx int) { ... }
//	// In event loop:
//	//   crc.Tick() at 10Hz
//	//   Route OverlayReady/Released and mouse events via crc methods
package std

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sync"
	"unsafe"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
	"mazzy/mazarin/uring"
	"mazzy/shared/wm"
)

// CornerRadialChooser is a pulsing circle that opens a radial overlay
// menu on click. It packages the throbber animation, overlay lifecycle,
// and radial menu into a single self-contained interactor.
//
// The chooser publishes a ValueAttr — an int64 attribute whose value
// corresponds to the selected segment. Applications provide both labels
// and values at construction time. When the user selects segment i,
// ValueAttr is set to values[i]. Other interactors can bind to
// ValueURI() to react to changes via the constraint network.
type CornerRadialChooser struct {
	impl.Interactor

	// OnSelect is called when the user selects a segment.
	// The argument is the segment index.
	OnSelect func(int)

	// ValueAttr is the published attribute holding the currently
	// selected value. Other interactors can bind to ValueURI().
	ValueAttr *attr.Attribute[int64]

	// FocusParent is the interactor that owns this chooser. When
	// clicked, it calls FocusParent.SetFocusToSelf() to claim focus.
	FocusParent mancini.FocusClaimer

	theme      mancini.Theme
	pal        mancini.Palette
	rachelSID  int
	mainDC     mancini.DrawContext
	labels     []string
	values     []int64
	radialMenu *RadialNOfMChooser

	// Throbber animation state.
	tick int

	// Position tracking — updated every Draw.
	drawnX, drawnY int64

	// App screen origin — set via SetAppScreenPos.
	appScreenX, appScreenY int32

	// Screen dimensions — set via SetScreenSize.
	screenW, screenH int32

	// fanLeft is true when the overlay fans left-up instead of right-down
	// (auto-detected when the throbber is near the right/bottom screen edge).
	fanLeft bool

	// Overlay state.
	mu                             sync.Mutex
	overlayID                      int32
	overlayDC                      mancini.DrawContext
	overlayImg                     *image.RGBA
	overlayW, overlayH             int32
	overlayScreenX, overlayScreenY int32
}

// NewCornerRadialChooser creates a CornerRadialChooser. labels are the
// segment texts and values are the corresponding int64 values published
// to ValueAttr when a segment is selected. initialSel is the initially
// selected segment index. rachelSID is rachel's shepherd ID for overlay IPC.
func NewCornerRadialChooser(myName, parent string,
	theme mancini.Theme, pal mancini.Palette,
	rachelSID int,
	labels []string, values []int64, initialSel int) *CornerRadialChooser {

	if myName == "" {
		myName = mancini.DefaultName("cornradial")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Width.Set(20)
	lh.Height.Set(20)

	// Publish the value attribute with the initial selection's value.
	initialValue := int64(0)
	if initialSel >= 0 && initialSel < len(values) {
		initialValue = values[initialSel]
	}
	valueURI := mancini.LayoutURI(myName, mancini.DataTypeInt64,
		mancini.LayoutProp("value"))
	valueAttr := attr.ValueI64(valueURI, initialValue)

	crc := &CornerRadialChooser{
		theme:     theme,
		pal:       pal,
		rachelSID: rachelSID,
		labels:    labels,
		values:    values,
		ValueAttr: valueAttr,
		overlayID: -1,
	}
	crc.Interactor.Initialize(crc, lh)

	// Arc fans lower-right from the bottom-right corner: 3.75° to 86.25°.
	crc.radialMenu = NewRadialNOfMChooserNamed(
		myName+"_radial", "", theme,
		5, 5, 60, 160, 3.75, 86.25,
		labels, 18, 1,
	)
	crc.radialMenu.Select(initialSel)
	crc.radialMenu.SetVisible(false)

	return crc
}

// ValueURI returns the URI of the published value attribute, for use
// in constraint bindings.
func (crc *CornerRadialChooser) ValueURI() string {
	return crc.ValueAttr.URI()
}

// SetAppScreenPos updates the app's screen origin. Call this after
// BackingStoreReady and on WindowMoved events.
func (crc *CornerRadialChooser) SetAppScreenPos(x, y int32) {
	crc.appScreenX = x
	crc.appScreenY = y
}

// SetScreenSize sets the screen dimensions so the chooser can detect
// edge proximity and fan in the appropriate direction.
func (crc *CornerRadialChooser) SetScreenSize(w, h int32) {
	crc.screenW = w
	crc.screenH = h
}


// --- Throbber animation ---

// Tick advances the throbber by one 10Hz step.
func (crc *CornerRadialChooser) Tick() bool {
	crc.tick = (crc.tick + 1) % trcFullTicks
	return true
}

func (crc *CornerRadialChooser) fraction() float64 {
	if crc.tick < trcHalfTicks {
		return float64(crc.tick) / float64(trcHalfTicks)
	}
	return float64(trcFullTicks-crc.tick) / float64(trcHalfTicks)
}

func (crc *CornerRadialChooser) throbColor() color.NRGBA {
	f := crc.fraction()
	r := trcDimR + uint8(f*float64(trcBrightR-trcDimR))
	g := trcDimG + uint8(f*float64(trcBrightG-trcDimG))
	b := trcDimB + uint8(f*float64(trcBrightB-trcDimB))
	return mancini.SwapRB(color.NRGBA{R: r, G: g, B: b, A: 255})
}

// Draw renders the throbber as a pulsing filled circle. It also
// captures the drawn position and DC for overlay use.
func (crc *CornerRadialChooser) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	if !crc.Damaged(damage) {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}
	if w <= 0 || h <= 0 {
		return
	}

	// Track position and DC for overlay calculations.
	if crc.drawnX != x || crc.drawnY != y {
		fmt.Printf("[corner-radial] drawn at app=(%d,%d) size=(%dx%d)\n", x, y, w, h)
	}
	crc.drawnX = x
	crc.drawnY = y
	if crc.mainDC == nil {
		crc.mainDC = dc
	}

	cx := float64(x) + float64(w)/2
	cy := float64(y) + float64(h)/2
	sz := w
	if h < sz {
		sz = h
	}
	maxRad := float64(sz) / 2
	f := crc.fraction()
	rad := maxRad * (0.7 + 0.3*f)

	dc.SetColor(crc.throbColor())
	dc.DrawCircle(cx, cy, rad)
	dc.Fill()

	crc.ClearDamage()
}

// --- Overlay lifecycle ---

// RequestOverlay sends an OverlayAllocate to rachel. If an overlay is
// already active, dismisses it instead.
func (crc *CornerRadialChooser) RequestOverlay() {
	crc.mu.Lock()
	id := crc.overlayID
	crc.mu.Unlock()
	if id >= 0 {
		msg := wm.EncodeOverlayRelease(&wm.OverlayRelease{OverlayID: id})
		_ = uring.Send(crc.rachelSID, &msg)
		return
	}

	// Use the actual drawn position (not layout attrs which may be stale).
	lh := crc.GetLayout()
	throbW := lh.Width.Get()
	throbH := lh.Height.Get()

	outerR := 160.0
	pad := 5.0
	overlaySize := int32(outerR + pad + pad)

	// Anchor at bottom-right of throbber.
	anchorAppX := float64(crc.drawnX) + float64(throbW)
	anchorAppY := float64(crc.drawnY) + float64(throbH)
	anchorScreenX := float64(crc.appScreenX) + anchorAppX
	anchorScreenY := float64(crc.appScreenY) + anchorAppY

	// Determine fan direction based on screen edge proximity.
	crc.fanLeft = int32(anchorScreenX)+overlaySize > crc.screenW && crc.screenW > 0

	var x1, y1, x2, y2 int32
	if crc.fanLeft {
		// Fan left-down: overlay extends leftward from the throbber's bottom-left.
		anchorAppX = float64(crc.drawnX)
		anchorScreenX = float64(crc.appScreenX) + anchorAppX
		x1 = int32(anchorScreenX - outerR - pad)
		y1 = int32(anchorScreenY - pad)
		x2 = int32(anchorScreenX + pad)
		y2 = int32(anchorScreenY + outerR + pad)
	} else {
		// Fan right-down (default).
		x1 = int32(anchorScreenX - pad)
		y1 = int32(anchorScreenY - pad)
		x2 = int32(anchorScreenX + outerR + pad)
		y2 = int32(anchorScreenY + outerR + pad)
	}

	fmt.Printf("[corner-radial] requesting overlay: screen rect (%d,%d)-(%d,%d) fanLeft=%v\n",
		x1, y1, x2, y2, crc.fanLeft)

	msg := wm.EncodeOverlayAllocate(&wm.OverlayAllocate{
		ScreenX1: x1, ScreenY1: y1, ScreenX2: x2, ScreenY2: y2,
	})
	_ = uring.Send(crc.rachelSID, &msg)
}

// HandleOverlayReady processes the OverlayReady message from rachel.
func (crc *CornerRadialChooser) HandleOverlayReady(m wm.OverlayReady) {
	crc.overlayW = m.Width
	crc.overlayH = m.Height
	crc.overlayScreenX = m.ScreenX
	crc.overlayScreenY = m.ScreenY

	stride := int(m.Stride)
	bufLen := stride * int(m.Height)
	ovSlice := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(m.OverlayAddr))), bufLen)
	crc.overlayImg = &image.RGBA{
		Pix:    ovSlice,
		Stride: stride,
		Rect:   image.Rect(0, 0, int(m.Width), int(m.Height)),
	}

	crc.overlayDC = crc.mainDC.NewChildContext(crc.overlayImg)

	// Adjust radial menu geometry based on fan direction.
	if crc.fanLeft {
		// Fan left-down: center at top-right of overlay.
		crc.radialMenu.CX = float64(m.Width) - 5
		crc.radialMenu.CY = 5
		crc.radialMenu.StartDeg = 93.75
		crc.radialMenu.EndDeg = 176.25
	} else {
		// Fan right-down: center at top-left of overlay.
		crc.radialMenu.CX = 5
		crc.radialMenu.CY = 5
		crc.radialMenu.StartDeg = 3.75
		crc.radialMenu.EndDeg = 86.25
	}

	// Sync radial menu selection from the attribute value.
	crc.syncSelectionFromAttr()

	crc.radialMenu.SetVisible(true)
	crc.radialMenu.SetDC(crc.overlayDC)
	crc.radialMenu.Draw(crc.radialMenu, 0, 0, int64(m.Width), int64(m.Height),
		image.Rect(0, 0, int(m.Width), int(m.Height)))

	crc.mu.Lock()
	crc.overlayID = m.OverlayID
	crc.mu.Unlock()

	blit := wm.EncodeOverlayBlit(&wm.OverlayBlit{OverlayID: m.OverlayID})
	_ = uring.Send(crc.rachelSID, &blit)
}

// HandleOverlayReleased cleans up overlay state.
func (crc *CornerRadialChooser) HandleOverlayReleased(m wm.OverlayReleased) {
	crc.mu.Lock()
	crc.overlayID = -1
	crc.mu.Unlock()
	crc.overlayDC = nil
	crc.overlayImg = nil
	crc.radialMenu.SetVisible(false)
}

// OverlayActive returns true if an overlay is currently allocated.
func (crc *CornerRadialChooser) OverlayActive() bool {
	crc.mu.Lock()
	id := crc.overlayID
	crc.mu.Unlock()
	return id >= 0
}

// --- ClickDraggable protocol ---

// ClickDragStart implements mancini.ClickDraggable. Opens the overlay
// immediately on press so the user can drag to a segment.
func (crc *CornerRadialChooser) ClickDragStart(ev *mancini.InputEvent) bool {
	fmt.Printf("[corner-radial] ClickDragStart at (%d,%d)\n", ev.X, ev.Y)
	if crc.FocusParent != nil {
		crc.FocusParent.SetFocusToSelf()
	}
	crc.RequestOverlay()
	return true
}

// ClickDragMove implements mancini.ClickDraggable. Highlights the
// segment under the cursor as the user drags over the overlay.
func (crc *CornerRadialChooser) ClickDragMove(ev *mancini.InputEvent, startEv *mancini.InputEvent, outsideBounds bool) bool {
	if !crc.OverlayActive() {
		return true
	}
	olx := float64(int32(ev.X) + crc.appScreenX - crc.overlayScreenX)
	oly := float64(int32(ev.Y) + crc.appScreenY - crc.overlayScreenY)
	seg := crc.radialMenu.HitTestSegment(olx, oly)
	if crc.radialMenu.SetHovered(seg) {
		crc.radialMenu.SetDC(crc.overlayDC)
		crc.radialMenu.Draw(crc.radialMenu, 0, 0, int64(crc.overlayW), int64(crc.overlayH),
			image.Rect(0, 0, int(crc.overlayW), int(crc.overlayH)))
		crc.mu.Lock()
		id := crc.overlayID
		crc.mu.Unlock()
		if id >= 0 {
			blit := wm.EncodeOverlayBlit(&wm.OverlayBlit{OverlayID: id})
			_ = uring.Send(crc.rachelSID, &blit)
		}
	}
	return true
}

// ClickDragEnd implements mancini.ClickDraggable. Selects the segment
// under the cursor (if any) and dismisses the overlay.
func (crc *CornerRadialChooser) ClickDragEnd(ev *mancini.InputEvent, outsideBounds bool) bool {
	if !crc.OverlayActive() {
		return true
	}
	olx := float64(int32(ev.X) + crc.appScreenX - crc.overlayScreenX)
	oly := float64(int32(ev.Y) + crc.appScreenY - crc.overlayScreenY)
	seg := crc.radialMenu.HitTestSegment(olx, oly)

	dx := olx - crc.radialMenu.CX
	dy := oly - crc.radialMenu.CY
	r := math.Sqrt(dx*dx + dy*dy)
	angle := math.Atan2(dy, dx) * 180 / math.Pi
	fmt.Printf("[corner-radial] ClickDragEnd: overlay-local(%.0f,%.0f) r=%.0f angle=%.1f° seg=%d\n",
		olx, oly, r, angle, seg)

	crc.mu.Lock()
	id := crc.overlayID
	crc.mu.Unlock()

	if seg >= 0 && id >= 0 {
		crc.radialMenu.ForceSelect(seg)
		if seg < len(crc.values) {
			crc.ValueAttr.Set(crc.values[seg])
			fmt.Printf("[corner-radial] selected seg=%d value=%d\n", seg, crc.values[seg])
		}
		if crc.OnSelect != nil {
			crc.OnSelect(seg)
		}
	}

	// Dismiss the overlay.
	if id >= 0 {
		crc.mu.Lock()
		crc.overlayID = -1
		crc.mu.Unlock()
		rel := wm.EncodeOverlayRelease(&wm.OverlayRelease{OverlayID: id})
		_ = uring.Send(crc.rachelSID, &rel)
	}
	crc.radialMenu.SetHovered(-1)
	return true
}

// HitTest returns true if (lx, ly) in app-local coordinates is within
// the chooser's bounds (with 10px padding for easy clicking).
func (crc *CornerRadialChooser) HitTest(lx, ly int) bool {
	lh := crc.GetLayout()
	bx := int(lh.X.Get())
	by := int(lh.Y.Get())
	bw := int(lh.Width.Get())
	bh := int(lh.Height.Get())
	pad := 10
	return lx >= bx-pad && lx < bx+bw+pad && ly >= by-pad && ly < by+bh+pad
}

// syncSelectionFromAttr sets the radial menu's selection to match the
// current ValueAttr value, ensuring the display is consistent.
func (crc *CornerRadialChooser) syncSelectionFromAttr() {
	if crc.radialMenu == nil {
		return
	}
	val := crc.ValueAttr.Get()
	for i, v := range crc.values {
		if v == val {
			crc.radialMenu.ForceSelect(i)
			return
		}
	}
}

// Selected returns the currently selected segment index, or -1.
func (crc *CornerRadialChooser) Selected() int {
	if crc.radialMenu == nil {
		return -1
	}
	sel := crc.radialMenu.Selected()
	if len(sel) == 0 {
		return -1
	}
	return sel[0]
}
