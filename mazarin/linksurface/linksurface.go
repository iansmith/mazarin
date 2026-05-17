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
// Page layout. RX and TX are *not* symmetric: the kernel page-aligns
// RX descriptors (handleNetRearmDesc rejects non-page-aligned VAs)
// while TX lets us pass an in-page Off field. So TX gets a 6-byte
// alignment pad up front (yielding 16-byte-aligned L3); RX has the
// virtio_net_hdr at page+0 and L3 lands at offset 26 unaligned.
//
//	TX page (we choose this layout):
//	┌──────┬──────────────┬──────────────────┬──────────────────────────┐
//	│ pad  │virtio_net_hdr│ ethernet header  │  L3 bytes (IP+…)         │
//	│ 6 B  │   12 B       │     14 B         │                          │
//	└──────┴──────────────┴──────────────────┴──────────────────────────┘
//	 0..5    6..17           18..31              32..32+L3Len-1
//	→ SendPacket.Offset() = 32 == Device.Headroom()
//
//	RX page (kernel-imposed):
//	┌──────────────┬──────────────────┬──────────────────────────────────┐
//	│virtio_net_hdr│ ethernet header  │  L3 bytes (IP+…)                 │
//	│   12 B       │     14 B         │                                  │
//	└──────────────┴──────────────────┴──────────────────────────────────┘
//	 0..11           12..25              26..26+L3Len-1
//	→ ReceivePacket.Offset() = 26 (NOT 16-aligned)
//
// Device.Headroom() reports the TX value (32). L3 plugins that care
// about 16-aligned RX must copy or arrange their own alignment;
// gvisor copies into its PacketBuffer so this is mostly invisible.
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

// LinkSurfaceInjector is the cross-.maz contract for the net.elf ⇄ L3-plugin
// boundary. The plugin's MazarinShepherd type-asserts its `interface{}` arg
// to this interface (not the concrete *LinkSurfaceInit — concrete-struct
// assertions across .maz module boundaries are unreliable).
type LinkSurfaceInjector interface {
	GetDevice() Device
	GetAllocator() Allocator
	GetRecvChan() chan RxEnvelope
	RegisterTxChan(ch chan TxEnvelope)
}

// LinkSurfaceInit implements LinkSurfaceInjector and is the host's
// concrete bag. Net.elf fills Device, Allocator, and RecvChan; the L3
// plugin calls RegisterTxChan from inside its MazarinShepherd. Net.elf
// reads TxChan back after MazarinShepherd returns.
type LinkSurfaceInit struct {
	Device    Device
	Allocator Allocator
	RecvChan  chan RxEnvelope
	TxChan    chan TxEnvelope
}

// GetDevice implements LinkSurfaceInjector.
func (l *LinkSurfaceInit) GetDevice() Device { return l.Device }

// GetAllocator implements LinkSurfaceInjector.
func (l *LinkSurfaceInit) GetAllocator() Allocator { return l.Allocator }

// GetRecvChan implements LinkSurfaceInjector.
func (l *LinkSurfaceInit) GetRecvChan() chan RxEnvelope { return l.RecvChan }

// RegisterTxChan implements LinkSurfaceInjector.
func (l *LinkSurfaceInit) RegisterTxChan(ch chan TxEnvelope) { l.TxChan = ch }

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

// EthFramingInjector is the cross-.maz contract for the net.elf ⇄
// ethernet-plugin boundary. ethernet.maz's MazarinShepherd type-asserts
// its `interface{}` arg to this interface.
type EthFramingInjector interface {
	GetAllocator() Allocator
	RegisterFraming(f EthFraming)
}

// EthFramingInit implements EthFramingInjector. Net.elf fills Allocator;
// ethernet.maz calls RegisterFraming from inside its MazarinShepherd.
// Net.elf reads Framing back after MazarinShepherd returns.
type EthFramingInit struct {
	Allocator Allocator
	Framing   EthFraming
}

// GetAllocator implements EthFramingInjector.
func (e *EthFramingInit) GetAllocator() Allocator { return e.Allocator }

// RegisterFraming implements EthFramingInjector.
func (e *EthFramingInit) RegisterFraming(f EthFraming) { e.Framing = f }
