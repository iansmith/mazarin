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
	"mazzy/mazarin/uring"
	"mazzy/mazarin/vm"
	"mazzy/shared/wm"
	"os"
	"time"
)

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
	v := w.ta.bounds.Get()
	if v.Type() == vm.TypeTribool {
		return false
	}
	x0, y0, x1, y1 := v.AsRectangle()
	return int64(x) >= x0 && int64(x) < x1 && int64(y) >= y0 && int64(y) < y1
}

func (w *WindowInteractor) SID() int { return w.ta.sid }

func (w *WindowInteractor) Press(ev *input.InputEvent) bool {
	msg := wm.EncodeMousePress(&wm.MousePress{
		X: ev.X, Y: ev.Y, Button: int32(ev.Code), Mods: ev.Mods,
	})
	if err := uring.Send(w.ta.sid, &msg); err != nil {
		fmt.Fprintf(os.Stderr, "[rachel:press] uring.Send to SID %d: %v\n", w.ta.sid, err)
	}
	return true
}

func (w *WindowInteractor) Release(ev *input.InputEvent) bool {
	msg := wm.EncodeMouseRelease(&wm.MouseRelease{
		X: ev.X, Y: ev.Y, Button: int32(ev.Code), Mods: ev.Mods,
	})
	if err := uring.Send(w.ta.sid, &msg); err != nil {
		fmt.Fprintf(os.Stderr, "[rachel:release] uring.Send to SID %d: %v\n", w.ta.sid, err)
	}
	return true
}

func (w *WindowInteractor) Move(ev *input.InputEvent) bool {
	msg := wm.EncodeMouseMove(&wm.MouseMove{
		X: ev.X, Y: ev.Y, Mods: ev.Mods,
	})
	if err := uring.Send(w.ta.sid, &msg); err != nil {
		fmt.Fprintf(os.Stderr, "[rachel:move] uring.Send to SID %d: %v\n", w.ta.sid, err)
	}
	return true
}

func (w *WindowInteractor) KeyDown(ev *input.InputEvent) bool {
	msg := wm.EncodeKeyPress(&wm.KeyPress{Code: ev.Code, Mods: ev.Mods})
	if err := uring.Send(w.ta.sid, &msg); err != nil {
		fmt.Fprintf(os.Stderr, "[rachel:key] uring.Send to SID %d: %v\n", w.ta.sid, err)
	}
	return true
}

func (w *WindowInteractor) KeyUp(ev *input.InputEvent) bool {
	msg := wm.EncodeKeyRelease(&wm.KeyRelease{Code: ev.Code, Mods: ev.Mods})
	if err := uring.Send(w.ta.sid, &msg); err != nil {
		fmt.Fprintf(os.Stderr, "[rachel:key] uring.Send to SID %d: %v\n", w.ta.sid, err)
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
	target     input.Interactor
	buttonCode uint16
}

func (a *DragAgent) Name() string                  { return "drag" }
func (a *DragAgent) FocusTarget() input.Interactor { return a.target }
func (a *DragAgent) SetFocus(t input.Interactor)   { a.target = t }

// StartDrag establishes the drag target and the button code that ends it.
func (a *DragAgent) StartDrag(target input.Interactor, buttonCode uint16) {
	a.target = target
	a.buttonCode = buttonCode
}

func (a *DragAgent) Deliver(ev *input.InputEvent, target input.Interactor) bool {
	if ev.IsMouseMove() {
		if m, ok := target.(input.Movable); ok {
			return m.Move(ev)
		}
		return false
	}
	if ev.IsMouseButton() && ev.IsRelease() && ev.Code == a.buttonCode {
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
		// Unfocused window: begin click detection.
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
		fmt.Printf("[rachel:focus] single-click → raise+focus SID %d\n", a.target.ta.sid)
		grantFocus(a.target.ta.sid)
		a.keyFwd.SetFocus(a.target)
	}
	a.reset()
}

func (a *FocusChangeAgent) commitDoubleClick() {
	if a.target != nil {
		fmt.Printf("[rachel:focus] double-click → focus (no raise) SID %d\n", a.target.ta.sid)
		grantFocusNoRaise(a.target.ta.sid)
		a.keyFwd.SetFocus(a.target)
	}
	a.reset()
}

func (a *FocusChangeAgent) commitTripleClick() {
	if a.target != nil {
		fmt.Printf("[rachel:focus] triple-click SID %d (no action yet)\n", a.target.ta.sid)
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
	a.dragAgent.StartDrag(target, ev.Code)

	fmt.Printf("[rachel:input] mouse press %s at (%d,%d)\n",
		buttonName(ev.Code), ev.X, ev.Y)

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
