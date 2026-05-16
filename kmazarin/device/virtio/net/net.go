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

	// TX payload DMA buffer (Device-nGnRnE driver page, allocated by txInit).
	// One synchronous in-flight TX at a time copies its frame here before
	// submit — keeps the whole TX path cache-management-free.
	TxBufPA uintptr // Physical address of the TX payload buffer
	TxBufVA uintptr // Kernel virtual address of the TX payload buffer

	// txInUse serializes SendTx against itself. SendTx is //go:nosplit so it
	// cannot acquire a sync.Mutex (would call morestack); the CAS pattern is
	// used instead. Cleared on every SendTx exit path including timeout —
	// see SendTx doc for the rationale on the timeout/leak case.
	txInUse uint32

	// RX buffer pool — netRxBufCount Device-nGnRnE buffers, each holding one
	// [VirtIONetHdr][Ethernet frame]. Pre-posted to the RX Engine; the device
	// writes received frames into them.
	RxBufPA [netRxBufCount]uintptr
	RxBufVA [netRxBufCount]uintptr
	// rxInFlight maps an RX descriptor index → the RX-buffer index posted under it.
	rxInFlight [netQueueSize]uint16
	// rxRepostChain is the pre-built 1-descriptor chain that DrainRx hands to
	// Engine.Submit when re-posting a buffer. Hoisting it onto the device
	// struct (instead of building a fresh DescChain on the stack each
	// iteration) keeps DrainRx's nosplit frame ~136 bytes lighter, which is
	// what lets the IRQ top-half chain `NonTimerIRQTopHalf → DrainRx →
	// PopUsed → ...` fit under the 792-byte nosplit budget (MAZ-26). Count,
	// Len, and Flags are filled once by rxInit; DrainRx only writes the PA.
	// Single-writer (DrainRx is non-reentrant), so no concurrency concern.
	rxRepostChain virtio.DescChain
	// RX drain state (bring-up verification; a real consumer replaces the peek).
	rxCount      uint32   // atomic: total frames drained
	rxLastLen    uint32   // UsedLen of the most-recently-drained frame
	rxLastSrcMAC [6]uint8 // src MAC of the most-recently-drained frame

	// Device configuration — populated from config space during bring-up (MAZ-19).
	HWAddr            [6]uint8 // Device MAC address; valid iff VIRTIO_NET_F_MAC negotiated
	Status            uint16   // Link status; valid iff VIRTIO_NET_F_STATUS
	MaxVirtqueuePairs uint16   // Valid iff VIRTIO_NET_F_MQ
	MTU               uint16   // Valid iff VIRTIO_NET_F_MTU

	// Interrupt-driven I/O — wired in MAZ-21.
	IRQNum uint32 // Assigned IRQ number (0 = not yet wired)
}
