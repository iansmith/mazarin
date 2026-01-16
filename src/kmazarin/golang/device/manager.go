package device

import (
	"errors"
	"fmt"
	"kmazarin/deviceapi"
	"kmazarin/dtb"
)

// Device storage by type - use typed interface values for type safety
var (
	// Map from compatible string to list of drivers that handle it.
	// Multiple drivers can match the same compatible string (e.g., VirtIO devices).
	driverRegistry = make(map[string][]Discoverable)

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

	// Devices that need interrupt wiring after discovery
	interruptUsers []InterruptUser
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
		driverRegistry[compatible] = append(driverRegistry[compatible], driver)
	}
}

// InitFromDTB discovers devices from the Device Tree and initializes them.
// This function is silent - no output until a console device is available.
// Returns error if DTB parsing fails or a critical device initialization fails.
func InitFromDTB(dtbAddr uintptr) error {
	tree, err := dtb.Parse(dtbAddr)
	if err != nil {
		return fmt.Errorf("parse DTB: %w", err)
	}

	// Walk device tree and initialize devices
	return tree.Walk(func(node *dtb.Node) error {
		return initNodeDevice(node)
	})
}

// initNodeDevice tries to initialize a device for the given DTB node.
// It tries each compatible string and each matching driver until one succeeds.
func initNodeDevice(node *dtb.Node) error {
	// Try to match compatible strings (in order of preference)
	for _, compatible := range node.Compatible {
		drivers, ok := driverRegistry[compatible]
		if !ok {
			continue // No drivers for this compatible string
		}

		// Try each driver that matches this compatible string
		for _, driver := range drivers {
			// Check if driver can handle this specific node (DTB-only checks)
			if !driver.Probe(node) {
				continue
			}

			// Try to initialize - driver may return ErrNotMyDevice
			dev, err := driver.Init(node)
			if err != nil {
				if errors.Is(err, deviceapi.ErrNotMyDevice) {
					// Not this driver's device type, try next driver
					continue
				}
				// Real error - fail the initialization
				return fmt.Errorf("init %s (%s): %w", node.Name, compatible, err)
			}

			// Success! Store device by name
			devices[dev.Name()] = dev

			// Index by interfaces
			indexDeviceByInterfaces(dev)

			// Found handler, don't check other compatible strings or drivers
			return nil
		}
	}

	// No driver matched - that's OK, not all DTB nodes are devices we care about
	return nil
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

	// Check InterruptUser (devices that need IRQ wiring)
	if iu, ok := dev.(InterruptUser); ok {
		interruptUsers = append(interruptUsers, iu)
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

// WireInterrupts connects all InterruptUser devices to the InterruptController.
// This must be called after InitFromDTB and after the interrupt controller is ready.
// Returns error if no interrupt controller exists or if wiring fails.
func WireInterrupts() error {
	ic, ok := GetInterruptController()
	if !ok {
		if len(interruptUsers) > 0 {
			return fmt.Errorf("no interrupt controller but %d devices need IRQs", len(interruptUsers))
		}
		return nil // No IC and no users - that's fine
	}

	for _, iu := range interruptUsers {
		if err := iu.WireInterrupts(ic); err != nil {
			return fmt.Errorf("wire interrupts for %s: %w", iu.Name(), err)
		}
	}

	return nil
}

// InitConsoleFromDTB finds and initializes ONLY the first UART/console device.
// This is called very early in boot, before any other devices are discovered,
// to enable console output as soon as possible.
//
// Returns the ByteStream device for console output, or nil if not found.
// Does not print anything - caller should handle logging once console is available.
func InitConsoleFromDTB(dtbAddr uintptr) ByteStream {
	tree, err := dtb.Parse(dtbAddr)
	if err != nil {
		return nil
	}

	var console ByteStream

	// Walk device tree looking for UART/console devices
	_ = tree.Walk(func(node *dtb.Node) error {
		// If we already found a console, skip remaining nodes
		if console != nil {
			return nil
		}

		// Try to match compatible strings
		for _, compatible := range node.Compatible {
			// Check if this is a known UART type
			if !isUARTCompatible(compatible) {
				continue
			}

			drivers, ok := driverRegistry[compatible]
			if !ok {
				continue
			}

			// Try each driver that matches
			for _, driver := range drivers {
				if !driver.Probe(node) {
					continue
				}

				dev, err := driver.Init(node)
				if err != nil {
					if errors.Is(err, deviceapi.ErrNotMyDevice) {
						continue
					}
					// Real error - skip this device
					continue
				}

				// Check if it's a ByteStream (console device)
				if bs, ok := dev.(ByteStream); ok {
					console = bs
					// Store in device registry
					devices[dev.Name()] = dev
					byteStreams = append(byteStreams, bs)
					return nil // Stop walking
				}

				// Not a console device, close it
				dev.Close()
			}
		}
		return nil
	})

	return console
}

// isUARTCompatible checks if a compatible string identifies a UART device
func isUARTCompatible(compatible string) bool {
	// Known UART compatible strings
	switch compatible {
	case "arm,pl011", "arm,primecell":
		return true
	case "ns16550", "ns16550a", "snps,dw-apb-uart":
		return true
	case "xlnx,xuartps", "cdns,uart-r1p8":
		return true
	default:
		return false
	}
}
