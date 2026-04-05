package input

// Keymap translates raw evdev keycodes into characters, tracking modifier state.
// Implements mancini.KeyMapper for use as a built-in US QWERTY fallback
// until keymapper.maz is loaded.
type Keymap struct {
	shift    bool
	ctrl     bool
	alt      bool
	capsLock bool
}

// Name implements mancini.KeyMapper.
func (k *Keymap) Name() string { return "us" }

// Map implements mancini.KeyMapper. It translates an evdev keycode
// using the modifier bitmask from the InputEvent. Internal modifier
// tracking (shift, capslock) is updated from the mods bitmask.
func (k *Keymap) Map(code uint16, pressed bool, mods uint64) (rune, string) {
	return k.Feed(KeyEvent{Code: code, Pressed: pressed})
}

// Feed processes a raw KeyEvent, updates modifier state, and returns:
//   - ch: the translated character (0 if non-printable or modifier-only)
//   - action: a named action for non-character keys ("enter", "backspace", etc.)
//
// Only press and repeat events produce output; releases return (0, "").
func (k *Keymap) Feed(ev KeyEvent) (ch rune, action string) {
	code := ev.Code

	// Update modifier state on press and release
	switch code {
	case 42, 54: // LSHIFT, RSHIFT
		k.shift = ev.Pressed
		return 0, ""
	case 29, 97: // LCTRL, RCTRL
		k.ctrl = ev.Pressed
		return 0, ""
	case 56, 100: // LALT, RALT
		k.alt = ev.Pressed
		return 0, ""
	case 58: // CAPSLOCK — toggle on press only
		if ev.Pressed && !ev.Repeat {
			k.capsLock = !k.capsLock
		}
		return 0, ""
	}

	// Only produce output on press/repeat
	if !ev.Pressed {
		return 0, ""
	}

	// Check action keys
	if a := actionKeys[code]; a != "" {
		return 0, a
	}

	// Character lookup
	if int(code) < len(normalChars) && normalChars[code] != 0 {
		normal := normalChars[code]
		shifted := shiftedChars[code]

		isLetter := (code >= 16 && code <= 25) || (code >= 30 && code <= 38) || (code >= 44 && code <= 50)

		wantUpper := k.shift
		if isLetter && k.capsLock {
			wantUpper = !wantUpper // CapsLock XOR Shift for letters
		}

		if wantUpper && shifted != 0 {
			return shifted, ""
		}
		return normal, ""
	}

	return 0, ""
}

// US QWERTY normal (unshifted) characters, indexed by evdev keycode.
var normalChars = [128]rune{
	2: '1', 3: '2', 4: '3', 5: '4', 6: '5', 7: '6', 8: '7', 9: '8', 10: '9', 11: '0',
	12: '-', 13: '=',
	16: 'q', 17: 'w', 18: 'e', 19: 'r', 20: 't', 21: 'y', 22: 'u', 23: 'i', 24: 'o', 25: 'p',
	26: '[', 27: ']',
	30: 'a', 31: 's', 32: 'd', 33: 'f', 34: 'g', 35: 'h', 36: 'j', 37: 'k', 38: 'l',
	39: ';', 40: '\'', 41: '`', 43: '\\',
	44: 'z', 45: 'x', 46: 'c', 47: 'v', 48: 'b', 49: 'n', 50: 'm',
	51: ',', 52: '.', 53: '/',
	57: ' ',
}

// US QWERTY shifted characters, indexed by evdev keycode.
var shiftedChars = [128]rune{
	2: '!', 3: '@', 4: '#', 5: '$', 6: '%', 7: '^', 8: '&', 9: '*', 10: '(', 11: ')',
	12: '_', 13: '+',
	16: 'Q', 17: 'W', 18: 'E', 19: 'R', 20: 'T', 21: 'Y', 22: 'U', 23: 'I', 24: 'O', 25: 'P',
	26: '{', 27: '}',
	30: 'A', 31: 'S', 32: 'D', 33: 'F', 34: 'G', 35: 'H', 36: 'J', 37: 'K', 38: 'L',
	39: ':', 40: '"', 41: '~', 43: '|',
	44: 'Z', 45: 'X', 46: 'C', 47: 'V', 48: 'B', 49: 'N', 50: 'M',
	51: '<', 52: '>', 53: '?',
}

// Action keys: non-character keys that produce named actions.
var actionKeys = [128]string{
	1:   "escape",
	14:  "backspace",
	15:  "tab",
	28:  "enter",
	96:  "enter", // keypad enter
	102: "home",
	103: "up",
	104: "pageup",
	105: "left",
	106: "right",
	107: "end",
	108: "down",
	109: "pagedown",
	110: "insert",
	111: "delete",
	59:  "f1", 60: "f2", 61: "f3", 62: "f4", 63: "f5",
	64: "f6", 65: "f7", 66: "f8", 67: "f9", 68: "f10",
	87: "f11", 88: "f12",
}
