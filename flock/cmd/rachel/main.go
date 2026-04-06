// rachel is the window manager shepherd. It claims the WM role to receive
// all input events, enriches them with position data, and forwards them
// to the focused shepherd via ring buffer IPC.
//
// Uses the focus-based input routing system: RequestWindowManager gives rachel
// all keyboard, mouse-click, and mouse-move events via per-device-class queues.
package main

import (
	"fmt"
	"image"
	"image/color"
	"mazzy/mazarin/attr"
	"mazzy/mazarin/file"
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/input"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	mctheme "mazzy/mazarin/mancini/theme"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/flat"
	"mazzy/shared/constants"
	mfont "mazzy/shared/font"
	"mazzy/shared/hid"
	"mazzy/shared/ipc"
	"mazzy/shared/wm"
	"os"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	toml "github.com/pelletier/go-toml/v2"
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
	inputNone  = 0
	inputWheel = 3
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

// processRawEvent handles pre-dispatch processing for a single HID event:
// modifier state tracking, cursor position accumulation, repeat suppression.
// For EV_KEY events (keyboard and mouse buttons), it creates an InputEvent
// and dispatches through the subArctic pipeline.
func processRawEvent(ev hid.HIDEvent, keyHeld *[256]bool, modState *input.ModifierState,
	posChanged *bool, wmd *wmDispatch) {

	switch ev.Type {
	case EV_KEY:
		// Debug: log button presses with pick result.
		if ev.Code >= BTN_LEFT {
			pw := pickWindow(int64(mouseX), int64(mouseY))
			sys.UartWriteString(fmt.Sprintf("[rachel:btn] code=0x%x val=%d at (%d,%d) pick=%d\n",
				ev.Code, ev.Value, mouseX, mouseY, pw))
		}
		// Update modifier state; consume modifier key events (not forwarded).
		if input.IsModifierKey(ev.Code) {
			modState.Update(ev.Code, ev.Value == 1)
			return
		}

		if ev.Code < BTN_LEFT {
			// Keyboard: suppress repeats.
			if ev.Value == 1 {
				if ev.Code < 256 && keyHeld[ev.Code] {
					return
				}
				if ev.Code < 256 {
					keyHeld[ev.Code] = true
				}
			} else if ev.Value == 0 {
				if ev.Code < 256 {
					keyHeld[ev.Code] = false
				}
			} else {
				return // skip value=2 (explicit repeat)
			}
		}

		// Background click: if a mouse button is pressed and nothing is
		// under the cursor, revoke all focus. This is handled outside the
		// dispatch pipeline because an empty pick list means no agent fires.
		if ev.Code >= BTN_LEFT && ev.Value == 1 {
			if pickWindow(int64(mouseX), int64(mouseY)) < 0 {
				revokeFocus()
				wmd.keyFwd.SetFocus(nil)
				// Also cancel any pending focus-change FSM.
				wmd.focusChangeAgent.reset()
				return
			}
		}

		// Dispatch through the subArctic pipeline.
		inputEv := &input.InputEvent{
			Type:  ev.Type,
			Code:  ev.Code,
			Value: ev.Value,
			X:     mouseX,
			Y:     mouseY,
			Mods:  modState.Mods(),
		}
		wmd.dispatcher.Dispatch(inputEv)

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
		*posChanged = true

	case EV_ABS:
		switch ev.Code {
		case hid.AbsX:
			mouseX = int32((uint32(ev.Value) * uint32(displayWidth)) / (hid.AbsMax + 1))
			if mouseX < 0 {
				mouseX = 0
			}
			if mouseX >= displayWidth {
				mouseX = displayWidth - 1
			}
		case hid.AbsY:
			mouseY = int32((uint32(ev.Value) * uint32(displayHeight)) / (hid.AbsMax + 1))
			if mouseY < 0 {
				mouseY = 0
			}
			if mouseY >= displayHeight {
				mouseY = displayHeight - 1
			}
		}
		*posChanged = true
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


// displayWidth and displayHeight are set at startup from kernel's screen
// dimension attributes. Mouse clamping and tablet mapping use these values.
var displayWidth int32
var displayHeight int32

// blitRateStart is the time rachel became ready (for blit rate reporting).
var blitRateStart time.Time

// Framebuffer state — rachel owns the GPU framebuffer and is the only
// writer. Shepherds draw to their backing stores; rachel blits to here.
var fbCtx *mancini.FramebufferContext
var fbPix []byte  // raw pixel data of the GPU framebuffer
var fbStride int  // bytes per scanline of the GPU framebuffer

// Drag compositing state — pre-rendered background for fast window dragging.
var dragBG []byte            // screen-sized buffer, allocated lazily on first drag
var dragBGStride int         // = int(displayWidth) * 4
var dragActive bool          // true during a titlebar drag
var dragSID int              // SID of the window being dragged
var dragPrevRect image.Rectangle // previous screen rect of the dragged window

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

// postBatchInputUpdate runs after draining a full batch of input events.
// Dispatches a synthetic mouse-move event if the cursor moved (for drag
// forwarding via the DragAgent) and updates cursor appearance.
func postBatchInputUpdate(posChanged *bool, modState *input.ModifierState, wmd *wmDispatch) {
	// Check focus-change agent's double/triple-click timer.
	wmd.focusChangeAgent.CheckTimer()

	if *posChanged {
		// Dispatch synthetic mouse-move through the pipeline.
		// The DragAgent (focus policy) will forward to the drag target
		// if a drag is active; otherwise the event is not consumed.
		moveEv := &input.InputEvent{
			Type: input.EvMouseMove,
			X:    mouseX,
			Y:    mouseY,
			Mods: modState.Mods(),
		}
		wmd.dispatcher.Dispatch(moveEv)
		*posChanged = false
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
// Initialized from wmTheme in main(). Must be large enough for the active
// SurfaceStyle's shadows + title bar.
var (
	borderTop    = 52
	borderRight  = 60
	borderBottom = 60
	borderLeft   = 30

	titleBarHeight = 20 // height of the title bar in pixels
	shadowTop      = 30 // shadow margin above the NeuBox face
)

// wmTheme provides decoration parameters for rachel. Initialized in main().
var wmTheme *mctheme.DefaultWMTheme

// pal is the shared palette for rachel's decoration rendering and compositing.
// Initialized in main() before any windows are tracked.
var pal *mctheme.DefaultPalette

// desktopBG is the desktop background color in NRGBA (R,G,B order).
// The framebuffer uses BGRA byte order; this is the logical color.
// Set from pal.DesktopBG() during initialization.
var desktopBG color.NRGBA

// trackedApp holds rachel's state for a managed shepherd window.
type trackedApp struct {
	sid          int
	title        string                     // window title from AppWindow.Title attribute
	bounds       *attr.Attribute[vm.Value] // tracks shepherd's AppWindow/layout/Bounds
	titleAttr    *attr.Attribute[string]    // constrained to track shepherd's AppWindow/Title
	bgColorAttr  *attr.Attribute[int64]     // constrained to track shepherd's Palette/Surface
	backingStore []byte                     // full buffer including borders (RGBA)
	decorFocused []byte                     // pre-rendered border pixels (Raised)
	decorUnfocused []byte                   // pre-rendered border pixels (Flush)
	x, y         int32                      // screen position of app area
	bsWidth      int32                      // total buffer width (app + borders)
	bsHeight     int32                      // total buffer height (app + borders)
	bsStride     int32                      // bytes per scanline (bsWidth * 4)
	appWidth     int32                      // client drawing area width
	appHeight    int32                      // client drawing area height
	zOrder       int                        // higher = on top; assigned at AppStart
	interactor   *WindowInteractor          // dispatch target for this window
}

// keyboardFocusSID is the SID of the shepherd that currently has keyboard focus.
// -1 means no shepherd has keyboard focus.
var keyboardFocusSID = -1

// mouseFocusSID is the SID of the shepherd that currently has mouse focus.
// -1 means no shepherd has mouse focus.
var mouseFocusSID = -1

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
// Uses rachel's local position (includes borders) rather than the shepherd's
// constraint bounds which may be at (0,0).
func pickWindow(x, y int64) int {
	for _, sid := range zOrder {
		ta, ok := trackedApps[sid]
		if !ok {
			continue
		}
		ox := int64(ta.x) - int64(borderLeft)
		oy := int64(ta.y) - int64(borderTop)
		if x >= ox && x < ox+int64(ta.bsWidth) && y >= oy && y < oy+int64(ta.bsHeight) {
			return sid
		}
	}
	return -1
}

// moveWindowTo updates a window's screen position and redraws.
// Used by DragAgent during titlebar drag.
// Constraint 1: the cursor (drag point) cannot leave the screen — enforced
//   by the input layer (QEMU clamps mouse coords), so no extra work here.
// Constraint 2: the leftmost 50px of the title bar must remain on screen.
//   The title bar left edge is at ta.x in screen coords.
func moveWindowTo(ta *trackedApp, newX, newY int32) {
	dw := int32(displayWidth)
	const minTitleVisible int32 = 50

	// Horizontal: at least minTitleVisible pixels of titlebar on screen.
	// Title bar left edge = newX, so newX must be >= 0 (can't go off left)
	// and newX + minTitleVisible <= dw (grab area stays on screen right).
	if newX < 0 {
		newX = 0
	}
	if newX+minTitleVisible > dw {
		newX = dw - minTitleVisible
	}

	// Vertical: keep backing store top on screen (can't blit above y=0).
	// No bottom clamp — window may extend below screen, clipped by exposedRegion.
	if newY-int32(borderTop) < 0 {
		newY = int32(borderTop)
	}

	if dragActive && ta.sid == dragSID {
		oldRect := dragPrevRect
		ta.x = newX
		ta.y = newY
		newRect := windowVisibleRect(ta, true) // focused = face + light shadow pad

		// 1. Restore newly-exposed area from drag background.
		exposed := rectSubtract(oldRect, newRect)
		for _, r := range exposed {
			copyRectFromBuffer(fbPix, fbStride, dragBG, dragBGStride, r)
		}

		// 2. Restore shadow zone background from dragBG for correct alpha.
		face := faceScreenRect(ta)
		shadowRect := newRect // newRect = face expanded by lightShadowPad
		// The shadow zone is the difference between the visible rect and the face.
		shadowStrips := rectSubtract(shadowRect, face)
		for _, s := range shadowStrips {
			copyRectFromBuffer(fbPix, fbStride, dragBG, dragBGStride, s)
		}

		// 3. Blit the dragged window at its new position (with alpha).
		blitWindow(ta.sid, []image.Rectangle{newRect}, fbPix, fbStride, true)

		// 4. Flush only the union of old and new rects.
		union := oldRect.Union(newRect)
		flushRect(union.Min.X, union.Min.Y, union.Dx(), union.Dy())

		dragPrevRect = newRect
		return
	}

	ta.x = newX
	ta.y = newY
	timedBlitAllWindows()
}

// grantFocus gives both keyboard and mouse focus to newSID and raises it
// to the front of the z-order. This is the single-click-on-unfocused-window path.
func grantFocus(newSID int) {
	grantFocusNoRaise(newSID)
	raiseToFront(newSID)
	timedBlitAllWindows() // re-composite with new z-order
}

// grantFocusNoRaise gives both keyboard and mouse focus to newSID without
// changing the z-order. This is the double-click-on-unfocused-window path.
func grantFocusNoRaise(newSID int) {
	revokeFocus()
	keyboardFocusSID = newSID
	mouseFocusSID = newSID
	if ta, ok := trackedApps[newSID]; ok {
		msg := wm.EncodeKeyboardFocusGained()
		_ = uring.Send(newSID, &msg)
		msg = wm.EncodeMouseFocusGained()
		_ = uring.Send(newSID, &msg)
		applyDecorations(ta, true)
	}
}

// revokeFocus clears both keyboard and mouse focus, notifying the current
// focused shepherd(s). Does nothing if no shepherd has focus.
func revokeFocus() {
	// Swap to unfocused decoration (fast copy from cache).
	if keyboardFocusSID >= 0 {
		if ta, ok := trackedApps[keyboardFocusSID]; ok {
			msg := wm.EncodeKeyboardFocusLost()
			_ = uring.Send(keyboardFocusSID, &msg)
			applyDecorations(ta, false)
		}
	}
	if mouseFocusSID >= 0 {
		if _, ok := trackedApps[mouseFocusSID]; ok {
			msg := wm.EncodeMouseFocusLost()
			_ = uring.Send(mouseFocusSID, &msg)
		}
	}
	keyboardFocusSID = -1
	mouseFocusSID = -1
}

// hasFocus returns true if sid currently has both keyboard and mouse focus.
func hasFocus(sid int) bool {
	return sid == keyboardFocusSID && sid == mouseFocusSID
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
	grantFocus(zOrder[0])
}

// Linux evdev keycode for F1.
const KEY_F1 = 59

// blitAllWindows re-blits every tracked window back-to-front (z-order)
// so that overlapping windows are composited correctly.
func blitAllWindows() {
	// Clear framebuffer to desktop background color.
	for i := 0; i+3 < len(fbPix); i += 4 {
		fbPix[i] = desktopBG.B
		fbPix[i+1] = desktopBG.G
		fbPix[i+2] = desktopBG.R
		fbPix[i+3] = desktopBG.A
	}

	// Walk z-order back-to-front (last element = backmost).
	// Unfocused windows blit only their face area (titlebar + content).
	// The focused window blits the full buffer including neumorphic shadows.
	for i := len(zOrder) - 1; i >= 0; i-- {
		sid := zOrder[i]
		ta, ok := trackedApps[sid]
		if !ok || ta.backingStore == nil {
			continue
		}
		focused := sid == mouseFocusSID
		regions := exposedRegion(sid)
		if !focused {
			// Clip exposed regions to face area — no shadow borders.
			face := faceScreenRect(ta)
			var clipped []image.Rectangle
			for _, r := range regions {
				isect := r.Intersect(face)
				if !isect.Empty() {
					clipped = append(clipped, isect)
				}
			}
			regions = clipped
		}
		blitWindow(sid, regions, fbPix, fbStride, focused)
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

	// Create a constraint tracking the shepherd's AppWindow title.
	var title string
	remoteTitleURI := wm.AppWindowTitleURI(sidStr)
	if t, ok := attr.DerefStr(remoteTitleURI); ok {
		title = t
	}
	localTitleURI := attr.ShepherdURI("string", "tracked/"+sidStr+"/Title")
	titleAttr := attr.ConstraintStr(localTitleURI, mancini.EqualStr(remoteTitleURI))
	titleAttr.SetEager(true)

	// Create a constraint tracking the shepherd's Palette/Surface color.
	remoteBgURI := wm.PaletteColorURI(sidStr, "Surface")
	localBgURI := attr.ShepherdURI("int64", "tracked/"+sidStr+"/BgColor")
	bgColorAttr := attr.ConstraintI64(localBgURI, mancini.EqualI64(remoteBgURI))
	bgColorAttr.SetEager(true)

	ta := &trackedApp{sid: sid, title: title, bounds: bounds, titleAttr: titleAttr, bgColorAttr: bgColorAttr}
	trackedApps[sid] = ta
	return ta
}

// forceKeyMapperItab ensures the linker includes the mancini.KeyMapperInjector
// interface itab and method wrappers for (*KeyMapperInit, KeyMapperInjector).
// Without this, keymapper.maz's type assertion fails because the host binary
// doesn't include the interface type in its typelinks.
//
//go:noinline
func forceKeyMapperItab(v interface{}) {
	inj, ok := v.(mancini.KeyMapperInjector)
	if !ok {
		return
	}
	inj.GetKeymapName()
	inj.RegisterKeyMapper(nil)
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
	inj.RegisterOpenFontHandler(nil)
	inj.RegisterRequestGlyphHandler(nil)
	inj.RegisterInternalOpenFont(nil)
	inj.RegisterInternalGlyphByGID(nil)
}

// wmEventLoop receives typed WM messages from the uring Dispatcher (wmCh)
// and HID events from the InputAcquirer (inputCh). Input is dispatched
// through the subArctic-style policy/agent pipeline built by buildDispatcher.
func wmEventLoop(wmCh <-chan any, inputCh <-chan hid.HIDEvent,
	dirtyCh <-chan []uint16,
	timeSeconds, timeNanos *attr.Attribute[int64],
	intervalStart, intervalEndOpen *attr.Attribute[int64],
	wmd *wmDispatch) {

	sys.UartWriteString("[rachel:wm] event loop started\n")
	var keyHeld [256]bool
	var modState input.ModifierState
	var rachelMsgAppStart, rachelMsgBlit, rachelMsgOther, rachelHIDEvents int64
	var rachelNotifyCount int64
	var prevNanos int64

	for {
		select {
		case raw := <-wmCh:
			rachelNotifyCount++
			// Check focus-change timer on WM messages so that single-click
			// commits even when no further HID input arrives after the click.
			if wmd.focusChangeAgent.CheckTimer() {
				timedBlitAllWindows()
			}
			wmMsg, ok := raw.(wm.WMNotifyMsg)
			if !ok {
				rachelMsgOther++
				continue
			}
			senderSID := int(wmMsg.SenderSID)
			switch msg := wmMsg.Msg.(type) {
			case wm.AppStart:
				rachelMsgAppStart++
				sys.UartWriteString(fmt.Sprintf("[rachel:wm] AppStart sid=%d w=%d h=%d\n", senderSID, msg.Width, msg.Height))

				// Track the shepherd's AppWindow Bounds in rachel's constraint space.
				if trackAppBounds(senderSID) == nil {
					sys.UartWriteString("[rachel:wm] trackAppBounds failed\n")
					continue
				}
				ta := trackedApps[senderSID]

				// Create a WindowInteractor for this shepherd.
				ta.interactor = &WindowInteractor{ta: ta}

				// If client didn't specify a window size, skip backing store setup.
				// (linux shepherd draws directly to framebuffer, doesn't need one.)
				if msg.Width <= 0 || msg.Height <= 0 {
					sys.UartWriteString("[rachel:wm] no window size, skipping backing store\n")
					grantFocus(senderSID)
					// Update keyboard forward agent's focus to new window.
					wmd.keyFwd.SetFocus(ta.interactor)
					continue
				}

				// HACK: cascade windows so they overlap for focus testing.
				// Each window after the first is offset 80px down and left.
				if windowCount := len(zOrder); windowCount > 0 {
					msg.X -= int32(windowCount * 80)
					msg.Y += int32(windowCount * 80)
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

				sys.UartWriteString(fmt.Sprintf("[rachel:wm] sid=%d pos=(%d,%d) bs=%dx%d pick-rect=(%d,%d)-(%d,%d)\n",
					senderSID, ta.x, ta.y, ta.bsWidth, ta.bsHeight,
					int32(appX)-int32(borderLeft), int32(appY)-int32(borderTop),
					int32(appX)-int32(borderLeft)+ta.bsWidth, int32(appY)-int32(borderTop)+ta.bsHeight))

				// Allocate backing store pages.
				bsBytes := totalW * 4 * totalH
				bsPages := (bsBytes + 4095) / 4096
				bsSlice, allocErr := mem.AllocPagesSlice(bsPages, mem.PageShared)
				if allocErr != nil {
					sys.UartWriteString("[rachel:wm] backing store alloc failed\n")
					continue
				}
				ta.backingStore = bsSlice

				// Pre-render both focused and unfocused decorations.
				// Apply focused state since new windows get focus immediately.
				preRenderDecorations(ta)

				// Share pages with the client shepherd.
				bsVA := uintptr(unsafe.Pointer(&bsSlice[0]))
				clientVA, shareErr := sys.SharePagesWithTarget(senderSID, bsVA, bsPages)
				if shareErr != nil {
					sys.UartWriteString("[rachel:wm] share pages failed\n")
					continue
				}

				// Send BackingStoreReady to client via uring.
				// NOTE: Do NOT use fmt.Printf before sending — it delegates to linux
				// (Write syscall), but linux may be blocked waiting for this very
				// BackingStoreReady, causing a deadlock.
				bsr := wm.EncodeBackingStoreReady(&wm.BackingStoreReady{
					BackingStoreAddr: int64(clientVA),
					TotalWidth:       int32(totalW),
					TotalHeight:      int32(totalH),
					TotalStride:      int32(totalW * 4),
					LeftInset:        int32(borderLeft),
					TopInset:         int32(borderTop),
					AppWidth:         msg.Width,
					AppHeight:        msg.Height,
					AppX:             ta.x,
					AppY:             ta.y,
				})
				if err := uring.Send(senderSID, &bsr); err != nil {
					sys.UartWriteString("[rachel:wm] uring.Send BackingStoreReady failed\n")
				}
				sys.UartWriteString(fmt.Sprintf("[rachel:wm] SID %d: backing store %dx%d at (%d,%d)\n",
					senderSID, totalW, totalH, ta.x, ta.y))

				// Grant focus to this new shepherd.
				grantFocus(senderSID)
				// Update keyboard forward agent's focus to new window.
				wmd.keyFwd.SetFocus(ta.interactor)

				// Initial blit (all windows, z-ordered).
				timedBlitAllWindows()

			case wm.Blit:
				rachelMsgBlit++
				if rachelMsgBlit%10 == 0 {
					sys.UartWriteString(fmt.Sprintf("[rachel] notify=%d appStart=%d blit=%d other=%d hid=%d\n",
						rachelNotifyCount, rachelMsgAppStart, rachelMsgBlit, rachelMsgOther, rachelHIDEvents))
				}
				if rachelMsgBlit%20 == 0 {
					blitTimingReport()
				}
				if rachelMsgBlit%50 == 0 && !blitRateStart.IsZero() {
					ms := time.Since(blitRateStart).Milliseconds()
					if ms > 0 {
						rateX10 := rachelMsgBlit * 10000 / ms
						whole := rateX10 / 10
						frac := rateX10 % 10
						secs := ms / 1000
						secFrac := (ms / 100) % 10
						sys.UartWriteString("[rachel:rate] " + strconv.FormatInt(rachelMsgBlit, 10) +
							" blits in " + strconv.FormatInt(secs, 10) + "." + strconv.FormatInt(secFrac, 10) + "s = " +
							strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(frac, 10) +
							" blits/sec\n")
					}
				}
				ta, ok := trackedApps[senderSID]
				if !ok || ta.backingStore == nil {
					continue
				}
				regions, occDur := timedExposedRegion(senderSID)

				focused := senderSID == mouseFocusSID
				if focused {
					// Restore correct background under border zones so shadows
					// composite over lower windows, not stale FB content.
					restoreBorderBackground(senderSID, regions, fbPix, fbStride)
				} else {
					// Unfocused: clip to face area — no shadow borders.
					face := faceScreenRect(ta)
					var clipped []image.Rectangle
					for _, r := range regions {
						isect := r.Intersect(face)
						if !isect.Empty() {
							clipped = append(clipped, isect)
						}
					}
					regions = clipped
				}
				copyDur := timedBlitWindow(senderSID, regions, fbPix, fbStride, focused)

				ox, oy := screenOrigin(ta)
				flushDur := timedFlushRect(ox, oy, int(ta.bsWidth), int(ta.bsHeight))
				blitTimingRecord(occDur.Microseconds(), copyDur.Microseconds(), flushDur.Microseconds())

			case wm.AnimationRegister:
				registerAnimation(senderSID, msg)

			case wm.AnimationUnregister:
				unregisterAnimation(senderSID, msg)

			default:
				rachelMsgOther++
			}

		case <-dirtyCh:
			nowNanos := timeSeconds.Get()*1_000_000_000 + timeNanos.Get()
			if prevNanos != 0 {
				intervalStart.Set(prevNanos)
				intervalEndOpen.Set(nowNanos)
				tickAnimations(prevNanos, nowNanos)
			}
			prevNanos = nowNanos

		case ev := <-inputCh:
			rachelHIDEvents++
			var posChanged bool
			processRawEvent(ev, &keyHeld, &modState, &posChanged, wmd)

			// Drain any remaining buffered events before blocking again.
			drained := 1
		drainLoop:
			for {
				select {
				case ev2 := <-inputCh:
					processRawEvent(ev2, &keyHeld, &modState, &posChanged, wmd)
					drained++
				default:
					break drainLoop
				}
			}
			inputEventsProcessed += drained
			inputWakeups++
			if inputWakeups%100 == 0 {
				sys.UartWriteString(fmt.Sprintf("[rachel:input] wakeups=%d events=%d\n",
					inputWakeups, inputEventsProcessed))
			}
			postBatchInputUpdate(&posChanged, &modState, wmd)
		}
	}
}

func main() {
	sys.UartWriteString("[rachel] Starting window manager\n")

	// Initialize palette and desktop background early — everything that
	// fills or composites the framebuffer reads desktopBG.
	pal = mctheme.NewDefaultPaletteSwapRB()
	pal.SetDesktopBG(color.NRGBA{R: 224, G: 224, B: 224, A: 255})
	desktopBG = pal.DesktopBG() // BGRA byte order (RB-swapped)

	// Initialize WMTheme from palette — border vars derive from it.
	wmTheme = mctheme.NewDefaultWMTheme(pal)
	wmTheme.SetStyle(std.NewNeumorphicStyle(
		mctheme.NewDefaultNeumorphicParams().Heavy(),
		mctheme.NewDefaultNeumorphicParams().Light()))
	borderTop = wmTheme.BorderTop()
	borderRight = wmTheme.BorderRight()
	borderBottom = wmTheme.BorderBottom()
	borderLeft = wmTheme.BorderLeft()
	titleBarHeight = wmTheme.TitleBarHeight()
	shadowTop = wmTheme.ShadowTop()

	// Claim window manager role — rachel gets ALL input events automatically
	sys.UartWriteString("[rachel] requesting WM role...\n")
	if err := sys.RequestWindowManager(); err != nil {
		fmt.Printf("[rachel] failed to become window manager: %v\n", err)
		return
	}
	sys.UartWriteString("[rachel] WM role granted, attr.Init...\n")
	// Initialize constraint system early — mailbox handler creates constraints.
	attr.Init()
	sys.UartWriteString("[rachel] attr.Init done\n")

	// Set up the built-in US QWERTY keymap as fallback.
	// When keymapper.maz is wired, this will be replaced with the
	// configured layout from rachel.toml.
	wmKeyMapper = &input.Keymap{}
	sys.UartWriteString("[rachel] keyMapper: " + wmKeyMapper.Name() + "\n")

	// Publish default text font size as an attribute so shepherds can bind to it.
	defaultFontSize := int64(24)
	defaultTextFontSizeAttr := attr.ValueI64(
		attr.ShepherdURI("int64", "defaultTextFontSize"), defaultFontSize)
	_ = defaultTextFontSizeAttr

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

	// Set up io_uring for HID input events. The kernel IRQ top-half writes
	// CQEs directly; rachel's InputAcquirer blocks in IOUringEnter.
	inputAcq, err := NewInputAcquirer(128)
	if err != nil {
		panic(fmt.Sprintf("[rachel] FATAL: NewInputAcquirer: %v", err))
	}
	go inputAcq.Run()

	// Wait for fs shepherd to be ready before loading .maz files.
	if err := sys.WaitForShepherdReady("fs", 10); err != nil {
		panic(fmt.Sprintf("[rachel] FATAL: fs: %v", err))
	}

	// Read rachel.toml from the ext2 filesystem.
	var rachelCfg constants.RachelConfig
	lf, lfErr := file.LoadFile("/rachel.toml")
	if lfErr != nil {
		sys.UartWriteString("[rachel] rachel.toml not found, using defaults\n")
	} else {
		tomlData := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(lf.StartVA))), lf.BytesRead)
		if err := toml.Unmarshal(tomlData, &rachelCfg); err != nil {
			sys.UartWriteString("[rachel] rachel.toml parse error: " + err.Error() + "\n")
		} else {
			sys.UartWriteString("[rachel] rachel.toml loaded: keymap=" + rachelCfg.Keymap + "\n")
		}
		// Free the loaded pages.
		syscall.RawSyscall6(syscall.SYS_MUNMAP, uintptr(lf.StartVA),
			uintptr(lf.NumPages)*4096, 0, 0, 0, 0)
	}

	// Apply config: update default font size if specified.
	if rachelCfg.DefaultTextFontSize > 0 {
		defaultTextFontSizeAttr.Set(rachelCfg.DefaultTextFontSize)
		sys.UartWriteString(fmt.Sprintf("[rachel] defaultTextFontSize=%d (from toml)\n",
			rachelCfg.DefaultTextFontSize))
	}

	// Load keymapper.maz and set up the configured keyboard layout.
	kmInit := &mancini.KeyMapperInit{KeymapName: rachelCfg.Keymap}
	forceKeyMapperItab(kmInit)
	kmPath := sys.LoadMazByName("/keymapper")
	kmMain, kmInitAddr, kmErr := mazhost.LoadMazBootstrap(kmPath, nil)
	if kmErr != nil {
		sys.UartWriteString("[rachel] LoadMazBootstrap(keymapper) failed: " + kmErr.Error() + "\n")
	} else {
		if kmInitAddr != 0 {
			type funcval struct{ fn uintptr }
			fv := &funcval{fn: kmInitAddr}
			shepherdInit := *(*func(interface{}) error)(unsafe.Pointer(&fv))
			if err := shepherdInit(kmInit); err != nil {
				sys.UartWriteString("[rachel] keymapper MazarinShepherd failed: " + err.Error() + "\n")
			} else if kmInit.Mapper != nil {
				wmKeyMapper = kmInit.Mapper
				sys.UartWriteString("[rachel] keyMapper: " + wmKeyMapper.Name() + " (from keymapper.maz)\n")
			}
		}
		// keymapper.MazarinMain is a no-op, but run it for consistency.
		_ = kmMain
	}

	// Force linker to include FontSvcInjector itab for cross-module type assertions.
	initData := &fontcache.FontSvcInit{}
	forceFontSvcItab(initData)

	// Load fontsvc.maz — it registers a handler callback for font requests.
	fontSvcPath := sys.LoadMazByName("/fontsvc")
	fontSvcMain, fontSvcInitAddr, fontSvcErr := mazhost.LoadMazBootstrap(fontSvcPath, nil)
	if fontSvcErr != nil {
		fmt.Printf("[rachel] LoadMazBootstrap(fontsvc) failed: %v\n", fontSvcErr)
	} else {
		// Inject the callback registration into fontsvc via MazarinShepherd.
		if fontSvcInitAddr != 0 {
			type funcval struct{ fn uintptr }
			fv := &funcval{fn: fontSvcInitAddr}
			shepherdInit := *(*func(interface{}) error)(unsafe.Pointer(&fv))
			if err := shepherdInit(initData); err != nil {
				fmt.Printf("[rachel] fontsvc MazarinShepherd failed: %v\n", err)
			}
		}
		go mazhost.RunMaz(fontSvcMain)
	}

	// Set up uring Dispatcher — WM goes to channel, font requests to fontsvc callbacks.
	wmCh := make(chan any, 32)
	disp := uring.NewDispatcher()
	disp.On(ipc.ProtoWMNotify, wm.DecodeWMNotify, wmCh)

	// Rachel's own FontCache for rendering title bar text. Font requests
	// go via uring to ourselves; the dispatcher goroutine processes them
	// through the fontsvc callbacks and sends replies back.
	rachelFC := fontcache.New(os.Getpid())
	disp.On(ipc.ProtoFontResponse, wm.DecodeFontResponse, rachelFC.ReplyCh)

	if initData.HandleOpenFont != nil {
		openFontCb := initData.HandleOpenFont
		glyphCb := initData.HandleRequestGlyph
		disp.OnFunc(ipc.ProtoFontRequest, wm.DecodeFontRequest, func(raw any) {
			// Type switch in rachel's runtime context (correct type metadata).
			frm := raw.(wm.FontRequestMsg)
			sid := int(frm.SenderSID)
			switch msg := frm.Msg.(type) {
			case wm.OpenFont:
				openFontCb(sid, msg.Variant, msg.Size, msg.Path)
			case wm.RequestGlyph:
				glyphCb(sid, msg.FontID, msg.GID, msg.Codepoint)
			}
		})
		sys.UartWriteString("[rachel] font requests wired to fontsvc callbacks\n")
	}
	// Handle peer death — remove the dead shepherd from tracked state.
	disp.OnDeath(func(deadSID int16) {
		sys.UartWriteString(fmt.Sprintf("[rachel] shepherd %d died\n", deadSID))
	})

	disp.Start()
	sys.UartWriteString("[rachel] disp.Start() returned, building dispatcher...\n")

	// Create the window title bar used for all managed windows.
	// Use the internal provider (direct in-process calls to fontsvc)
	// instead of the uring-based provider. SharePages to yourself
	// is rejected by the kernel, so the IPC path cannot work for
	// rachel's own font rendering.
	if initData.InternalOpenFont != nil {
		titleGlyphProvider = fontcache.NewInternalGlyphProvider(initData.InternalOpenFont, initData.InternalGlyphByGID)
		sys.UartWriteString("[rachel] using internal font provider (no IPC)\n")
	} else {
		titleGlyphProvider = fontcache.NewFontSvcGlyphProvider(rachelFC)
		sys.UartWriteString("[rachel] WARNING: internal font provider not available, falling back to IPC\n")
	}
	windowTitleBar = std.NewStripedTitleBar(pal, &mancini.FontConfig{
		FontRegular: mfont.DefaultSans,
		FontBold:    mfont.DefaultSans,
	}, 22, true)
	sys.UartWriteString("[rachel] StripedTitleBar created\n")

	// Build the subArctic input dispatch pipeline.
	wmd := buildDispatcher()

	// Time-interval attributes for animation protocol.
	// Bind to both kernel UTC seconds and nanos so we can construct full
	// epoch nanos (seconds*1e9 + nanos) for animation timestamps.
	timeSecProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/time/utc_seconds")
	timeSeconds := attr.ConstraintI64(attr.ShepherdURI("int64", "time_sec"), timeSecProg)
	_ = timeSeconds.Get()
	timeNanosProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/time/utc_nanos")
	timeNanos := attr.ConstraintI64(attr.ShepherdURI("int64", "time_nanos"), timeNanosProg)
	timeNanos.SetEager(true)
	_ = timeNanos.Get()
	intervalStart := attr.ValueI64(attr.ShepherdURI("int64", "intervalStart"), 0)
	intervalEndOpen := attr.ValueI64(attr.ShepherdURI("int64", "intervalEndOpen"), 0)
	dirtyCh := attr.OnDirty()

	sys.UartWriteString("[rachel] dispatcher built, starting wmEventLoop...\n")

	// Start WM event loop — receives typed messages from uring Dispatcher,
	// HID events from the InputAcquirer, and dirty ticks for animations.
	go wmEventLoop(wmCh, inputAcq.Events(), dirtyCh, timeSeconds, timeNanos, intervalStart, intervalEndOpen, wmd)

	// Publish ready status to constraint network using the well-known URI.
	// This must happen BEFORE LaunchMaz("prefs") because prefs uses fmt.Printf
	// which delegates to linux (stdio), and linux can't launch until rachel is ready.
	ready := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = ready
	sys.SetReady(true)
	sys.UartWriteString("[rachel] Ready=true\n")

	// Record time at rachel ready for blit rate reporting.
	blitRateStart = time.Now()

	// Load and launch prefs.maz (after ready, since this uses FS/stdio delegation).
	mazhost.LaunchMaz("prefs")

	// Block main goroutine forever
	select {}
}
