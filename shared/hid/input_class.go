package hid

// InputClass constants for classifying HID events.
const (
	InputClassKeyboard   = 0
	InputClassMouseClick = 1
	InputClassMouseMove  = 2
)

// BtnMouse is the Linux evdev code boundary between keyboard keys and mouse buttons.
// EV_KEY events with Code < BtnMouse are keyboard events; >= BtnMouse are mouse buttons.
const BtnMouse = 0x110
