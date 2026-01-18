
package main

import (
	"fmt"
	"kmazarin/device"
)

// TestDeviceDiscovery tests the DTB-based device discovery system
// This is a temporary test function to verify DTB parsing and device matching
// without disrupting the current boot sequence
func TestDeviceDiscovery() {
	fmt.Println("\n[DeviceTest] === Testing DTB Device Discovery ===")

	// Register all device drivers
	device.RegisterAllDrivers()

	// Get DTB address from runtime config
	cfg := GetRuntimeConfig()
	if cfg == nil {
		fmt.Println("[DeviceTest] ERROR: RuntimeConfig not available")
		return
	}

	dtbAddr := cfg.DTBAddress
	fmt.Printf("[DeviceTest] DTB address: 0x%X\n", dtbAddr)

	// Parse DTB and discover devices
	err := device.InitFromDTB(dtbAddr)
	if err != nil {
		fmt.Printf("[DeviceTest] ERROR: %v\n", err)
		return
	}

	// Show what was discovered
	fmt.Println("\n[DeviceTest] Discovered devices:")

	// Check for byte streams (UART)
	if uart, ok := device.GetByteStream(); ok {
		fmt.Printf("  - ByteStream: %s\n", uart.Name())
	}

	// Check for interrupt controller (GIC)
	if gic, ok := device.GetInterruptController(); ok {
		fmt.Printf("  - InterruptController: %s\n", gic.Name())
	}

	// Check for random source (VirtIO RNG)
	if rng, ok := device.GetRandomSource(); ok {
		fmt.Printf("  - RandomSource: %s\n", rng.Name())
	}

	// Check for block devices
	if blk, ok := device.GetBlockDevice(); ok {
		fmt.Printf("  - BlockDevice: %s\n", blk.Name())
	}

	fmt.Println("[DeviceTest] === Test Complete ===\n")
}
