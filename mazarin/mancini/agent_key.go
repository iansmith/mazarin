package mancini

// KeyPressable receives matched key-press events (down+up pair).
// The KeyAgent handles keymap translation via a KeyMapper and matches
// EvKeyDown with EvKeyUp to produce KeyPress callbacks.
type KeyPressable interface {
	KeyPress(ch rune, action string, ev *InputEvent) bool
}

// KeyAgent forwards keyboard events to a designated focus target
// that implements KeyPressable. Char and Action are pre-translated
// by rachel's KeyMapper and carried in the InputEvent.
type KeyAgent struct {
	target Interactor
}

// Name returns the agent's identifier for dispatch logging.
func (a *KeyAgent) Name() string { return "keyboard" }

// FocusTarget returns the [Interactor] currently receiving key events, or nil.
func (a *KeyAgent) FocusTarget() Interactor { return a.target }

// SetFocus directs subsequent key events to t.
func (a *KeyAgent) SetFocus(t Interactor) { a.target = t }

// Deliver translates keyboard events via the KeyMapper and fires
// KeyPress on the focus target.
func (a *KeyAgent) Deliver(ev *InputEvent, target Interactor) bool {
	if !ev.IsKeyboard() {
		return false
	}
	kp, ok := target.(KeyPressable)
	if !ok {
		return false
	}

	// Only fire KeyPress on key-down for responsiveness.
	if ev.Kind != EvKeyDown {
		return false
	}

	// Char and Action are pre-translated by rachel's KeyMapper
	// and carried in the wire message.
	ch := ev.Char
	action := ev.Action

	if ch == 0 && action == "" {
		return false
	}
	return kp.KeyPress(ch, action, ev)
}
