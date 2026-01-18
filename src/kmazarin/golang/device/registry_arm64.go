
package device

import (
	"kmazarin/arch/arm64/gic"
)

// ArchSpecificDrivers contains ARM64-specific device drivers.
//
// These drivers typically use ARM-specific instructions or system registers:
// - MSR/MRS instructions for system registers
// - ARM-specific interrupt handling
// - ARM memory barriers
//
// When adding a new ARM64 driver:
// 1. Import the driver's package from arch/arm64/
// 2. Add &DriverType{} to this list
var ArchSpecificDrivers = []Discoverable{
	// ARM Generic Interrupt Controller
	&gic.GICv2Driver{},

	// ARM Generic Timer - will be added
	// &timer.GenericTimerDriver{},
}
