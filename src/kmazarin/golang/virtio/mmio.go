package virtio

import "kmazarin/asm"

// MMIO register offsets (VirtIO 1.0 spec)
const (
	MagicValue       = 0x000 // "virt" magic value (0x74726976)
	Version          = 0x004 // Device version (must be 2 for VirtIO 1.0)
	DeviceID         = 0x008 // Device type (1=net, 4=rng, 16=gpu, etc.)
	VendorID         = 0x00C // Vendor ID
	DeviceFeatures   = 0x010 // Device features
	DriverFeatures   = 0x020 // Driver features
	QueueSel         = 0x030 // Queue selector
	QueueNumMax      = 0x034 // Maximum queue size
	QueueNum         = 0x038 // Queue size
	QueueReady       = 0x044 // Queue ready bit
	QueueNotify      = 0x050 // Queue notifier
	InterruptStatus  = 0x060 // Interrupt status
	InterruptACK     = 0x064 // Interrupt acknowledge
	Status           = 0x070 // Device status
	QueueDescLow     = 0x080 // Queue descriptor table address (low 32 bits)
	QueueDescHigh    = 0x084 // Queue descriptor table address (high 32 bits)
	QueueDriverLow   = 0x090 // Queue driver area address (low 32 bits)
	QueueDriverHigh  = 0x094 // Queue driver area address (high 32 bits)
	QueueDeviceLow   = 0x0A0 // Queue device area address (low 32 bits)
	QueueDeviceHigh  = 0x0A4 // Queue device area address (high 32 bits)
	ConfigGeneration = 0x0FC // Configuration atomicity value
	Config           = 0x100 // Device-specific configuration
)

// Device status bits
const (
	StatusAcknowledge = 1 << 0 // Guest OS has found the device
	StatusDriver      = 1 << 1 // Guest OS knows how to drive the device
	StatusDriverOK    = 1 << 2 // Driver is set up and ready
	StatusFeaturesOK  = 1 << 3 // Driver has acknowledged feature bits
	StatusFailed      = 1 << 7 // Something went wrong
)

// MMIOTransport provides access to VirtIO MMIO registers
type MMIOTransport struct {
	baseAddr uintptr
	irq      uint32
}

// ReadReg reads a 32-bit register
//
//go:nosplit
func (t *MMIOTransport) ReadReg(offset uintptr) uint32 {
	return asm.MmioRead32(t.baseAddr + offset)
}

// WriteReg writes a 32-bit register
//
//go:nosplit
func (t *MMIOTransport) WriteReg(offset uintptr, value uint32) {
	asm.MmioWrite32(t.baseAddr+offset, value)
}

// ReadDeviceType reads the device type from MMIO registers
func (t *MMIOTransport) ReadDeviceType() uint32 {
	return t.ReadReg(DeviceID)
}

// Reset resets the VirtIO device
func (t *MMIOTransport) Reset() error {
	t.WriteReg(Status, 0)
	return nil
}

// GetStatus reads the current device status
func (t *MMIOTransport) GetStatus() uint32 {
	return t.ReadReg(Status)
}

// SetStatus sets the device status
func (t *MMIOTransport) SetStatus(status uint32) {
	t.WriteReg(Status, status)
}

// AddStatus adds status bits to the current status
func (t *MMIOTransport) AddStatus(status uint32) {
	current := t.GetStatus()
	t.SetStatus(current | status)
}
