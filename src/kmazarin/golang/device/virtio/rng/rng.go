
package rng

import (
	"kmazarin/device"
	"unsafe"
)

// RNG implements a VirtIO RNG device driver
type RNG struct {
	barPhys    uintptr // Physical address of BAR (MMIO base)
	deviceID   uint32  // VirtIO device ID (1005 for legacy, 1044 for modern)
	irqNumber  int     // IRQ number for this device (if used)
	initialized bool   // True if Cardinal already initialized this device
}

// Compile-time check that RNG implements DeviceUpDown
var _ device.DeviceUpDown = (*RNG)(nil)

// NewRNG creates a new VirtIO RNG device
func NewRNG(barPhys uintptr, deviceID uint32, irqNumber int, initialized bool) *RNG {
	return &RNG{
		barPhys:    barPhys,
		deviceID:   deviceID,
		irqNumber:  irqNumber,
		initialized: initialized,
	}
}

// Startup initializes the VirtIO RNG device
// If Cardinal already initialized it (initialized=true), this is a no-op
func (r *RNG) Startup() error {
	// Cardinal already initialized the device, nothing to do
	// In the future, we might want to verify the device is functional
	return nil
}

// Shutdown cleanly shuts down the RNG device
func (r *RNG) Shutdown() error {
	// Nothing to do - VirtIO RNG doesn't need cleanup
	return nil
}

// Get16 reads 16 bytes of randomness from the VirtIO RNG device
// Returns an array of 16 random bytes
//
// VirtIO RNG operation (simplified):
// 1. Read from the RNG data register (offset varies by device version)
// 2. For modern VirtIO (1.0+), use queue-based interface
// 3. For legacy VirtIO, may use direct read
//
//go:nosplit
func (r *RNG) Get16() [16]byte {
	var result [16]byte

	// TODO: Implement proper VirtIO RNG read protocol
	// For now, this is a placeholder that would need to:
	// 1. Check device status
	// 2. Submit a buffer to the RNG queue
	// 3. Wait for device to fill buffer
	// 4. Read result from buffer
	//
	// The exact implementation depends on whether this is
	// legacy VirtIO (0.95) or modern VirtIO (1.0+)

	// Placeholder: Return zeros for now
	// Real implementation would interact with VirtIO queues
	_ = r.barPhys // Suppress unused warning

	return result
}

// readMMIO32 reads a 32-bit value from MMIO
//
//go:nosplit
func (r *RNG) readMMIO32(offset uintptr) uint32 {
	addr := r.barPhys + offset
	return *(*uint32)(unsafe.Pointer(addr))
}

// writeMMIO32 writes a 32-bit value to MMIO
//
//go:nosplit
func (r *RNG) writeMMIO32(offset uintptr, value uint32) {
	addr := r.barPhys + offset
	*(*uint32)(unsafe.Pointer(addr)) = value
}
