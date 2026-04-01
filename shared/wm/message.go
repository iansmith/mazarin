// Package wm defines message types and constants for communication
// between shepherds and the window manager (rachel).
package wm

// NotifyCode identifies the target type of a mailbox notification.
// Font codes (3, 4) must be disjoint from WM codes (1, 2) so fontsvc.maz
// can distinguish font requests from WM messages in the shared mailbox loop.
const (
	// WMNotify is sent by a shepherd TO the window manager.
	WMNotify int64 = 1
	// ShepherdNotify is sent by the window manager TO a shepherd.
	ShepherdNotify int64 = 2
	// FontNotify is sent by a shepherd TO fontsvc (font request).
	FontNotify int64 = 3
	// FontResponse is sent by fontsvc TO a shepherd (font reply).
	FontResponse int64 = 4
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
	// MsgKeyPress: WM→shepherd. "Key pressed."
	MsgKeyPress int64 = 7
	// MsgKeyRelease: WM→shepherd. "Key released."
	MsgKeyRelease int64 = 8
	// MsgBlit: shepherd→WM. "My backing store is ready, blit me."
	MsgBlit int64 = 9
	// MsgBackingStoreReady: WM→shepherd. "Here is your shared backing store."
	MsgBackingStoreReady int64 = 10
)

// AppStartMsg is sent by a shepherd to the WM when it starts up.
// Rachel allocates the backing store and shares it back via MsgBackingStoreReady.
// Layout: 8+8+4+4+4+4 + 96 pad = 128 bytes.
type AppStartMsg struct {
	Type   int64 // MsgAppStart
	SID    int64 // Shepherd ID of the sender
	X      int32 // desired screen X position
	Y      int32 // desired screen Y position
	Width  int32 // desired app width in pixels
	Height int32 // desired app height in pixels
	_      [96]byte
}

// BackingStoreReadyMsg is sent by the WM to a shepherd after allocating
// and sharing the backing store. The client uses BackingStoreAddr to create
// a []byte slice over the full buffer (TotalWidth × TotalHeight), then
// sets up a DrawContext with Translate(LeftInset, TopInset) and clips to
// (0, 0, AppWidth, AppHeight) so all drawing is in app-local coordinates.
// Layout: 8+8+4+4+4+4+4+4+4+4+4+4 = 56, + 72 pad = 128 bytes.
type BackingStoreReadyMsg struct {
	Type             int64 // MsgBackingStoreReady
	BackingStoreAddr int64 // client's VA of the shared backing store
	TotalWidth       int32 // full buffer width including borders
	TotalHeight      int32 // full buffer height including borders
	TotalStride      int32 // bytes per scanline (TotalWidth * 4)
	LeftInset        int32 // pixels from left edge to app area
	TopInset         int32 // pixels from top edge to app area
	AppWidth         int32 // app drawing area width
	AppHeight        int32 // app drawing area height
	AppX             int32 // rachel's chosen screen X for app area
	AppY             int32 // rachel's chosen screen Y for app area
	_                [72]byte
}

// BlitMsg is sent by a shepherd to the WM after completing a draw pass.
// Rachel computes the exposed region and copies visible pixels to the
// framebuffer.
// Layout: 8+8 + 112 pad = 128 bytes.
type BlitMsg struct {
	Type int64 // MsgBlit
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

// KeyPressMsg is sent by the WM to a shepherd when a key is pressed.
// Layout: 8 + 2 + 2 + 116 = 128 bytes.
type KeyPressMsg struct {
	Type int64  // MsgKeyPress
	Code uint16 // evdev keycode
	_    [118]byte
}

// KeyReleaseMsg is sent by the WM to a shepherd when a key is released.
// Layout: 8 + 2 + 118 = 128 bytes.
type KeyReleaseMsg struct {
	Type int64  // MsgKeyRelease
	Code uint16 // evdev keycode
	_    [118]byte
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
