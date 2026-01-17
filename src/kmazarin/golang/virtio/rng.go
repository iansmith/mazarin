package virtio

import (
	"kmazarin/deviceapi"
	"kmazarin/dtb"
)

// VirtIO Device IDs (from VirtIO spec)
const (
	DeviceIDRNG = 4  // Random Number Generator
	DeviceIDRTC = 17 // Real-Time Clock
)

// RNGDriver implements deviceapi.Discoverable for VirtIO RNG
type RNGDriver struct{}

func (d *RNGDriver) Compatible() []string {
	return []string{"virtio,mmio"}
}

func (d *RNGDriver) Probe(node *dtb.Node) bool {
	// Check if this is a VirtIO MMIO device
	if node.Reg == nil || len(node.Reg) == 0 {
		return false
	}

	// TODO: Read device type from MMIO registers to verify this is an RNG
	// For now, just accept any virtio,mmio device and check type later in Init()
	// This avoids accessing hardware during probe which may cause page faults
	// if the device isn't mapped yet.
	return true
}

func (d *RNGDriver) Init(node *dtb.Node) (deviceapi.Closable, error) {
	var irq uint32
	if node.Interrupts != nil && len(node.Interrupts) > 0 {
		irq = node.Interrupts[0]
	}

	// Convert physical address to high-memory kernel address
	// Physical: 0x0A000000 → Kernel: 0xFFFFFFFF0A000000
	const KernelMMIOOffset = 0xFFFFFFFF00000000

	transport := &MMIOTransport{
		baseAddr: node.Reg[0].Address + KernelMMIOOffset,
		irq:      irq,
	}

	// TODO: Verify this is actually an RNG device (DeviceID == 4)
	// This requires reading MMIO registers which may not be mapped yet during Init()
	// For now, we accept any virtio,mmio device and rely on DTB ordering
	// deviceID := transport.ReadDeviceType()
	// if deviceID != DeviceIDRNG {
	// 	return nil, deviceapi.ErrNotMyDevice
	// }

	rng := &RNGDevice{
		transport: transport,
		buffer:    make([]byte, 256),
	}

	// TODO: Initialize VirtIO device hardware
	// Skip hardware init for now - just detect the device
	// This requires proper MMIO mapping before accessing registers
	// if err := rng.init(); err != nil {
	// 	return nil, err
	// }

	return rng, nil
}

// RNGDevice implements RandomSource interface
type RNGDevice struct {
	transport *MMIOTransport
	buffer    []byte
	bufPos    int
	bufLen    int
}

// Closable implementation
func (r *RNGDevice) Name() string {
	return "virtio-rng"
}

func (r *RNGDevice) Close() error {
	return r.transport.Reset()
}

// RandomSource implementation
func (r *RNGDevice) Read(p []byte) (n int, err error) {
	n = 0
	for n < len(p) {
		// Refill buffer if empty
		if r.bufPos >= r.bufLen {
			if err := r.fillBuffer(); err != nil {
				return n, err
			}
		}

		// Copy from buffer
		copied := copy(p[n:], r.buffer[r.bufPos:r.bufLen])
		r.bufPos += copied
		n += copied
	}
	return n, nil
}

// IsRandomSource is a marker method to distinguish from ByteStream
func (r *RNGDevice) IsRandomSource() {}

func (r *RNGDevice) init() error {
	// VirtIO initialization sequence
	// 1. Reset device
	r.transport.Reset()

	// 2. Set ACKNOWLEDGE status
	r.transport.AddStatus(StatusAcknowledge)

	// 3. Set DRIVER status
	r.transport.AddStatus(StatusDriver)

	// 4. Read and negotiate features
	// For RNG, we don't need any special features
	// Just acknowledge what we support (nothing specific for basic RNG)

	// 5. Set FEATURES_OK status
	r.transport.AddStatus(StatusFeaturesOK)

	// 6. Verify FEATURES_OK is still set
	if r.transport.GetStatus()&StatusFeaturesOK == 0 {
		r.transport.AddStatus(StatusFailed)
		return ErrFeatureNegotiation
	}

	// 7. Set DRIVER_OK status
	r.transport.AddStatus(StatusDriverOK)

	// TODO: Set up virtqueues for actual entropy requests
	// For now, this is a stub that will need virtqueue implementation

	return nil
}

func (r *RNGDevice) fillBuffer() error {
	// TODO: Implement VirtQueue operations to request entropy
	// For now, return an error indicating not implemented
	return ErrNotImplemented
}

// Error types
var (
	ErrFeatureNegotiation = &VirtIOError{"feature negotiation failed"}
	ErrNotImplemented     = &VirtIOError{"not implemented"}
)

// VirtIOError represents a VirtIO-specific error
type VirtIOError struct {
	msg string
}

func (e *VirtIOError) Error() string {
	return "virtio: " + e.msg
}
