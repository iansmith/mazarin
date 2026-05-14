// device.go — device.NetDevice interface plumbing (MAZ-23).
//
// Wires *VirtIONetDevice into the kernel's device model, mirroring how the
// block driver's interface methods live in block.go. The SendTx / DrainRx
// methods of device.NetDevice are defined alongside their implementations in
// tx.go / rx.go; this file holds the remaining methods (Name, Close, MAC) and
// the compile-time interface-satisfaction assertion.
package net

import "mazzy/kmazarin/device"

// Compile-time assertion that *VirtIONetDevice satisfies device.NetDevice.
// If any interface method is missing or mis-typed, the build fails here.
var _ device.NetDevice = (*VirtIONetDevice)(nil)

// Name returns the device name.
func (d *VirtIONetDevice) Name() string {
	return "virtio-net"
}

// Close shuts down the device.
func (d *VirtIONetDevice) Close() error {
	// TODO: Implement graceful shutdown.
	return nil
}

// MAC returns the device's 6-byte hardware (MAC) address.
func (d *VirtIONetDevice) MAC() [6]uint8 {
	return d.HWAddr
}
