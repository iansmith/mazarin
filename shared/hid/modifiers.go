// Modifier key bitmask constants and helpers.
//
// The kernel publishes a modifier bitmask as attr:///kernel/int64/input/modifiers.
// Use these helpers to test modifier state — do not manipulate bits directly.
package hid

// Modifier bit positions within the bitmask.
const (
	ModLShift uint64 = 1 << 0
	ModRShift uint64 = 1 << 1
	ModLCtrl  uint64 = 1 << 2
	ModRCtrl  uint64 = 1 << 3
	ModLAlt   uint64 = 1 << 4
	ModRAlt   uint64 = 1 << 5 // AltGr on international keyboards
	ModLMeta  uint64 = 1 << 6
	ModRMeta  uint64 = 1 << 7

	modShiftBoth = ModLShift | ModRShift
	modCtrlBoth  = ModLCtrl | ModRCtrl
	modAltBoth   = ModLAlt | ModRAlt
	modMetaBoth  = ModLMeta | ModRMeta
)

// Linux evdev key codes for modifier keys.
const (
	KeyLShift = 42
	KeyRShift = 54
	KeyLCtrl  = 29
	KeyRCtrl  = 97
	KeyLAlt   = 56
	KeyRAlt   = 100 // AltGr
	KeyLMeta  = 125
	KeyRMeta  = 126
)

// --- Combined testers (either left or right) ---

func Shift(mods int64) bool { return uint64(mods)&modShiftBoth != 0 }
func Ctrl(mods int64) bool  { return uint64(mods)&modCtrlBoth != 0 }
func Alt(mods int64) bool   { return uint64(mods)&modAltBoth != 0 }
func Meta(mods int64) bool  { return uint64(mods)&modMetaBoth != 0 }

// --- Individual key testers ---

func LShift(mods int64) bool { return uint64(mods)&ModLShift != 0 }
func RShift(mods int64) bool { return uint64(mods)&ModRShift != 0 }
func LCtrl(mods int64) bool  { return uint64(mods)&ModLCtrl != 0 }
func RCtrl(mods int64) bool  { return uint64(mods)&ModRCtrl != 0 }
func LAlt(mods int64) bool   { return uint64(mods)&ModLAlt != 0 }
func RAlt(mods int64) bool   { return uint64(mods)&ModRAlt != 0 }
func AltGr(mods int64) bool  { return uint64(mods)&ModRAlt != 0 }
func LMeta(mods int64) bool  { return uint64(mods)&ModLMeta != 0 }
func RMeta(mods int64) bool  { return uint64(mods)&ModRMeta != 0 }
