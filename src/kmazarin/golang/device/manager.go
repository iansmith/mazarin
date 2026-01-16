package device

import (
	"fmt"
	"kmazarin/dtb"
)

// Device storage by type - use typed interface values for type safety
var (
	// Map from compatible string to driver
	driverRegistry = make(map[string]Discoverable)

	// All initialized devices by name
	devices = make(map[string]Closable)

	// Devices by interface type (typed!)
	byteStreams          []ByteStream
	blockDevices         []BlockDevice
	randomSources        []RandomSource
	clocks               []Clock
	displays             []Display
	inputDevices         []InputDevice
	gpios                []GPIO
	interruptControllers []InterruptController
)

// RegisterAllDrivers registers all device drivers.
// Must be called before InitFromDTB.
func RegisterAllDrivers() {
	// Register architecture-independent drivers
	for _, driver := range ArchIndependentDrivers {
		registerDriver(driver)
	}

	// Register architecture-specific drivers
	for _, driver := range ArchSpecificDrivers {
		registerDriver(driver)
	}
}

// registerDriver adds a driver to the registry
func registerDriver(driver Discoverable) {
	for _, compatible := range driver.Compatible() {
		driverRegistry[compatible] = driver
	}
}

// InitFromDTB discovers devices from the Device Tree and initializes them.
// Returns error if DTB parsing fails or any device initialization fails.
func InitFromDTB(dtbAddr uintptr) error {
	fmt.Printf("[Device] Initializing from DTB at 0x%X\n", dtbAddr)

	tree, err := dtb.Parse(dtbAddr)
	if err != nil {
		return fmt.Errorf("parse DTB: %w", err)
	}

	// Walk device tree and initialize devices
	return tree.Walk(func(node *dtb.Node) error {
		// Try to match compatible strings
		for _, compatible := range node.Compatible {
			driver, ok := driverRegistry[compatible]
			if !ok {
				continue // No driver for this compatible string
			}

			// Check if driver can handle this specific node
			if !driver.Probe(node) {
				continue
			}

			fmt.Printf("[Device] %s: initializing '%s' (compatible: %s)\n",
				node.Name, getDriverTypeName(driver), compatible)

			// Initialize device
			device, err := driver.Init(node)
			if err != nil {
				return fmt.Errorf("init %s (%s): %w", node.Name, compatible, err)
			}

			// Store device by name
			devices[device.Name()] = device

			// Index by interfaces - use typed interface values!
			indexDeviceByInterfaces(device)

			fmt.Printf("[Device] %s: initialized as '%s'\n", node.Name, device.Name())

			// Found handler, don't check other compatible strings
			break
		}
		return nil
	})
}

// getDriverTypeName returns a human-readable name for the driver type
func getDriverTypeName(driver Discoverable) string {
	// Use type assertion to get driver type name
	switch driver.(type) {
	default:
		// Get the type name from compatible strings
		compat := driver.Compatible()
		if len(compat) > 0 {
			return compat[0]
		}
		return "unknown"
	}
}

// indexDeviceByInterfaces adds device to interface-specific lists.
// IMPORTANT: Use typed interface values for type safety!
func indexDeviceByInterfaces(dev Closable) {
	// Check ByteStream
	if bs, ok := dev.(ByteStream); ok {
		byteStreams = append(byteStreams, bs)
	}

	// Check BlockDevice
	if bd, ok := dev.(BlockDevice); ok {
		blockDevices = append(blockDevices, bd)
	}

	// Check RandomSource
	if rs, ok := dev.(RandomSource); ok {
		randomSources = append(randomSources, rs)
	}

	// Check Clock
	if clk, ok := dev.(Clock); ok {
		clocks = append(clocks, clk)
	}

	// Check Display
	if disp, ok := dev.(Display); ok {
		displays = append(displays, disp)
	}

	// Check InputDevice
	if input, ok := dev.(InputDevice); ok {
		inputDevices = append(inputDevices, input)
	}

	// Check GPIO
	if gpio, ok := dev.(GPIO); ok {
		gpios = append(gpios, gpio)
	}

	// Check InterruptController
	if ic, ok := dev.(InterruptController); ok {
		interruptControllers = append(interruptControllers, ic)
	}
}

// GetByName returns a device by name.
// Returns nil, false if device not found.
func GetByName(name string) (Closable, bool) {
	dev, ok := devices[name]
	return dev, ok
}

// GetByteStream returns the first byte stream device.
// Typically used for the console UART.
// Returns nil, false if no byte stream device exists.
func GetByteStream() (ByteStream, bool) {
	if len(byteStreams) == 0 {
		return nil, false
	}
	return byteStreams[0], true
}

// GetAllByteStreams returns all byte stream devices.
func GetAllByteStreams() []ByteStream {
	return byteStreams
}

// GetBlockDevice returns the first block device.
// Returns nil, false if no block device exists.
func GetBlockDevice() (BlockDevice, bool) {
	if len(blockDevices) == 0 {
		return nil, false
	}
	return blockDevices[0], true
}

// GetAllBlockDevices returns all block devices.
func GetAllBlockDevices() []BlockDevice {
	return blockDevices
}

// GetRandomSource returns the random number generator.
// Returns nil, false if no random source exists.
func GetRandomSource() (RandomSource, bool) {
	if len(randomSources) == 0 {
		return nil, false
	}
	return randomSources[0], true
}

// GetClock returns the system clock.
// Returns nil, false if no clock exists.
func GetClock() (Clock, bool) {
	if len(clocks) == 0 {
		return nil, false
	}
	return clocks[0], true
}

// GetDisplay returns the display device.
// Returns nil, false if no display exists.
func GetDisplay() (Display, bool) {
	if len(displays) == 0 {
		return nil, false
	}
	return displays[0], true
}

// GetInputDevice returns the first input device.
// Returns nil, false if no input device exists.
func GetInputDevice() (InputDevice, bool) {
	if len(inputDevices) == 0 {
		return nil, false
	}
	return inputDevices[0], true
}

// GetAllInputDevices returns all input devices.
func GetAllInputDevices() []InputDevice {
	return inputDevices
}

// GetGPIO returns the first GPIO controller.
// Returns nil, false if no GPIO exists.
func GetGPIO() (GPIO, bool) {
	if len(gpios) == 0 {
		return nil, false
	}
	return gpios[0], true
}

// GetInterruptController returns the interrupt controller.
// Returns nil, false if no interrupt controller exists.
func GetInterruptController() (InterruptController, bool) {
	if len(interruptControllers) == 0 {
		return nil, false
	}
	return interruptControllers[0], true
}
