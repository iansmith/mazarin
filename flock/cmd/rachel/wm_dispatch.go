// wm_dispatch.go implements rachel's subArctic-style input dispatch pipeline.
//
// Rachel's dispatch pipeline has five policies in priority order:
//
//  1. "wm-accel"      (focus)       — AcceleratorAgent: WM keyboard shortcuts
//  2. "drag"          (focus)       — DragAgent: mouse move/release during active drag
//  3. "focus-change"  (positional)  — FocusChangeAgent: single/double/triple click FSM
//  4. "mouse"         (positional)  — PressAgent: forward press to focused window
//  5. "keyboard"      (focus)       — KeyForwardAgent: forward keyboard to focused app
//
// Interactors:
//   - WMInteractor: rachel herself (receives accelerator actions)
//   - WindowInteractor: a tracked shepherd window (forwards events via uring IPC)
package main

import (
	"fmt"
	"mazzy/mazarin/input"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/wm"
	"time"
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
		sys.UartWriteString(fmt.Sprintf(
			"[keymap:stat] calls=%d chars=%d actions=%d MISS=%d avg=%dµs\n",
			kmDiagMapCalls, kmDiagChars, kmDiagActions, kmDiagUntranslated, avgUs))
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

func (w *WindowInteractor) Press(ev *input.InputEvent) bool {
	msg := wm.EncodeMousePress(&wm.MousePress{
		X: ev.X, Y: ev.Y, Button: int32(ev.Code), Mods: ev.Mods,
	})
	if err := uring.Send(w.ta.sid, &msg); err != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:press] uring.Send to SID %d: %v\n", w.ta.sid, err))
	}
	return true
}

func (w *WindowInteractor) Release(ev *input.InputEvent) bool {
	msg := wm.EncodeMouseRelease(&wm.MouseRelease{
		X: ev.X, Y: ev.Y, Button: int32(ev.Code), Mods: ev.Mods,
	})
	if err := uring.Send(w.ta.sid, &msg); err != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:release] uring.Send to SID %d: %v\n", w.ta.sid, err))
	}
	return true
}

func (w *WindowInteractor) Move(ev *input.InputEvent) bool {
	msg := wm.EncodeMouseMove(&wm.MouseMove{
		X: ev.X, Y: ev.Y, Mods: ev.Mods,
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
}

func (a *DragAgent) Name() string                  { return "drag" }
func (a *DragAgent) FocusTarget() input.Interactor { return a.target }
func (a *DragAgent) SetFocus(t input.Interactor)   { a.target = t }

// StartDrag establishes the drag target and the button code that ends it.
// If the press is in the title bar, the drag moves the window directly.
func (a *DragAgent) StartDrag(target input.Interactor, buttonCode uint16, pressX, pressY int32) {
	a.target = target
	a.buttonCode = buttonCode
	a.titlebarDrag = false
	if wi, ok := target.(*WindowInteractor); ok && wi.InTitleBar(pressX, pressY) {
		a.titlebarDrag = true
		a.dragOffsetX = pressX - wi.ta.x
		a.dragOffsetY = pressY - wi.ta.y
		a.prevCursorX = pressX
		a.prevCursorY = pressY
		sys.UartWriteString(fmt.Sprintf("[rachel:drag] titlebar drag SID=%d offset=(%d,%d)\n",
			wi.ta.sid, a.dragOffsetX, a.dragOffsetY))
		startDragComposite(wi.ta.sid)
	}
}

func (a *DragAgent) Deliver(ev *input.InputEvent, target input.Interactor) bool {
	if ev.IsMouseMove() {
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
		if a.titlebarDrag {
			endDragComposite()
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

// --- FocusChangeAgent ---
//
// Positional agent implementing a 5-state FSM that distinguishes single-click,
// double-click, and triple-click on unfocused windows. Only consumes clicks
// that are about focus management; clicks on already-focused windows pass
// through to PressAgent.
//
// Actions:
//   - Single click (unfocused): raise to front + grant focus
//   - Double click (unfocused): grant focus, no raise
//   - Triple click (unfocused): reserved (no action yet)
//   - Click on empty space: revoke all focus

type focusState int

const (
	fcIdle focusState = iota
	fcGotPress
	fcWaitSecond
	fcGotSecondPress
	fcWaitThird
	fcGotThirdPress
)

// doubleClickTimeout is the maximum time between clicks for multi-click
// detection. Checked by CheckTimer after each input batch.
const doubleClickTimeout = 250 * time.Millisecond

// clickRadius is the maximum distance (in pixels) the cursor can move
// between press and release for the gesture to count as a click.
const clickRadius int32 = 5

type FocusChangeAgent struct {
	state    focusState
	target   *WindowInteractor // the unfocused window being clicked
	pressX   int32             // position of the initial press
	pressY   int32
	deadline time.Time // when the current wait state expires
	keyFwd   *KeyForwardAgent
}

func (a *FocusChangeAgent) Name() string { return "focus-change" }

// Deliver is called by the positional dispatch policy for each picked
// interactor under the cursor.
func (a *FocusChangeAgent) Deliver(ev *input.InputEvent, target input.Interactor) bool {
	// Only handle mouse button events.
	if !ev.IsMouseButton() {
		return false
	}

	wi, isWindow := target.(*WindowInteractor)

	switch a.state {
	case fcIdle:
		if !ev.IsPress() {
			return false
		}
		// Click on empty space handled by the pick list being empty —
		// but if we reach here with a non-window target, ignore it.
		if !isWindow {
			return false
		}
		// Already focused — let it pass through to PressAgent.
		if hasFocus(wi.ta.sid) {
			return false
		}
		// Titlebar click on unfocused window: immediately grant focus
		// and let PressAgent start a drag so the user can focus+move
		// in one gesture.
		if wi.InTitleBar(ev.X, ev.Y) {
			sys.UartWriteString(fmt.Sprintf("[rachel:focus] titlebar-click → raise+focus SID %d\n", wi.ta.sid))
			grantFocus(wi.ta.sid)
			a.keyFwd.SetFocus(wi)
			return false // pass through to PressAgent for drag
		}
		// Client area click on unfocused window: begin click detection FSM.
		a.state = fcGotPress
		a.target = wi
		a.pressX = ev.X
		a.pressY = ev.Y
		return true // consume the press

	case fcGotPress:
		if !ev.IsRelease() {
			return true // consume stray events while waiting for release
		}
		if !a.nearPress(ev.X, ev.Y) {
			// Moved too far — commit as single click immediately.
			a.commitSingleClick()
			return true
		}
		// Clean release near the press point. Wait for possible second click.
		a.state = fcWaitSecond
		a.deadline = time.Now().Add(doubleClickTimeout)
		return true

	case fcWaitSecond:
		if !ev.IsPress() {
			return false
		}
		if isWindow && wi == a.target && a.nearPress(ev.X, ev.Y) {
			// Second press on same target within area.
			a.state = fcGotSecondPress
			return true
		}
		// Press on a different target or too far away.
		a.commitSingleClick()
		// Don't consume — let the new press be re-evaluated from Idle
		// on the next dispatch cycle.
		return false

	case fcGotSecondPress:
		if !ev.IsRelease() {
			return true
		}
		if !a.nearPress(ev.X, ev.Y) {
			// Moved too far — fall back to single click.
			a.commitSingleClick()
			return true
		}
		// Clean second release. Wait for possible third click.
		a.state = fcWaitThird
		a.deadline = time.Now().Add(doubleClickTimeout)
		return true

	case fcWaitThird:
		if !ev.IsPress() {
			return false
		}
		if isWindow && wi == a.target && a.nearPress(ev.X, ev.Y) {
			a.state = fcGotThirdPress
			return true
		}
		// Different target or too far — commit as double click.
		a.commitDoubleClick()
		return false

	case fcGotThirdPress:
		if !ev.IsRelease() {
			return true
		}
		if !a.nearPress(ev.X, ev.Y) {
			// Moved too far — fall back to double click.
			a.commitDoubleClick()
			return true
		}
		// Triple click completed.
		a.commitTripleClick()
		return true
	}
	return false
}

// CheckTimer should be called after each input batch to commit pending
// clicks whose deadline has expired. Returns true if a timer fired.
func (a *FocusChangeAgent) CheckTimer() bool {
	switch a.state {
	case fcWaitSecond:
		if time.Now().After(a.deadline) {
			a.commitSingleClick()
			return true
		}
	case fcWaitThird:
		if time.Now().After(a.deadline) {
			a.commitDoubleClick()
			return true
		}
	}
	return false
}

func (a *FocusChangeAgent) commitSingleClick() {
	if a.target != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:focus] single-click → raise+focus SID %d\n", a.target.ta.sid))
		grantFocus(a.target.ta.sid)
		a.keyFwd.SetFocus(a.target)
	}
	a.reset()
}

func (a *FocusChangeAgent) commitDoubleClick() {
	if a.target != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:focus] double-click → focus (no raise) SID %d\n", a.target.ta.sid))
		grantFocusNoRaise(a.target.ta.sid)
		a.keyFwd.SetFocus(a.target)
	}
	a.reset()
}

func (a *FocusChangeAgent) commitTripleClick() {
	if a.target != nil {
		sys.UartWriteString(fmt.Sprintf("[rachel:focus] triple-click SID %d (no action yet)\n", a.target.ta.sid))
	}
	a.reset()
}

func (a *FocusChangeAgent) reset() {
	a.state = fcIdle
	a.target = nil
	a.deadline = time.Time{}
}

func (a *FocusChangeAgent) nearPress(x, y int32) bool {
	dx := x - a.pressX
	dy := y - a.pressY
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx <= clickRadius && dy <= clickRadius
}

// PressAgent handles mouse button press events on already-focused windows.
// It forwards the press to the target and establishes a drag.
type PressAgent struct {
	dragAgent *DragAgent
}

func (a *PressAgent) Name() string { return "press" }

func (a *PressAgent) Deliver(ev *input.InputEvent, target input.Interactor) bool {
	if !ev.IsMouseButton() || !ev.IsPress() {
		return false
	}
	p, ok := target.(input.Pressable)
	if !ok {
		return false
	}

	// Establish drag focus so DragAgent handles subsequent move/release.
	// Pass press coordinates so DragAgent can detect titlebar drags.
	a.dragAgent.StartDrag(target, ev.Code, ev.X, ev.Y)

	// If this is a titlebar drag, don't forward the press to the shepherd —
	// the shepherd doesn't need to know about WM-level window moves.
	if a.dragAgent.titlebarDrag {
		return true
	}

	sys.UartWriteString(fmt.Sprintf("[rachel:input] mouse press %s at (%d,%d)\n",
		buttonName(ev.Code), ev.X, ev.Y))

	return p.Press(ev)
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
	dispatcher       *input.InputDispatcher
	dragAgent        *DragAgent
	keyFwd           *KeyForwardAgent
	focusChangeAgent *FocusChangeAgent
}

// buildDispatcher creates rachel's five-policy dispatch pipeline.
func buildDispatcher() *wmDispatch {
	wmTarget := &WMInteractor{}

	// Agents
	accelAgent := NewAcceleratorAgent(wmTarget, map[uint16]string{
		KEY_F1: "cycle-focus",
	})
	dragAgent := &DragAgent{}
	keyFwd := &KeyForwardAgent{}
	focusAgent := &FocusChangeAgent{keyFwd: keyFwd}
	pressAgent := &PressAgent{dragAgent: dragAgent}

	// Build policies in priority order.
	d := input.NewInputDispatcher()

	// 1. WM accelerators (focus) — highest priority, consumes hotkeys.
	accelPolicy := input.NewDispatchPolicy("wm-accel", input.PolicyFocus)
	accelPolicy.AddAgent(accelAgent)
	d.AddPolicy(accelPolicy)

	// 2. Drag (focus) — handles move/release during active drag.
	dragPolicy := input.NewDispatchPolicy("drag", input.PolicyFocus)
	dragPolicy.AddAgent(dragAgent)
	d.AddPolicy(dragPolicy)

	// 3. Focus change (positional) — single/double/triple click on unfocused windows.
	focusPolicy := input.NewDispatchPolicy("focus-change", input.PolicyPositional)
	focusPolicy.AddAgent(focusAgent)
	d.AddPolicy(focusPolicy)

	// 4. Mouse press (positional) — forward press to already-focused window.
	mousePolicy := input.NewDispatchPolicy("mouse", input.PolicyPositional)
	mousePolicy.AddAgent(pressAgent)
	d.AddPolicy(mousePolicy)

	// 5. Keyboard forward (focus) — sends keys to focused app.
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
		dispatcher:       d,
		dragAgent:        dragAgent,
		keyFwd:           keyFwd,
		focusChangeAgent: focusAgent,
	}
}
