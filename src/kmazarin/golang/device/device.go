//go:build qemuvirt && aarch64

package device

// DeviceUpDown represents a device that can be started and stopped
// All device drivers in kmazarin should implement this interface
type DeviceUpDown interface {
	// Startup initializes the device
	// Called during kernel initialization
	// Returns error if device cannot be started
	Startup() error

	// Shutdown cleanly shuts down the device
	// Called during kernel shutdown (if implemented)
	// Returns error if device cannot be shut down cleanly
	Shutdown() error
}
