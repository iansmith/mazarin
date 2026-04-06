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
type Blit struct {
	SID int32
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
	default:
		return nil
	}
}
