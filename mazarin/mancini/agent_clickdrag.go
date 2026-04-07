package mancini

// ClickDragAgent handles the three-phase press-drag-release protocol
// for ClickDraggable interactors (scrollbar thumbs, buttons, sliders).
//
// On EvPress, it checks if the picked target implements ClickDraggable
// and calls ClickDragStart. If accepted, the agent captures the target —
// all subsequent EvMove and the final EvRelease are delivered focus-style,
// regardless of cursor position. The agent computes outsideBounds by
// testing the cursor against the target's layout bounds.
type ClickDragAgent struct {
	captured   Interactor  // non-nil during active interaction
	buttonCode uint16      // button that started the interaction
	startEvent *InputEvent // event that initiated the drag (from ClickDragStart)
}

func (a *ClickDragAgent) Name() string { return "clickdrag" }

// FocusTarget returns the captured target, or nil when idle.
func (a *ClickDragAgent) FocusTarget() Interactor { return a.captured }

// SetFocus sets the captured target. Typically only used internally.
func (a *ClickDragAgent) SetFocus(t Interactor) { a.captured = t }

// Deliver routes an event to the appropriate ClickDraggable method.
func (a *ClickDragAgent) Deliver(ev *InputEvent, target Interactor) bool {
	cd, ok := target.(ClickDraggable)
	if !ok {
		return false
	}

	switch ev.Kind {
	case EvPress:
		// Only start if not already captured.
		if a.captured != nil {
			return false
		}
		if cd.ClickDragStart(ev) {
			a.captured = target
			a.buttonCode = ev.Code
			a.startEvent = ev
			return true
		}
		return false

	case EvMove:
		// Only deliver to captured target.
		if a.captured != target {
			return false
		}
		outside := !InsideBounds(target, ev.X, ev.Y)
		return cd.ClickDragMove(ev, a.startEvent, outside)

	case EvRelease:
		// Only deliver to captured target, matching button.
		if a.captured != target || ev.Code != a.buttonCode {
			return false
		}
		outside := !InsideBounds(target, ev.X, ev.Y)
		result := cd.ClickDragEnd(ev, outside)
		a.captured = nil
		a.buttonCode = 0
		a.startEvent = nil
		return result
	}

	return false
}
