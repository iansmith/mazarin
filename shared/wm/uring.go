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
	MsgTypeBlit              uint32 = 9
	MsgTypeBackingStoreReady uint32 = 10
)

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
type YouHaveFocus struct{}

// YouLostFocus is sent by rachel to a shepherd when it loses focus.
type YouLostFocus struct{}

// MousePress is sent by rachel to a shepherd when a mouse button is pressed.
type MousePress struct {
	X      int32
	Y      int32
	Button int32
}

// MouseRelease is sent by rachel to a shepherd when a mouse button is released.
type MouseRelease struct {
	X      int32
	Y      int32
	Button int32
}

// MouseMove is sent by rachel to a shepherd while a button is held.
type MouseMove struct {
	X int32
	Y int32
}

// KeyPress is sent by rachel to a shepherd when a key is pressed.
type KeyPress struct {
	Code uint16
}

// KeyRelease is sent by rachel to a shepherd when a key is released.
type KeyRelease struct {
	Code uint16
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
	default:
		panic("wm.DecodeWMNotify: unknown message type")
	}
}

// DecodeShepherdNotify decodes a ProtoShepherdNotify message (rachel→shepherd).
// Panics on unknown message type.
func DecodeShepherdNotify(msg *ipc.UringIPCMsg) any {
	msgType := *(*uint32)(unsafe.Pointer(&msg.Payload[0]))
	switch msgType {
	case MsgTypeBackingStoreReady:
		return *(*BackingStoreReady)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeYouHaveFocus:
		return YouHaveFocus{}
	case MsgTypeYouLostFocus:
		return YouLostFocus{}
	case MsgTypeMousePress:
		return *(*MousePress)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeMouseRelease:
		return *(*MouseRelease)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeMouseMove:
		return *(*MouseMove)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeKeyPress:
		return *(*KeyPress)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeKeyRelease:
		return *(*KeyRelease)(unsafe.Pointer(&msg.Payload[4]))
	default:
		panic("wm.DecodeShepherdNotify: unknown message type")
	}
}
