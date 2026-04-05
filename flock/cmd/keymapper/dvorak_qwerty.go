package main

import "mazzy/mazarin/mancini"

// dvorakQwertyKeyMapper implements the Mac "Dvorak - QWERTY Cmd" layout.
// Normal typing uses Dvorak key positions; when Meta (Cmd) is held,
// keys revert to QWERTY so that keyboard shortcuts remain familiar.
//
// Since evdev always reports physical key positions (QWERTY scan codes),
// normal typing remaps through the Dvorak tables below. With Meta held,
// no remapping occurs — the QWERTY character is used directly.
type dvorakQwertyKeyMapper struct {
	capsLock bool
}

func (k *dvorakQwertyKeyMapper) Name() string { return "dvorak-qwerty" }

func (k *dvorakQwertyKeyMapper) Map(code uint16, pressed bool, mods uint64) (rune, string) {
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

	// Action keys are the same regardless of layout.
	if a := actionForCode(code); a != "" {
		return 0, a
	}

	// With Meta held, use QWERTY (no remapping) for keyboard shortcuts.
	if mancini.IsMetaDown(mods) {
		return usLookup(code, mancini.IsShiftDown(mods), k.capsLock), ""
	}

	// Normal typing: Dvorak layout.
	return dvorakLookup(code, mancini.IsShiftDown(mods), k.capsLock), ""
}

// dvorakLookup returns the Dvorak character for a physical key position,
// accounting for shift and capslock state.
func dvorakLookup(code uint16, shift, capsLock bool) rune {
	if int(code) >= len(dvorakNormal) {
		return 0
	}
	normal := dvorakNormal[code]
	if normal == 0 {
		return 0
	}
	wantUpper := shift
	if isLetter(code) && capsLock {
		wantUpper = !wantUpper
	}
	if wantUpper {
		if s := dvorakShifted[code]; s != 0 {
			return s
		}
	}
	return normal
}

// dvorakNormal maps evdev keycodes (physical QWERTY positions) to
// Dvorak unshifted characters. Number keys and space are unchanged.
var dvorakNormal = [128]rune{
	// Number row — digits same as QWERTY, punctuation differs.
	2: '1', 3: '2', 4: '3', 5: '4', 6: '5', 7: '6', 8: '7', 9: '8', 10: '9', 11: '0',
	12: '[', // QWERTY '-' → Dvorak '['
	13: ']', // QWERTY '=' → Dvorak ']'
	15: '\t',
	// Top row.
	16: '\'', // QWERTY 'q' → Dvorak '''
	17: ',',  // QWERTY 'w' → Dvorak ','
	18: '.',  // QWERTY 'e' → Dvorak '.'
	19: 'p',  // QWERTY 'r' → Dvorak 'p'
	20: 'y',  // QWERTY 't' → Dvorak 'y'
	21: 'f',  // QWERTY 'y' → Dvorak 'f'
	22: 'g',  // QWERTY 'u' → Dvorak 'g'
	23: 'c',  // QWERTY 'i' → Dvorak 'c'
	24: 'r',  // QWERTY 'o' → Dvorak 'r'
	25: 'l',  // QWERTY 'p' → Dvorak 'l'
	26: '/',  // QWERTY '[' → Dvorak '/'
	27: '=',  // QWERTY ']' → Dvorak '='
	// Home row.
	30: 'a', // unchanged
	31: 'o', // QWERTY 's' → Dvorak 'o'
	32: 'e', // QWERTY 'd' → Dvorak 'e'
	33: 'u', // QWERTY 'f' → Dvorak 'u'
	34: 'i', // QWERTY 'g' → Dvorak 'i'
	35: 'd', // QWERTY 'h' → Dvorak 'd'
	36: 'h', // QWERTY 'j' → Dvorak 'h'
	37: 't', // QWERTY 'k' → Dvorak 't'
	38: 'n', // QWERTY 'l' → Dvorak 'n'
	39: 's', // QWERTY ';' → Dvorak 's'
	40: '-', // QWERTY ''' → Dvorak '-'
	41: '`', 43: '\\',
	// Bottom row.
	44: ';', // QWERTY 'z' → Dvorak ';'
	45: 'q', // QWERTY 'x' → Dvorak 'q'
	46: 'j', // QWERTY 'c' → Dvorak 'j'
	47: 'k', // QWERTY 'v' → Dvorak 'k'
	48: 'x', // QWERTY 'b' → Dvorak 'x'
	49: 'b', // QWERTY 'n' → Dvorak 'b'
	50: 'm', // unchanged
	51: 'w', // QWERTY ',' → Dvorak 'w'
	52: 'v', // QWERTY '.' → Dvorak 'v'
	53: 'z', // QWERTY '/' → Dvorak 'z'
	57: ' ',
}

// dvorakShifted maps evdev keycodes (physical QWERTY positions) to
// Dvorak shifted characters.
var dvorakShifted = [128]rune{
	2: '!', 3: '@', 4: '#', 5: '$', 6: '%', 7: '^', 8: '&', 9: '*', 10: '(', 11: ')',
	12: '{', // QWERTY '_' → Dvorak '{'
	13: '}', // QWERTY '+' → Dvorak '}'
	// Top row.
	16: '"', // QWERTY 'Q' → Dvorak '"'
	17: '<', // QWERTY 'W' → Dvorak '<'
	18: '>', // QWERTY 'E' → Dvorak '>'
	19: 'P', 20: 'Y', 21: 'F', 22: 'G', 23: 'C', 24: 'R', 25: 'L',
	26: '?', // QWERTY '{' → Dvorak '?'
	27: '+', // QWERTY '}' → Dvorak '+'
	// Home row.
	30: 'A', 31: 'O', 32: 'E', 33: 'U', 34: 'I', 35: 'D', 36: 'H', 37: 'T', 38: 'N',
	39: 'S', // QWERTY ':' → Dvorak 'S'
	40: '_', // QWERTY '"' → Dvorak '_'
	41: '~', 43: '|',
	// Bottom row.
	44: ':', // QWERTY 'Z' → Dvorak ':'
	45: 'Q', 46: 'J', 47: 'K', 48: 'X', 49: 'B', 50: 'M',
	51: 'W', 52: 'V', 53: 'Z',
}
