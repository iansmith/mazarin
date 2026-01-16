package device

import (
	"kmazarin/uart"
	// "kmazarin/virtio"  // Disabled for now
)

// ArchIndependentDrivers contains device drivers that work on any architecture.
// These are pure MMIO devices with no architecture-specific requirements.
//
// When adding a new driver:
// 1. Import the driver's package
// 2. Add &DriverType{} to this list
// 3. The driver will be automatically registered and matched against DTB
var ArchIndependentDrivers = []Discoverable{
	// UART devices (pure MMIO)
	&uart.PL011Driver{},
	// &uart.NS16550Driver{},

	// VirtIO devices (MMIO-based, arch-independent) - disabled for now
	// &virtio.RNGDriver{},
	// &virtio.RTCDriver{},
	// &virtio.InputDriver{},
	// &virtio.BlockDriver{},
	// &virtio.NetDriver{},

	// GPIO devices - will be added
	// &gpio.BCM2835Driver{},

	// Block devices - will be added
	// &block.AHCIDriver{},

	// Display devices - will be added
	// &display.SimpleFBDriver{},
	// &display.BochsVGADriver{},
}
