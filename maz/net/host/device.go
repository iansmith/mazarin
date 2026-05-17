package host

import "mazzy/mazarin/linksurface"

// QEMU's default MAC for virtio-net (also hard-coded in maz/net/main.go
// for the Phase A ARP probe). Replace with a kernel SysNetReadMAC syscall
// when one is added (see maz/net/main.go comment).
var defaultHWAddr = [6]uint8{0x52, 0x54, 0x00, 0x12, 0x34, 0x56}

// deviceImpl is the host-side linksurface.Device. Its three accessors
// satisfy the contract gvisor.maz and other L3 plugins see: read your
// MAC, read your headroom, get the Allocator handle.
type deviceImpl struct {
	hwAddr    [6]uint8
	allocator *Allocator
}

// NewDevice constructs a linksurface.Device backed by alloc. mac may be
// the zero value to fall back to QEMU's default virtio-net MAC.
func NewDevice(alloc *Allocator, mac [6]uint8) linksurface.Device {
	if mac == ([6]uint8{}) {
		mac = defaultHWAddr
	}
	return &deviceImpl{hwAddr: mac, allocator: alloc}
}

// GetEthernetAddr packs the 6-byte MAC into the low six bytes of a
// big-endian int64 (high two bytes zero), matching linksurface.Device's
// 48-bit-in-int64 convention.
func (d *deviceImpl) GetEthernetAddr() int64 {
	return int64(d.hwAddr[0])<<40 |
		int64(d.hwAddr[1])<<32 |
		int64(d.hwAddr[2])<<24 |
		int64(d.hwAddr[3])<<16 |
		int64(d.hwAddr[4])<<8 |
		int64(d.hwAddr[5])
}

// Headroom returns Allocator.Headroom — the bytes between page base
// and the L3 plugin's first writable byte.
func (d *deviceImpl) Headroom() int { return d.allocator.Headroom() }

// Allocator returns the underlying Allocator as the contract interface.
func (d *deviceImpl) Allocator() linksurface.Allocator { return d.allocator }
