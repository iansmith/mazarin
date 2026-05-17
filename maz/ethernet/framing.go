package main

import (
	"encoding/binary"
	"unsafe"

	"mazzy/mazarin/linksurface"
)

// EthHeaderLen is the 14-byte Ethernet II header — what framingImpl
// owns and what Headroom() reports.
const EthHeaderLen = 14

// VirtIONetHdrLen is the 12-byte virtio_net_hdr the kernel/host
// owns. It's stripped from the buffer the host hands to
// ValidateReceivePacket — the plugin never sees it on RX.
// AddSendBytes still has to account for it in wireOffset.
const VirtIONetHdrLen = 12

// AlignmentPad is the 6-byte pad at the start of every TX page that
// keeps L3 16-byte-aligned (6 + 12 + 14 = 32, which is round_up(26,
// 16)). The wire-bytes range AddSendBytes returns starts after this
// pad so the device sees only [virtio_net_hdr][eth][L3].
const AlignmentPad = VirtIONetHdrLen + EthHeaderLen - 16 // = 6+12+14 - 32 reversed; literal 6

// defaultHWAddr — QEMU's default MAC for virtio-net. Hard-coded for
// now; matches maz/net/host/device.go and maz/net/main.go. Replace
// with a value injected from the host's Device when an EthFraming-
// side MAC accessor lands (see maz/net/host/device.go TODO).
var defaultHWAddr = [6]uint8{0x52, 0x54, 0x00, 0x12, 0x34, 0x56}

// framingImpl is the EthFraming the plugin registers with net.elf.
// Holds the Allocator the host injected — the stub doesn't yet self-
// TX (ARP, neighbour solicitations) but the field documents the
// wiring for when it does.
type framingImpl struct {
	hwAddr [6]uint8
	alloc  linksurface.Allocator
}

// newFraming constructs a framingImpl with the default MAC.
func newFraming(alloc linksurface.Allocator) *framingImpl {
	return &framingImpl{hwAddr: defaultHWAddr, alloc: alloc}
}

// Headroom implements EthFraming. 14 bytes is just the ethernet header
// — net.elf adds the virtio_net_hdr separately when computing
// Device.Headroom (round_up(12+14, 16) = 32).
func (f *framingImpl) Headroom() int { return EthHeaderLen }

// ValidateReceivePacket implements EthFraming. The host has already
// stripped virtio_net_hdr; rawVA points at the ethernet header.
// Layout: [dstMAC 6][srcMAC 6][ethertype 2][payload].
func (f *framingImpl) ValidateReceivePacket(rawVA uintptr, rawLen int) (
	l3Offset int, l3Len int,
	srcMAC int64, ethertype uint16,
	ok bool, reason string,
) {
	if rawLen < EthHeaderLen {
		return 0, 0, 0, 0, false, "ethernet: runt frame"
	}
	eth := unsafe.Slice((*byte)(unsafe.Pointer(rawVA)), rawLen)
	srcMAC = int64(eth[6])<<40 |
		int64(eth[7])<<32 |
		int64(eth[8])<<24 |
		int64(eth[9])<<16 |
		int64(eth[10])<<8 |
		int64(eth[11])
	ethertype = binary.BigEndian.Uint16(eth[12:14])
	return EthHeaderLen, rawLen - EthHeaderLen, srcMAC, ethertype, true, ""
}

// AddSendBytes implements EthFraming. The L3 plugin laid down its
// bytes at [VABase+Offset, VABase+Offset+Len). We write the 14-byte
// eth header directly in front (at [VABase+Offset-14, VABase+Offset))
// and zero the 12-byte virtio_net_hdr in the slot before that. The
// wire range we return covers [VABase+AlignmentPad, end of L3] —
// 6+12+14+Len bytes, starting at page+6.
func (f *framingImpl) AddSendBytes(env linksurface.TxEnvelope) (wireOffset int, wireLen int, err error) {
	if env.Packet == nil {
		return 0, 0, &framingError{"ethernet: nil Packet"}
	}
	pkt := env.Packet
	l3Offset := pkt.Offset()
	if l3Offset < AlignmentPad+VirtIONetHdrLen+EthHeaderLen {
		return 0, 0, &framingError{"ethernet: insufficient headroom"}
	}

	base := pkt.VABase()

	// virtio_net_hdr: 12 zero bytes (flags=0, gso=NONE, hdr_len=0,…).
	vhdr := unsafe.Slice((*byte)(unsafe.Pointer(base+uintptr(AlignmentPad))), VirtIONetHdrLen)
	for i := range vhdr {
		vhdr[i] = 0
	}

	// Ethernet header just before L3: dstMAC, srcMAC, ethertype.
	eth := unsafe.Slice((*byte)(unsafe.Pointer(base+uintptr(l3Offset-EthHeaderLen))), EthHeaderLen)
	eth[0] = uint8(env.DstMAC >> 40)
	eth[1] = uint8(env.DstMAC >> 32)
	eth[2] = uint8(env.DstMAC >> 24)
	eth[3] = uint8(env.DstMAC >> 16)
	eth[4] = uint8(env.DstMAC >> 8)
	eth[5] = uint8(env.DstMAC)
	eth[6], eth[7], eth[8], eth[9], eth[10], eth[11] = f.hwAddr[0], f.hwAddr[1], f.hwAddr[2], f.hwAddr[3], f.hwAddr[4], f.hwAddr[5]
	binary.BigEndian.PutUint16(eth[12:14], env.Ethertype)

	wireOffset = AlignmentPad
	wireLen = VirtIONetHdrLen + EthHeaderLen + env.Len
	return wireOffset, wireLen, nil
}

// framingError carries a plain string. EthFraming.AddSendBytes returns
// `error`, but the .maz boundary is happier with concrete types from
// inside the plugin (cross-module fmt.Errorf-style allocations have
// burned us in other plugins). Single-purpose, single-allocation.
type framingError struct{ msg string }

func (e *framingError) Error() string { return e.msg }
