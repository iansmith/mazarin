// Package hid defines shared HID (Human Interface Device) types
// used by both kernel and userspace for soft IRQ event delivery.
package hid

// HIDEvent matches the virtio_input_event layout (8 bytes).
// Fields correspond to Linux evdev event types.
type HIDEvent struct {
	Type  uint16 // EV_SYN=0, EV_KEY=1, EV_REL=2, EV_ABS=3
	Code  uint16 // Key code, REL_X/REL_Y, ABS_X/ABS_Y, etc.
	Value uint32 // 1=press, 0=release, or axis value
}

// Linux evdev event type constants.
const (
	EvSyn = 0 // Synchronization event
	EvKey = 1 // Key press/release
	EvRel = 2 // Relative axis (mouse movement)
	EvAbs = 3 // Absolute axis (touchscreen)
)

// MaxHIDEvents is the maximum number of events in a single SoftIRQReturn.
const MaxHIDEvents = 32

// SoftIRQReturn is the structure passed by pointer to the WaitSoftIRQ syscall.
// The kernel fills it with events before unblocking the caller.
// Total size: 4 + 4 + 32*8 = 264 bytes.
type SoftIRQReturn struct {
	Length uint32                 // Number of valid events in Events array
	_      uint32                 // Padding for 8-byte alignment
	Events [MaxHIDEvents]HIDEvent // Events array
}

// InputDeviceInfo describes an available input device.
// Returned by the QueryInputDevices syscall.
type InputDeviceInfo struct {
	IRQNum     uint32 // IRQ number for use with RegisterSoftIRQ
	DeviceType uint32 // DeviceTypeKeyboard or DeviceTypeMouse
}

// Device type constants for InputDeviceInfo.DeviceType.
const (
	DeviceTypeKeyboard = 1
	DeviceTypeMouse    = 2
)
