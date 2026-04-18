package main

// isModifier returns true for modifier keys that should not produce output.
func isModifier(code uint16) bool {
	switch code {
	case 42, 54: // LSHIFT, RSHIFT
		return true
	case 29, 97: // LCTRL, RCTRL
		return true
	case 56, 100: // LALT, RALT
		return true
	case 125, 126: // LMETA, RMETA
		return true
	case 58: // CAPSLOCK
		return true
	}
	return false
}

// isLetter returns true if the evdev code corresponds to a letter key.
func isLetter(code uint16) bool {
	return (code >= 16 && code <= 25) || // Q-P
		(code >= 30 && code <= 38) || // A-L
		(code >= 44 && code <= 50) // Z-M
}

// actionForCode returns the action string for non-character keys.
func actionForCode(code uint16) string {
	if int(code) < len(actionKeys) {
		return actionKeys[code]
	}
	return ""
}

// usLookup returns the US QWERTY character for a keycode, accounting
// for shift and capslock state.
func usLookup(code uint16, shift, capsLock bool) rune {
	if int(code) >= len(usNormal) {
		return 0
	}
	normal := usNormal[code]
	if normal == 0 {
		return 0
	}
	wantUpper := shift
	if isLetter(code) && capsLock {
		wantUpper = !wantUpper
	}
	if wantUpper {
		if s := usShifted[code]; s != 0 {
			return s
		}
	}
	return normal
}

// Action keys: non-character keys that produce named actions.
// Indexed by Linux evdev keycode. Modifier keys and character-producing
// keys are excluded — they are handled separately.
var actionKeys = [256]string{
	1:  "escape",
	14: "backspace",
	15: "tab",
	28: "enter",

	// Function keys.
	59: "f1", 60: "f2", 61: "f3", 62: "f4", 63: "f5",
	64: "f6", 65: "f7", 66: "f8", 67: "f9", 68: "f10",
	87: "f11", 88: "f12",

	// Lock keys.
	69: "numlock",
	70: "scrolllock",

	// Keypad.
	96: "kpenter",

	// System.
	99:  "sysrq",
	116: "power",
	119: "pause",
	127: "compose",
	142: "sleep",
	143: "wakeup",

	// Navigation.
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

	// Volume / media.
	113: "mute",
	114: "volumedown",
	115: "volumeup",
	163: "nextsong",
	164: "playpause",
	165: "previoussong",
	166: "stopcd",
	167: "record",
	168: "rewind",
	200: "playcd",
	201: "pausecd",
	207: "play",
	208: "fastforward",

	// Misc.
	138: "help",
	139: "menu",
	152: "screenlock",
	158: "back",
	159: "forward",
	161: "ejectcd",
	172: "homepage",
	173: "refresh",
	205: "suspend",
	210: "print",
	217: "search",

	// Laptop.
	224: "brightnessdown",
	225: "brightnessup",
	248: "micmute",

	// Extended function keys.
	183: "f13", 184: "f14", 185: "f15", 186: "f16",
	187: "f17", 188: "f18", 189: "f19", 190: "f20",
	191: "f21", 192: "f22", 193: "f23", 194: "f24",
}

// US QWERTY unshifted characters, indexed by evdev keycode.
var usNormal = [128]rune{
	2: '1', 3: '2', 4: '3', 5: '4', 6: '5', 7: '6', 8: '7', 9: '8', 10: '9', 11: '0',
	12: '-', 13: '=',
	15: '\t',
	16: 'q', 17: 'w', 18: 'e', 19: 'r', 20: 't', 21: 'y', 22: 'u', 23: 'i', 24: 'o', 25: 'p',
	26: '[', 27: ']',
	30: 'a', 31: 's', 32: 'd', 33: 'f', 34: 'g', 35: 'h', 36: 'j', 37: 'k', 38: 'l',
	39: ';', 40: '\'', 41: '`', 43: '\\',
	44: 'z', 45: 'x', 46: 'c', 47: 'v', 48: 'b', 49: 'n', 50: 'm',
	51: ',', 52: '.', 53: '/',
	57: ' ',
}

// US QWERTY shifted characters, indexed by evdev keycode.
var usShifted = [128]rune{
	2: '!', 3: '@', 4: '#', 5: '$', 6: '%', 7: '^', 8: '&', 9: '*', 10: '(', 11: ')',
	12: '_', 13: '+',
	16: 'Q', 17: 'W', 18: 'E', 19: 'R', 20: 'T', 21: 'Y', 22: 'U', 23: 'I', 24: 'O', 25: 'P',
	26: '{', 27: '}',
	30: 'A', 31: 'S', 32: 'D', 33: 'F', 34: 'G', 35: 'H', 36: 'J', 37: 'K', 38: 'L',
	39: ':', 40: '"', 41: '~', 43: '|',
	44: 'Z', 45: 'X', 46: 'C', 47: 'V', 48: 'B', 49: 'N', 50: 'M',
	51: '<', 52: '>', 53: '?',
}
