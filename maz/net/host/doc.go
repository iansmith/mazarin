// Package host contains the net.elf-side implementations of the
// mazarin/linksurface contracts: the DMA-page allocator, the Device, the
// LinkSurfaceInit / EthFramingInit constructors with their itab-force
// helpers, the plugin loader glue (LoadEthFramingPlugin /
// LoadLinkSurfacePlugin), and the RX dispatcher + TX worker pool that
// move frames between the io_uring rings and the plugin channels.
//
// This package is consumed only by net.elf (maz/net/main.go); it is not
// a .maz module. Plugins import mazarin/linksurface directly for the
// interface definitions.
//
// Page layout (one DMA page per packet) — see mazarin/linksurface
// package doc for the full diagram. The Allocator hands out pages with
// Offset() = Device.Headroom() = 32 (round_up(virtio_net_hdr_size +
// EthFraming.Headroom(), 16) for plain Ethernet); the wire bytes start
// at page+6 (skipping the 6-byte alignment pad) and run through
// virtio_net_hdr, eth header, and L3 contiguously.
package host
