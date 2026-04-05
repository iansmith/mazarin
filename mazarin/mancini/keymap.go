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

func IsLeftShiftDown(mods uint64) bool  { return mods&hid.ModLShift != 0 }
func IsRightShiftDown(mods uint64) bool { return mods&hid.ModRShift != 0 }
func IsShiftDown(mods uint64) bool      { return mods&(hid.ModLShift|hid.ModRShift) != 0 }

func IsLeftControlDown(mods uint64) bool  { return mods&hid.ModLCtrl != 0 }
func IsRightControlDown(mods uint64) bool { return mods&hid.ModRCtrl != 0 }
func IsControlDown(mods uint64) bool      { return mods&(hid.ModLCtrl|hid.ModRCtrl) != 0 }

func IsLeftAltDown(mods uint64) bool  { return mods&hid.ModLAlt != 0 }
func IsRightAltDown(mods uint64) bool { return mods&hid.ModRAlt != 0 }
func IsAltDown(mods uint64) bool      { return mods&(hid.ModLAlt|hid.ModRAlt) != 0 }

func IsLeftMetaDown(mods uint64) bool  { return mods&hid.ModLMeta != 0 }
func IsRightMetaDown(mods uint64) bool { return mods&hid.ModRMeta != 0 }
func IsMetaDown(mods uint64) bool      { return mods&(hid.ModLMeta|hid.ModRMeta) != 0 }
