// Package linksurface defines the two injection boundaries between net.elf
// and the .maz plugins it hosts.
//
// LinkSurface is the boundary between net.elf and an L3+ protocol plugin
// (e.g. gvisor.maz). The plugin sees a Device with an Allocator and a pair
// of channels carrying RxEnvelope / TxEnvelope; it never touches io_uring,
// DMA pages, or the kernel net device directly.
//
// EthFraming is the boundary between net.elf and the link-layer plugin
// (ethernet.maz). The plugin tells net.elf how much L2 headroom it needs,
// validates inbound frames, and writes the L2 header onto outbound ones.
//
// Both boundaries are passed via the existing MazarinShepherd init-struct
// pattern; net.elf fills the host-side fields before MazarinShepherd runs,
// the plugin fills its side from inside MazarinShepherd.
//
// Page layout (a single net.elf-owned DMA page per packet):
//
//	┌──────┬──────────────┬──────────────────┬──────────────────────────┐
//	│ pad  │virtio_net_hdr│ ethernet header  │  L3 bytes (IP+…)         │
//	│ 6 B  │   12 B       │     14 B         │                          │
//	└──────┴──────────────┴──────────────────┴──────────────────────────┘
//	 0..5    6..17           18..31              32..32+L3Len-1
//
// Device.Headroom() = round_up(virtio_net_hdr_size + EthFraming.Headroom(), 16)
// — 32 for plain Ethernet. SendPacket.Offset() on a freshly-allocated TX page
// equals Device.Headroom(); the L3 plugin writes its bytes starting there.
package linksurface

// ReceivePacket is a shallow view into a net.elf-owned page. It must not
// outlive the RxEnvelope that carries it; the L3 plugin returns the page
// to the pool by passing this back to Allocator.Release.
type ReceivePacket interface {
	VABase() uintptr
	Offset() int
	Len() int
}

// SendPacket is the L3 plugin's handle on a freshly-allocated TX page.
// The plugin writes L3 bytes at [VABase()+Offset(), VABase()+Offset()+Len),
// then carries the packet in a TxEnvelope that records Len and addressing.
type SendPacket interface {
	VABase() uintptr
	Offset() int
}

// RxEnvelope is delivered on LinkSurfaceInit.RecvChan. The L3 plugin reads
// L3 bytes via Packet, then calls Allocator.Release(Packet) to free the page.
type RxEnvelope struct {
	Packet    ReceivePacket
	SrcMAC    int64
	Ethertype uint16
}

// TxEnvelope is sent by the L3 plugin on LinkSurfaceInit.TxChan. The plugin
// fills Len, DstMAC, and Ethertype after writing its L3 bytes into Packet.
// Net.elf's TX worker reads, calls EthFraming.AddSendBytes, and submits.
type TxEnvelope struct {
	Packet    SendPacket
	Len       int
	DstMAC    int64
	Ethertype uint16
}

// Device is the L3 plugin's view of the underlying NIC.
type Device interface {
	GetEthernetAddr() int64
	Headroom() int
	Allocator() Allocator
}

// Allocator is the L3 plugin's gate to net.elf's DMA page pool.
type Allocator interface {
	AllocTx() SendPacket
	Release(ReceivePacket)
}

// LinkSurfaceInit is the injection bag for the net.elf ⇄ L3-plugin boundary.
// Net.elf fills Device, Allocator, and RecvChan; the L3 plugin fills TxChan
// from inside its MazarinShepherd.
type LinkSurfaceInit struct {
	Device    Device
	Allocator Allocator
	RecvChan  chan RxEnvelope
	TxChan    chan TxEnvelope
}

// EthFraming is the link-layer plugin's contract with net.elf.
//
// ValidateReceivePacket inspects a raw inbound frame (starting at the
// ethernet header — virtio_net_hdr is already stripped by net.elf). It
// returns the L3 boundary and the addressing fields net.elf needs to build
// an RxEnvelope. ok=false means malformed; net.elf releases the page and
// logs reason plus a 64-byte hex dump.
//
// AddSendBytes writes the L2 header in front of the L3 bytes the plugin
// already laid down at env.Packet.VABase()+env.Packet.Offset(), and returns
// the wire-bytes range net.elf hands to io_uring TX (the range covers
// virtio_net_hdr + ethernet header + L3).
type EthFraming interface {
	Headroom() int

	ValidateReceivePacket(rawVA uintptr, rawLen int) (
		l3Offset int, l3Len int,
		srcMAC int64, ethertype uint16,
		ok bool, reason string,
	)

	AddSendBytes(env TxEnvelope) (wireOffset int, wireLen int, err error)
}

// EthFramingInit is the injection bag for the net.elf ⇄ ethernet-plugin
// boundary. Net.elf fills Allocator; ethernet.maz fills Framing from inside
// its MazarinShepherd.
type EthFramingInit struct {
	Allocator Allocator
	Framing   EthFraming
}
