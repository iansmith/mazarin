// rachel is the window manager shepherd. It claims the WM role to receive
// all input events, and routes focus to other shepherds via SetInputFocus.
//
// Uses the focus-based input routing system: RequestWindowManager gives rachel
// all keyboard, mouse-click, and mouse-move events via per-device-class queues.
package main

import (
	"fmt"
	"mazzy/mazarin/attr"
	"mazzy/mazarin/input"
	"mazzy/mazarin/sys"
	"mazzy/shared/hid"
	// mazhost import forces the linker to retain all runtime functions
	// needed by .maz thin stubs (via MazKeepAliveSymbols).
	_ "mazzy/mazarin/mazhost"
	"os"
	"runtime"
	"time"
	"unsafe"
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

// Input type tracking — when the type of input changes, we emit a newline
// so different input types appear on separate lines in the serial console.
const (
	inputNone     = 0
	inputKeyboard = 1
	inputButton   = 2
	inputWheel    = 3
)

var lastInputType int
var kbdDbgCount int

// switchInput prints a newline if the input type changed, then records
// the new type. Call before printing any event output.
func switchInput(newType int) {
	if lastInputType != inputNone && lastInputType != newType {
		fmt.Println()
	}
	lastInputType = newType
}

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

func keyboardLoop() {
	fmt.Println("[rachel] keyboard goroutine started (WM)")
	var buf hid.SoftIRQReturn
	var km input.Keymap
	// Track per-key held state to suppress repeats. QEMU on macOS sends
	// repeated EV_KEY value=1 (press) events for auto-repeat, not value=2.
	var keyHeld [256]bool
	for {
		n, err := sys.WaitInputEvent(hid.InputClassKeyboard, &buf)
		if err != nil {
			fmt.Printf("[rachel:kbd] WaitInputEvent error: %v\n", err)
			continue
		}
		for i := 0; i < n; i++ {
			ev := buf.Events[i]
			if ev.Type != EV_KEY {
				continue
			}
			code := ev.Code
			// DEBUG: dump first 10 keyboard events to serial
			if kbdDbgCount < 10 {
				kbdDbgCount++
				fmt.Fprintf(os.Stderr, "[kbd t=%d c=%d v=%d]", ev.Type, ev.Code, ev.Value)
			}
			if ev.Value == 1 { // press
				if code < 256 && keyHeld[code] {
					continue // suppress repeat (already held)
				}
				if code < 256 {
					keyHeld[code] = true
				}
			} else if ev.Value == 0 { // release
				if code < 256 {
					keyHeld[code] = false
				}
				continue // don't generate output for releases
			} else {
				continue // skip value=2 (explicit repeat) and other values
			}
			ke := input.KeyEvent{
				Code:    code,
				Pressed: true,
				Repeat:  false,
			}
			ch, action := km.Feed(ke)
			if ch != 0 {
				// DEBUG: confirm character generated (to serial via stderr)
				if kbdDbgCount < 20 {
					fmt.Fprintf(os.Stderr, "[ch=%c]", ch)
				}
				switchInput(inputKeyboard)
				fmt.Print(string(ch))
			} else if action == "enter" {
				switchInput(inputKeyboard)
				fmt.Println()
			} else if action == "backspace" {
				switchInput(inputKeyboard)
				fmt.Print("\b \b")
			} else if action == "tab" {
				switchInput(inputKeyboard)
				fmt.Print("\t")
			}
		}
	}
}

func mouseClickLoop() {
	fmt.Println("[rachel] mouse-click goroutine started (WM)")
	var buf hid.SoftIRQReturn
	for {
		n, err := sys.WaitInputEvent(hid.InputClassMouseClick, &buf)
		if err != nil {
			fmt.Printf("[rachel:click] WaitInputEvent error: %v\n", err)
			continue
		}
		for i := 0; i < n; i++ {
			ev := buf.Events[i]
			if ev.Type == EV_KEY {
				switchInput(inputButton)
				action := "pressed"
				if ev.Value == 0 {
					action = "released"
				}
				fmt.Printf("[rachel:mouse] %s %s\n", buttonName(ev.Code), action)
			}
		}
	}
}

func mouseMovementLoop() {
	fmt.Println("[rachel] mouse-move goroutine started (WM)")
	var buf hid.SoftIRQReturn
	batches := 0
	for {
		n, err := sys.WaitInputEvent(hid.InputClassMouseMove, &buf)
		if err != nil {
			fmt.Printf("[rachel:move] WaitInputEvent error: %v\n", err)
			continue
		}
		batches++
		for i := 0; i < n; i++ {
			ev := buf.Events[i]
			if ev.Type == EV_REL {
				if batches <= 3 {
					fmt.Fprintf(os.Stderr, "[rel c=%d v=%d]", ev.Code, int32(ev.Value))
				}
				if ev.Code == REL_WHEEL {
					switchInput(inputWheel)
					fmt.Printf("[rachel:mouse] wheel %+d\n", int32(ev.Value))
				}
			}
		}
	}
}

func main() {
	fmt.Println("[rachel] Starting window manager")

	// Claim window manager role — rachel gets ALL input events automatically
	if err := sys.RequestWindowManager(); err != nil {
		fmt.Printf("[rachel] failed to become window manager: %v\n", err)
		return
	}
	fmt.Println("[rachel] became window manager")

	// Launch event loops for all three device classes.
	// As WM, rachel receives all events and can handle global shortcuts,
	// log clicks, etc. For now, processes keyboard and logs mouse clicks.
	go keyboardLoop()
	runtime.Gosched()
	go mouseClickLoop()
	runtime.Gosched()
	go mouseMovementLoop()
	runtime.Gosched()

	// Stderr test (stdio shepherd disabled for now, but keep for diagnostics).
	go func() {
		time.Sleep(2 * time.Second)
		fmt.Fprintln(os.Stderr, "[rachel] stderr test: this should be dark red")
	}()

	// Load and launch .maz/.mzr test program
	hwPath := sys.LoadMazByName("/helloworld")
	fmt.Printf("[rachel] loading %s...\n", hwPath)
	mazResult, mazErr := sys.LoadMaz(hwPath)
	if mazErr != nil {
		fmt.Printf("[rachel] LoadMaz failed: %v\n", mazErr)
	} else {
		fmt.Printf("[rachel] loaded %s: entry=0x%X base=0x%X size=0x%X\n",
			hwPath, mazResult.EntryPoint, mazResult.LoadBase, mazResult.LoadSize)

		sys.RegisterMazModule(mazResult)

		// Convert entry point to a callable function and launch as goroutine
		type funcval struct{ fn uintptr }
		fv := &funcval{fn: uintptr(mazResult.EntryPoint)}
		mazMain := *(*func())(unsafe.Pointer(&fv))
		go runWithLargeStack(mazMain)
		fmt.Println("[rachel] .maz goroutine launched")
	}

	// Publish ready status to constraint network.
	attr.Init()
	ready := attr.ValueBool("attr:///shepherd/rachel/bool/ready", true)
	_ = ready
	fmt.Println("[rachel] ready=true published to constraint network")

	// Block main goroutine forever
	select {}
}

// runWithLargeStack allocates a 256KB stack frame before calling fn,
// preventing .maz code from hitting its broken morestack (which hangs
// forever due to uninitialized runtime globals in the PIE binary).
// The buffer is kept alive across fn() so GC's shrinkstack doesn't
// shrink the goroutine stack while .maz code is running.
//
//go:noinline
func runWithLargeStack(fn func()) {
	var buf [262144]byte
	buf[0] = 1
	buf[len(buf)-1] = 1
	if buf[131072] != 0 {
		panic("unreachable")
	}
	fn()
	runtime.KeepAlive(&buf)
}
