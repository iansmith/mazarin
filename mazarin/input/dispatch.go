// dispatch.go implements the subArctic input dispatch model (Hudson/Henry).
//
// The architecture has three layers:
//
//  1. Dispatch Policies — ordered list of high-level routing strategies
//     (focus, positional, monitor-focus, monitor-positional).
//  2. Dispatch Agents — each policy contains an ordered list of agents.
//     Each agent implements a specific input protocol (Go interface) and
//     maintains its own FSM for translating raw events into semantic callbacks.
//  3. Interactors — objects that receive dispatched events by implementing
//     protocol interfaces (Pressable, KeyReceiver, Movable, Acceleratable).
//
// Events flow through policies in priority order. Within each policy,
// agents are tried in order. For positional policies, each agent is tried
// against each interactor in the pick list. An event is consumed when
// an agent's Deliver returns true — dispatch halts (unless the policy is
// a monitor, which never halts dispatch).
//
// Reference: Hudson, Mankoff, Smith. "Extensible Input Handling in the
// subArctic Toolkit." CHI 2005.
// Henry, Hudson, Newell. "Integrating Gesture and Snapping into a User
// Interface Toolkit." UIST 1990.
package input

import "mazzy/shared/dlist"

// PolicyKind determines how a DispatchPolicy routes events to interactors.
type PolicyKind int

const (
	// PolicyFocus delivers events to a designated focus target regardless
	// of cursor position. The event is consumed when an agent returns true.
	PolicyFocus PolicyKind = iota

	// PolicyPositional delivers events to interactors found beneath the
	// cursor via picking. The event is consumed when an agent returns true.
	PolicyPositional

	// PolicyMonitorFocus delivers to focus target but never consumes.
	// Events always continue to subsequent policies.
	PolicyMonitorFocus

	// PolicyMonitorPositional delivers positionally but never consumes.
	PolicyMonitorPositional
)

// EvMouseMove is a synthetic event type used for batched cursor motion.
// Raw EV_REL/EV_ABS events are accumulated into a single position update,
// then dispatched as an EvMouseMove event after the batch.
const EvMouseMove uint16 = 0xFF

// InputEvent is an enriched HID event carrying cursor position and
// modifier state through the dispatch pipeline.
type InputEvent struct {
	Type  uint16 // EV_KEY (1), or EvMouseMove (0xFF)
	Code  uint16 // evdev keycode or button code
	Value uint32 // 1=press, 0=release
	X, Y  int32  // current cursor position
	Mods  uint64 // modifier key bitmask (hid.ModXxx constants)
}

// BtnLeft is the boundary between keyboard and mouse button evdev codes.
const BtnLeft = 0x110

func (e *InputEvent) IsKeyboard() bool   { return e.Type == 1 && e.Code < BtnLeft }
func (e *InputEvent) IsMouseButton() bool { return e.Type == 1 && e.Code >= BtnLeft }
func (e *InputEvent) IsMouseMove() bool   { return e.Type == EvMouseMove }
func (e *InputEvent) IsPress() bool       { return e.Value == 1 }
func (e *InputEvent) IsRelease() bool     { return e.Value == 0 }

// --- Input Protocols ---
// Interactors declare which protocols they support by implementing
// the corresponding Go interface. Agents check via type assertion.

// Interactor is any object that participates in input dispatch.
type Interactor interface {
	// PickedBy returns true if (x, y) hits this interactor's bounds.
	// Interactors used only with focus policies may return false.
	PickedBy(x, y int32) bool
}

// Pressable handles mouse button press and release events.
type Pressable interface {
	Press(ev *InputEvent) bool
	Release(ev *InputEvent) bool
}

// Movable handles mouse movement events (typically during drag).
type Movable interface {
	Move(ev *InputEvent) bool
}

// KeyReceiver handles keyboard key down and key up events.
type KeyReceiver interface {
	KeyDown(ev *InputEvent) bool
	KeyUp(ev *InputEvent) bool
}

// Acceleratable receives matched keyboard accelerator actions.
type Acceleratable interface {
	Accelerator(action string, ev *InputEvent) bool
}

// --- Dispatch Agents ---

// DispatchAgent translates raw events into semantic protocol callbacks.
// Each agent targets a specific protocol interface and maintains its own
// state machine (e.g., press-move-release for dragging).
type DispatchAgent interface {
	// Name returns a human-readable identifier for debugging.
	Name() string

	// Deliver attempts to deliver ev to target via the agent's protocol.
	// Returns true if the event was consumed.
	Deliver(ev *InputEvent, target Interactor) bool
}

// FocusableAgent is a DispatchAgent that maintains a designated focus target.
// Used with PolicyFocus and PolicyMonitorFocus policies. Each focus-based
// agent manages its own focus independently.
type FocusableAgent interface {
	DispatchAgent
	FocusTarget() Interactor
	SetFocus(target Interactor)
}

// --- Dispatch Policy ---

// DispatchPolicy is a named routing strategy containing an ordered list
// of agents. The policy's Kind determines whether events are delivered
// via focus or positional picking, and whether delivery consumes events.
type DispatchPolicy struct {
	name   string
	kind   PolicyKind
	agents *dlist.List[DispatchAgent]
}

// NewDispatchPolicy creates a policy with the given name and routing kind.
func NewDispatchPolicy(name string, kind PolicyKind) *DispatchPolicy {
	return &DispatchPolicy{
		name:   name,
		kind:   kind,
		agents: dlist.New[DispatchAgent](),
	}
}

func (p *DispatchPolicy) Name() string     { return p.name }
func (p *DispatchPolicy) Kind() PolicyKind { return p.kind }

// AddAgent appends an agent to the end of this policy's agent list.
func (p *DispatchPolicy) AddAgent(agent DispatchAgent) *dlist.Node[DispatchAgent] {
	return p.agents.PushBack(agent)
}

// AddAgentFront inserts an agent at the front of this policy's agent list.
func (p *DispatchPolicy) AddAgentFront(agent DispatchAgent) *dlist.Node[DispatchAgent] {
	return p.agents.PushFront(agent)
}

// RemoveAgent removes an agent node from this policy's agent list.
func (p *DispatchPolicy) RemoveAgent(node *dlist.Node[DispatchAgent]) {
	p.agents.Remove(node)
}

// --- Input Dispatcher ---

// InputDispatcher is the top-level dispatch pipeline. It holds an ordered
// list of policies and a pick function for positional dispatch.
type InputDispatcher struct {
	policies *dlist.List[*DispatchPolicy]
	// PickFn is called for positional policies to determine which
	// interactors are under the cursor. Returns interactors in priority
	// order (front-to-back, topmost first).
	PickFn func(x, y int32) []Interactor
}

// NewInputDispatcher creates an empty dispatcher.
func NewInputDispatcher() *InputDispatcher {
	return &InputDispatcher{
		policies: dlist.New[*DispatchPolicy](),
	}
}

// AddPolicy appends a policy to the end of the policy list.
func (d *InputDispatcher) AddPolicy(p *DispatchPolicy) *dlist.Node[*DispatchPolicy] {
	return d.policies.PushBack(p)
}

// AddPolicyFront inserts a policy at the front of the policy list.
func (d *InputDispatcher) AddPolicyFront(p *DispatchPolicy) *dlist.Node[*DispatchPolicy] {
	return d.policies.PushFront(p)
}

// RemovePolicy removes a policy node from the dispatcher.
func (d *InputDispatcher) RemovePolicy(node *dlist.Node[*DispatchPolicy]) {
	d.policies.Remove(node)
}

// Dispatch routes ev through the policy pipeline.
//
// For each policy (in priority order):
//   - Focus/MonitorFocus: each agent's FocusTarget receives the event.
//   - Positional/MonitorPositional: PickFn determines targets; each agent
//     is tried against each picked interactor.
//
// Dispatch halts when an agent's Deliver returns true (consumed), unless
// the consuming policy is a monitor (monitors never halt dispatch).
// Returns true if the event was consumed.
func (d *InputDispatcher) Dispatch(ev *InputEvent) bool {
	var pickList []Interactor // lazily computed for positional policies

	for pn := d.policies.Front(); pn != nil; pn = pn.Next() {
		p := pn.Value
		isMonitor := p.kind == PolicyMonitorFocus || p.kind == PolicyMonitorPositional

		consumed := d.dispatchPolicy(ev, p, &pickList)

		if consumed && !isMonitor {
			return true
		}
	}
	return false
}

// dispatchPolicy routes ev through a single policy's agents.
func (d *InputDispatcher) dispatchPolicy(ev *InputEvent, p *DispatchPolicy, pickList *[]Interactor) bool {
	for an := p.agents.Front(); an != nil; an = an.Next() {
		agent := an.Value

		switch p.kind {
		case PolicyFocus, PolicyMonitorFocus:
			fa, ok := agent.(FocusableAgent)
			if !ok {
				continue
			}
			target := fa.FocusTarget()
			if target == nil {
				continue
			}
			if agent.Deliver(ev, target) {
				return true
			}

		case PolicyPositional, PolicyMonitorPositional:
			if *pickList == nil && d.PickFn != nil {
				*pickList = d.PickFn(ev.X, ev.Y)
			}
			for _, target := range *pickList {
				if agent.Deliver(ev, target) {
					return true
				}
			}
		}
	}
	return false
}

// --- Pick Collector ---

// PickCollector accumulates interactors found during a pick traversal.
// Used by the pick function to build the ordered list of targets for
// positional dispatch. Results are in priority order (topmost first).
type PickCollector struct {
	items *dlist.List[Interactor]
}

// NewPickCollector creates an empty collector.
func NewPickCollector() *PickCollector {
	return &PickCollector{items: dlist.New[Interactor]()}
}

// Add appends an interactor to the pick list.
func (pc *PickCollector) Add(i Interactor) {
	pc.items.PushBack(i)
}

// AddFront inserts an interactor at the front of the pick list.
func (pc *PickCollector) AddFront(i Interactor) {
	pc.items.PushFront(i)
}

// Results returns the collected interactors as a slice.
func (pc *PickCollector) Results() []Interactor {
	result := make([]Interactor, 0, pc.items.Len())
	pc.items.Range(func(n *dlist.Node[Interactor]) bool {
		result = append(result, n.Value)
		return true
	})
	return result
}

// Len returns the number of collected interactors.
func (pc *PickCollector) Len() int {
	return pc.items.Len()
}
