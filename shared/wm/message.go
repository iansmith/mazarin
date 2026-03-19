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
