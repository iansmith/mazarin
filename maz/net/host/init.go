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
// (*linksurface.LinkSurfaceInit, linksurface.LinkSurfaceInjector) itab.
// Without this the host binary's typelinks don't include the interface
// type, so the plugin's `arg.(LinkSurfaceInjector)` assertion fails.
// Mirrors rachel's forceKeyMapperItab pattern.
//
//go:noinline
func ForceLinkSurfaceItab(v interface{}) {
	inj, ok := v.(linksurface.LinkSurfaceInjector)
	if !ok {
		return
	}
	inj.GetDevice()
	inj.GetAllocator()
	inj.GetRecvChan()
	inj.RegisterTxChan(nil)
}
