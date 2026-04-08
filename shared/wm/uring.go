// Typed message structs for uring-based IPC between shepherds and rachel.
//
// Each struct represents a single message type, carrying only meaningful data
// (no padding, no type discriminator — Protocol + MsgType handle routing).
// Encode functions pack a typed struct into an ipc.UringIPCMsg; Decode
// functions unpack UringIPCMsg.Payload into typed structs.
//
// Payload layout: [0:4] MsgType uint32, [4:112] message body (108 bytes).
package wm

import (
	"mazzy/shared/ipc"
	"unsafe"
)

// MsgType values stored at Payload[0:4]. These reuse the existing message
// type discriminator values from the mailbox-era structs.
const (
	MsgTypeAppStart          uint32 = 1
	MsgTypeYouHaveFocus      uint32 = 2
	MsgTypeYouLostFocus      uint32 = 3
	MsgTypeMousePress        uint32 = 4
	MsgTypeMouseRelease      uint32 = 5
	MsgTypeMouseMove         uint32 = 6
	MsgTypeKeyPress          uint32 = 7
	MsgTypeKeyRelease        uint32 = 8
	MsgTypeBlit                  uint32 = 9
	MsgTypeBackingStoreReady     uint32 = 10
	MsgTypeKeyboardFocusGained  uint32 = 11
	MsgTypeKeyboardFocusLost  uint32 = 12
	MsgTypeMouseFocusGained     uint32 = 13
	MsgTypeMouseFocusLost     uint32 = 14

	MsgTypeAnimationRegister     uint32 = 20 // shepherd → rachel
	MsgTypeAnimationRegistered   uint32 = 21 // rachel → shepherd
	MsgTypeAnimationStart        uint32 = 22 // rachel → shepherd
	MsgTypeAnimationUpdate       uint32 = 23 // rachel → shepherd
	MsgTypeAnimationFinish       uint32 = 24 // rachel → shepherd
	MsgTypeAnimationUnregister   uint32 = 25 // shepherd → rachel
	MsgTypeAnimationUnregistered uint32 = 26 // rachel → shepherd

	MsgTypeWindowResized uint32 = 30 // rachel → shepherd
	MsgTypeWindowMoved   uint32 = 31 // rachel → shepherd

	MsgTypeOverlayAllocate uint32 = 40 // shepherd → rachel
	MsgTypeOverlayReady    uint32 = 41 // rachel → shepherd
	MsgTypeOverlayBlit     uint32 = 42 // shepherd → rachel
	MsgTypeOverlayRelease  uint32 = 43 // shepherd → rachel
	MsgTypeOverlayReleased uint32 = 44 // rachel → shepherd
	// 45 reserved (was OverlayInput)
)

// AnimationAlways is used as the EndNanos for animations that run
// indefinitely until explicitly unregistered. Jan 1, 2045 00:00 UTC.
const AnimationAlways int64 = 2366841600_000_000_000 // time.Date(2045,1,1,0,0,0,0,time.UTC).UnixNano()

// --- Typed message structs (WM protocol) ---

// AppStart is sent by a shepherd to rachel when it starts up.
type AppStart struct {
	SID    int32
	X      int32
	Y      int32
	Width  int32
	Height int32
}

// Blit is sent by a shepherd to rachel after completing a draw pass.
// DrawnWidth/DrawnHeight report the app-area dimensions actually rendered,
// allowing rachel to clip compositing to the drawn region during resize
// (undrawn pixels are not blitted, so the drag background shows through).
type Blit struct {
	SID         int32
	DrawnWidth  int32
	DrawnHeight int32
}

// BackingStoreReady is sent by rachel to a shepherd after allocating
// and sharing the backing store.
type BackingStoreReady struct {
	BackingStoreAddr int64
	TotalWidth       int32
	TotalHeight      int32
	TotalStride      int32
	LeftInset        int32
	TopInset         int32
	AppWidth         int32
	AppHeight        int32
	AppX             int32
	AppY             int32
}

// YouHaveFocus is sent by rachel to a shepherd when it gains focus.
// Deprecated: use KeyboardFocusGained / MouseFocusGained instead.
type YouHaveFocus struct{}

// YouLostFocus is sent by rachel to a shepherd when it loses focus.
// Deprecated: use KeyboardFocusLost / MouseFocusLost instead.
type YouLostFocus struct{}

// KeyboardFocusGained is sent by rachel when a shepherd gains keyboard focus.
type KeyboardFocusGained struct{}

// KeyboardFocusLost is sent by rachel when a shepherd loses keyboard focus.
type KeyboardFocusLost struct{}

// MouseFocusGained is sent by rachel when a shepherd gains mouse focus.
type MouseFocusGained struct{}

// MouseFocusLost is sent by rachel when a shepherd loses mouse focus.
type MouseFocusLost struct{}

// MousePress is sent by rachel to a shepherd when a mouse button is pressed.
type MousePress struct {
	X      int32
	Y      int32
	Button int32
	Mods   uint64 // modifier key bitmask (hid.ModXxx)
}

// MouseRelease is sent by rachel to a shepherd when a mouse button is released.
type MouseRelease struct {
	X      int32
	Y      int32
	Button int32
	Mods   uint64 // modifier key bitmask (hid.ModXxx)
}

// MouseMove is sent by rachel to a shepherd while a button is held.
type MouseMove struct {
	X    int32
	Y    int32
	Mods uint64 // modifier key bitmask (hid.ModXxx)
}

// KeyPress is sent by rachel to a shepherd when a key is pressed.
// Char and Action are pre-translated by rachel's KeyMapper.
type KeyPress struct {
	Code   uint16 // raw evdev keycode
	Action Action // translated action (0 = none)
	_      uint8  // pad
	Char   uint32 // translated Unicode codepoint (0 = none)
	Mods   uint64 // modifier key bitmask (hid.ModXxx)
}

// KeyRelease is sent by rachel to a shepherd when a key is released.
// Char and Action are pre-translated by rachel's KeyMapper.
type KeyRelease struct {
	Code   uint16 // raw evdev keycode
	Action Action // translated action (0 = none)
	_      uint8  // pad
	Char   uint32 // translated Unicode codepoint (0 = none)
	Mods   uint64 // modifier key bitmask (hid.ModXxx)
}

// AnimationRegister is sent by a shepherd to rachel to request an animation
// spanning [StartNanos, EndNanos]. Nonce is a caller-chosen value that rachel
// echoes back in AnimationRegistered for correlation.
type AnimationRegister struct {
	StartNanos int64
	EndNanos   int64
	Nonce      uint64
}

// AnimationRegistered is sent by rachel to confirm registration.
// AnimationID is rachel's global ID; Nonce is echoed from AnimationRegister.
type AnimationRegistered struct {
	AnimationID uint64
	Nonce       uint64
}

// AnimationStart is sent by rachel when the animation's start time is reached.
type AnimationStart struct {
	AnimationID uint64
	StartNanos  int64
}

// AnimationUpdate is sent by rachel each interval tick while the animation
// is active. CoveredStart and CoveredEnd are fractions in [0, 1].
// NanosSinceStart is the elapsed time since the animation began — useful
// for AnimationAlways animations where the fractions stay near zero.
type AnimationUpdate struct {
	AnimationID    uint64
	StartNanos     int64
	EndNanos       int64
	CoveredStart   float64
	CoveredEnd     float64
	NanosSinceStart int64
}

// AnimationFinish is sent by rachel when the animation's end time has passed.
type AnimationFinish struct {
	AnimationID uint64
	EndNanos    int64
}

// AnimationUnregister is sent by a shepherd to cancel an active animation.
// AnimationID is rachel's global ID; Nonce is the shepherd's local ID
// (echoed back in AnimationUnregistered).
type AnimationUnregister struct {
	AnimationID uint64
	Nonce       uint64
}

// AnimationUnregistered is sent by rachel to confirm cancellation.
// AnimationID is rachel's global ID; Nonce is echoed from AnimationUnregister.
type AnimationUnregistered struct {
	AnimationID uint64
	Nonce       uint64
}

// WindowResized is sent by rachel to a shepherd when its window has been
// resized. The shepherd must rebuild its DrawContext from the new backing
// store address and dimensions, then redraw and blit.
type WindowResized struct {
	BackingStoreAddr int64
	TotalWidth       int32
	TotalHeight      int32
	TotalStride      int32
	LeftInset        int32
	TopInset         int32
	AppWidth         int32
	AppHeight        int32
	AppX             int32
	AppY             int32
}

// WindowMoved is sent by rachel to a shepherd when its window has been
// moved (e.g. by a title bar drag). The shepherd can update its screen
// origin so that overlay positioning and other screen-coordinate
// calculations remain correct.
type WindowMoved struct {
	AppX int32
	AppY int32
}

// --- Overlay messages ---

// OverlayAllocate is sent by a shepherd to rachel to request a shared
// overlay buffer at the given screen rectangle. Rachel allocates shared
// pages, maps them into the shepherd, grants focus, and responds with
// OverlayReady. The overlay is auto-dismissed on focus loss.
type OverlayAllocate struct {
	ScreenX1 int32 // upper-left X in screen coords
	ScreenY1 int32 // upper-left Y in screen coords
	ScreenX2 int32 // lower-right X in screen coords
	ScreenY2 int32 // lower-right Y in screen coords
}

// OverlayReady is sent by rachel to a shepherd after allocating the
// overlay buffer. The shepherd creates an image.RGBA from OverlayAddr
// and draws into it.
type OverlayReady struct {
	OverlayID   int32
	OverlayAddr int64 // VA in shepherd's address space
	Width       int32
	Height      int32
	Stride      int32
	ScreenX     int32 // confirmed upper-left X
	ScreenY     int32 // confirmed upper-left Y
}

// OverlayBlit is sent by a shepherd to rachel after drawing into the
// overlay buffer. Rachel alpha-blends it onto the framebuffer and flushes.
type OverlayBlit struct {
	OverlayID int32
}

// OverlayRelease is sent by a shepherd to rachel when it is done with
// the overlay. Rachel frees the shared pages, repaints the covered area,
// and responds with OverlayReleased.
type OverlayRelease struct {
	OverlayID int32
}

// OverlayReleased is sent by rachel to confirm the overlay has been torn
// down. After receiving this, the shepherd must not touch the overlay
// buffer — the pages have been unmapped.
type OverlayReleased struct {
	OverlayID int32
}

// --- Encode functions (typed struct → UringIPCMsg) ---

func EncodeAppStart(as *AppStart) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoWMNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeAppStart
	*(*AppStart)(unsafe.Pointer(&msg.Payload[4])) = *as
	return msg
}

func EncodeBlit(b *Blit) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoWMNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeBlit
	*(*Blit)(unsafe.Pointer(&msg.Payload[4])) = *b
	return msg
}

func EncodeBackingStoreReady(bsr *BackingStoreReady) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeBackingStoreReady
	*(*BackingStoreReady)(unsafe.Pointer(&msg.Payload[4])) = *bsr
	return msg
}

func EncodeYouHaveFocus() ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeYouHaveFocus
	return msg
}

func EncodeYouLostFocus() ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeYouLostFocus
	return msg
}

func EncodeKeyboardFocusGained() ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeKeyboardFocusGained
	return msg
}

func EncodeKeyboardFocusLost() ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeKeyboardFocusLost
	return msg
}

func EncodeMouseFocusGained() ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeMouseFocusGained
	return msg
}

func EncodeMouseFocusLost() ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeMouseFocusLost
	return msg
}

func EncodeMousePress(m *MousePress) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeMousePress
	*(*MousePress)(unsafe.Pointer(&msg.Payload[4])) = *m
	return msg
}

func EncodeMouseRelease(m *MouseRelease) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeMouseRelease
	*(*MouseRelease)(unsafe.Pointer(&msg.Payload[4])) = *m
	return msg
}

func EncodeMouseMove(m *MouseMove) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeMouseMove
	*(*MouseMove)(unsafe.Pointer(&msg.Payload[4])) = *m
	return msg
}

func EncodeKeyPress(k *KeyPress) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeKeyPress
	*(*KeyPress)(unsafe.Pointer(&msg.Payload[4])) = *k
	return msg
}

func EncodeKeyRelease(k *KeyRelease) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeKeyRelease
	*(*KeyRelease)(unsafe.Pointer(&msg.Payload[4])) = *k
	return msg
}

func EncodeAnimationRegister(a *AnimationRegister) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoWMNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeAnimationRegister
	*(*AnimationRegister)(unsafe.Pointer(&msg.Payload[4])) = *a
	return msg
}

func EncodeAnimationRegistered(a *AnimationRegistered) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeAnimationRegistered
	*(*AnimationRegistered)(unsafe.Pointer(&msg.Payload[4])) = *a
	return msg
}

func EncodeAnimationStart(a *AnimationStart) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeAnimationStart
	*(*AnimationStart)(unsafe.Pointer(&msg.Payload[4])) = *a
	return msg
}

func EncodeAnimationUpdate(a *AnimationUpdate) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeAnimationUpdate
	*(*AnimationUpdate)(unsafe.Pointer(&msg.Payload[4])) = *a
	return msg
}

func EncodeAnimationFinish(a *AnimationFinish) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeAnimationFinish
	*(*AnimationFinish)(unsafe.Pointer(&msg.Payload[4])) = *a
	return msg
}

func EncodeAnimationUnregister(a *AnimationUnregister) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoWMNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeAnimationUnregister
	*(*AnimationUnregister)(unsafe.Pointer(&msg.Payload[4])) = *a
	return msg
}

func EncodeAnimationUnregistered(a *AnimationUnregistered) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeAnimationUnregistered
	*(*AnimationUnregistered)(unsafe.Pointer(&msg.Payload[4])) = *a
	return msg
}

func EncodeWindowResized(wr *WindowResized) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeWindowResized
	*(*WindowResized)(unsafe.Pointer(&msg.Payload[4])) = *wr
	return msg
}

func EncodeWindowMoved(wm *WindowMoved) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeWindowMoved
	*(*WindowMoved)(unsafe.Pointer(&msg.Payload[4])) = *wm
	return msg
}

func EncodeOverlayAllocate(o *OverlayAllocate) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoWMNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeOverlayAllocate
	*(*OverlayAllocate)(unsafe.Pointer(&msg.Payload[4])) = *o
	return msg
}

func EncodeOverlayReady(o *OverlayReady) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeOverlayReady
	*(*OverlayReady)(unsafe.Pointer(&msg.Payload[4])) = *o
	return msg
}

func EncodeOverlayBlit(o *OverlayBlit) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoWMNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeOverlayBlit
	*(*OverlayBlit)(unsafe.Pointer(&msg.Payload[4])) = *o
	return msg
}

func EncodeOverlayRelease(o *OverlayRelease) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoWMNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeOverlayRelease
	*(*OverlayRelease)(unsafe.Pointer(&msg.Payload[4])) = *o
	return msg
}

func EncodeOverlayReleased(o *OverlayReleased) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoShepherdNotify
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeOverlayReleased
	*(*OverlayReleased)(unsafe.Pointer(&msg.Payload[4])) = *o
	return msg
}

// --- Decode functions (UringIPCMsg → typed struct) ---

// WMNotifyMsg wraps a decoded WM notification with the sender's SID
// from the uring header. Rachel uses SenderSID to identify which
// shepherd sent the message.
type WMNotifyMsg struct {
	SenderSID int16
	Msg       any // AppStart or Blit
}

// DecodeWMNotify decodes a ProtoWMNotify message (shepherd→rachel).
// Returns a WMNotifyMsg wrapping the typed struct with the sender's SID.
// Panics on unknown message type.
func DecodeWMNotify(msg *ipc.UringIPCMsg) any {
	senderSID := msg.SenderSID
	msgType := *(*uint32)(unsafe.Pointer(&msg.Payload[0]))
	switch msgType {
	case MsgTypeAppStart:
		return WMNotifyMsg{
			SenderSID: senderSID,
			Msg:       *(*AppStart)(unsafe.Pointer(&msg.Payload[4])),
		}
	case MsgTypeBlit:
		return WMNotifyMsg{
			SenderSID: senderSID,
			Msg:       *(*Blit)(unsafe.Pointer(&msg.Payload[4])),
		}
	case MsgTypeAnimationRegister:
		return WMNotifyMsg{
			SenderSID: senderSID,
			Msg:       *(*AnimationRegister)(unsafe.Pointer(&msg.Payload[4])),
		}
	case MsgTypeAnimationUnregister:
		return WMNotifyMsg{
			SenderSID: senderSID,
			Msg:       *(*AnimationUnregister)(unsafe.Pointer(&msg.Payload[4])),
		}
	case MsgTypeOverlayAllocate:
		return WMNotifyMsg{
			SenderSID: senderSID,
			Msg:       *(*OverlayAllocate)(unsafe.Pointer(&msg.Payload[4])),
		}
	case MsgTypeOverlayBlit:
		return WMNotifyMsg{
			SenderSID: senderSID,
			Msg:       *(*OverlayBlit)(unsafe.Pointer(&msg.Payload[4])),
		}
	case MsgTypeOverlayRelease:
		return WMNotifyMsg{
			SenderSID: senderSID,
			Msg:       *(*OverlayRelease)(unsafe.Pointer(&msg.Payload[4])),
		}
	default:
		panic("wm.DecodeWMNotify: unknown message type")
	}
}

// DecodeShepherdNotify decodes a ProtoShepherdNotify message (rachel→shepherd).
// Panics on unknown message type.
func DecodeShepherdNotify(msg *ipc.UringIPCMsg) any {
	return DecodeShepherdNotifyFromPayload(msg.Payload[:])
}

// DecodeShepherdNotifyFromPayload decodes a shepherd notification from raw
// payload bytes. Used by .maz modules that receive raw payloads from the
// shepherd's uring dispatcher and need to decode in their own type namespace.
// Returns nil on unknown message type.
func DecodeShepherdNotifyFromPayload(payload []byte) any {
	if len(payload) < 4 {
		return nil
	}
	msgType := *(*uint32)(unsafe.Pointer(&payload[0]))
	switch msgType {
	case MsgTypeBackingStoreReady:
		return *(*BackingStoreReady)(unsafe.Pointer(&payload[4]))
	case MsgTypeYouHaveFocus:
		return YouHaveFocus{}
	case MsgTypeYouLostFocus:
		return YouLostFocus{}
	case MsgTypeKeyboardFocusGained:
		return KeyboardFocusGained{}
	case MsgTypeKeyboardFocusLost:
		return KeyboardFocusLost{}
	case MsgTypeMouseFocusGained:
		return MouseFocusGained{}
	case MsgTypeMouseFocusLost:
		return MouseFocusLost{}
	case MsgTypeMousePress:
		return *(*MousePress)(unsafe.Pointer(&payload[4]))
	case MsgTypeMouseRelease:
		return *(*MouseRelease)(unsafe.Pointer(&payload[4]))
	case MsgTypeMouseMove:
		return *(*MouseMove)(unsafe.Pointer(&payload[4]))
	case MsgTypeKeyPress:
		return *(*KeyPress)(unsafe.Pointer(&payload[4]))
	case MsgTypeKeyRelease:
		return *(*KeyRelease)(unsafe.Pointer(&payload[4]))
	case MsgTypeAnimationRegistered:
		return *(*AnimationRegistered)(unsafe.Pointer(&payload[4]))
	case MsgTypeAnimationStart:
		return *(*AnimationStart)(unsafe.Pointer(&payload[4]))
	case MsgTypeAnimationUpdate:
		return *(*AnimationUpdate)(unsafe.Pointer(&payload[4]))
	case MsgTypeAnimationFinish:
		return *(*AnimationFinish)(unsafe.Pointer(&payload[4]))
	case MsgTypeAnimationUnregistered:
		return *(*AnimationUnregistered)(unsafe.Pointer(&payload[4]))
	case MsgTypeWindowResized:
		return *(*WindowResized)(unsafe.Pointer(&payload[4]))
	case MsgTypeWindowMoved:
		return *(*WindowMoved)(unsafe.Pointer(&payload[4]))
	case MsgTypeOverlayReady:
		return *(*OverlayReady)(unsafe.Pointer(&payload[4]))
	case MsgTypeOverlayReleased:
		return *(*OverlayReleased)(unsafe.Pointer(&payload[4]))
	default:
		return nil
	}
}
