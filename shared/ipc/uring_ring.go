// Package ipc defines IPC data structures shared between kernel and userspace.
//
// This file defines the uring-based IPC ring layout. Each shepherd gets a
// kernel-allocated message ring (3 pages, 64 slots of 128 bytes each).
// Multiple producers write via SysUringSend; a single consumer reads via
// SysUringRecv. The kernel holds a per-ring spinlock for producer serialization.
package ipc

import "unsafe"

// Ring capacity and layout constants.
const (
	UringIPCSlotSize   = 128 // bytes per message slot
	UringIPCCapacity   = 64  // number of message slots
	UringIPCMask       = UringIPCCapacity - 1
	UringIPCHeaderSize = 128 // ring header padded to one slot width
	UringIPCPagesNeeded = 3  // 12KB: header + 64 * 128-byte slots
)

// Protocol discriminators for UringIPCMsg.Protocol.
const (
	ProtoInvalid         uint32 = 0
	ProtoWMNotify        uint32 = 1 // Window manager notification
	ProtoShepherdNotify  uint32 = 2 // Generic shepherd-to-shepherd notification
	ProtoDeath           uint32 = 3 // Shepherd death notification (kernel → shepherd)
	ProtoFSRequest       uint32 = 4 // Forwarded filesystem syscall (kernel → fs shepherd)
	ProtoFSResponse      uint32 = 5 // Filesystem syscall response (fs shepherd → caller)
	ProtoAppStart        uint32 = 6 // Application start notification
)

// UringIPCRingHeader is the metadata at offset 0 of the ring's first page.
// Padded to 128 bytes (one cache-line pair) to avoid false sharing with slot 0.
//
// The kernel owns the Lock field — userspace never touches it.
// Head is advanced by the consumer (SysUringRecv).
// Tail is advanced by the kernel on behalf of producers (SysUringSend).
type UringIPCRingHeader struct {
	Head     uint32   // consumer read position (advanced after consuming)
	Tail     uint32   // producer write position (advanced after writing)
	Capacity uint32   // always UringIPCCapacity (64)
	RingMask uint32   // always UringIPCMask (63)
	OwnerID  uint64   // 64-bit uring ID for this shepherd
	OwnerSID int16    // shepherd that consumes from this ring
	_pad0    [2]byte
	Lock     int32    // kernel-side producer spinlock (0=free, 1=held)
	_pad1    [96]byte // pad to 128 bytes total
}

// UringIPCMsg is a single 128-byte message slot in the ring.
// The Protocol field determines how to interpret Payload.
type UringIPCMsg struct {
	Protocol  uint32    // protocol discriminator (ProtoWMNotify, etc.)
	SenderSID int16     // shepherd that sent this message
	Flags     uint16    // reserved, must be 0
	SenderID  uint64    // sender's uring ID (for verification)
	Payload   [112]byte // protocol-specific payload
}

// Compile-time size assertions.
var _ [UringIPCHeaderSize]byte = [unsafe.Sizeof(UringIPCRingHeader{})]byte{}
var _ [UringIPCSlotSize]byte = [unsafe.Sizeof(UringIPCMsg{})]byte{}
