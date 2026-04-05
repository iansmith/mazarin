// dispatch.go implements the Henry-Hudson (subArctic) input dispatch model
// for shepherd applications. This is the in-app dispatch pipeline — rachel
// handles WM-level dispatch (focus granting, window selection), then forwards
// typed events to the focused shepherd. The shepherd's AppDispatcher routes
// those events to the correct mancini interactors.
//
// Three layers:
//   1. AppPolicy — ordered routing strategies (focus or positional)
//   2. Agent — each policy contains agents that translate raw events into
//      semantic protocol callbacks via FSMs
//   3. Protocol Interfaces — Clickable, ClickDraggable, KeyReceivable, etc.
//
// Reference: Hudson, Mankoff, Smith. "Extensible Input Handling in the
// subArctic Toolkit." CHI 2005.
package mancini

import (
	"fmt"

	"mazzy/shared/wm"
)

// --- InputEvent ---

// EventKind identifies the type of input event.
type EventKind uint8

const (
	EvPress   EventKind = iota // mouse button press
	EvRelease                  // mouse button release
	EvMove                     // mouse move
	EvKeyDown                  // key press
	EvKeyUp                    // key release
)

// InputEvent is a local-coordinate input event. Coordinates are relative
// to the AppWindow origin — the AppDispatcher translates from screen
// coordinates before dispatching.
type InputEvent struct {
	Kind   EventKind
	Code   uint16 // evdev keycode or mouse button code
	X, Y   int64  // local coordinates
	Mods   uint64 // modifier bitmask (hid.ModXxx)
	Char   rune   // translated character (0 if non-printable); set by rachel's KeyMapper
	Action string // action name ("backspace", "enter", etc.); set by rachel's KeyMapper
}

// BtnLeft is the evdev boundary between keyboard and mouse button codes.
const BtnLeft = 0x110

func (e *InputEvent) IsMouseButton() bool { return e.Kind == EvPress || e.Kind == EvRelease }
func (e *InputEvent) IsMouseMove() bool   { return e.Kind == EvMove }
func (e *InputEvent) IsKeyboard() bool    { return e.Kind == EvKeyDown || e.Kind == EvKeyUp }

// --- Protocol Interfaces ---

// Clickable receives single-click callbacks.
type Clickable interface {
	Click(ev *InputEvent) bool
}

// DoubleClickable receives double-click callbacks.
type DoubleClickable interface {
	DoubleClick(ev *InputEvent) bool
}

// TripleClickable receives triple-click callbacks.
type TripleClickable interface {
	TripleClick(ev *InputEvent) bool
}

// ClickDraggable is the three-phase press-drag-release protocol for
// scrollbar thumbs, buttons, sliders, and any interactor where the user
// presses, optionally moves, and releases.
//
// The agent manages the FSM: on press it calls ClickDragStart. The agent
// then captures mouse movement (focus-style, regardless of cursor position)
// and delivers ClickDragMove with outsideBounds indicating whether the
// cursor has left the interactor's bounds. On release, ClickDragEnd is
// called with the same outsideBounds signal.
//
// The outsideBounds parameter drives visual feedback: a button shows
// "armed" when inside, "disarmed" when outside. A scrollbar thumb
// continues tracking regardless. The interactor decides what outsideBounds
// means for its own semantics.
type ClickDraggable interface {
	ClickDragStart(ev *InputEvent) bool
	ClickDragMove(ev *InputEvent, outsideBounds bool) bool
	ClickDragEnd(ev *InputEvent, outsideBounds bool) bool
}

// KeyReceivable handles keyboard events.
type KeyReceivable interface {
	KeyDown(ev *InputEvent) bool
	KeyUp(ev *InputEvent) bool
}

// --- Agent ---

// Agent translates raw InputEvents into semantic protocol callbacks.
// Each agent targets a specific protocol interface and maintains its
// own state machine.
type Agent interface {
	Name() string
	Deliver(ev *InputEvent, target Interactor) bool
}

// FocusAgent is an Agent that maintains a designated focus target.
// Used with AppPolicyFocus policies. When FocusTarget returns non-nil,
// events are delivered to that target regardless of cursor position.
type FocusAgent interface {
	Agent
	FocusTarget() Interactor
	SetFocus(target Interactor)
}

// --- AppPolicy ---

// AppPolicyKind determines how a policy routes events.
type AppPolicyKind int

const (
	// AppPolicyFocus delivers to the agent's focus target.
	AppPolicyFocus AppPolicyKind = iota
	// AppPolicyPositional delivers to interactors under the cursor via PickFn.
	AppPolicyPositional
	// AppPolicyMonitorFocus delivers to focus but never consumes.
	AppPolicyMonitorFocus
	// AppPolicyMonitorPositional delivers positionally but never consumes.
	AppPolicyMonitorPositional
)

// AppPolicy is a named routing strategy with an ordered agent list.
type AppPolicy struct {
	name   string
	kind   AppPolicyKind
	agents []Agent
}

// NewAppPolicy creates a policy with the given name and routing kind.
func NewAppPolicy(name string, kind AppPolicyKind) *AppPolicy {
	return &AppPolicy{name: name, kind: kind}
}

// Name returns the policy's name.
func (p *AppPolicy) Name() string { return p.name }

// Kind returns the policy's routing kind.
func (p *AppPolicy) Kind() AppPolicyKind { return p.kind }

// AddAgent appends an agent to this policy's agent list.
func (p *AppPolicy) AddAgent(a Agent) { p.agents = append(p.agents, a) }

// --- AppDispatcher ---

// AppDispatcher is the top-level dispatch pipeline for a shepherd app.
// It converts WM messages to local-coordinate InputEvents and routes
// them through ordered policies.
type AppDispatcher struct {
	policies []*AppPolicy
	root     Interactor

	// PickFn returns interactors under (x, y) in local coords,
	// front-to-back. Default: PickTree.
	PickFn func(x, y int64) []Interactor

	// OriginX, OriginY are the AppWindow's screen-space origin.
	// Set from BackingStoreReady inset values. Used to translate
	// screen coords to local coords.
	OriginX, OriginY int64

	// Debug enables tracing of dispatch decisions to UART.
	Debug bool
	// Tag is a short prefix for debug output (e.g. "clocks").
	Tag string
}

// NewAppDispatcher creates a dispatcher rooted at the given interactor.
func NewAppDispatcher(root Interactor) *AppDispatcher {
	d := &AppDispatcher{root: root}
	d.PickFn = func(x, y int64) []Interactor {
		return PickTree(x, y, root)
	}
	return d
}

// AddPolicy appends a policy to the dispatch pipeline.
func (d *AppDispatcher) AddPolicy(p *AppPolicy) {
	d.policies = append(d.policies, p)
}

// DispatchWM is the entry point called from the shepherd's message loop.
// It accepts a raw WM message (any from wm.DecodeShepherdNotify),
// converts to InputEvent with local coordinates, and routes through
// the policy pipeline. Returns true if consumed.
func (d *AppDispatcher) DispatchWM(wmMsg any) bool {
	ev := d.convertWMEvent(wmMsg)
	if ev == nil {
		return false
	}
	return d.dispatch(ev)
}

func (d *AppDispatcher) convertWMEvent(wmMsg any) *InputEvent {
	switch m := wmMsg.(type) {
	case wm.MousePress:
		return &InputEvent{
			Kind: EvPress,
			Code: uint16(m.Button),
			X:    int64(m.X) - d.OriginX,
			Y:    int64(m.Y) - d.OriginY,
			Mods: m.Mods,
		}
	case wm.MouseRelease:
		return &InputEvent{
			Kind: EvRelease,
			Code: uint16(m.Button),
			X:    int64(m.X) - d.OriginX,
			Y:    int64(m.Y) - d.OriginY,
			Mods: m.Mods,
		}
	case wm.MouseMove:
		return &InputEvent{
			Kind: EvMove,
			X:    int64(m.X) - d.OriginX,
			Y:    int64(m.Y) - d.OriginY,
			Mods: m.Mods,
		}
	case wm.KeyPress:
		return &InputEvent{
			Kind:   EvKeyDown,
			Code:   m.Code,
			Mods:   m.Mods,
			Char:   rune(m.Char),
			Action: wm.ActionName(m.Action),
		}
	case wm.KeyRelease:
		return &InputEvent{
			Kind:   EvKeyUp,
			Code:   m.Code,
			Mods:   m.Mods,
			Char:   rune(m.Char),
			Action: wm.ActionName(m.Action),
		}
	}
	return nil
}

func (d *AppDispatcher) dispatch(ev *InputEvent) bool {
	if d.Debug {
		kind := "?"
		switch ev.Kind {
		case EvPress:
			kind = "Press"
		case EvRelease:
			kind = "Release"
		case EvMove:
			kind = "Move"
		case EvKeyDown:
			kind = "KeyDown"
		case EvKeyUp:
			kind = "KeyUp"
		}
		fmt.Printf("[%s:dispatch] %s code=%d local=(%d,%d)\n", d.Tag, kind, ev.Code, ev.X, ev.Y)
	}

	var pickList []Interactor
	for _, p := range d.policies {
		if d.dispatchPolicy(ev, p, &pickList) {
			if d.Debug {
				fmt.Printf("[%s:dispatch] consumed by policy %q\n", d.Tag, p.name)
			}
			return true
		}
	}
	if d.Debug {
		fmt.Printf("[%s:dispatch] not consumed\n", d.Tag)
	}
	return false
}

func (d *AppDispatcher) dispatchPolicy(ev *InputEvent, p *AppPolicy, pickList *[]Interactor) bool {
	isMonitor := p.kind == AppPolicyMonitorFocus || p.kind == AppPolicyMonitorPositional
	isFocus := p.kind == AppPolicyFocus || p.kind == AppPolicyMonitorFocus

	if isFocus {
		for _, agent := range p.agents {
			fa, ok := agent.(FocusAgent)
			if !ok {
				continue
			}
			target := fa.FocusTarget()
			if target == nil {
				continue
			}
			if agent.Deliver(ev, target) && !isMonitor {
				return true
			}
		}
		return false
	}

	// Positional: lazy-compute pick list.
	if *pickList == nil {
		SetPickDebug(d.Debug && (ev.Kind == EvPress || ev.Kind == EvRelease))
		*pickList = d.PickFn(ev.X, ev.Y)
		SetPickDebug(false)
		if d.Debug && (ev.Kind == EvPress || ev.Kind == EvRelease) {
			names := make([]string, len(*pickList))
			for i, inter := range *pickList {
				if l, ok := inter.(Layouter); ok && l.GetLayout() != nil {
					lh := l.GetLayout()
					names[i] = fmt.Sprintf("%s(%d,%d %dx%d)", lh.Name(),
						lh.X.Get(), lh.Y.Get(), lh.Width.Get(), lh.Height.Get())
				} else {
					names[i] = "?"
				}
			}
			fmt.Printf("[%s:pick] %d hits at (%d,%d): %v\n", d.Tag, len(*pickList), ev.X, ev.Y, names)
		}
	}
	for _, agent := range p.agents {
		for _, target := range *pickList {
			if agent.Deliver(ev, target) && !isMonitor {
				return true
			}
		}
	}
	return false
}

// StandardPipeline creates the standard three-policy dispatch pipeline:
//
//  1. "clickdrag" (focus) — ClickDragAgent captures move/release during
//     active ClickDraggable interaction
//  2. "clickdrag-pick" (positional) — ClickDragAgent picks ClickDraggable
//     targets on press
//  3. "click" (positional) — ClickAgent recognizes single/double/triple clicks
//  4. "keyboard" (focus) — KeyAgent forwards to keyboard focus target
//
// Returns the dispatcher, the ClickAgent (for CheckTimer calls), and the KeyAgent.
func StandardPipeline(root Interactor) (*AppDispatcher, *ClickAgent, *KeyAgent) {
	d := NewAppDispatcher(root)
	clickDrag := &ClickDragAgent{}

	// Focus policy: captured ClickDraggable gets move/release.
	dragFocus := NewAppPolicy("clickdrag", AppPolicyFocus)
	dragFocus.AddAgent(clickDrag)
	d.AddPolicy(dragFocus)

	// Positional policy: pick ClickDraggable targets on press.
	dragPick := NewAppPolicy("clickdrag-pick", AppPolicyPositional)
	dragPick.AddAgent(clickDrag)
	d.AddPolicy(dragPick)

	// Positional policy: click recognition.
	click := NewClickAgent()
	clickPolicy := NewAppPolicy("click", AppPolicyPositional)
	clickPolicy.AddAgent(click)
	d.AddPolicy(clickPolicy)

	// Focus policy: keyboard forwarding.
	key := &KeyAgent{}
	keyPolicy := NewAppPolicy("keyboard", AppPolicyFocus)
	keyPolicy.AddAgent(key)
	d.AddPolicy(keyPolicy)

	return d, click, key
}
