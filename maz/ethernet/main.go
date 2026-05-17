// ethernet.maz is the link-layer plugin net.elf loads to handle the
// 14-byte Ethernet II header on inbound + outbound frames. It is a
// passive `EthFraming` implementation — `MazarinMain` is a no-op
// (matches keymapper.maz), all work happens via `MazarinShepherd`
// registering a `framingImpl` that net.elf calls per packet.
package main

import (
	"fmt"

	"mazzy/mazarin/linksurface"
)

// MazEntryPoint pins MazarinMain to keep the linker from dropping it.
var MazEntryPoint func() = MazarinMain

// MazarinShepherdAddr pins MazarinShepherd similarly.
var MazarinShepherdAddr func(interface{}) error = MazarinShepherd

func init() {
	if MazEntryPoint == nil {
		panic("unreachable")
	}
	if MazarinShepherdAddr == nil {
		panic("unreachable")
	}
}

// MazarinShepherd accepts an EthFramingInjector from net.elf, pulls
// the Allocator the host pre-filled, and registers a framingImpl as
// the plugin's EthFraming. Net.elf reads the impl back via
// init.Framing after this returns.
//
//go:noinline
func MazarinShepherd(arg interface{}) error {
	inj, ok := arg.(linksurface.EthFramingInjector)
	if !ok {
		return fmt.Errorf("ethernet: expected EthFramingInjector, got %T", arg)
	}
	alloc := inj.GetAllocator()
	if alloc == nil {
		return fmt.Errorf("ethernet: host did not provide an Allocator")
	}
	inj.RegisterFraming(newFraming(alloc))
	return nil
}

// MazarinMain is the .maz entry point. Ethernet framing is a passive
// per-packet callback, not an event-loop service — nothing to run.
//
//go:noinline
func MazarinMain() {
	// no-op
}

// forceFramingMethods keeps the linker from dropping the EthFraming
// method implementations on framingImpl. Without this the linker may
// set Ifn=-1 on one of the three methods, causing "unreachable
// method called" panics on the host side when it calls through the
// EthFraming itab. Mirrors keymapper's forceKeyMapperMethods.
//
//go:noinline
func forceFramingMethods(f linksurface.EthFraming) {
	f.Headroom()
	f.ValidateReceivePacket(0, 0)
	_, _, _ = f.AddSendBytes(linksurface.TxEnvelope{})
}

func main() {
	forceFramingMethods(&framingImpl{})
}
