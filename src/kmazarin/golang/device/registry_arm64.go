//go:build linux && arm64

package device

// ArchSpecificDrivers contains ARM64-specific device drivers.
// Go will only compile this file when GOARCH=arm64.
//
// These drivers typically use ARM-specific instructions or system registers:
// - MSR/MRS instructions for system registers
// - ARM-specific interrupt handling
// - ARM memory barriers
//
// When adding a new ARM64 driver:
// 1. Import the driver's package from arch/arm64/
// 2. Add &DriverType{} to this list
//
// NOTE: Temporarily disabled to avoid import cycles.
// The GIC driver needs to be refactored to avoid importing device package.
var ArchSpecificDrivers = []Discoverable{
	// ARM Generic Interrupt Controller - disabled due to import cycle
	// &gic.GICv2Driver{},
	// &gic.GICv3Driver{},

	// ARM Generic Timer - will be added
	// &timer.GenericTimerDriver{},
}
