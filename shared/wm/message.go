// Package wm defines message types and constants for communication
// between shepherds and the window manager (rachel).
package wm

// NotifyCode identifies the target type of a mailbox notification.
const (
	// WMNotify is sent by a shepherd TO the window manager.
	WMNotify int64 = 1
	// ShepherdNotify is sent by the window manager TO a shepherd.
	ShepherdNotify int64 = 2
)

// SizeWMMessage is the fixed size of all messages exchanged between
// shepherds and the window manager. The first int64 is always the
// message type discriminator.
const SizeWMMessage = 128

// DefaultSlotCount is the default number of slots in a WM ring buffer.
const DefaultSlotCount = 16

// Message type discriminators — first int64 in every message.
const (
	// MsgAppStart: shepherd→WM. "I exist, here is my SID."
	MsgAppStart int64 = 1
	// MsgYouHaveFocus: WM→shepherd. "You now have input focus."
	MsgYouHaveFocus int64 = 2
	// MsgYouLostFocus: WM→shepherd. "You no longer have input focus."
	MsgYouLostFocus int64 = 3
	// MsgMousePress: WM→shepherd. "Mouse button pressed at (X, Y)."
	MsgMousePress int64 = 4
	// MsgMouseRelease: WM→shepherd. "Mouse button released at (X, Y)."
	MsgMouseRelease int64 = 5
	// MsgMouseMove: WM→shepherd. "Mouse moved to (X, Y) while button held."
	MsgMouseMove int64 = 6
)

// AppStartMsg is sent by a shepherd to the WM when it starts up.
type AppStartMsg struct {
	Type int64 // MsgAppStart
	SID  int64 // Shepherd ID of the sender
	_    [112]byte
}

// YouHaveFocusMsg is sent by the WM to a shepherd when it gains focus.
type YouHaveFocusMsg struct {
	Type int64 // MsgYouHaveFocus
	_    [120]byte
}

// YouLostFocusMsg is sent by the WM to a shepherd when it loses focus.
type YouLostFocusMsg struct {
	Type int64 // MsgYouLostFocus
	_    [120]byte
}

// MousePressMsg is sent by the WM to a shepherd when a mouse button is pressed.
// X and Y are screen coordinates at the time of the press.
// Layout: 8 + 4 + 4 + 4 + 108 = 128 bytes.
type MousePressMsg struct {
	Type   int64 // MsgMousePress
	X      int32 // screen X coordinate
	Y      int32 // screen Y coordinate
	Button int32 // button code (BTN_LEFT=0x110, BTN_RIGHT=0x111, etc.)
	_      [108]byte
}

// MouseReleaseMsg is sent by the WM to a shepherd when a mouse button is released.
// Layout: 8 + 4 + 4 + 4 + 108 = 128 bytes.
type MouseReleaseMsg struct {
	Type   int64 // MsgMouseRelease
	X      int32 // screen X coordinate
	Y      int32 // screen Y coordinate
	Button int32 // button code
	_      [108]byte
}

// MouseMoveMsg is sent by the WM to a shepherd while a button is held.
// Layout: 8 + 4 + 4 + 108 = 124 ... pad to 128.
type MouseMoveMsg struct {
	Type int64 // MsgMouseMove
	X    int32 // screen X coordinate
	Y    int32 // screen Y coordinate
	_    [112]byte
}

// Well-known attribute URIs.
//
// These define the contract between shepherds and the window manager.
// A shepherd publishes Ready=true once it has drawn its first frame and
// its constraint handles are valid. The WM must not read any other
// attributes from a shepherd until Ready is true.

// ReadyURI returns the URI for a shepherd's Ready flag.
// A shepherd sets this to true after first draw + successful Bounds evaluation.
// The WM gates all interaction on this: no Ready, no tracking.
func ReadyURI(sid string) string {
	return "attr:///shepherd/" + sid + "/bool/Ready"
}

// AppWindowBoundsURI returns the URI for a shepherd's AppWindow bounding rectangle.
// This is a Rectangle2D constraint: rect(X, Y, X+W, Y+H) in screen coordinates.
func AppWindowBoundsURI(sid string) string {
	return "attr:///shepherd/" + sid + "/rect/AppWindow/layout/Bounds"
}
