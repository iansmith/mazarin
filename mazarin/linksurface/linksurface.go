// Package linksurface defines the injection boundary between net.elf and the
// L3+ protocol plugin (e.g. gvisor.maz). The plugin sees a Device with an
// Allocator and a pair of channels carrying RxEnvelope / TxEnvelope; it never
// touches io_uring, DMA pages, or the kernel net device directly.
//
// The L3+ plugin owns the link-layer header (ethernet, …) — gvisor's
// link/ethernet wrapper writes/parses the 14-byte L2 frame on top of the L3
// payload. Net.elf only owns the 12-byte virtio_net_hdr that prefixes every
// page.
//
// Both pages are laid out identically — no TX alignment pad, no L2 framing
// on the host side:
//
//	page-start                                                      page-end
//	┌──────────────┬─────────────────────────────────────────────────────┐
//	│virtio_net_hdr│ plugin-owned bytes (eth header + L3 + …)            │
//	│   12 B       │                                                     │
//	└──────────────┴─────────────────────────────────────────────────────┘
//	 0..11           12..12+Len-1
//	→ Packet.Offset() = 12 == Device.Headroom()
//
// On TX, net.elf zeros virtio_net_hdr at page+0 and submits
// (addr=pageVA, off=0, len=12+TxEnvelope.Len). On RX, net.elf hands the
// plugin a ReceivePacket with Offset=12 and Len=usedLen-12 — the eth header
// lands at the start of the plugin-visible range.
//
// The injection bag is passed via the existing MazarinShepherd init-struct
// pattern; net.elf fills the host-side fields before MazarinShepherd runs,
// the plugin fills its side from inside MazarinShepherd.
package linksurface

// ReceivePacket is a shallow view into a net.elf-owned page. It must not
// outlive the RxEnvelope that carries it; the L3 plugin returns the page to
// the pool by passing this back to Allocator.Release.
type ReceivePacket interface {
	VABase() uintptr
	Offset() int
	Len() int
}

// SendPacket is the L3 plugin's handle on a freshly-allocated TX page.
// The plugin writes its bytes (eth header + L3 + …) at
// [VABase()+Offset(), VABase()+Offset()+Len), then carries the packet in a
// TxEnvelope that records the byte count.
type SendPacket interface {
	VABase() uintptr
	Offset() int
}

// RxEnvelope is delivered on LinkSurfaceInit.RecvChan. The L3 plugin reads
// the bytes via Packet (starting at eth header), then calls
// Allocator.Release(Packet) to free the page.
type RxEnvelope struct {
	Packet ReceivePacket
}

// TxEnvelope is sent by the L3 plugin on LinkSurfaceInit.TxChan. The plugin
// fills Len after writing its bytes (eth header + L3 + …) into Packet.
// Net.elf's TX worker reads, zeros virtio_net_hdr, and submits.
type TxEnvelope struct {
	Packet SendPacket
	Len    int
}

// Device is the L3 plugin's view of the underlying NIC.
type Device interface {
	// GetEthernetAddr returns the 6-byte MAC, packed BE into the low 6 bytes
	// of a int64 (high two bytes are zero).
	GetEthernetAddr() int64

	// Headroom is the byte position at which the plugin should start writing
	// (= the byte count the plugin must leave alone at the start of each
	// page for net.elf's virtio_net_hdr). 12 bytes today.
	Headroom() int

	Allocator() Allocator
}

// Allocator is the L3 plugin's gate to net.elf's DMA page pool.
type Allocator interface {
	AllocTx() SendPacket
	Release(ReceivePacket)

	// ReleaseTx returns a TX page to the pool on the plugin's drop path —
	// when the plugin allocated via AllocTx but is abandoning the packet
	// before queuing it for the host (txChan full, oversized, etc.).
	ReleaseTx(SendPacket)
}

// LinkSurfaceInjector is the cross-.maz contract for the net.elf ⇄ L3-plugin
// boundary. The plugin's MazarinShepherd type-asserts its `any` arg
// to this interface (not the concrete *LinkSurfaceInit — concrete-struct
// assertions across .maz module boundaries are unreliable).
type LinkSurfaceInjector interface {
	GetDevice() Device
	GetAllocator() Allocator
	GetRecvChan() chan RxEnvelope
	RegisterTxChan(ch chan TxEnvelope)
}

// LinkSurfaceInit implements LinkSurfaceInjector and is the host's concrete
// bag. Net.elf fills Device, Allocator, and RecvChan; the L3 plugin calls
// RegisterTxChan from inside its MazarinShepherd. Net.elf reads TxChan back
// after MazarinShepherd returns.
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
