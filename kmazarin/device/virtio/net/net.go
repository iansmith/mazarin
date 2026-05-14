// Package net implements the VirtIO network device protocol layer for kmazarin.
// Step 1 (MAZ-18) is types and constants only — discovery, handshake, and I/O
// are added in later MAZ-16 subtasks. The block driver is the structural template.
package net

import "mazzy/kmazarin/device/virtio"

// VirtIO net PCI device IDs
const (
	VIRTIO_NET_DEVICE_ID_LEGACY = 0x1000 // Transitional device
	VIRTIO_NET_DEVICE_ID_MODERN = 0x1041 // Non-transitional (VirtIO 1.0+)
)

// VirtIONetDevice represents a VirtIO network device. Embeds virtio.PCIDevice
// for shared PCI transport state. Net is PCI-only — unlike the block driver
// there is no MMIO/legacy fallback path (and RISC-V is gone from the project).
type VirtIONetDevice struct {
	virtio.PCIDevice // Shared PCI transport (Bus/Slot/Func, capability bases, IRQ state)

	// Two data virtqueues — net is asymmetric: RX buffers are pre-posted,
	// TX descriptors are submitted on demand. One Engine each.
	RxEng virtio.Engine // Receive queue  (virtqueue 0)
	TxEng virtio.Engine // Transmit queue (virtqueue 1)

	// Device-nGnRnE slots for per-packet VirtIONetHdr headers.
	Sidecars virtio.SidecarPool

	// Device configuration — populated from config space during bring-up (MAZ-19).
	MAC               [6]uint8 // Valid iff VIRTIO_NET_F_MAC negotiated
	Status            uint16   // Link status; valid iff VIRTIO_NET_F_STATUS
	MaxVirtqueuePairs uint16   // Valid iff VIRTIO_NET_F_MQ
	MTU               uint16   // Valid iff VIRTIO_NET_F_MTU

	// Interrupt-driven I/O — wired in MAZ-21.
	IRQNum uint32 // Assigned IRQ number (0 = not yet wired)
}
