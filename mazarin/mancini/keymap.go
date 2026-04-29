package mancini

import "mazzy/shared/hid"

// KeyMapper translates raw evdev keycodes into characters and actions,
// accounting for modifier state (shift, ctrl, alt, meta) and internal
// state like capslock. Implementations must be safe for concurrent use.
//
// Rachel loads a KeyMapper from keymapper.maz at startup. The TOML
// config specifies which keymap to use (e.g. keymap = "us").
type KeyMapper interface {
	// Map translates a raw evdev keycode into a character and/or action.
	// code is the evdev keycode, pressed is true for key-down / false for
	// key-up, and mods is the modifier bitmask (hid.ModXxx).
	// Returns:
	//   ch: the translated character (0 if modifier-only or non-printable)
	//   action: a named action ("enter", "backspace", "escape", etc.) or ""
	Map(code uint16, pressed bool, mods uint64) (ch rune, action string)

	// Name returns the keymap name (e.g. "identity", "us", "dvorak-qwerty").
	Name() string
}

// --- Modifier bitmask helpers ---
// These test individual modifier keys in the mods bitmask carried by
// InputEvent.Mods. Use these instead of manipulating bits directly.

// IsLeftShiftDown reports whether the left shift modifier is set in mods.
func IsLeftShiftDown(mods uint64) bool { return mods&hid.ModLShift != 0 }

// IsRightShiftDown reports whether the right shift modifier is set in mods.
func IsRightShiftDown(mods uint64) bool { return mods&hid.ModRShift != 0 }

// IsShiftDown reports whether either shift modifier is set in mods.
func IsShiftDown(mods uint64) bool { return mods&(hid.ModLShift|hid.ModRShift) != 0 }

// IsLeftControlDown reports whether the left control modifier is set in mods.
func IsLeftControlDown(mods uint64) bool { return mods&hid.ModLCtrl != 0 }

// IsRightControlDown reports whether the right control modifier is set in mods.
func IsRightControlDown(mods uint64) bool { return mods&hid.ModRCtrl != 0 }

// IsControlDown reports whether either control modifier is set in mods.
func IsControlDown(mods uint64) bool { return mods&(hid.ModLCtrl|hid.ModRCtrl) != 0 }

// IsLeftAltDown reports whether the left alt modifier is set in mods.
func IsLeftAltDown(mods uint64) bool { return mods&hid.ModLAlt != 0 }

// IsRightAltDown reports whether the right alt modifier is set in mods.
func IsRightAltDown(mods uint64) bool { return mods&hid.ModRAlt != 0 }

// IsAltDown reports whether either alt modifier is set in mods.
func IsAltDown(mods uint64) bool { return mods&(hid.ModLAlt|hid.ModRAlt) != 0 }

// IsLeftMetaDown reports whether the left meta modifier is set in mods.
func IsLeftMetaDown(mods uint64) bool { return mods&hid.ModLMeta != 0 }

// IsRightMetaDown reports whether the right meta modifier is set in mods.
func IsRightMetaDown(mods uint64) bool { return mods&hid.ModRMeta != 0 }

// IsMetaDown reports whether either meta modifier is set in mods.
func IsMetaDown(mods uint64) bool { return mods&(hid.ModLMeta|hid.ModRMeta) != 0 }
