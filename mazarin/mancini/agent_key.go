package mancini

// KeyAgent forwards keyboard events to a designated focus target
// that implements KeyReceivable.
type KeyAgent struct {
	target Interactor
}

func (a *KeyAgent) Name() string             { return "keyboard" }
func (a *KeyAgent) FocusTarget() Interactor  { return a.target }
func (a *KeyAgent) SetFocus(t Interactor)    { a.target = t }

// Deliver forwards keyboard events to the focus target.
func (a *KeyAgent) Deliver(ev *InputEvent, target Interactor) bool {
	kr, ok := target.(KeyReceivable)
	if !ok {
		return false
	}
	switch ev.Kind {
	case EvKeyDown:
		return kr.KeyDown(ev)
	case EvKeyUp:
		return kr.KeyUp(ev)
	}
	return false
}
