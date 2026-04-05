package main

// identityKeyMapper performs no key remapping. It recognizes action keys
// (enter, backspace, etc.) but does not translate keycodes to characters.
// This is the fallback when a requested keymap name is not found.
type identityKeyMapper struct{}

func (k *identityKeyMapper) Name() string { return "identity" }

func (k *identityKeyMapper) Map(code uint16, pressed bool, mods uint64) (rune, string) {
	// Modifier keys produce nothing.
	if isModifier(code) {
		return 0, ""
	}
	// Only produce output on press.
	if !pressed {
		return 0, ""
	}
	// Action keys.
	if a := actionForCode(code); a != "" {
		return 0, a
	}
	return 0, ""
}
