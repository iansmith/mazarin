package host

import "mazzy/mazarin/linksurface"

// RecvChanBuffer is the buffered size of the host→plugin RecvChan.
// Drop-on-full at 32 matches the design doc's "32 always-armed RX"
// figure — RX bursts beyond that are slow enough we'd rather drop and
// log than block the dispatcher.
const RecvChanBuffer = 32

// NewLinkSurfaceInit builds the bag net.elf hands to an L3 plugin's
// MazarinShepherd. The plugin's MazarinShepherd type-asserts the
// arg to linksurface.LinkSurfaceInjector (NOT to *LinkSurfaceInit —
// concrete-struct assertions don't cross .maz boundaries).
//
// The plugin reads Device + Allocator + RecvChan; it creates its own
// TxChan and calls RegisterTxChan to give net.elf the receive side.
// Net.elf reads init.TxChan back after MazarinShepherd returns.
//
// Caller MUST run ForceLinkSurfaceItab on the result (or another
// *LinkSurfaceInit) before any plugin loads, to keep the
// (*LinkSurfaceInit, LinkSurfaceInjector) itab in the host binary.
func NewLinkSurfaceInit(dev linksurface.Device, alloc *Allocator) *linksurface.LinkSurfaceInit {
	return &linksurface.LinkSurfaceInit{
		Device:    dev,
		Allocator: alloc,
		RecvChan:  make(chan linksurface.RxEnvelope, RecvChanBuffer),
	}
}

// ForceLinkSurfaceItab keeps the linker from dropping the
// (*linksurface.LinkSurfaceInit, linksurface.LinkSurfaceInjector) itab
// AND the host-side concrete types' interface methods that plugins
// invoke through Device/Allocator. Without this, the linker DCEs
// methods like (*deviceImpl).GetEthernetAddr or (*Allocator).AllocTx —
// the host never calls them itself, so they're cold from the binary's
// perspective. Plugins then hit "unreachable method called. linker
// bug?" when they call through the itab at runtime.
//
// The function exercises the Injector interface (keeps the *Init →
// LinkSurfaceInjector itab live) plus the Device + Allocator interfaces
// (keeps the *deviceImpl + *Allocator method sets live).
//
//go:noinline
func ForceLinkSurfaceItab(v interface{}) {
	inj, ok := v.(linksurface.LinkSurfaceInjector)
	if !ok {
		return
	}
	dev := inj.GetDevice()
	alloc := inj.GetAllocator()
	inj.GetRecvChan()
	inj.RegisterTxChan(nil)

	// Force Device + Allocator method itabs to stay reachable. The host
	// always populates both (NewLinkSurfaceInit requires non-nil dev +
	// alloc), so nil-checks here would only mask construction bugs.
	dev.GetEthernetAddr()
	dev.Headroom()
	_ = dev.Allocator()
	_ = alloc.AllocTx()
	alloc.Release(nil)
	alloc.ReleaseTx(nil)
}
