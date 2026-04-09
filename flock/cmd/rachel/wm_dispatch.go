// wm_dispatch.go implements rachel's subArctic-style input dispatch pipeline.
//
// Rachel's dispatch pipeline has six policies in priority order:
//
//  1. "wm-accel"       (focus)       — AcceleratorAgent: WM keyboard shortcuts
//  2. "drag"           (focus)       — DragAgent: mouse move/release during active drag
//  3. "titlebar-drag"  (positional)  — TitlebarDragAgent: titlebar press → focus + move
//  4. "resize-drag"    (positional)  — ResizeDragAgent: resize handle press → focus + resize
//  5. "press"          (positional)  — PressAgent: content press → focus + forward to shepherd
//  6. "keyboard"       (focus)       — KeyForwardAgent: forward keyboard to focused app
//
// Interactors:
//   - WMInteractor: rachel herself (receives accelerator actions)
//   - WindowInteractor: a tracked shepherd window (forwards events via uring IPC)
package main

import (
	"fmt"
	"mazzy/mazarin/attr"
	"mazzy/mazarin/input"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/wm"
	"strconv"
	"time"
	"unsafe"
)

// wmKeyMapper is the global KeyMapper used by rachel to translate
// keycodes before forwarding to shepherds. Set during main() init.
var wmKeyMapper mancini.KeyMapper

// --- Keymapper diagnostics ---
// Tracks translation results and timing for every key event sent to
// the keymapper. Periodically dumps a summary to the serial console.

var kmDiagMapCalls int64      // total Map() invocations
var kmDiagMapNanos int64      // cumulative nanoseconds in Map()
var kmDiagUntranslated int64  // press events where ch==0 && action==""
var kmDiagChars int64         // press events that produced a character
var kmDiagActions int64       // press events that produced an action
var kmDiagLastDumpAt int64    // kmDiagMapCalls value at last summary dump

const kmDiagInterval = 50 // dump summary every N Map() calls

// kmDiagLog logs every key-down translation result and periodic summaries.
func kmDiagLog(code uint16, pressed bool, mods uint64, ch rune, action wm.Action, elapsed time.Duration) {
	kmDiagMapCalls++
	kmDiagMapNanos += elapsed.Nanoseconds()

	if pressed {
		if ch == 0 && action == wm.ActionNone {
			kmDiagUntranslated++
			// Always log untranslated presses — these are the ones we need to fix.
			sys.UartWriteString(fmt.Sprintf("[keymap:MISS] code=%d (%s) mods=0x%x elapsed=%v\n",
				code, keyName(code), mods, elapsed))
		} else if ch != 0 {
			kmDiagChars++
		} else {
			kmDiagActions++
		}
	}

	// Periodic summary.
	if kmDiagMapCalls-kmDiagLastDumpAt >= kmDiagInterval {
		kmDiagLastDumpAt = kmDiagMapCalls
		avgUs := int64(0)
		if kmDiagMapCalls > 0 {
			avgUs = kmDiagMapNanos / kmDiagMapCalls / 1000
		}
		fmt.Printf("[keymap:stat] calls=%d chars=%d actions=%d MISS=%d avg=%dµs\n",
			kmDiagMapCalls, kmDiagChars, kmDiagActions, kmDiagUntranslated, avgUs)
	}
}

// --- Interactors ---

// WMInteractor represents rachel herself as an input target.
// It receives keyboard accelerator actions from the AcceleratorAgent.
type WMInteractor struct{}

func (w *WMInteractor) PickedBy(x, y int32) bool { return false }

func (w *WMInteractor) Accelerator(action string, ev *input.InputEvent) bool {
	switch action {
	case "cycle-focus":
		cycleFocus()
		return true
	}
	return false
}

// WindowInteractor wraps a trackedApp, forwarding dispatched events to
// the shepherd via uring IPC. Implements Pressable, Movable, KeyReceiver.
type WindowInteractor struct {
	ta *trackedApp
}

func (w *WindowInteractor) PickedBy(x, y int32) bool {
	ta := w.ta
	// Use rachel's local position (includes borders) rather than the
	// shepherd's constraint bounds which may be at (0,0).
	ox := ta.x - int32(borderLeft)
	oy := ta.y - int32(borderTop)
	return x >= ox && x < ox+ta.bsWidth && y >= oy && y < oy+ta.bsHeight
}

func (w *WindowInteractor) SID() int { return w.ta.sid }

// InTitleBar returns true if screen point (x,y) is inside this window's
// title bar region. The title bar sits at [shadowTop, shadowTop+titleBarHeight)
// within the backing store, offset by the window's screen position.
func (w *WindowInteractor) InTitleBar(x, y int32) bool {
	ta := w.ta
	// Title bar in screen coordinates:
	//   X: from left edge of NeuBox face to right edge
	//   Y: from shadowTop to shadowTop+titleBarHeight (within the border zone)
	ox := ta.x - int32(borderLeft)
	oy := ta.y - int32(borderTop)
	tbY0 := oy + int32(shadowTop)
	tbY1 := tbY0 + int32(titleBarHeight)
	tbX0 := ox + int32(borderLeft)
	tbX1 := ox + ta.bsWidth - int32(borderRight)
	return x >= tbX0 && x < tbX1 && y >= tbY0 && y < tbY1
}

// ResizeEdge identifies which window edge is being resized.
type ResizeEdge int

const (
	EdgeNone   ResizeEdge = iota
	EdgeLeft              // left border center
	EdgeRight             // right border center
	EdgeBottom            // bottom border center
)

// InResizeEdge returns the resize edge under screen point (x,y), or
// EdgeNone if the point is not in a resize zone. Only bottom, left,
// and right edges are supported. Each edge has a semi-circle handle
// centered at the midpoint of the edge; hit testing uses circular
// distance and requires the point to be on the border side (not inside
// the app area).
func (w *WindowInteractor) InResizeEdge(x, y int32) ResizeEdge {
	ta := w.ta
	r := int32(resizeHandleRadius)

	// App area in screen coordinates.
	appX0 := ta.x
	appX1 := ta.x + ta.appWidth
	appY0 := ta.y
	appY1 := ta.y + ta.appHeight

	// Bottom handle: center at (midX, appY1), must be below app area.
	botCX := (appX0 + appX1) / 2
	botCY := appY1
	if y >= botCY && inCircle(x, y, botCX, botCY, r) {
		return EdgeBottom
	}

	// Left handle: center at (appX0, midY), must be left of app area.
	leftCX := appX0
	leftCY := (appY0 + appY1) / 2
	if x <= leftCX && inCircle(x, y, leftCX, leftCY, r) {
		return EdgeLeft
	}

	// Right handle: center at (appX1, midY), must be right of app area.
	rightCX := appX1
	rightCY := (appY0 + appY1) / 2
	if x >= rightCX && inCircle(x, y, rightCX, rightCY, r) {
		return EdgeRight
	}

	return EdgeNone
}

// inCircle returns true if (x,y) is within radius r of (cx,cy).
func inCircle(x, y, cx, cy, r int32) bool {
	dx := x - cx
	dy := y - cy
	return dx*dx+dy*dy <= r*r
}

func (w *WindowInteractor) Press(ev *input.InputEvent) bool {
	// Convert screen coords to app-local coords so the shepherd doesn't
	// need to know its own screen position (which changes after drags).
	msg := wm.EncodeMousePress(&wm.MousePress{
		X: ev.X - int32(w.ta.x), Y: ev.Y - int32(w.ta.y),
		Button: int32(ev.Code), Mods: ev.Mods,
	})
	if err := uring.Send(w.ta.sid, &msg); err != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:press] uring.Send to SID %d: %v\n", w.ta.sid, err))
	}
	return true
}

func (w *WindowInteractor) Release(ev *input.InputEvent) bool {
	msg := wm.EncodeMouseRelease(&wm.MouseRelease{
		X: ev.X - int32(w.ta.x), Y: ev.Y - int32(w.ta.y),
		Button: int32(ev.Code), Mods: ev.Mods,
	})
	if err := uring.Send(w.ta.sid, &msg); err != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:release] uring.Send to SID %d: %v\n", w.ta.sid, err))
	}
	return true
}

func (w *WindowInteractor) Move(ev *input.InputEvent) bool {
	msg := wm.EncodeMouseMove(&wm.MouseMove{
		X: ev.X - int32(w.ta.x), Y: ev.Y - int32(w.ta.y),
		Mods: ev.Mods,
	})
	if err := uring.Send(w.ta.sid, &msg); err != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:move] uring.Send to SID %d: %v\n", w.ta.sid, err))
	}
	return true
}

func (w *WindowInteractor) KeyDown(ev *input.InputEvent) bool {
	var ch rune
	var action wm.Action
	var elapsed time.Duration
	if wmKeyMapper != nil {
		t0 := time.Now()
		r, astr := wmKeyMapper.Map(ev.Code, true, ev.Mods)
		elapsed = time.Since(t0)
		ch = r
		action = wm.ActionByName(astr)
	}
	kmDiagLog(ev.Code, true, ev.Mods, ch, action, elapsed)
	kp := wm.KeyPress{
		Code: ev.Code, Mods: ev.Mods,
		Char: uint32(ch), Action: action,
	}
	msg := wm.EncodeKeyPress(&kp)
	if err := uring.Send(w.ta.sid, &msg); err != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:key] uring.Send to SID %d: %v\n", w.ta.sid, err))
	}
	// Start key-repeat animation for this key.
	startKeyRepeat(w.ta.sid, time.Now().UnixNano(), kp)
	return true
}

func (w *WindowInteractor) KeyUp(ev *input.InputEvent) bool {
	var ch rune
	var action wm.Action
	var elapsed time.Duration
	if wmKeyMapper != nil {
		t0 := time.Now()
		r, astr := wmKeyMapper.Map(ev.Code, false, ev.Mods)
		elapsed = time.Since(t0)
		ch = r
		action = wm.ActionByName(astr)
	}
	kmDiagLog(ev.Code, false, ev.Mods, ch, action, elapsed)
	// Stop key-repeat animation before sending release.
	stopKeyRepeat(ev.Code)
	msg := wm.EncodeKeyRelease(&wm.KeyRelease{
		Code: ev.Code, Mods: ev.Mods,
		Char: uint32(ch), Action: action,
	})
	if err := uring.Send(w.ta.sid, &msg); err != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:key] uring.Send to SID %d: %v\n", w.ta.sid, err))
	}
	return true
}

// --- Dispatch Agents ---

// AcceleratorAgent handles WM keyboard shortcuts. It checks key press
// events against an accelerator table and delivers matched actions to
// the Acceleratable target (WMInteractor). Consumes both press and
// subsequent release of matched keys.
type AcceleratorAgent struct {
	target   input.Interactor
	table    map[uint16]string // keycode → action name
	consumed map[uint16]bool   // keys consumed on press, waiting for release
}

func NewAcceleratorAgent(target input.Interactor, table map[uint16]string) *AcceleratorAgent {
	return &AcceleratorAgent{
		target:   target,
		table:    table,
		consumed: make(map[uint16]bool),
	}
}

func (a *AcceleratorAgent) Name() string                  { return "accel" }
func (a *AcceleratorAgent) FocusTarget() input.Interactor { return a.target }
func (a *AcceleratorAgent) SetFocus(t input.Interactor)   { a.target = t }

func (a *AcceleratorAgent) Deliver(ev *input.InputEvent, target input.Interactor) bool {
	if !ev.IsKeyboard() {
		return false
	}
	if ev.IsPress() {
		action, ok := a.table[ev.Code]
		if !ok {
			return false
		}
		a.consumed[ev.Code] = true
		if acc, ok := target.(input.Acceleratable); ok {
			return acc.Accelerator(action, ev)
		}
		return true
	}
	if ev.IsRelease() {
		if a.consumed[ev.Code] {
			delete(a.consumed, ev.Code)
			return true // suppress release of consumed accelerator
		}
	}
	return false
}

// DragAgent handles mouse move and release events during an active drag.
// The PressAgent sets the drag target on mouse button press; the DragAgent
// delivers subsequent events to that target until release.
type DragAgent struct {
	target       input.Interactor
	buttonCode   uint16
	titlebarDrag bool  // true = moving the window, false = content drag
	dragOffsetX  int32 // mouse offset from ta.x at press time
	dragOffsetY  int32 // mouse offset from ta.y at press time
	prevCursorX  int32 // cursor position of previous move event
	prevCursorY  int32 // cursor position of previous move event

	// Resize state.
	isResize     bool
	resizeEdge   ResizeEdge
	resizeOrigW  int32 // app width at drag start
	resizeOrigH  int32 // app height at drag start
	resizeMinW   int32 // minimum app width
	resizeMinH   int32 // minimum app height
	resizeMaxW   int32 // maximum app width (to screen edge)
	resizeMaxH   int32 // maximum app height (to screen edge)
	resizeOrigX  int32 // window X at drag start (for left-edge resize)
	resizePressX int32 // mouse X at drag start
	resizePressY int32 // mouse Y at drag start
	resizePrevW     int32  // previous frame's app width (skip no-op redraws)
	resizePrevH     int32  // previous frame's app height
	oversizedBuf    []byte // max-size backing store allocated at drag start
	oldBackingStore []byte // original backing store saved at drag start
}

func (a *DragAgent) Name() string                  { return "drag" }
func (a *DragAgent) FocusTarget() input.Interactor { return a.target }
func (a *DragAgent) SetFocus(t input.Interactor)   { a.target = t }

// StartTitlebarDrag sets up DragAgent for a window move via titlebar drag.
func (a *DragAgent) StartTitlebarDrag(wi *WindowInteractor, buttonCode uint16, pressX, pressY int32) {
	a.target = wi
	a.buttonCode = buttonCode
	a.titlebarDrag = true
	a.isResize = false
	a.dragOffsetX = pressX - wi.ta.x
	a.dragOffsetY = pressY - wi.ta.y
	a.prevCursorX = pressX
	a.prevCursorY = pressY
	fmt.Printf("[rachel:drag] titlebar drag SID=%d offset=(%d,%d)\n",
		wi.ta.sid, a.dragOffsetX, a.dragOffsetY)
	startDragComposite(wi.ta.sid)
}

// StartContentDrag sets up DragAgent to forward move/release to the target.
// Used for content-area clicks where the shepherd handles drag semantics.
func (a *DragAgent) StartContentDrag(wi *WindowInteractor, buttonCode uint16) {
	a.target = wi
	a.buttonCode = buttonCode
	a.titlebarDrag = false
	a.isResize = false
}

// StartResizeDrag sets up DragAgent for a window resize drag.
func (a *DragAgent) StartResizeDrag(wi *WindowInteractor, pressX, pressY int32, buttonCode uint16, edge ResizeEdge) {
	a.target = wi
	a.buttonCode = buttonCode
	a.titlebarDrag = false
	a.isResize = false
	a.startDragResize(wi, pressX, pressY, edge)
}

func (a *DragAgent) Deliver(ev *input.InputEvent, target input.Interactor) bool {
	if ev.IsMouseMove() {
		if a.isResize {
			if wi, ok := target.(*WindowInteractor); ok {
				a.dragMoveResize(ev, wi)
			}
			return true
		}
		if a.titlebarDrag {
			if wi, ok := target.(*WindowInteractor); ok {
				// Detect cursor teleportation: if the cursor jumped more
				// than half the screen in one frame, the host cursor
				// likely exited and re-entered the QEMU window. Absorb
				// the jump into the offset instead of moving the window.
				dx := ev.X - a.prevCursorX
				dy := ev.Y - a.prevCursorY
				if dx < 0 {
					dx = -dx
				}
				if dy < 0 {
					dy = -dy
				}
				halfW := int32(displayWidth) / 2
				halfH := int32(displayHeight) / 2
				if dx > halfW || dy > halfH {
					// Teleportation — re-sync offset, don't move window.
					a.dragOffsetX = ev.X - wi.ta.x
					a.dragOffsetY = ev.Y - wi.ta.y
				} else {
					moveWindowTo(wi.ta, ev.X-a.dragOffsetX, ev.Y-a.dragOffsetY)
					// Re-sync offset after clamping so the cursor must
					// travel back through "slack" before the window moves.
					a.dragOffsetX = ev.X - wi.ta.x
					a.dragOffsetY = ev.Y - wi.ta.y
				}
				a.prevCursorX = ev.X
				a.prevCursorY = ev.Y
			}
			return true
		}
		if m, ok := target.(input.Movable); ok {
			return m.Move(ev)
		}
		return false
	}
	if ev.IsMouseButton() && ev.IsRelease() && ev.Code == a.buttonCode {
		if a.isResize {
			if wi, ok := target.(*WindowInteractor); ok {
				a.dragEndResize(wi)
			}
			a.isResize = false
			a.target = nil
			a.buttonCode = 0
			return true
		}
		if a.titlebarDrag {
			endDragComposite()
			// Notify the shepherd of its new screen position.
			if wi, ok := target.(*WindowInteractor); ok {
				msg := wm.EncodeWindowMoved(&wm.WindowMoved{
					AppX: wi.ta.x,
					AppY: wi.ta.y,
				})
				_ = uring.Send(wi.ta.sid, &msg)
			}
			a.titlebarDrag = false
			a.target = nil
			a.buttonCode = 0
			return true
		}
		result := false
		if p, ok := target.(input.Pressable); ok {
			result = p.Release(ev)
		}
		a.target = nil
		a.buttonCode = 0
		return result
	}
	return false
}

// startDragResize begins a window resize operation.
func (a *DragAgent) startDragResize(wi *WindowInteractor, pressX, pressY int32, edge ResizeEdge) {
	ta := wi.ta
	a.isResize = true
	a.resizeEdge = edge
	a.resizeOrigW = ta.appWidth
	a.resizeOrigH = ta.appHeight
	a.resizeOrigX = ta.x

	// Content-driven resize: publish state for the Blit handler.
	dragIsResize = true
	dragResizeEdge = edge
	dragResizeOrigX = ta.x
	dragResizeOrigW = ta.appWidth
	a.resizePressX = pressX
	a.resizePressY = pressY
	a.resizePrevW = ta.appWidth
	a.resizePrevH = ta.appHeight

	// Read min size from app attributes (default 8).
	sidStr := strconv.Itoa(ta.sid)
	a.resizeMinW = 8
	a.resizeMinH = 8
	if v, ok := attr.DerefI64(wm.AppWindowMinWidthURI(sidStr)); ok && v > 0 {
		a.resizeMinW = int32(v)
	}
	if v, ok := attr.DerefI64(wm.AppWindowMinHeightURI(sidStr)); ok && v > 0 {
		a.resizeMinH = int32(v)
	}

	// Compute max possible size based on edge direction and screen bounds.
	dw := int32(displayWidth)
	dh := int32(displayHeight)
	switch edge {
	case EdgeRight:
		a.resizeMaxW = dw - ta.x - int32(borderRight)
		a.resizeMaxH = ta.appHeight // not resizing vertically
	case EdgeLeft:
		a.resizeMaxW = ta.x + ta.appWidth - int32(borderLeft)
		a.resizeMaxH = ta.appHeight
	case EdgeBottom:
		a.resizeMaxW = ta.appWidth // not resizing horizontally
		a.resizeMaxH = dh - ta.y - int32(borderBottom)
	}

	// Raise + focus if needed.
	if !hasFocus(ta.sid) {
		grantFocus(ta.sid)
	}

	// Determine max total buffer size for the oversized backing store.
	maxAppW := a.resizeMaxW
	maxAppH := a.resizeMaxH
	if edge == EdgeBottom {
		maxAppH = a.resizeMaxH
	}
	maxTotalW := maxAppW + int32(borderLeft) + int32(borderRight)
	maxTotalH := maxAppH + int32(borderTop) + int32(borderBottom)
	maxBytes := int(maxTotalW) * 4 * int(maxTotalH)
	maxPages := (maxBytes + 4095) / 4096

	// Allocate oversized backing store.
	oversized, err := mem.AllocPagesSlice(maxPages, mem.PageShared)
	if err != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:resize] oversized alloc failed: %v\n", err))
		a.isResize = false
		return
	}
	a.oversizedBuf = oversized

	// Save old backing store for release at end of resize.
	a.oldBackingStore = ta.backingStore

	// Copy current backing store content into top-left of new buffer.
	oldBS := ta.backingStore
	oldStride := int(ta.bsStride)
	newStride := int(maxTotalW) * 4
	rows := int(ta.bsHeight)
	for row := 0; row < rows; row++ {
		srcOff := row * oldStride
		dstOff := row * newStride
		if srcOff+oldStride <= len(oldBS) && dstOff+oldStride <= len(oversized) {
			copy(oversized[dstOff:dstOff+oldStride], oldBS[srcOff:srcOff+oldStride])
		}
	}

	// Update trackedApp to use oversized buffer. Keep bsWidth/bsHeight at
	// the current logical window size so rachel only blits the visible
	// portion. The stride must match the oversized buffer's row width so
	// pixel addressing is correct.
	ta.backingStore = oversized
	// bsWidth and bsHeight stay at their current values (unchanged).
	ta.bsStride = maxTotalW * 4

	// Re-render decorations for current (not yet resized) dimensions.
	decorTimingReset()
	decorPhaseReset()
	preRenderDecorations(ta)

	// Share oversized buffer with target app.
	bsVA := uintptr(unsafe.Pointer(&oversized[0]))
	clientVA, shareErr := sys.SharePagesWithTarget(ta.sid, bsVA, maxPages)
	if shareErr != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:resize] share pages failed: %v\n", shareErr))
		a.isResize = false
		return
	}

	// Send BackingStoreReady to app with the oversized buffer.
	// TotalWidth/TotalHeight must reflect the FULL oversized buffer so
	// the app allocates a bsSlice large enough for subsequent
	// WindowResized messages that may grow the window up to max size.
	bsr := wm.EncodeBackingStoreReady(&wm.BackingStoreReady{
		BackingStoreAddr: int64(clientVA),
		TotalWidth:       maxTotalW,
		TotalHeight:      maxTotalH,
		TotalStride:      ta.bsStride,
		LeftInset:        int32(borderLeft),
		TopInset:         int32(borderTop),
		AppWidth:         ta.appWidth,
		AppHeight:        ta.appHeight,
		AppX:             ta.x,
		AppY:             ta.y,
	})
	_ = uring.Send(ta.sid, &bsr)

	// Pre-render fixed background for compositing.
	startDragComposite(ta.sid)

	fmt.Printf("[rachel:resize] start edge=%d SID=%d orig=%dx%d max=%dx%d\n",
		edge, ta.sid, a.resizeOrigW, a.resizeOrigH, maxAppW, maxAppH)
}

// dragMoveResize handles mouse movement during a resize drag.
func (a *DragAgent) dragMoveResize(ev *input.InputEvent, wi *WindowInteractor) {
	ta := wi.ta

	newAppW := a.resizeOrigW
	newAppH := a.resizeOrigH
	newX := a.resizeOrigX

	dx := ev.X - a.resizePressX
	dy := ev.Y - a.resizePressY

	switch a.resizeEdge {
	case EdgeRight:
		newAppW = a.resizeOrigW + dx
	case EdgeLeft:
		newAppW = a.resizeOrigW - dx
		newX = a.resizeOrigX + dx
	case EdgeBottom:
		newAppH = a.resizeOrigH + dy
	}

	// Clamp to [min, max].
	if newAppW < a.resizeMinW {
		newAppW = a.resizeMinW
	}
	if newAppW > a.resizeMaxW {
		newAppW = a.resizeMaxW
	}
	if newAppH < a.resizeMinH {
		newAppH = a.resizeMinH
	}
	if newAppH > a.resizeMaxH {
		newAppH = a.resizeMaxH
	}

	// For left edge, re-derive X from width.
	if a.resizeEdge == EdgeLeft {
		newX = a.resizeOrigX + (a.resizeOrigW - newAppW)
	}

	// Skip if size unchanged.
	if newAppW == a.resizePrevW && newAppH == a.resizePrevH {
		return
	}
	a.resizePrevW = newAppW
	a.resizePrevH = newAppH

	// Content-driven resize: do NOT update ta dimensions or re-render
	// decorations here. ta stays at the last-blit visual size. Rachel
	// composites the window at the visual size until the app's Blit
	// confirms the new dimensions. This avoids decoration trails and
	// black gaps during fast drags.

	// Compute the target bsWidth/bsHeight and X for the WindowResized
	// message, but don't store them in ta.
	targetBsW := newAppW + int32(borderLeft) + int32(borderRight)
	targetBsH := newAppH + int32(borderTop) + int32(borderBottom)
	targetX := ta.x
	if a.resizeEdge == EdgeLeft {
		targetX = newX
	}

	// Send WindowResized to app with target dimensions.
	wr := wm.EncodeWindowResized(&wm.WindowResized{
		BackingStoreAddr: 0, // same buffer, no change
		TotalWidth:       targetBsW,
		TotalHeight:      targetBsH,
		TotalStride:      ta.bsStride, // oversized stride
		LeftInset:        int32(borderLeft),
		TopInset:         int32(borderTop),
		AppWidth:         newAppW,
		AppHeight:        newAppH,
		AppX:             targetX,
		AppY:             ta.y,
	})
	_ = uring.Send(ta.sid, &wr)

	// No composite here — the visual update happens when the app's
	// Blit arrives (see Blit handler in main.go).
}

// dragEndResize finalizes a window resize. Allocates a final-size buffer,
// copies content, shares it with the app, frees the oversized buffer.
//
// CRITICAL ORDERING: This must be called only AFTER the drag agent stops
// sending WindowResized messages. The app-side resize coalescing logic
// relies on BackingStoreReady appearing after all WindowResized messages
// for a given drag — if BackingStoreReady arrives interleaved with resizes,
// the app will skip a draw that should have been the final one.
func (a *DragAgent) dragEndResize(wi *WindowInteractor) {
	decorTimingReport()
	decorPhaseReport()
	dragIsResize = false
	ta := wi.ta

	// Content-driven resize: ta may still be at the last-blit size.
	// Update ta to the final drag target dimensions before allocating
	// the final buffer and sending BackingStoreReady.
	ta.appWidth = a.resizePrevW
	ta.appHeight = a.resizePrevH
	ta.bsWidth = a.resizePrevW + int32(borderLeft) + int32(borderRight)
	ta.bsHeight = a.resizePrevH + int32(borderTop) + int32(borderBottom)
	if a.resizeEdge == EdgeLeft {
		ta.x = a.resizeOrigX + (a.resizeOrigW - a.resizePrevW)
	}

	// Re-render decorations at final size.
	preRenderDecorations(ta)

	endDragComposite()

	// Allocate final-size backing store.
	finalW := ta.bsWidth
	finalH := ta.bsHeight
	finalBytes := int(finalW) * 4 * int(finalH)
	finalPages := (finalBytes + 4095) / 4096
	finalBuf, err := mem.AllocPagesSlice(finalPages, mem.PageShared)
	if err != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:resize] final alloc failed: %v\n", err))
		// Keep using oversized buffer — not ideal, but not fatal.
		a.oversizedBuf = nil
		return
	}

	// Copy content from oversized buffer to final buffer.
	oversized := a.oversizedBuf
	oversizedStride := int(ta.bsWidth) * 4 // bsWidth was updated during resize
	// The oversized buffer may have a wider stride from allocation.
	// We compute the actual oversized stride from the max dimensions.
	maxTotalW := int(a.resizeMaxW) + borderLeft + borderRight
	actualOversizedStride := maxTotalW * 4
	finalStride := int(finalW) * 4
	for row := 0; row < int(finalH); row++ {
		srcOff := row * actualOversizedStride
		dstOff := row * finalStride
		copyLen := finalStride
		if srcOff+copyLen <= len(oversized) && dstOff+copyLen <= len(finalBuf) {
			copy(finalBuf[dstOff:dstOff+copyLen], oversized[srcOff:srcOff+copyLen])
		}
	}
	_ = oversizedStride // used above

	// Update trackedApp to use final buffer.
	ta.backingStore = finalBuf
	ta.bsStride = int32(finalStride)

	// Share final buffer with target app.
	bsVA := uintptr(unsafe.Pointer(&finalBuf[0]))
	clientVA, shareErr := sys.SharePagesWithTarget(ta.sid, bsVA, finalPages)
	if shareErr != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:resize] final share failed: %v\n", shareErr))
	} else {
		// Send BackingStoreReady with final buffer.
		bsr := wm.EncodeBackingStoreReady(&wm.BackingStoreReady{
			BackingStoreAddr: int64(clientVA),
			TotalWidth:       finalW,
			TotalHeight:      finalH,
			TotalStride:      int32(finalStride),
			LeftInset:        int32(borderLeft),
			TopInset:         int32(borderTop),
			AppWidth:         ta.appWidth,
			AppHeight:        ta.appHeight,
			AppX:             ta.x,
			AppY:             ta.y,
		})
		_ = uring.Send(ta.sid, &bsr)
	}

	// Free oversized buffer (rachel's side).
	oversizedAddr := uintptr(unsafe.Pointer(&oversized[0]))
	if err := mem.Munmap(oversizedAddr, len(oversized)); err != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:resize] Munmap oversized: %v\n", err))
	}
	a.oversizedBuf = nil

	// Free old original backing store (rachel's side). The app releases
	// its mapping via the BackingStoreStack algorithm when it processes
	// the BackingStoreReady message above.
	if a.oldBackingStore != nil {
		oldAddr := uintptr(unsafe.Pointer(&a.oldBackingStore[0]))
		if err := mem.Munmap(oldAddr, len(a.oldBackingStore)); err != nil {
			sys.UartWriteString(fmt.Sprintf("[rachel:resize] Munmap old BS: %v\n", err))
		}
		a.oldBackingStore = nil
	}

	// Restore normal cursor.
	if standardCursorID >= 0 {
		_ = sys.SetCursor(standardCursorID)
		cursorIsResize = 0
		cursorIsInverse = false
	}

	fmt.Printf("[rachel:resize] end SID=%d final=%dx%d\n",
		ta.sid, ta.appWidth, ta.appHeight)
}

// --- TitlebarDragAgent ---
//
// Positional agent that fires only when the cursor is pressed in a
// window's title bar. Grants focus if needed and starts a titlebar
// drag so the user can focus+move in one gesture. Returns true to
// stop the pick-list loop from reaching windows behind the topmost.

type TitlebarDragAgent struct {
	dragAgent *DragAgent
	keyFwd    *KeyForwardAgent
}

func (a *TitlebarDragAgent) Name() string { return "titlebar-drag" }

func (a *TitlebarDragAgent) Deliver(ev *input.InputEvent, target input.Interactor) bool {
	if !ev.IsMouseButton() || !ev.IsPress() {
		return false
	}
	wi, ok := target.(*WindowInteractor)
	if !ok {
		return false
	}
	if !wi.InTitleBar(ev.X, ev.Y) {
		return false
	}
	// Grant focus if not already focused.
	if !hasFocus(wi.ta.sid) {
		fmt.Printf("[rachel:titlebar] raise+focus SID %d\n", wi.ta.sid)
		grantFocus(wi.ta.sid)
		a.keyFwd.SetFocus(wi)
	}
	a.dragAgent.StartTitlebarDrag(wi, ev.Code, ev.X, ev.Y)
	return true
}

// --- ResizeDragAgent ---
//
// Positional agent that fires only when the cursor is pressed on a
// resize handle (bottom, left, or right edge hot zones). Grants focus
// if needed and starts a resize drag. Returns true to stop the
// pick-list loop.

type ResizeDragAgent struct {
	dragAgent *DragAgent
	keyFwd    *KeyForwardAgent
}

func (a *ResizeDragAgent) Name() string { return "resize-drag" }

func (a *ResizeDragAgent) Deliver(ev *input.InputEvent, target input.Interactor) bool {
	if !ev.IsMouseButton() || !ev.IsPress() {
		return false
	}
	wi, ok := target.(*WindowInteractor)
	if !ok {
		return false
	}
	edge := wi.InResizeEdge(ev.X, ev.Y)
	if edge == EdgeNone {
		return false
	}
	// Grant focus if not already focused.
	if !hasFocus(wi.ta.sid) {
		fmt.Printf("[rachel:resize] raise+focus SID %d\n", wi.ta.sid)
		grantFocus(wi.ta.sid)
		a.keyFwd.SetFocus(wi)
	}
	a.dragAgent.StartResizeDrag(wi, ev.X, ev.Y, ev.Code, edge)
	return true
}

// PressAgent handles mouse button press events on windows. It is the
// catch-all positional agent: if TitlebarDragAgent and ResizeDragAgent
// both declined, this agent grants focus (if needed), establishes a
// content drag, and forwards the press to the shepherd.
type PressAgent struct {
	dragAgent *DragAgent
	keyFwd    *KeyForwardAgent
}

func (a *PressAgent) Name() string { return "press" }

func (a *PressAgent) Deliver(ev *input.InputEvent, target input.Interactor) bool {
	if !ev.IsMouseButton() || !ev.IsPress() {
		return false
	}
	wi, ok := target.(*WindowInteractor)
	if !ok {
		return false
	}

	// Grant focus if not already focused.
	if !hasFocus(wi.ta.sid) {
		fmt.Printf("[rachel:press] raise+focus SID %d\n", wi.ta.sid)
		grantFocus(wi.ta.sid)
		a.keyFwd.SetFocus(wi)
	}

	// Establish content drag so DragAgent forwards move/release to shepherd.
	a.dragAgent.StartContentDrag(wi, ev.Code)

	fmt.Printf("[rachel:input] mouse press %s at (%d,%d)\n",
		buttonName(ev.Code), ev.X, ev.Y)

	return wi.Press(ev)
}

// KeyForwardAgent forwards keyboard events to the focused shepherd window.
type KeyForwardAgent struct {
	target input.Interactor
}

func (a *KeyForwardAgent) Name() string                  { return "key-forward" }
func (a *KeyForwardAgent) FocusTarget() input.Interactor { return a.target }
func (a *KeyForwardAgent) SetFocus(t input.Interactor)   { a.target = t }

func (a *KeyForwardAgent) Deliver(ev *input.InputEvent, target input.Interactor) bool {
	if !ev.IsKeyboard() {
		return false
	}
	kr, ok := target.(input.KeyReceiver)
	if !ok {
		return false
	}
	if ev.IsPress() {
		return kr.KeyDown(ev)
	}
	if ev.IsRelease() {
		return kr.KeyUp(ev)
	}
	return false
}

// --- Dispatcher Construction ---

// wmDispatch holds the dispatcher and references to agents that need
// cross-agent coordination.
type wmDispatch struct {
	dispatcher   *input.InputDispatcher
	dragAgent    *DragAgent
	keyFwd       *KeyForwardAgent
}

// buildDispatcher creates rachel's dispatch pipeline.
//
// Policies in priority order:
//  1. "wm-accel"       (focus)       — AcceleratorAgent: WM keyboard shortcuts
//  2. "drag"           (focus)       — DragAgent: mouse move/release during active drag
//  3. "titlebar-drag"  (positional)  — TitlebarDragAgent: titlebar press → focus + move drag
//  4. "resize-drag"    (positional)  — ResizeDragAgent: resize handle press → focus + resize drag
//  5. "press"          (positional)  — PressAgent: content press → focus + forward to shepherd
//  6. "keyboard"       (focus)       — KeyForwardAgent: forward keyboard to focused app
func buildDispatcher() *wmDispatch {
	wmTarget := &WMInteractor{}

	// Agents
	accelAgent := NewAcceleratorAgent(wmTarget, map[uint16]string{
		KEY_F1: "cycle-focus",
	})
	dragAgent := &DragAgent{}
	keyFwd := &KeyForwardAgent{}
	titlebarAgent := &TitlebarDragAgent{dragAgent: dragAgent, keyFwd: keyFwd}
	resizeAgent := &ResizeDragAgent{dragAgent: dragAgent, keyFwd: keyFwd}
	pressAgent := &PressAgent{dragAgent: dragAgent, keyFwd: keyFwd}

	// Build policies in priority order.
	d := input.NewInputDispatcher()

	// 1. WM accelerators (focus) — consumes hotkeys.
	accelPolicy := input.NewDispatchPolicy("wm-accel", input.PolicyFocus)
	accelPolicy.AddAgent(accelAgent)
	d.AddPolicy(accelPolicy)

	// 2. Drag (focus) — handles move/release during active drag.
	dragPolicy := input.NewDispatchPolicy("drag", input.PolicyFocus)
	dragPolicy.AddAgent(dragAgent)
	d.AddPolicy(dragPolicy)

	// 3. Titlebar drag (positional) — titlebar press starts window move.
	titlebarPolicy := input.NewDispatchPolicy("titlebar-drag", input.PolicyPositional)
	titlebarPolicy.AddAgent(titlebarAgent)
	d.AddPolicy(titlebarPolicy)

	// 4. Resize drag (positional) — resize handle press starts window resize.
	resizePolicy := input.NewDispatchPolicy("resize-drag", input.PolicyPositional)
	resizePolicy.AddAgent(resizeAgent)
	d.AddPolicy(resizePolicy)

	// 5. Press (positional) — content press forwards to shepherd.
	pressPolicy := input.NewDispatchPolicy("press", input.PolicyPositional)
	pressPolicy.AddAgent(pressAgent)
	d.AddPolicy(pressPolicy)

	// 6. Keyboard forward (focus) — sends keys to focused app.
	kbdPolicy := input.NewDispatchPolicy("keyboard", input.PolicyFocus)
	kbdPolicy.AddAgent(keyFwd)
	d.AddPolicy(kbdPolicy)

	// Pick function: iterate z-order front-to-back. Empty list = background click.
	d.PickFn = func(x, y int32) []input.Interactor {
		collector := input.NewPickCollector()
		for _, sid := range zOrder {
			ta, ok := trackedApps[sid]
			if !ok || ta.interactor == nil {
				continue
			}
			if ta.interactor.PickedBy(x, y) {
				collector.Add(ta.interactor)
			}
		}
		return collector.Results()
	}

	return &wmDispatch{
		dispatcher: d,
		dragAgent:  dragAgent,
		keyFwd:     keyFwd,
	}
}
