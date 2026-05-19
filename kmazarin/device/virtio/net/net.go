// Package net implements the VirtIO-net device for kmazarin.
//
// The kernel side is intentionally thin: it brings the device up, exposes
// the RX/TX engines via accessors, and pushes a typed CQE per completion
// from the IRQ top-half. All TX submission and RX re-arming is driven by
// userspace (net.elf) over an io_uring ring, using the
// IOUringOpNetSubmitTx and IOUringOpNetRearmDesc opcodes.
//
// History note: MAZ-18..23 built a kernel-resident raw L2 path (DrainRx,
// SendTx, a 32-slot RX pool, an RxPending flag drained from
// KernelIdleLoop). MAZ-28 moved all of that into net.elf and deleted the
// kernel-side scaffolding. This file shrank accordingly.
package net

import (
	"sync"

	"mazzy/kmazarin/device/virtio"
)

// VirtIO net PCI device IDs
const (
	VIRTIO_NET_DEVICE_ID_LEGACY = 0x1000 // Transitional device
	VIRTIO_NET_DEVICE_ID_MODERN = 0x1041 // Non-transitional (VirtIO 1.0+)
)

// VirtIONetDevice represents a VirtIO network device. The kernel only
// owns enough state to bring the device up and route IRQs to
// io_uring CQEs; the RX page pool and TX frame buffers live in
// userspace (net.elf).
type VirtIONetDevice struct {
	virtio.PCIDevice // Shared PCI transport (Bus/Slot/Func, capability bases, IRQ state)

	RxEng virtio.Engine // virtqueue 0
	TxEng virtio.Engine // virtqueue 1

	// Sidecars is the pool of Device-nGnRnE slots for per-packet
	// VirtIONetHdr headers. Kept for future use (a TX path that builds
	// the header on the kernel side rather than in userspace would
	// rent one of these per send); currently unused — net.elf prepends
	// the virtio_net_hdr inline at the start of each TX frame.
	Sidecars virtio.SidecarPool

	// Device configuration (populated by virtioNetReadConfig).
	HWAddr            [6]uint8
	Status            uint16
	MaxVirtqueuePairs uint16
	MTU               uint16

	// IRQNum is the assigned IRQ vector (0 if MSI-X setup failed).
	IRQNum uint32

	// RxIRQTimestamps records the IRQ-arrival timestamp (ticks of the
	// platform timer) per RX descriptor slot. The IRQ top-half writes
	// it; net.elf reads it via SyscallNetReadRxLatencyUs after
	// dequeuing the corresponding CQE. Race accepted (kernel may
	// overwrite a slot before userspace reads it under back-to-back
	// IRQs on the same slot).
	RxIRQTimestamps [netQueueSize]uint64

	// TxTagByDescIdx maps a TX descriptor head index → the caller's
	// txTag. Written by the IOUringOpNetSubmitTx handler at submit
	// time, read by the IRQ top-half on TX completion to encode the
	// CQE's UserData (so net.elf knows which submit completed).
	TxTagByDescIdx [netQueueSize]uint16

	// TxSubmitMu serializes concurrent IOUringOpNetSubmitTx handlers
	// (full-Go-context SVC path).
	TxSubmitMu sync.Mutex
}
