// dapope is a userspace priest that receives keyboard and mouse events
// via the soft IRQ mechanism. It discovers available input devices,
// registers for their IRQs, and blocks waiting for HID events.
//
// Each device gets its own goroutine that blocks on WaitSoftIRQ (via Syscall6),
// allowing the Go runtime to hand off the M and run other goroutines.
package main

import (
	"fmt"
	"mazzy/flock/cmd/dapope/cursorgen"
	"mazzy/mazarin/gui/core"
	"mazzy/mazarin/input"
	"mazzy/mazarin/sys"
	"mazzy/shared/hid"
)

// Linux evdev event types
const (
	EV_SYN = 0
	EV_KEY = 1
	EV_REL = 2
	EV_ABS = 3
)

// REL axis codes
const (
	REL_X     = 0
	REL_Y     = 1
	REL_WHEEL = 8
)

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

// Mouse button codes (Linux evdev)
const (
	BTN_LEFT   = 0x110
	BTN_RIGHT  = 0x111
	BTN_MIDDLE = 0x112
)

func keyName(code uint16) string {
	if int(code) < len(keyNames) && keyNames[code] != "" {
		return keyNames[code]
	}
	return fmt.Sprintf("KEY_%d", code)
}

func buttonName(code uint16) string {
	switch code {
	case BTN_LEFT:
		return "LEFT"
	case BTN_RIGHT:
		return "RIGHT"
	case BTN_MIDDLE:
		return "MIDDLE"
	default:
		return fmt.Sprintf("BTN_%d", code)
	}
}

func keyboardLoop(slot int) {
	fmt.Printf("[dapope] keyboard goroutine started on slot %d\n", slot)
	var buf hid.SoftIRQReturn
	var km input.Keymap
	for {
		n, err := sys.WaitSoftIRQ(slot, &buf)
		if err != nil {
			fmt.Printf("[dapope:kbd] WaitSoftIRQ error: %v\n", err)
			continue
		}
		for i := 0; i < n; i++ {
			ev := buf.Events[i]
			if ev.Type != EV_KEY {
				continue
			}
			ke := input.KeyEvent{
				Code:    ev.Code,
				Pressed: ev.Value != 0,
				Repeat:  ev.Value == 2,
			}
			ch, action := km.Feed(ke)
			if !ke.Pressed {
				continue
			}
			if ch != 0 {
				fmt.Print(string(ch))
			} else if action == "enter" {
				fmt.Println()
			} else if action == "backspace" {
				fmt.Print("\b \b")
			} else if action == "tab" {
				fmt.Print("\t")
			}
		}
	}
}

func mouseLoop(slot int, stack core.CursorStack, images core.CursorImageMap, renderer *cursorRenderer) {
	fmt.Printf("[dapope] mouse goroutine started on slot %d\n", slot)
	var buf hid.SoftIRQReturn
	var dx, dy int
	for {
		n, err := sys.WaitSoftIRQ(slot, &buf)
		if err != nil {
			fmt.Printf("[dapope:mouse] WaitSoftIRQ error: %v\n", err)
			continue
		}
		for i := 0; i < n; i++ {
			ev := buf.Events[i]
			switch ev.Type {
			case EV_REL:
				switch ev.Code {
				case REL_X:
					dx += int(int32(ev.Value))
				case REL_Y:
					dy += int(int32(ev.Value))
				case REL_WHEEL:
					fmt.Printf("[dapope:mouse] wheel %+d\n", int32(ev.Value))
				}
			case EV_KEY:
				action := "pressed"
				if ev.Value == 0 {
					action = "released"
				}
				fmt.Printf("[dapope:mouse] %s %s\n", buttonName(ev.Code), action)
			case EV_SYN:
				if dx != 0 || dy != 0 {
					// evdev REL_Y is screen-top-positive; negate for quadrant-1
					stack.Move(dx, -dy)
					renderer.Draw(stack, images)
					dx, dy = 0, 0
				}
			}
		}
	}
}

func timerLoop(clock *clockRenderer, slot int) {
	fmt.Printf("[dapope:timer] timer goroutine started on slot %d\n", slot)
	var buf hid.SoftIRQReturn
	tick := 0
	for {
		ts, err := sys.GetTime()
		if err != nil {
			fmt.Printf("[dapope:timer] GetTime error: %v\n", err)
			return
		}
		// Set deadline 1 second from now
		if err := sys.SetTimerDeadline(slot, ts.Seconds+1, ts.Nanoseconds); err != nil {
			fmt.Printf("[dapope:timer] SetTimerDeadline error: %v\n", err)
			continue
		}
		n, err := sys.WaitSoftIRQ(slot, &buf)
		if err != nil || n == 0 {
			continue
		}
		// Extract time from events: [0]=sec low, [1]=nsec, [2]=sec high
		sec := uint64(buf.Events[0].Value)
		nsec := uint64(buf.Events[1].Value)
		if n >= 3 {
			sec |= uint64(buf.Events[2].Value) << 32
		}
		clock.Update(sys.TimeSpec{Seconds: sec, Nanoseconds: nsec})
		tick++
	}
}

func main() {
	sys.RegisterAsyncPreempt()

	fmt.Println("[dapope] Starting input event handler")

	devices, err := sys.QueryInputDevices()
	if err != nil {
		fmt.Printf("[dapope] QueryInputDevices failed: %v\n", err)
		return
	}

	if len(devices) == 0 {
		fmt.Println("[dapope] No input devices found")
		return
	}

	fmt.Printf("[dapope] Found %d input device(s)\n", len(devices))

	kbdSlot := -1
	mouseSlot := -1
	timerSlot := -1
	for i, dev := range devices {
		// Skip serial devices — handled by stdio priest
		if dev.DeviceType == hid.DeviceTypeSerial {
			continue
		}

		typeName := "keyboard"
		if dev.DeviceType == hid.DeviceTypeMouse {
			typeName = "mouse"
		} else if dev.DeviceType == hid.DeviceTypeTimer {
			typeName = "timer"
		}

		if err := sys.RegisterSoftIRQ(dev.IRQNum, i); err != nil {
			fmt.Printf("[dapope] RegisterSoftIRQ slot %d failed: %v\n", i, err)
			continue
		}
		fmt.Printf("[dapope] Registered %s on slot %d (IRQ %d)\n",
			typeName, i, dev.IRQNum)

		if dev.DeviceType == hid.DeviceTypeMouse {
			mouseSlot = i
		} else if dev.DeviceType == hid.DeviceTypeTimer {
			timerSlot = i
		} else {
			kbdSlot = i
		}
	}

	cursorImages := NewCursorImageDefault()
	cursorStack := NewCursorStackDefault()

	// Set up framebuffer and cursor rendering
	fb, err := sys.GetFramebuffer()
	if err != nil {
		fmt.Printf("[dapope] GetFramebuffer failed: %v\n", err)
		return
	}
	fmt.Printf("[dapope] Framebuffer: %dx%d pitch=%d addr=0x%x\n",
		fb.Width, fb.Height, fb.Pitch, fb.Addr)

	// Generate arrow cursor and register it
	arrowImg := cursorgen.GenerateArrowCursor()
	arrowCursor := cursorImages.CursorImageAdd(arrowImg)
	cursorStack.Bottom(arrowCursor)
	cursorStack.SetPosition(960, 540)

	renderer := newCursorRenderer(fb)
	// Don't draw cursor yet — the framebuffer background hasn't been
	// painted by other priests (stdio). The first mouse event will
	// trigger the initial draw, capturing the correct backing store.
	fmt.Println("[dapope] Cursor ready (deferred until first mouse event)")

	// Launch goroutines that block on WaitSoftIRQ (via Syscall6).
	// The Go runtime M-handoff allows both to block independently.
	if kbdSlot >= 0 {
		go keyboardLoop(kbdSlot)
	}
	if mouseSlot >= 0 {
		go mouseLoop(mouseSlot, cursorStack, cursorImages, renderer)
	}

	// Launch periodic timer goroutine with clock display
	clock := newClockRenderer(fb)
	if clock != nil && timerSlot >= 0 {
		// Render initial time immediately
		if ts, err := sys.GetTime(); err == nil {
			clock.Update(ts)
		}
		go timerLoop(clock, timerSlot)
	}

	// Block main goroutine forever
	select {}
}
