package mancini

import (
	"fmt"
	"time"
)

// ClickAgent implements a FSM that recognizes single, double, and triple
// clicks. It dispatches to Clickable, DoubleClickable, and TripleClickable
// protocol interfaces on the picked target.
//
// Single clicks are committed immediately on release for responsiveness.
// If the target also implements DoubleClickable, the agent continues
// listening — a second click within ClickTimeout fires DoubleClick,
// which overrides the already-committed single click. The interactor
// is responsible for handling the override (typically the DoubleClick
// just sets new state, overwriting what Click did).
//
// If the target only implements Clickable (not DoubleClickable), the
// agent resets immediately after committing — no timeout, zero delay.
type ClickAgent struct {
	state    clickState
	target   Interactor
	pressX   int64
	pressY   int64
	deadline time.Time

	// ClickTimeout is the maximum delay between clicks for multi-click
	// recognition. Default: 250ms.
	ClickTimeout time.Duration

	// ClickRadius is the maximum cursor movement between clicks.
	// Default: 5 pixels.
	ClickRadius int64
}

type clickState int

const (
	ckIdle clickState = iota
	ckGotPress
	ckWaitSecond     // single click committed, waiting for possible double
	ckGotSecondPress // second press arrived, waiting for release
	ckWaitThird      // double click committed, waiting for possible triple
	ckGotThirdPress  // third press arrived, waiting for release
)

// NewClickAgent creates a ClickAgent with default timeout and radius.
func NewClickAgent() *ClickAgent {
	return &ClickAgent{
		ClickTimeout: 250 * time.Millisecond,
		ClickRadius:  5,
	}
}

func (a *ClickAgent) Name() string { return "click" }

// Deliver processes a raw press/release event through the click FSM.
func (a *ClickAgent) Deliver(ev *InputEvent, target Interactor) bool {
	// Only handle mouse button events.
	if ev.Kind != EvPress && ev.Kind != EvRelease {
		return false
	}

	// Skip targets that implement ClickDraggable — the ClickDragAgent
	// handles those.
	if _, ok := target.(ClickDraggable); ok {
		return false
	}

	// Skip targets that don't implement any click protocol.
	_, isClickable := target.(Clickable)
	_, isDoubleClickable := target.(DoubleClickable)
	_, isTripleClickable := target.(TripleClickable)
	if !isClickable && !isDoubleClickable && !isTripleClickable {
		return false
	}

	switch a.state {
	case ckIdle:
		if ev.Kind == EvPress {
			a.state = ckGotPress
			a.target = target
			a.pressX = ev.X
			a.pressY = ev.Y
			return true
		}

	case ckGotPress:
		if ev.Kind == EvRelease {
			// Commit single click immediately.
			a.fireSingleClick(ev)

			// If target supports double-click, wait for a second press.
			if _, ok := a.target.(DoubleClickable); ok {
				a.state = ckWaitSecond
				a.deadline = time.Now().Add(a.ClickTimeout)
			} else {
				// No double-click protocol — done, zero delay.
				a.reset()
			}
			return true
		}

	case ckWaitSecond:
		if ev.Kind == EvPress {
			if !a.nearPress(ev.X, ev.Y) || target != a.target {
				// Too far or different target — start fresh.
				a.state = ckGotPress
				a.target = target
				a.pressX = ev.X
				a.pressY = ev.Y
				return true
			}
			a.state = ckGotSecondPress
			return true
		}

	case ckGotSecondPress:
		if ev.Kind == EvRelease {
			// Commit double click immediately (overrides single click).
			a.fireDoubleClick(ev)

			// If target supports triple-click, wait for a third press.
			if _, ok := a.target.(TripleClickable); ok {
				a.state = ckWaitThird
				a.deadline = time.Now().Add(a.ClickTimeout)
			} else {
				a.reset()
			}
			return true
		}

	case ckWaitThird:
		if ev.Kind == EvPress {
			if !a.nearPress(ev.X, ev.Y) || target != a.target {
				a.state = ckGotPress
				a.target = target
				a.pressX = ev.X
				a.pressY = ev.Y
				return true
			}
			a.state = ckGotThirdPress
			return true
		}

	case ckGotThirdPress:
		if ev.Kind == EvRelease {
			a.fireTripleClick(ev)
			a.reset()
			return true
		}
	}

	return false
}

// CheckTimer cleans up stale state whose deadline has expired.
// With immediate commit, clicks are already fired — the timer just
// resets the FSM so it stops waiting for multi-click follow-ups.
// Returns true if state was cleaned up (caller should redraw).
func (a *ClickAgent) CheckTimer() bool {
	if a.target == nil {
		return false
	}
	now := time.Now()
	switch a.state {
	case ckWaitSecond:
		if now.After(a.deadline) {
			// Single click already fired. Just clean up.
			a.reset()
			return true
		}
	case ckWaitThird:
		if now.After(a.deadline) {
			// Double click already fired. Just clean up.
			a.reset()
			return true
		}
	}
	return false
}

func (a *ClickAgent) fireSingleClick(ev *InputEvent) {
	if c, ok := a.target.(Clickable); ok {
		useEv := a.makeEv(ev)
		fmt.Printf("[click-agent] Click on %T\n", a.target)
		c.Click(useEv)
	}
}

func (a *ClickAgent) fireDoubleClick(ev *InputEvent) {
	if c, ok := a.target.(DoubleClickable); ok {
		useEv := a.makeEv(ev)
		fmt.Printf("[click-agent] DoubleClick on %T\n", a.target)
		c.DoubleClick(useEv)
	}
}

func (a *ClickAgent) fireTripleClick(ev *InputEvent) {
	if c, ok := a.target.(TripleClickable); ok {
		useEv := a.makeEv(ev)
		fmt.Printf("[click-agent] TripleClick on %T\n", a.target)
		c.TripleClick(useEv)
	}
}

func (a *ClickAgent) makeEv(ev *InputEvent) *InputEvent {
	if ev != nil {
		return ev
	}
	return &InputEvent{Kind: EvPress, X: a.pressX, Y: a.pressY}
}

func (a *ClickAgent) reset() {
	a.state = ckIdle
	a.target = nil
}

func (a *ClickAgent) nearPress(x, y int64) bool {
	dx := x - a.pressX
	dy := y - a.pressY
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx <= a.ClickRadius && dy <= a.ClickRadius
}
