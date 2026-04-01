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
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/input"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/ringbuf"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/flat"
	"mazzy/shared/hid"
	"mazzy/shared/wm"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
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

// Input ring diagnostics
var inputEventsProcessed int
var inputWakeups int

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

// processInputEvent handles a single HID event from the shared completion ring.
// Classifies the event (keyboard, mouse button, mouse movement) and processes
// it in userspace, replacing the former keyboardLoop/mouseClickLoop/mouseMovementLoop
// goroutines.
func processInputEvent(ev hid.HIDEvent, km *input.Keymap, keyHeld *[256]bool) {
	switch ev.Type {
	case EV_KEY:
		if ev.Code < BTN_LEFT {
			// Keyboard event
			code := ev.Code
			if ev.Value == 1 { // press
				if code < 256 && keyHeld[code] {
					return // suppress repeat (already held)
				}
				if code < 256 {
					keyHeld[code] = true
				}
				// WM hotkey interception — consume and do not forward.
				if code == KEY_F1 {
					cycleFocus()
					return
				}
				forwardKeyboardEvent(code, true)
			} else if ev.Value == 0 { // release
				if code < 256 {
					keyHeld[code] = false
				}
				// Suppress release for consumed hotkeys.
				if code == KEY_F1 {
					return
				}
				forwardKeyboardEvent(code, false)
				return
			} else {
				return // skip value=2 (explicit repeat)
			}
			ke := input.KeyEvent{
				Code:    code,
				Pressed: true,
				Repeat:  false,
			}
			ch, action := km.Feed(ke)
			if ch != 0 {
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
		} else {
			// Mouse button event
			x, y := mouseX, mouseY
			if ev.Value == 1 { // press
				// Focus-change-on-click: clicking a different window
				// switches focus. Clicking empty space (rachel's desktop)
				// clears focus so rachel can handle WM-level interactions.
				hit := pickWindow(int64(x), int64(y))
				if hit < 0 {
					// Desktop click — defocus current window.
					if focusedSID >= 0 {
						if ta, ok := trackedApps[focusedSID]; ok && ta.returnRb != nil {
							var msg wm.YouLostFocusMsg
							msg.Type = wm.MsgYouLostFocus
							ta.returnRb.Push(unsafe.Pointer(&msg))
							_ = sys.MailboxSend(focusedSID, wm.ShepherdNotify, ta.returnRb.Addr())
						}
						focusedSID = -1
					}
					mouseButtonHeld = int32(ev.Code)
					// TODO: rachel desktop click handling (context menu, etc.)
					return
				}
				if hit != focusedSID {
					changeFocus(hit)
				}
				mouseButtonHeld = int32(ev.Code)
				forwardMouseEvent(wm.MsgMousePress, x, y, int32(ev.Code))
			} else if ev.Value == 0 { // release
				mouseButtonHeld = 0
				forwardMouseEvent(wm.MsgMouseRelease, x, y, int32(ev.Code))
			}
		}

	case EV_REL:
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
		}

	case EV_ABS:
		switch ev.Code {
		case hid.AbsX:
			mouseX = int32((uint32(ev.Value) * uint32(displayWidth)) / (hid.AbsMax + 1))
		case hid.AbsY:
			mouseY = int32((uint32(ev.Value) * uint32(displayHeight)) / (hid.AbsMax + 1))
		}
	}
}

// forwardKeyboardEvent sends a key press or release to the focused shepherd.
func forwardKeyboardEvent(code uint16, pressed bool) {
	sid := focusedSID
	if sid < 0 {
		return
	}
	ta, ok := trackedApps[sid]
	if !ok || ta.returnRb == nil {
		return
	}
	if pressed {
		var msg wm.KeyPressMsg
		msg.Type = wm.MsgKeyPress
		msg.Code = code
		ta.returnRb.Push(unsafe.Pointer(&msg))
	} else {
		var msg wm.KeyReleaseMsg
		msg.Type = wm.MsgKeyRelease
		msg.Code = code
		ta.returnRb.Push(unsafe.Pointer(&msg))
	}
	if err := sys.MailboxSend(sid, wm.ShepherdNotify, ta.returnRb.Addr()); err != nil {
		fmt.Fprintf(os.Stderr, "[rachel:kbd] MailboxSend to SID %d failed: %v\n", sid, err)
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
// Initialized to center of screen in main() after reading kernel dimensions.
var mouseX int32
var mouseY int32

// mouseButtonHeld tracks whether any mouse button is currently held.
// Set by mouseClickLoop on press, cleared on release.
// Read by mouseMovementLoop to decide whether to forward moves.
var mouseButtonHeld int32 // 0 = no button held, >0 = button code

// displayWidth and displayHeight are set at startup from kernel's screen
// dimension attributes. Mouse clamping and tablet mapping use these values.
var displayWidth int32
var displayHeight int32

// Framebuffer state — rachel owns the GPU framebuffer and is the only
// writer. Shepherds draw to their backing stores; rachel blits to here.
var fbCtx *mancini.FramebufferContext
var fbPix []byte  // raw pixel data of the GPU framebuffer
var fbStride int  // bytes per scanline of the GPU framebuffer

// flushRect tells the GPU to update a rectangular region of the framebuffer.
// Coordinates are clamped to the display bounds.
func flushRect(x, y, w, h int) {
	if fbCtx == nil {
		return
	}
	x0, y0 := x, y
	x1, y1 := x+w, y+h
	// Clamp to display bounds.
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	dw, dh := int(displayWidth), int(displayHeight)
	if x1 > dw {
		x1 = dw
	}
	if y1 > dh {
		y1 = dh
	}
	if x0 >= x1 || y0 >= y1 {
		return
	}
	fbCtx.Flush(int32(x0), int32(y0), int32(x1), int32(y1))
}

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

	invImg := generateInverseCursor()
	id, err = sys.RegisterCursor(invImg, 0, 0)
	if err != nil {
		fmt.Printf("[rachel] RegisterCursor(inverse) failed: %v\n", err)
		return
	}
	inverseCursorID = id

	// Set the standard cursor as the active cursor at startup.
	if err = sys.SetCursor(standardCursorID); err != nil {
		fmt.Printf("[rachel] SetCursor(standard) failed: %v\n", err)
	}
}

// pointInAnyAppBounds returns true if (x,y) is inside any tracked app's Bounds rectangle.
func pointInAnyAppBounds(x, y int64) bool {
	return pickWindow(x, y) >= 0
}

// postBatchInputUpdate runs after draining a full batch of input events from
// the shared ring. Handles mouse move forwarding and cursor state.
func postBatchInputUpdate() {
	// Forward move to focused shepherd while a button is held.
	if mouseButtonHeld != 0 {
		forwardMouseEvent(wm.MsgMouseMove, mouseX, mouseY, 0)
	}

	// Check cursor state (inverse when over app bounds).
	if standardCursorID >= 0 && inverseCursorID >= 0 {
		inApp := pointInAnyAppBounds(int64(mouseX), int64(mouseY))
		if inApp && !cursorIsInverse {
			if err := sys.SetCursor(inverseCursorID); err == nil {
				cursorIsInverse = true
			}
		} else if !inApp && cursorIsInverse {
			if err := sys.SetCursor(standardCursorID); err == nil {
				cursorIsInverse = false
			}
		}
	}
}

// Border sizes around each client's drawing area (in pixels).
const (
	borderTop    = 20
	borderRight  = 10
	borderBottom = 1
	borderLeft   = 10
)

// trackedApp holds rachel's state for a managed shepherd window.
type trackedApp struct {
	sid          int
	bounds       *attr.Attribute[vm.Value] // tracks shepherd's AppWindow/layout/Bounds
	returnRb     *ringbuf.RingBuffer       // ring buffer for sending messages back to this shepherd
	backingStore []byte                     // full buffer including borders (RGBA)
	x, y         int32                      // screen position of app area
	bsWidth      int32                      // total buffer width (app + borders)
	bsHeight     int32                      // total buffer height (app + borders)
	bsStride     int32                      // bytes per scanline (bsWidth * 4)
	appWidth     int32                      // client drawing area width
	appHeight    int32                      // client drawing area height
	zOrder       int                        // higher = on top; assigned at AppStart
}

// focusedSID is the SID of the shepherd that currently has input focus.
// -1 means no shepherd has focus.
var focusedSID = -1

// trackedApps maps SID → trackedApp for all shepherds rachel is managing.
var trackedApps = make(map[int]*trackedApp)

// zOrder is the window stack, front-to-back. zOrder[0] is the topmost window.
var zOrder []int

// raiseToFront moves sid to the front of the z-order stack.
// If sid is not in the stack, it is prepended.
func raiseToFront(sid int) {
	for i, s := range zOrder {
		if s == sid {
			// Remove from current position.
			zOrder = append(zOrder[:i], zOrder[i+1:]...)
			break
		}
	}
	zOrder = append([]int{sid}, zOrder...)
}

// removeFromZOrder removes sid from the z-order stack.
func removeFromZOrder(sid int) {
	for i, s := range zOrder {
		if s == sid {
			zOrder = append(zOrder[:i], zOrder[i+1:]...)
			return
		}
	}
}

// pickWindow returns the SID of the topmost window whose bounds contain (x,y),
// or -1 if no window is hit. Iterates front-to-back through zOrder.
func pickWindow(x, y int64) int {
	for _, sid := range zOrder {
		ta, ok := trackedApps[sid]
		if !ok {
			continue
		}
		v := ta.bounds.Get()
		if v.Type() == vm.TypeTribool {
			continue
		}
		x0, y0, x1, y1 := v.AsRectangle()
		if x0 <= x && x < x1 && y0 <= y && y < y1 {
			return sid
		}
	}
	return -1
}

// changeFocus switches input focus from the current focusedSID to newSID.
// Sends YouLostFocus to the old window and YouHaveFocus to the new one.
// Also raises newSID to the front of the z-order.
func changeFocus(newSID int) {
	if newSID == focusedSID {
		return
	}
	// Notify old focused window.
	if focusedSID >= 0 {
		if ta, ok := trackedApps[focusedSID]; ok && ta.returnRb != nil {
			var msg wm.YouLostFocusMsg
			msg.Type = wm.MsgYouLostFocus
			ta.returnRb.Push(unsafe.Pointer(&msg))
			_ = sys.MailboxSend(focusedSID, wm.ShepherdNotify, ta.returnRb.Addr())
		}
	}
	// Raise and focus new window.
	raiseToFront(newSID)
	focusedSID = newSID
	if ta, ok := trackedApps[newSID]; ok && ta.returnRb != nil {
		var msg wm.YouHaveFocusMsg
		msg.Type = wm.MsgYouHaveFocus
		ta.returnRb.Push(unsafe.Pointer(&msg))
		_ = sys.MailboxSend(newSID, wm.ShepherdNotify, ta.returnRb.Addr())
	}
}

// cycleFocus rotates the z-order: the current front window goes to back,
// and the new front window gets focus. Does nothing if fewer than 2 windows.
func cycleFocus() {
	if len(zOrder) < 2 {
		return
	}
	// Move front to back.
	front := zOrder[0]
	zOrder = append(zOrder[1:], front)
	// Focus the new front.
	changeFocus(zOrder[0])
}

// Linux evdev keycode for F1.
const KEY_F1 = 59

// blitAllWindows re-blits every tracked window back-to-front (z-order)
// so that overlapping windows are composited correctly.
func blitAllWindows() {
	// Walk z-order back-to-front (last element = backmost).
	for i := len(zOrder) - 1; i >= 0; i-- {
		sid := zOrder[i]
		ta, ok := trackedApps[sid]
		if !ok || ta.backingStore == nil {
			continue
		}
		drawBorders(ta)
		blitWindow(sid, fbPix, fbStride)
	}
	// Flush the entire display once.
	flushRect(0, 0, int(displayWidth), int(displayHeight))
}

// trackAppBounds creates a local constraint that mirrors a shepherd's
// AppWindow Bounds rectangle. Returns nil if the shepherd is not Ready
// or has no Bounds attribute (shepherd probably crashed).
func trackAppBounds(sid int) *trackedApp {
	sidStr := strconv.Itoa(sid)

	// Gate on Ready — the shepherd must have published Ready=true before
	// we read any of its constraint attributes.
	readyURI := wm.ReadyURI(sidStr)
	if !attr.Exists(readyURI) {
		return nil
	}

	// Check that the shepherd's AppWindow Bounds attribute exists.
	boundsURI := wm.AppWindowBoundsURI(sidStr)
	if !attr.Exists(boundsURI) {
		return nil
	}

	// Create a constraint in rachel's namespace that tracks the remote Bounds.
	prog := mancini.BindStrings(mancini.ProgIdentityRect, "_0_", boundsURI)
	localURI := attr.ShepherdURI("rect", "tracked/"+sidStr+"/Bounds")
	bounds := attr.ConstraintComposite(localURI, flat.TypeRectangle, prog)

	// Force initial evaluation to wire dependency edges.
	v := bounds.Get()
	if v.Type() == vm.TypeTribool {
		return nil
	}

	ta := &trackedApp{sid: sid, bounds: bounds}
	trackedApps[sid] = ta
	return ta
}

// forceFontSvcItab ensures the linker includes the fontcache.FontSvcInjector
// interface itab and method wrappers for (*FontSvcInit, FontSvcInjector).
// Without this, fontsvc.maz's type assertion fails because the host binary
// doesn't include the interface type in its typelinks.
//
//go:noinline
func forceFontSvcItab(v interface{}) {
	inj, ok := v.(fontcache.FontSvcInjector)
	if !ok {
		return
	}
	_ = inj.GetRachelChannel()
}

// mailboxLoop receives mailbox notifications forwarded by fontsvc.maz.
// fontsvc owns the MailboxRecv loop and forwards non-font notifications
// (WMNotify, ShepherdNotify, InputEventCode, etc.) to this channel.
// When an InputEventCode notification arrives, it drains the shared
// completion ring and classifies events in userspace.
func mailboxLoop(ch <-chan sys.MailboxNotification, inputRing *hid.CompletionRing) {
	var events [64]hid.HIDEvent
	var km input.Keymap
	var keyHeld [256]bool

	for notif := range ch {
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

					// Track the shepherd's AppWindow Bounds in rachel's constraint space.
					if trackAppBounds(senderSID) == nil {
						continue
					}

					// Create return ring buffer to the sender (reused for all future messages).
					returnRb, err := ringbuf.New(senderSID, 0, wm.SizeWMMessage, wm.DefaultSlotCount)
					if err != nil {
						fmt.Printf("[rachel:mailbox] return ring failed: %v\n", err)
						continue
					}
					ta := trackedApps[senderSID]
					ta.returnRb = returnRb

					// If client didn't specify a window size, skip backing store setup.
					// (linux shepherd draws directly to framebuffer, doesn't need one.)
					if msg.Width <= 0 || msg.Height <= 0 {
						fmt.Printf("[rachel:mailbox] SID %d: no window size, skipping backing store\n", senderSID)
						changeFocus(senderSID)
						continue
					}

					// Rachel owns the backing store. Compute total size including borders.
					ta.appWidth = msg.Width
					ta.appHeight = msg.Height
					totalW := int(msg.Width) + borderLeft + borderRight
					totalH := int(msg.Height) + borderTop + borderBottom
					ta.bsWidth = int32(totalW)
					ta.bsHeight = int32(totalH)
					ta.bsStride = int32(totalW * 4)

					// Clamp position so all 4 borders fit within the framebuffer.
					dw, dh := int(displayWidth), int(displayHeight)
					appX := int(msg.X)
					appY := int(msg.Y)
					if appX < borderLeft {
						appX = borderLeft
					}
					if appY < borderTop {
						appY = borderTop
					}
					if appX+int(msg.Width)+borderRight > dw {
						appX = dw - int(msg.Width) - borderRight
					}
					if appY+int(msg.Height)+borderBottom > dh {
						appY = dh - int(msg.Height) - borderBottom
					}
					ta.x = int32(appX)
					ta.y = int32(appY)

					// Allocate backing store pages.
					bsBytes := totalW * 4 * totalH
					bsPages := (bsBytes + 4095) / 4096
					bsSlice, allocErr := mem.AllocPagesSlice(bsPages, mem.PageShared)
					if allocErr != nil {
						fmt.Printf("[rachel:mailbox] SID %d: backing store alloc failed: %v\n", senderSID, allocErr)
						continue
					}
					ta.backingStore = bsSlice

					// Share pages with the client shepherd.
					bsVA := uintptr(unsafe.Pointer(&bsSlice[0]))
					clientVA, shareErr := sys.SharePagesWithTarget(senderSID, bsVA, bsPages)
					if shareErr != nil {
						fmt.Printf("[rachel:mailbox] SID %d: share pages failed: %v\n", senderSID, shareErr)
						continue
					}

					fmt.Printf("[rachel:mailbox] SID %d: backing store %dx%d (app %dx%d) at (%d,%d), clientVA=0x%x\n",
						senderSID, totalW, totalH, ta.appWidth, ta.appHeight, ta.x, ta.y, clientVA)

					// Send BackingStoreReady to client.
					var resp wm.BackingStoreReadyMsg
					resp.Type = wm.MsgBackingStoreReady
					resp.BackingStoreAddr = int64(clientVA)
					resp.TotalWidth = int32(totalW)
					resp.TotalHeight = int32(totalH)
					resp.TotalStride = int32(totalW * 4)
					resp.LeftInset = borderLeft
					resp.TopInset = borderTop
					resp.AppWidth = msg.Width
					resp.AppHeight = msg.Height
					resp.AppX = ta.x
					resp.AppY = ta.y
					ta.returnRb.Push(unsafe.Pointer(&resp))
					_ = sys.MailboxSend(senderSID, wm.ShepherdNotify, ta.returnRb.Addr())

					// Grant focus to this new shepherd.
					changeFocus(senderSID)

					// Initial blit (all windows, z-ordered).
					blitAllWindows()

				case wm.MsgBlit:
					msg := (*wm.BlitMsg)(unsafe.Pointer(&raw[0]))
					sid := int(msg.SID)
					ta, ok := trackedApps[sid]
					if !ok || ta.backingStore == nil {
						continue
					}
					drawBorders(ta)
					blitWindow(sid, fbPix, fbStride)
					ox, oy := screenOrigin(ta)
					flushRect(ox, oy, int(ta.bsWidth), int(ta.bsHeight))
				}
			}

		case hid.InputEventCode:
			// Drain shared completion ring and process all events
			batchTotal := 0
			for {
				n := sys.PollCompletionRing(inputRing, events[:], len(events))
				if n == 0 {
					break
				}
				batchTotal += n
				for i := 0; i < n; i++ {
					processInputEvent(events[i], &km, &keyHeld)
				}
			}
			inputEventsProcessed += batchTotal
			inputWakeups++
			if inputWakeups%100 == 0 {
				dropped := atomic.LoadUint32(&inputRing.Flags)
				sys.UartWriteString(fmt.Sprintf("[rachel:input] wakeups=%d events=%d dropped=%d\n",
					inputWakeups, inputEventsProcessed, dropped))
			}
			postBatchInputUpdate()

		default:
		}
	}
}

func main() {
	sys.UartWriteString("[rachel] Starting window manager\n")

	// Claim window manager role — rachel gets ALL input events automatically
	if err := sys.RequestWindowManager(); err != nil {
		fmt.Printf("[rachel] failed to become window manager: %v\n", err)
		return
	}
	// Initialize constraint system early — mailbox handler creates constraints.
	attr.Init()

	// Read kernel screen dimensions via constraints.
	screenWProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/width")
	screenW := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), screenWProg)

	screenHProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/height")
	screenH := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), screenHProg)

	w := int32(screenW.Get())
	h := int32(screenH.Get())
	// Set display dimensions for mouse clamping and initial cursor position.
	displayWidth = w
	displayHeight = h
	mouseX = w / 2
	mouseY = h / 2

	// Publish visibleArea as a Rectangle2D. For now, full screen (no decorations).
	// When we add taskbars/panels, the insets will shrink this area.
	visibleAreaRect := attr.ValueRectangle(
		attr.ShepherdURI("rect", "visibleArea"),
		vm.RectangleVal(0, 0, int64(w), int64(h)))
	_ = visibleAreaRect

	// Publish individual visibleArea edges as int64 values for constraint programs
	// that need to decompose the rectangle (VM lacks rect field accessors).
	vaX := attr.ValueI64(attr.ShepherdURI("int64", "visibleArea/x"), int64(0))
	vaY := attr.ValueI64(attr.ShepherdURI("int64", "visibleArea/y"), int64(0))
	vaW := attr.ValueI64(attr.ShepherdURI("int64", "visibleArea/w"), int64(w))
	vaH := attr.ValueI64(attr.ShepherdURI("int64", "visibleArea/h"), int64(h))
	_, _, _, _ = vaX, vaY, vaW, vaH
	// Register standard and inverse cursors with the GPU.
	initCursors()

	// Map the GPU framebuffer — rachel is the sole writer.
	fbCtx = mancini.NewFramebufferContext()
	fbImage := fbCtx.Image()
	fbPix = fbImage.Pix
	fbStride = fbImage.Stride

	// Allocate and register a shared completion ring for HID input events.
	// The kernel IRQ top-half writes events directly to this page;
	// rachel drains it when woken via mailbox notification.
	inputRingPage, err := mem.AllocPages(1, mem.PageShared)
	if err != nil {
		panic(fmt.Sprintf("[rachel] FATAL: AllocPages for input ring: %v", err))
	}
	inputRing := (*hid.CompletionRing)(inputRingPage)
	if err := sys.RegisterCompletionRing(uintptr(inputRingPage), 1); err != nil {
		panic(fmt.Sprintf("[rachel] FATAL: RegisterCompletionRing(input): %v", err))
	}

	// Wait for fs shepherd to be ready before loading .maz files.
	if err := sys.WaitForShepherdReady("fs", 10); err != nil {
		panic(fmt.Sprintf("[rachel] FATAL: fs: %v", err))
	}

	// Load fontsvc.maz — it owns the MailboxRecv loop and forwards non-font
	// notifications to rachel via a Go channel.
	rachelCh := make(chan sys.MailboxNotification, 32)

	// Force linker to include FontSvcInjector itab for cross-module type assertions.
	initData := &fontcache.FontSvcInit{RachelCh: rachelCh}
	forceFontSvcItab(initData)

	fontSvcPath := sys.LoadMazByName("/fontsvc")
	fontSvcMain, fontSvcInitAddr, fontSvcErr := mazhost.LoadMazBootstrap(fontSvcPath, nil)
	if fontSvcErr != nil {
		fmt.Printf("[rachel] LoadMazBootstrap(fontsvc) failed: %v\n", fontSvcErr)
		// Fall back to direct mailbox loop if fontsvc is not available.
		go func() {
			for {
				notif, err := sys.MailboxRecv()
				if err != nil {
					continue
				}
				rachelCh <- notif
			}
		}()
	} else {
		// Inject the rachel channel into fontsvc via MazarinShepherd.
		if fontSvcInitAddr != 0 {
			type funcval struct{ fn uintptr }
			fv := &funcval{fn: fontSvcInitAddr}
			shepherdInit := *(*func(interface{}) error)(unsafe.Pointer(&fv))
			if err := shepherdInit(initData); err != nil {
				fmt.Printf("[rachel] fontsvc MazarinShepherd failed: %v\n", err)
			}
		}
		// Start fontsvc's MailboxRecv loop (which forwards non-font messages to rachelCh).
		go mazhost.RunMaz(fontSvcMain)
	}
	runtime.Gosched()

	// Start mailbox receiver — reads from channel populated by fontsvc.
	// Also handles InputEventCode notifications by draining the shared ring.
	go mailboxLoop(rachelCh, inputRing)
	runtime.Gosched()

	// Stderr test (linux shepherd disabled for now, but keep for diagnostics).
	go func() {
		time.Sleep(2 * time.Second)
		fmt.Fprintln(os.Stderr, "[rachel] stderr test: this should be dark red")
	}()

	// Load and launch prefs.maz
	mazhost.LaunchMaz("prefs")

	// Publish ready status to constraint network using the well-known URI.
	ready := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = ready
	sys.SetReady(true)
	sys.UartWriteString("[rachel] Ready=true\n")

	// Block main goroutine forever
	select {}
}
