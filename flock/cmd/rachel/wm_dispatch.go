// wm_dispatch.go implements rachel's subArctic-style input dispatch pipeline.
//
// Rachel's dispatch pipeline has four policies in priority order:
//
//  1. "wm-accel" (focus) — AcceleratorAgent: WM keyboard shortcuts (F1 cycle, etc.)
//  2. "drag" (focus) — DragAgent: mouse move/release during active drag
//  3. "mouse" (positional) — PressAgent: mouse button press, click-to-focus
//  4. "keyboard" (focus) — KeyForwardAgent: forward keyboard to focused app
//
// Interactors:
//   - WMInteractor: rachel herself (receives accelerator actions)
//   - WindowInteractor: a tracked shepherd window (forwards events via uring IPC)
//   - DesktopInteractor: catches clicks on empty space (defocuses current window)
package main

import (
	"fmt"
	"mazzy/mazarin/input"
	"mazzy/mazarin/uring"
	"mazzy/mazarin/vm"
	"mazzy/shared/wm"
	"os"
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

// DesktopInteractor catches clicks that miss all windows.
// Press defocuses the current window; release is a no-op.
type DesktopInteractor struct{}

func (d *DesktopInteractor) PickedBy(x, y int32) bool { return true }

func (d *DesktopInteractor) Press(ev *input.InputEvent) bool {
	if focusedSID >= 0 {
		if _, ok := trackedApps[focusedSID]; ok {
			msg := wm.EncodeYouLostFocus()
			_ = uring.Send(focusedSID, &msg)
		}
		focusedSID = -1
	}
	return true
}

func (d *DesktopInteractor) Release(ev *input.InputEvent) bool {
	return true // consumed but no action
}

// --- Dispatch Agents ---

// AcceleratorAgent handles WM keyboard shortcuts. It checks key press
// events against an accelerator table and delivers matched actions to
// the Acceleratable target (WMInteractor). Consumes both press and
// subsequent release of matched keys.
type AcceleratorAgent struct {
	target   input.Interactor
	table    map[uint16]string  // keycode → action name
	consumed map[uint16]bool    // keys consumed on press, waiting for release
}

func NewAcceleratorAgent(target input.Interactor, table map[uint16]string) *AcceleratorAgent {
	return &AcceleratorAgent{
		target:   target,
		table:    table,
		consumed: make(map[uint16]bool),
	}
}

func (a *AcceleratorAgent) Name() string                 { return "accel" }
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

func (a *DragAgent) Name() string                 { return "drag" }
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

// PressAgent handles mouse button press events positionally. It picks
// the window under the cursor, manages click-to-focus, establishes the
// drag target, and delivers the press to the target.
type PressAgent struct {
	dragAgent *DragAgent
	keyFwd    *KeyForwardAgent
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

	// Click-to-focus: if the target is a window and not already focused,
	// switch keyboard focus to it.
	if wi, ok := target.(*WindowInteractor); ok {
		if wi.ta.sid != focusedSID {
			changeFocus(wi.ta.sid)
			// Update the keyboard forward agent's focus target.
			a.keyFwd.SetFocus(wi)
		}
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

func (a *KeyForwardAgent) Name() string                 { return "key-forward" }
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
// cross-agent coordination (e.g., PressAgent setting DragAgent's focus).
type wmDispatch struct {
	dispatcher *input.InputDispatcher
	dragAgent  *DragAgent
	keyFwd     *KeyForwardAgent
	desktop    *DesktopInteractor
}

// buildDispatcher creates rachel's four-policy dispatch pipeline.
func buildDispatcher() *wmDispatch {
	wmTarget := &WMInteractor{}
	desktop := &DesktopInteractor{}

	// Agents
	accelAgent := NewAcceleratorAgent(wmTarget, map[uint16]string{
		KEY_F1: "cycle-focus",
	})
	dragAgent := &DragAgent{}
	keyFwd := &KeyForwardAgent{}
	pressAgent := &PressAgent{dragAgent: dragAgent, keyFwd: keyFwd}

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

	// 3. Mouse press (positional) — picks target, click-to-focus.
	mousePolicy := input.NewDispatchPolicy("mouse", input.PolicyPositional)
	mousePolicy.AddAgent(pressAgent)
	d.AddPolicy(mousePolicy)

	// 4. Keyboard forward (focus) — sends keys to focused app.
	kbdPolicy := input.NewDispatchPolicy("keyboard", input.PolicyFocus)
	kbdPolicy.AddAgent(keyFwd)
	d.AddPolicy(kbdPolicy)

	// Pick function: iterate z-order front-to-back, always include desktop last.
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
		collector.Add(desktop) // desktop catches everything
		return collector.Results()
	}

	return &wmDispatch{
		dispatcher: d,
		dragAgent:  dragAgent,
		keyFwd:     keyFwd,
		desktop:    desktop,
	}
}
