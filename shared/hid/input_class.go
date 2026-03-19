package hid

// InputClass constants for focus-based input routing.
// Each device class has independent focus tracking.
const (
	InputClassKeyboard   = 0
	InputClassMouseClick = 1
	InputClassMouseMove  = 2
	InputClassCount      = 3
)

// BtnMouse is the Linux evdev code boundary between keyboard keys and mouse buttons.
// EV_KEY events with Code < BtnMouse are keyboard events; >= BtnMouse are mouse buttons.
const BtnMouse = 0x110
