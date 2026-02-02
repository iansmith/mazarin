// Package input provides Go-idiomatic channel-based APIs for keyboard and
// mouse events. It auto-initializes on first use, discovering devices,
// registering soft IRQ slots, and launching background goroutines that
// deliver typed events on channels.
//
// Usage:
//
//	keys, _ := input.Keyboard()
//	for ev := range keys {
//	    fmt.Printf("%s pressed=%v\n", ev.Key, ev.Pressed)
//	}
package input

import (
	"errors"
	"fmt"
	"mazzy/mazarin/sys"
	"mazzy/shared/hid"
	"sync"
)

// Soft IRQ slots — high numbers to avoid conflicts with dapope (0-3)
// and timer (30).
const (
	kbdSlot   = 28
	mouseSlot = 29
)

// KeyEvent represents a keyboard key press, release, or repeat.
type KeyEvent struct {
	Key     string // Human-readable: "A", "ENTER", "SPACE", "F1", etc.
	Code    uint16 // Raw evdev keycode for programmatic use
	Pressed bool   // true = press/repeat, false = release
	Repeat  bool   // true = auto-repeat (held down)
}

// MouseEvent represents one frame of mouse input (delivered on EV_SYN).
type MouseEvent struct {
	DX, DY  int    // Relative motion (Y-positive = up, screen coords)
	Wheel   int    // Scroll wheel delta (positive = up)
	Button  string // "Left", "Right", "Middle", or "" if no button change
	Pressed bool   // Button state (only meaningful when Button != "")
}

// Linux evdev event types
const (
	evSyn = 0
	evKey = 1
	evRel = 2
)

// REL axis codes
const (
	relX     = 0
	relY     = 1
	relWheel = 8
)

// Mouse button codes (Linux evdev)
const (
	btnLeft   = 0x110
	btnRight  = 0x111
	btnMiddle = 0x112
)

var (
	kbdOnce  sync.Once
	kbdErr   error
	kbdCh    chan KeyEvent
	kbdIRQ   uint32

	mouseOnce sync.Once
	mouseErr  error
	mouseCh   chan MouseEvent
	mouseIRQ  uint32

	devOnce sync.Once
	devErr  error
	devs    []hid.InputDeviceInfo
)

func discoverDevices() {
	devOnce.Do(func() {
		devs, devErr = sys.QueryInputDevices()
	})
}

// Keyboard returns a receive-only channel of keyboard events.
// The first call triggers device discovery and slot registration.
// Subsequent calls return the same channel.
func Keyboard() (<-chan KeyEvent, error) {
	discoverDevices()
	if devErr != nil {
		return nil, devErr
	}

	kbdOnce.Do(func() {
		for _, dev := range devs {
			if dev.DeviceType == hid.DeviceTypeKeyboard {
				if err := sys.RegisterSoftIRQ(dev.IRQNum, kbdSlot); err != nil {
					kbdErr = fmt.Errorf("input: register keyboard slot: %w", err)
					return
				}
				kbdCh = make(chan KeyEvent, 64)
				go keyboardLoop()
				return
			}
		}
		kbdErr = errors.New("input: no keyboard device found")
	})
	return kbdCh, kbdErr
}

// Mouse returns a receive-only channel of mouse events.
// The first call triggers device discovery and slot registration.
// Subsequent calls return the same channel.
func Mouse() (<-chan MouseEvent, error) {
	discoverDevices()
	if devErr != nil {
		return nil, devErr
	}

	mouseOnce.Do(func() {
		for _, dev := range devs {
			if dev.DeviceType == hid.DeviceTypeMouse {
				if err := sys.RegisterSoftIRQ(dev.IRQNum, mouseSlot); err != nil {
					mouseErr = fmt.Errorf("input: register mouse slot: %w", err)
					return
				}
				mouseCh = make(chan MouseEvent, 128)
				go mouseLoop()
				return
			}
		}
		mouseErr = errors.New("input: no mouse device found")
	})
	return mouseCh, mouseErr
}

func keyboardLoop() {
	var buf hid.SoftIRQReturn
	for {
		n, err := sys.WaitSoftIRQ(kbdSlot, &buf)
		if err != nil || n == 0 {
			continue
		}
		for i := 0; i < n; i++ {
			ev := buf.Events[i]
			if ev.Type != evKey {
				continue
			}
			ke := KeyEvent{
				Key:     keyName(ev.Code),
				Code:    ev.Code,
				Pressed: ev.Value != 0,
				Repeat:  ev.Value == 2,
			}
			select {
			case kbdCh <- ke:
			default:
				// Drop oldest to make room
				<-kbdCh
				kbdCh <- ke
			}
		}
	}
}

func mouseLoop() {
	var buf hid.SoftIRQReturn
	var dx, dy, wheel int
	var button string
	var buttonPressed bool
	var hasButton bool

	for {
		n, err := sys.WaitSoftIRQ(mouseSlot, &buf)
		if err != nil || n == 0 {
			continue
		}
		for i := 0; i < n; i++ {
			ev := buf.Events[i]
			switch ev.Type {
			case evRel:
				switch ev.Code {
				case relX:
					dx += int(int32(ev.Value))
				case relY:
					dy += int(int32(ev.Value))
				case relWheel:
					wheel += int(int32(ev.Value))
				}
			case evKey:
				button = buttonName(ev.Code)
				buttonPressed = ev.Value != 0
				hasButton = true
			case evSyn:
				if dx != 0 || dy != 0 || wheel != 0 || hasButton {
					me := MouseEvent{
						DX:      dx,
						DY:      -dy, // evdev top-positive → screen Y-up
						Wheel:   wheel,
						Button:  button,
						Pressed: buttonPressed,
					}
					if !hasButton {
						me.Button = ""
					}
					select {
					case mouseCh <- me:
					default:
						<-mouseCh
						mouseCh <- me
					}
					dx, dy, wheel = 0, 0, 0
					button = ""
					buttonPressed = false
					hasButton = false
				}
			}
		}
	}
}

func buttonName(code uint16) string {
	switch code {
	case btnLeft:
		return "Left"
	case btnRight:
		return "Right"
	case btnMiddle:
		return "Middle"
	default:
		return fmt.Sprintf("BTN_%d", code)
	}
}

// Key names for common keys (Linux evdev keycodes)
var keyNames = [256]string{
	1: "ESC", 2: "1", 3: "2", 4: "3", 5: "4", 6: "5", 7: "6", 8: "7", 9: "8", 10: "9",
	11: "0", 12: "-", 13: "=", 14: "BACKSPACE", 15: "TAB",
	16: "Q", 17: "W", 18: "E", 19: "R", 20: "T", 21: "Y", 22: "U", 23: "I", 24: "O", 25: "P",
	26: "[", 27: "]", 28: "ENTER", 29: "LCTRL",
	30: "A", 31: "S", 32: "D", 33: "F", 34: "G", 35: "H", 36: "J", 37: "K", 38: "L",
	39: ";", 40: "'", 41: "`", 42: "LSHIFT", 43: "\\",
	44: "Z", 45: "X", 46: "C", 47: "V", 48: "B", 49: "N", 50: "M",
	51: ",", 52: ".", 53: "/", 54: "RSHIFT", 55: "KP*",
	56: "LALT", 57: "SPACE", 58: "CAPSLOCK",
	59: "F1", 60: "F2", 61: "F3", 62: "F4", 63: "F5", 64: "F6",
	65: "F7", 66: "F8", 67: "F9", 68: "F10",
	87: "F11", 88: "F12",
	96: "KPENTER", 97: "RCTRL", 100: "RALT",
	102: "HOME", 103: "UP", 104: "PGUP",
	105: "LEFT", 106: "RIGHT",
	107: "END", 108: "DOWN", 109: "PGDN",
	110: "INSERT", 111: "DELETE",
}

func keyName(code uint16) string {
	if int(code) < len(keyNames) && keyNames[code] != "" {
		return keyNames[code]
	}
	return fmt.Sprintf("KEY_%d", code)
}
