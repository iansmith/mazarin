// Package hid defines shared HID (Human Interface Device) types
// used by both kernel and userspace for soft IRQ event delivery.
package hid

// InterruptType identifies the kind of interrupt source.
// 64-bit constant: lower 32 bits = type discriminator, upper 32 bits = optional payload.
type InterruptType uint64

const (
	KeyboardInterrupt InterruptType = 1
	MouseInterrupt    InterruptType = 2
	DiskInterrupt     InterruptType = 3
	SerialInterrupt   InterruptType = 4
)

// InterruptTypeOf extracts the type discriminator (lower 32 bits).
func InterruptTypeOf(t InterruptType) InterruptType {
	return t & 0xFFFFFFFF
}

// InterruptPayload extracts the upper 32-bit payload.
func InterruptPayload(t InterruptType) uint32 {
	return uint32(t >> 32)
}

// WithPayload packs a 32-bit payload into the upper bits of an InterruptType.
func WithPayload(t InterruptType, payload uint32) InterruptType {
	return (InterruptType(payload) << 32) | (t & 0xFFFFFFFF)
}

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

// MaxHIDEvents is the maximum number of events in a single KeyboardInterruptReturn or MouseInterruptReturn.
const MaxHIDEvents = 32

// KeyboardInterruptReturn is returned by WaitSoftIRQ for keyboard interrupt slots.
// The first field is InterruptType so callers can discriminate multi-source slots.
// Total size: 8 + 4 + 4 + 32*8 = 272 bytes.
type KeyboardInterruptReturn struct {
	Interrupt InterruptType            // KeyboardInterrupt (with optional payload)
	Length    uint32                   // Number of valid events in Events array
	_         uint32                   // Padding for 8-byte alignment
	Events   [MaxHIDEvents]HIDEvent   // Events array
}

// MouseInterruptReturn is returned by WaitSoftIRQ for mouse interrupt slots.
// Total size: 8 + 4 + 4 + 32*8 = 272 bytes.
type MouseInterruptReturn struct {
	Interrupt InterruptType            // MouseInterrupt (with optional payload)
	Length    uint32                   // Number of valid events in Events array
	_         uint32                   // Padding for 8-byte alignment
	Events   [MaxHIDEvents]HIDEvent   // Events array
}

// SoftIRQReturn is a generic return structure used by WaitSoftIRQ.
// The InterruptType field allows callers to determine the source and cast accordingly.
// Total size: 8 + 4 + 4 + 32*8 = 272 bytes.
type SoftIRQReturn struct {
	Interrupt InterruptType            // Discriminator (KeyboardInterrupt, MouseInterrupt, etc.)
	Length    uint32                   // Number of valid events in Events array
	_         uint32                   // Padding for 8-byte alignment
	Events   [MaxHIDEvents]HIDEvent   // Events array
}

// InputDeviceInfo describes an available input device.
// Returned by the QueryInputDevices syscall.
type InputDeviceInfo struct {
	IRQNum        uint32        // IRQ number for use with RegisterSoftIRQ
	DeviceType    uint32        // DeviceTypeKeyboard or DeviceTypeMouse
	InterruptKind InterruptType // KeyboardInterrupt or MouseInterrupt
}

// Device type constants for InputDeviceInfo.DeviceType.
const (
	DeviceTypeKeyboard = 1
	DeviceTypeMouse    = 2
)
