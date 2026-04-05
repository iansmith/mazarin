package main

import "mazzy/mazarin/mancini"

// usKeyMapper translates evdev keycodes to US QWERTY characters.
type usKeyMapper struct {
	capsLock bool
}

func (k *usKeyMapper) Name() string { return "us" }

func (k *usKeyMapper) Map(code uint16, pressed bool, mods uint64) (rune, string) {
	// Toggle capslock on press.
	if code == 58 && pressed {
		k.capsLock = !k.capsLock
	}

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

	// Character lookup.
	return usLookup(code, mancini.IsShiftDown(mods), k.capsLock), ""
}
