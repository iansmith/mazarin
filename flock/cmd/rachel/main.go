// rachel is the window manager shepherd. It claims the WM role to receive
// all input events, enriches them with position data, and forwards them
// to the focused shepherd via ring buffer IPC.
//
// Uses the focus-based input routing system: RequestWindowManager gives rachel
// all keyboard, mouse-click, and mouse-move events via per-device-class queues.
package main

import (
	"fmt"
	"mazzy/mazarin/attr"
	"mazzy/mazarin/input"
	"mazzy/mazarin/interactor"
	"mazzy/mazarin/ringbuf"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/flat"
	"mazzy/shared/hid"
	"mazzy/shared/wm"
	// mazhost import forces the linker to retain all runtime functions
	// needed by .maz thin stubs (via MazKeepAliveSymbols).
	_ "mazzy/mazarin/mazhost"
	"os"
	"runtime"
	"strconv"
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
			if ev.Type != EV_KEY {
				continue
			}
			switchInput(inputButton)
			x, y := mouseX, mouseY

			if ev.Value == 1 { // press
				mouseButtonHeld = int32(ev.Code)
				fmt.Printf("[rachel:mouse] %s pressed at (%d,%d)\n", buttonName(ev.Code), x, y)
				forwardMouseEvent(wm.MsgMousePress, x, y, int32(ev.Code))
			} else if ev.Value == 0 { // release
				mouseButtonHeld = 0
				fmt.Printf("[rachel:mouse] %s released at (%d,%d)\n", buttonName(ev.Code), x, y)
				forwardMouseEvent(wm.MsgMouseRelease, x, y, int32(ev.Code))
			}
		}
	}
}

// forwardMouseEvent sends a mouse event to the focused shepherd.
func forwardMouseEvent(msgType int64, x, y, button int32) {
	sid := focusedSID
	if sid < 0 {
		return
	}
	ta, ok := trackedApps[sid]
	if !ok || ta.returnRb == nil {
		return
	}
	switch msgType {
	case wm.MsgMousePress:
		var msg wm.MousePressMsg
		msg.Type = wm.MsgMousePress
		msg.X = x
		msg.Y = y
		msg.Button = button
		ta.returnRb.Push(unsafe.Pointer(&msg))
	case wm.MsgMouseRelease:
		var msg wm.MouseReleaseMsg
		msg.Type = wm.MsgMouseRelease
		msg.X = x
		msg.Y = y
		msg.Button = button
		ta.returnRb.Push(unsafe.Pointer(&msg))
	case wm.MsgMouseMove:
		var msg wm.MouseMoveMsg
		msg.Type = wm.MsgMouseMove
		msg.X = x
		msg.Y = y
		ta.returnRb.Push(unsafe.Pointer(&msg))
	}
	if err := sys.MailboxSend(sid, wm.ShepherdNotify, ta.returnRb.Addr()); err != nil {
		fmt.Fprintf(os.Stderr, "[rachel:mouse] MailboxSend to SID %d failed: %v\n", sid, err)
	}
}

// Cursor state — set by initCursors, used by mouseMovementLoop.
var standardCursorID = -1
var inverseCursorID = -1
var cursorIsInverse bool // current cursor state

// Mouse position — accumulated from relative events. Clamped to display.
var mouseX int32 = 864 // start at center of display
var mouseY int32 = 558

// mouseButtonHeld tracks whether any mouse button is currently held.
// Set by mouseClickLoop on press, cleared on release.
// Read by mouseMovementLoop to decide whether to forward moves.
var mouseButtonHeld int32 // 0 = no button held, >0 = button code

const displayWidth = 1728
const displayHeight = 1117

// generateStandardCursor returns a 64x64 NRGBA cursor image (white outline, black fill).
func generateStandardCursor() []byte {
	img := make([]byte, 64*64*4)
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			off := (y*64 + x) * 4
			v := cursorBitmap[y][x]
			switch v {
			case 0: // transparent
				img[off+0] = 0   // R
				img[off+1] = 0   // G
				img[off+2] = 0   // B
				img[off+3] = 0   // A
			case 1: // white outline
				img[off+0] = 255 // R
				img[off+1] = 255 // G
				img[off+2] = 255 // B
				img[off+3] = 255 // A
			case 2: // black fill
				img[off+0] = 0   // R
				img[off+1] = 0   // G
				img[off+2] = 0   // B
				img[off+3] = 255 // A
			}
		}
	}
	return img
}

// generateInverseCursor returns a 64x64 NRGBA cursor image (black outline, white fill).
func generateInverseCursor() []byte {
	img := make([]byte, 64*64*4)
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			off := (y*64 + x) * 4
			v := cursorBitmap[y][x]
			switch v {
			case 0: // transparent
				img[off+0] = 0   // R
				img[off+1] = 0   // G
				img[off+2] = 0   // B
				img[off+3] = 0   // A
			case 1: // black outline (inverted from white)
				img[off+0] = 0   // R
				img[off+1] = 0   // G
				img[off+2] = 0   // B
				img[off+3] = 255 // A
			case 2: // white fill (inverted from black)
				img[off+0] = 255 // R
				img[off+1] = 255 // G
				img[off+2] = 255 // B
				img[off+3] = 255 // A
			}
		}
	}
	return img
}

// cursorBitmap is the same arrow shape used by the kernel's built-in cursor.
// 0 = transparent, 1 = outline, 2 = fill.
var cursorBitmap = [64][64]byte{
	{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 1, 0, 1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 1, 0, 0, 1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 1, 0, 0, 0, 0, 1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 0, 0, 0, 0, 0, 1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
}

// initCursors generates and registers the standard and inverse cursor images.
func initCursors() {
	stdImg := generateStandardCursor()
	id, err := sys.RegisterCursor(stdImg, 0, 0)
	if err != nil {
		fmt.Printf("[rachel] RegisterCursor(standard) failed: %v\n", err)
		return
	}
	standardCursorID = id
	fmt.Printf("[rachel] registered standard cursor ID=%d\n", id)

	invImg := generateInverseCursor()
	id, err = sys.RegisterCursor(invImg, 0, 0)
	if err != nil {
		fmt.Printf("[rachel] RegisterCursor(inverse) failed: %v\n", err)
		return
	}
	inverseCursorID = id
	fmt.Printf("[rachel] registered inverse cursor ID=%d\n", id)

	// Set the standard cursor as the active cursor at startup.
	if err = sys.SetCursor(standardCursorID); err != nil {
		fmt.Printf("[rachel] SetCursor(standard) failed: %v\n", err)
	}
}

// pointInAnyAppBounds returns true if (x,y) is inside any tracked app's Bounds rectangle.
func pointInAnyAppBounds(x, y int32) bool {
	for _, ta := range trackedApps {
		v := ta.bounds.Get()
		if v.Type() == vm.TypeTribool {
			continue
		}
		x0, y0, x1, y1 := v.AsRectangle()
		if int32(x0) <= x && x < int32(x1) && int32(y0) <= y && y < int32(y1) {
			return true
		}
	}
	return false
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
			switch ev.Type {
			case EV_REL:
				if batches <= 3 {
					fmt.Fprintf(os.Stderr, "[rel c=%d v=%d]", ev.Code, int32(ev.Value))
				}
				switch ev.Code {
				case REL_X:
					mouseX += int32(ev.Value)
					if mouseX < 0 {
						mouseX = 0
					}
					if mouseX >= displayWidth {
						mouseX = displayWidth - 1
					}
				case REL_Y:
					mouseY += int32(ev.Value)
					if mouseY < 0 {
						mouseY = 0
					}
					if mouseY >= displayHeight {
						mouseY = displayHeight - 1
					}
				case REL_WHEEL:
					switchInput(inputWheel)
					fmt.Printf("[rachel:mouse] wheel %+d\n", int32(ev.Value))
				}
			case EV_ABS:
				// Tablet absolute coordinates (0-32767) → screen coordinates.
				switch ev.Code {
				case hid.AbsX:
					mouseX = int32((uint32(ev.Value) * displayWidth) / (hid.AbsMax + 1))
				case hid.AbsY:
					mouseY = int32((uint32(ev.Value) * displayHeight) / (hid.AbsMax + 1))
				}
			}
		}

		// Forward move to focused shepherd while a button is held.
		if mouseButtonHeld != 0 {
			forwardMouseEvent(wm.MsgMouseMove, mouseX, mouseY, 0)
		}

		// After processing all events in this batch, check cursor state.
		if standardCursorID >= 0 && inverseCursorID >= 0 {
			inApp := pointInAnyAppBounds(mouseX, mouseY)
			// Log every 50th batch so we can see position + state even without transitions.
			if batches%50 == 0 {
				cur := "std"
				if cursorIsInverse {
					cur = "inv"
				}
				in := "out"
				if inApp {
					in = "IN"
				}
				fmt.Fprintf(os.Stderr, "[rachel:pos] (%d,%d) %s %s batch=%d\n", mouseX, mouseY, cur, in, batches)
			}
			if inApp && !cursorIsInverse {
				if err := sys.SetCursor(inverseCursorID); err == nil {
					cursorIsInverse = true
					fmt.Fprintf(os.Stderr, "[rachel:cursor] → inverse at (%d,%d)\n", mouseX, mouseY)
				}
			} else if !inApp && cursorIsInverse {
				if err := sys.SetCursor(standardCursorID); err == nil {
					cursorIsInverse = false
					fmt.Fprintf(os.Stderr, "[rachel:cursor] → standard at (%d,%d)\n", mouseX, mouseY)
				}
			}
		}
	}
}

// trackedApp holds rachel's constraint handles for a managed shepherd.
type trackedApp struct {
	sid      int
	bounds   *attr.Handle[vm.Value]  // tracks shepherd's AppWindow/layout/Bounds
	returnRb *ringbuf.RingBuffer     // ring buffer for sending messages back to this shepherd
}

// focusedSID is the SID of the shepherd that currently has input focus.
// -1 means no shepherd has focus.
var focusedSID = -1

// trackedApps maps SID → trackedApp for all shepherds rachel is managing.
var trackedApps = make(map[int]*trackedApp)

// trackAppBounds creates a local constraint that mirrors a shepherd's
// AppWindow Bounds rectangle. Returns nil if the shepherd is not Ready
// or has no Bounds attribute (shepherd probably crashed).
func trackAppBounds(sid int) *trackedApp {
	sidStr := strconv.Itoa(sid)

	// Gate on Ready — the shepherd must have published Ready=true before
	// we read any of its constraint attributes.
	readyURI := wm.ReadyURI(sidStr)
	if !attr.Exists(readyURI) {
		fmt.Printf("[rachel:wm] SID %d has no Ready attribute — ignoring\n", sid)
		return nil
	}

	// Check that the shepherd's AppWindow Bounds attribute exists.
	boundsURI := wm.AppWindowBoundsURI(sidStr)
	if !attr.Exists(boundsURI) {
		fmt.Printf("[rachel:wm] SID %d Ready but no AppWindow Bounds — ignoring\n", sid)
		return nil
	}

	// Create a constraint in rachel's namespace that tracks the remote Bounds.
	prog := interactor.BindStrings(interactor.ProgIdentityRect, boundsURI)
	localURI := attr.ShepherdURI("rect", "tracked/"+sidStr+"/Bounds")
	bounds := attr.ConstraintComposite(localURI, flat.TypeRectangle, prog)

	// Force initial evaluation to wire dependency edges.
	v := bounds.Get()
	if v.Type() == vm.TypeTribool {
		fmt.Printf("[rachel:wm] SID %d Bounds deref returned unknown — ignoring\n", sid)
		return nil
	}
	x0, y0, x1, y1 := v.AsRectangle()
	fmt.Printf("[rachel:wm] tracking SID %d Bounds: (%d,%d)-(%d,%d)\n", sid, x0, y0, x1, y1)

	ta := &trackedApp{sid: sid, bounds: bounds}
	trackedApps[sid] = ta
	return ta
}

// mailboxLoop receives mailbox notifications from other shepherds.
// When a shepherd sends AppStart, rachel sets input focus for it
// and sends YouHaveFocus back via a return ring buffer.
func mailboxLoop() {
	fmt.Println("[rachel] mailbox goroutine started")
	for {
		notif, err := sys.MailboxRecv()
		if err != nil {
			fmt.Printf("[rachel:mailbox] recv error: %v\n", err)
			continue
		}

		switch notif.Code {
		case wm.WMNotify:
			// A shepherd sent us a message — open ring at translated VA
			rb := ringbuf.Open(uintptr(notif.RingAddr))
			var raw [wm.SizeWMMessage]byte
			for rb.Pop(unsafe.Pointer(&raw[0])) {
				msgType := *(*int64)(unsafe.Pointer(&raw[0]))
				switch msgType {
				case wm.MsgAppStart:
					msg := (*wm.AppStartMsg)(unsafe.Pointer(&raw[0]))
					senderSID := int(msg.SID)
					fmt.Printf("[rachel:mailbox] AppStart from SID %d\n", senderSID)

					// Track the shepherd's AppWindow Bounds in rachel's constraint space.
					// If the Bounds attribute doesn't exist, the shepherd crashed or
					// didn't publish constraints — ignore the AppStart.
					if trackAppBounds(senderSID) == nil {
						fmt.Printf("[rachel:mailbox] SID %d has no trackable Bounds, skipping\n", senderSID)
						continue
					}

					// Create return ring buffer to the sender (reused for all future messages).
					returnRb, err := ringbuf.New(senderSID, 0, wm.SizeWMMessage, wm.DefaultSlotCount)
					if err != nil {
						fmt.Printf("[rachel:mailbox] return ring failed: %v\n", err)
						continue
					}
					trackedApps[senderSID].returnRb = returnRb

					// Grant focus to this shepherd.
					focusedSID = senderSID
					fmt.Printf("[rachel:mailbox] set focus → SID %d\n", senderSID)

					// Send YouHaveFocus
					var focusMsg wm.YouHaveFocusMsg
					focusMsg.Type = wm.MsgYouHaveFocus
					returnRb.Push(unsafe.Pointer(&focusMsg))
					if err := sys.MailboxSend(senderSID, wm.ShepherdNotify, returnRb.Addr()); err != nil {
						fmt.Printf("[rachel:mailbox] send YouHaveFocus failed: %v\n", err)
					} else {
						fmt.Printf("[rachel:mailbox] sent YouHaveFocus → SID %d\n", senderSID)
					}

				default:
					fmt.Printf("[rachel:mailbox] unknown msg type %d from SID %d\n", msgType, notif.SenderSID)
				}
			}

		default:
			fmt.Printf("[rachel:mailbox] unknown notify code %d from SID %d\n", notif.Code, notif.SenderSID)
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

	// Initialize constraint system early — mailbox handler creates constraints.
	attr.Init()
	fmt.Printf("[rachel] attr init done (SID=%s)\n", attr.SID())

	// Register standard and inverse cursors with the GPU.
	initCursors()

	// Start mailbox receiver — handles AppStart from other shepherds.
	go mailboxLoop()
	runtime.Gosched()

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

	// Publish ready status to constraint network using the well-known URI.
	ready := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = ready
	fmt.Printf("[rachel] Ready=true published (SID=%s)\n", attr.SID())

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
