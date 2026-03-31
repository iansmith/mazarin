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
	TimerInterrupt    InterruptType = 5
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

// CompletionRingSize is the number of event slots in a CompletionRing.
const CompletionRingSize = 256

// CompletionRing is a shared-memory MPSC ring buffer for IRQ completion
// delivery. The kernel IRQ top-half writes events directly to this page;
// userspace polls it without any syscall.
//
// Producer (kernel top-half): acquires Lock, writes event, advances Tail.
// Consumer (userspace): reads events, advances Head (no lock needed).
//
// The page is allocated by userspace and pinned by the kernel via
// SysRegisterCompletionRing. It must not contain Go pointers.
//
// Total size: 32 + 256*8 = 2080 bytes (fits in a 4KB page).
type CompletionRing struct {
	Head     uint32                        // atomic: consumer index (userspace)
	Tail     uint32                        // atomic: producer index (kernel top-half)
	Capacity uint32                        // ring size (256), set by kernel
	Flags    uint32                        // overflow counter (low bits)
	Lock     uint32                        // CAS spinlock for multi-core producers
	_        [3]uint32                     // pad header to 32 bytes
	Events   [CompletionRingSize]HIDEvent  // event slots
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
	DeviceTypeSerial   = 3
	DeviceTypeTimer    = 4
	DeviceTypeBlock    = 5
	DeviceTypeTablet   = 6
)

// Linux evdev axis codes for EV_REL and EV_ABS events.
const (
	RelX     = 0x00 // REL_X
	RelY     = 0x01 // REL_Y
	RelWheel = 0x08 // REL_WHEEL

	AbsX = 0x00 // ABS_X
	AbsY = 0x01 // ABS_Y
)

// AbsMax is the maximum absolute axis value reported by virtio-tablet (0-32767).
const AbsMax = 32767

// BlockVirtualIRQ is the virtual IRQ number for the kernel block device.
// Used by the disk shepherd to register for block device ownership via RegisterSoftIRQ.
const BlockVirtualIRQ uint32 = 201

// TimerVirtualIRQ is the virtual IRQ number for the kernel timer device.
// Well above real device IRQs, within the 256-entry irqToSlot array.
const TimerVirtualIRQ uint32 = 200
