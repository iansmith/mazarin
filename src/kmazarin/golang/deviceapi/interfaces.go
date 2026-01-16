// Package deviceapi defines device interfaces without importing any driver packages.
// This breaks import cycles between device and driver packages.
package deviceapi

import (
	"errors"
	"kmazarin/dtb"
)

// ErrNotMyDevice is returned by Init when a driver determines this device
// is not the type it handles. This allows multiple drivers to match the same
// compatible string (e.g., multiple VirtIO device types all match "virtio,mmio").
// The device manager will try the next matching driver when this error is returned.
var ErrNotMyDevice = errors.New("not my device type")

// Discoverable handles DTB matching and device initialization.
// All device drivers implement this interface.
type Discoverable interface {
	// Compatible returns DTB compatible strings this driver handles.
	// Example: []string{"arm,pl011", "arm,primecell"}
	Compatible() []string

	// Probe checks if this driver can handle the given DTB node.
	// Use this for additional validation beyond compatible string matching.
	// Return true if the driver can handle this node.
	Probe(node *dtb.Node) bool

	// Init initializes the device from the DTB node.
	// Returns a Closable that manages the device lifecycle.
	// Return error if initialization fails.
	Init(node *dtb.Node) (Closable, error)
}

// Closable represents an initialized device with lifecycle management.
// All devices returned by Init() must implement this interface.
type Closable interface {
	// Name returns a human-readable device name.
	// Used for device lookup and debugging.
	// Example: "pl011-uart", "virtio-rng", "gicv2"
	Name() string

	// Close shuts down the device and releases resources.
	// Called during system shutdown or device removal.
	Close() error
}

// ByteStream is for devices that provide byte-oriented I/O.
// Examples: UART, serial console, pipes
type ByteStream interface {
	Closable

	// Read reads up to len(p) bytes into p.
	// Returns number of bytes read and any error.
	// May block if no data available.
	Read(p []byte) (n int, err error)

	// Write writes len(p) bytes from p to the device.
	// Returns number of bytes written and any error.
	// May block if device buffer is full.
	Write(p []byte) (n int, err error)
}

// RandomSource is for random number generators.
// Examples: VirtIO RNG, hardware RNG, TPM
type RandomSource interface {
	Closable

	// Read fills p with random bytes.
	// Returns number of bytes read and any error.
	// Should not block - returns immediately with available entropy.
	Read(p []byte) (n int, err error)

	// IsRandomSource is a marker method to distinguish RandomSource from ByteStream.
	// Both interfaces have Read(), but only RandomSource has this marker.
	IsRandomSource()
}

// BlockDevice is for block storage devices.
// Examples: Hard disks, SSDs, SD cards, NVMe
type BlockDevice interface {
	Closable

	// ReadBlock reads a single block at the given LBA.
	// buf must be at least BlockSize() bytes.
	ReadBlock(lba uint64, buf []byte) error

	// WriteBlock writes a single block at the given LBA.
	// buf must be exactly BlockSize() bytes.
	WriteBlock(lba uint64, buf []byte) error

	// BlockSize returns the size of a single block in bytes.
	// Typically 512 or 4096.
	BlockSize() uint64

	// NumBlocks returns the total number of blocks on the device.
	NumBlocks() uint64
}

// Clock is for time sources.
// Examples: RTC, VirtIO RTC, platform timers
type Clock interface {
	Closable

	// Now returns the current time in seconds and nanoseconds since epoch.
	Now() (seconds, nanoseconds uint64)

	// SetTime sets the clock to the given time.
	// May return error if clock is read-only.
	SetTime(seconds, nanoseconds uint64) error
}

// Display is for framebuffer and display devices.
// Examples: Simple framebuffer, VirtIO GPU, hardware framebuffers
type Display interface {
	Closable

	// GetInfo returns framebuffer information.
	GetInfo() *FramebufferInfo

	// Blit copies pixels to the framebuffer at (x, y).
	// pixels must be in the format specified by GetInfo().
	Blit(x, y int, width, height int, pixels []byte) error

	// Clear fills the entire framebuffer with the given color.
	Clear(color uint32) error
}

// FramebufferInfo describes a framebuffer's properties
type FramebufferInfo struct {
	Width       uint32      // Width in pixels
	Height      uint32      // Height in pixels
	Stride      uint32      // Bytes per line
	PixelFormat PixelFormat // Pixel format
	BaseAddr    uintptr     // Physical framebuffer address
}

// PixelFormat describes how pixels are encoded
type PixelFormat int

const (
	PixelFormatRGB888  PixelFormat = iota // 24-bit RGB (8:8:8)
	PixelFormatRGBA888                    // 32-bit RGBA (8:8:8:8)
	PixelFormatBGR888                     // 24-bit BGR (8:8:8)
	PixelFormatBGRA888                    // 32-bit BGRA (8:8:8:8)
)

// InputDevice is for input devices.
// Examples: Keyboard, mouse, touchscreen, VirtIO input
type InputDevice interface {
	Closable

	// ReadEvent reads the next input event.
	// Blocks until an event is available.
	ReadEvent() (*InputEvent, error)
}

// InputEvent represents an input event
type InputEvent struct {
	Type  InputEventType // Event type
	Code  uint16         // Event code (key code, button, etc.)
	Value int32          // Event value (pressed/released, position, etc.)
}

// InputEventType identifies the type of input event
type InputEventType uint16

const (
	InputEventKey InputEventType = iota // Keyboard or button
	InputEventRel                       // Relative movement (mouse)
	InputEventAbs                       // Absolute position (touchscreen)
)

// GPIO is for general-purpose I/O pins.
// Examples: Raspberry Pi GPIO, SoC GPIO controllers
type GPIO interface {
	Closable

	// SetPin sets a pin to high (true) or low (false)
	SetPin(pin uint32, value bool) error

	// GetPin reads the current value of a pin
	GetPin(pin uint32) (bool, error)

	// SetMode configures the pin mode (input, output, etc.)
	SetMode(pin uint32, mode PinMode) error
}

// PinMode represents GPIO pin configuration
type PinMode uint8

const (
	PinModeInput  PinMode = iota // Input mode
	PinModeOutput                // Output mode
	PinModeAlt0                  // Alternate function 0
	PinModeAlt1                  // Alternate function 1
)

// InterruptController manages hardware interrupts.
// Examples: ARM GIC, RISC-V PLIC, x86 APIC
type InterruptController interface {
	Closable

	// RegisterHandler registers an interrupt handler for the given IRQ.
	// handler will be called when the interrupt fires.
	// handler must be nosplit and very fast.
	RegisterHandler(irq uint32, handler func())

	// EnableIRQ enables the given IRQ.
	EnableIRQ(irq uint32)

	// DisableIRQ disables the given IRQ.
	DisableIRQ(irq uint32)
}

// InterruptUser is implemented by devices that use interrupts.
// After device discovery, the device manager calls WireInterrupts()
// to connect these devices to the InterruptController.
type InterruptUser interface {
	Closable

	// WireInterrupts registers this device's interrupt handler(s) with the
	// interrupt controller and enables the IRQ(s).
	// Called after both the device and interrupt controller are initialized.
	WireInterrupts(ic InterruptController) error
}
