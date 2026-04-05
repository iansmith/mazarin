package wm

// Action is a uint8 enum for non-character key actions carried in
// KeyPress/KeyRelease wire messages. Rachel's KeyMapper translates
// evdev keycodes to (Char, Action) pairs before forwarding.
type Action uint8

const (
	ActionNone Action = 0

	// Editing.
	ActionEscape    Action = 1
	ActionBackspace Action = 2
	ActionTab       Action = 3
	ActionEnter     Action = 4
	ActionInsert    Action = 5
	ActionDelete    Action = 6

	// Navigation.
	ActionHome     Action = 10
	ActionEnd      Action = 11
	ActionPageUp   Action = 12
	ActionPageDown Action = 13
	ActionUp       Action = 14
	ActionDown     Action = 15
	ActionLeft     Action = 16
	ActionRight    Action = 17

	// Function keys.
	ActionF1  Action = 20
	ActionF2  Action = 21
	ActionF3  Action = 22
	ActionF4  Action = 23
	ActionF5  Action = 24
	ActionF6  Action = 25
	ActionF7  Action = 26
	ActionF8  Action = 27
	ActionF9  Action = 28
	ActionF10 Action = 29
	ActionF11 Action = 30
	ActionF12 Action = 31
	ActionF13 Action = 32
	ActionF14 Action = 33
	ActionF15 Action = 34
	ActionF16 Action = 35
	ActionF17 Action = 36
	ActionF18 Action = 37
	ActionF19 Action = 38
	ActionF20 Action = 39
	ActionF21 Action = 40
	ActionF22 Action = 41
	ActionF23 Action = 42
	ActionF24 Action = 43

	// Lock keys.
	ActionNumLock    Action = 50
	ActionScrollLock Action = 51

	// Keypad.
	ActionKPEnter Action = 52

	// System.
	ActionSysRq   Action = 60
	ActionPower   Action = 61
	ActionPause   Action = 62
	ActionCompose Action = 63
	ActionSleep   Action = 64
	ActionWakeup  Action = 65

	// Volume / media.
	ActionMute         Action = 70
	ActionVolumeDown   Action = 71
	ActionVolumeUp     Action = 72
	ActionNextSong     Action = 73
	ActionPlayPause    Action = 74
	ActionPreviousSong Action = 75
	ActionStopCD       Action = 76
	ActionRecord       Action = 77
	ActionRewind       Action = 78
	ActionPlayCD       Action = 79
	ActionPauseCD      Action = 80
	ActionPlay         Action = 81
	ActionFastForward  Action = 82

	// Misc.
	ActionHelp       Action = 90
	ActionMenu       Action = 91
	ActionScreenLock Action = 92
	ActionBack       Action = 93
	ActionForward    Action = 94
	ActionEjectCD    Action = 95
	ActionHomepage   Action = 96
	ActionRefresh    Action = 97
	ActionSuspend    Action = 98
	ActionPrint      Action = 99
	ActionSearch     Action = 100

	// Laptop.
	ActionBrightnessDown Action = 110
	ActionBrightnessUp   Action = 111
	ActionMicMute        Action = 112
)

// actionNames maps Action → string name. Used by ActionName().
var actionNames = [256]string{
	ActionEscape:    "escape",
	ActionBackspace: "backspace",
	ActionTab:       "tab",
	ActionEnter:     "enter",
	ActionInsert:    "insert",
	ActionDelete:    "delete",

	ActionHome:     "home",
	ActionEnd:      "end",
	ActionPageUp:   "pageup",
	ActionPageDown: "pagedown",
	ActionUp:       "up",
	ActionDown:     "down",
	ActionLeft:     "left",
	ActionRight:    "right",

	ActionF1:  "f1",
	ActionF2:  "f2",
	ActionF3:  "f3",
	ActionF4:  "f4",
	ActionF5:  "f5",
	ActionF6:  "f6",
	ActionF7:  "f7",
	ActionF8:  "f8",
	ActionF9:  "f9",
	ActionF10: "f10",
	ActionF11: "f11",
	ActionF12: "f12",
	ActionF13: "f13",
	ActionF14: "f14",
	ActionF15: "f15",
	ActionF16: "f16",
	ActionF17: "f17",
	ActionF18: "f18",
	ActionF19: "f19",
	ActionF20: "f20",
	ActionF21: "f21",
	ActionF22: "f22",
	ActionF23: "f23",
	ActionF24: "f24",

	ActionNumLock:    "numlock",
	ActionScrollLock: "scrolllock",
	ActionKPEnter:    "kpenter",

	ActionSysRq:   "sysrq",
	ActionPower:   "power",
	ActionPause:   "pause",
	ActionCompose: "compose",
	ActionSleep:   "sleep",
	ActionWakeup:  "wakeup",

	ActionMute:         "mute",
	ActionVolumeDown:   "volumedown",
	ActionVolumeUp:     "volumeup",
	ActionNextSong:     "nextsong",
	ActionPlayPause:    "playpause",
	ActionPreviousSong: "previoussong",
	ActionStopCD:       "stopcd",
	ActionRecord:       "record",
	ActionRewind:       "rewind",
	ActionPlayCD:       "playcd",
	ActionPauseCD:      "pausecd",
	ActionPlay:         "play",
	ActionFastForward:  "fastforward",

	ActionHelp:       "help",
	ActionMenu:       "menu",
	ActionScreenLock: "screenlock",
	ActionBack:       "back",
	ActionForward:    "forward",
	ActionEjectCD:    "ejectcd",
	ActionHomepage:   "homepage",
	ActionRefresh:    "refresh",
	ActionSuspend:    "suspend",
	ActionPrint:      "print",
	ActionSearch:     "search",

	ActionBrightnessDown: "brightnessdown",
	ActionBrightnessUp:   "brightnessup",
	ActionMicMute:        "micmute",
}

// ActionName returns the string name for an action code.
// Returns "" for ActionNone or unknown codes.
func ActionName(a Action) string {
	return actionNames[a]
}

// actionByName maps string name → Action. Built lazily by ActionByName.
var actionByName map[string]Action

// ActionByName returns the Action for a string name.
// Returns ActionNone for unknown names.
func ActionByName(name string) Action {
	if actionByName == nil {
		actionByName = make(map[string]Action, 64)
		for i, n := range actionNames {
			if n != "" {
				actionByName[n] = Action(i)
			}
		}
	}
	a, ok := actionByName[name]
	if !ok {
		return ActionNone
	}
	return a
}
