package input

import "mazzy/shared/hid"

// ModifierState tracks the current state of modifier keys (shift, ctrl,
// alt, meta) as a bitmask using the constants from shared/hid/modifiers.go.
//
// Rachel maintains a single ModifierState, updating it on every modifier
// key press/release. The current bitmask is stamped into every InputEvent
// before dispatch. Modifier key events are consumed by rachel and NOT
// forwarded to shepherds — shepherds see modifier state only via the
// Mods field on forwarded events.
type ModifierState struct {
	mods uint64
}

// Update processes a key press or release for a modifier key.
// code is the Linux evdev keycode; pressed is true for press, false for release.
func (m *ModifierState) Update(code uint16, pressed bool) {
	var bit uint64
	switch code {
	case hid.KeyLShift:
		bit = hid.ModLShift
	case hid.KeyRShift:
		bit = hid.ModRShift
	case hid.KeyLCtrl:
		bit = hid.ModLCtrl
	case hid.KeyRCtrl:
		bit = hid.ModRCtrl
	case hid.KeyLAlt:
		bit = hid.ModLAlt
	case hid.KeyRAlt:
		bit = hid.ModRAlt
	case hid.KeyLMeta:
		bit = hid.ModLMeta
	case hid.KeyRMeta:
		bit = hid.ModRMeta
	default:
		return
	}
	if pressed {
		m.mods |= bit
	} else {
		m.mods &^= bit
	}
}

// Mods returns the current modifier bitmask.
func (m *ModifierState) Mods() uint64 {
	return m.mods
}

// IsModifierKey returns true if the evdev keycode is one of the 8 modifier keys.
func IsModifierKey(code uint16) bool {
	switch code {
	case hid.KeyLShift, hid.KeyRShift,
		hid.KeyLCtrl, hid.KeyRCtrl,
		hid.KeyLAlt, hid.KeyRAlt,
		hid.KeyLMeta, hid.KeyRMeta:
		return true
	}
	return false
}
