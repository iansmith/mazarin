package device

import (
	"mazzy/kmazarin/rtc"
	"mazzy/kmazarin/uart"
)

// ArchIndependentDrivers contains device drivers that work on any architecture.
// These are pure MMIO devices with no architecture-specific requirements.
//
// When adding a new driver:
// 1. Import the driver's package
// 2. Add &DriverType{} to this list
// 3. The driver will be automatically registered and matched against DTB
//
// Note: VirtIO devices (block, GPU, input, RNG) are PCI-based and initialized
// directly from main.go via their respective Init() functions, not through DTB.
var ArchIndependentDrivers = []Discoverable{
	// UART devices (pure MMIO)
	&uart.PL011Driver{},
	&uart.NS16550Driver{},

	// RTC devices (pure MMIO)
	&rtc.PL031Driver{},
	&rtc.GoldfishDriver{},
}
