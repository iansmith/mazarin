// gvisor.maz is the L3+ protocol-stack plugin net.elf loads. It wraps
// gvisor.dev/gvisor/pkg/tcpip — ARP, IPv4 (v6 forthcoming via MAZ-33),
// ICMP, UDP, TCP — and bolts on net.elf's DMA-pool / io_uring TX path
// via the linksurface boundary.
//
// Architecture: a "raw L3" inner endpoint implements stack.LinkEndpoint
// such that LinkHeader/MaxHeaderLength = 0. We then wrap it with
// gvisor's link/ethernet.New to add the 14-byte ethernet header on the
// wire. The combined endpoint is what gets attached to gvisor's
// tcpip.Stack as NIC #1. See endpoint.go for the inner; stack.go for
// the wrapping + NIC setup.
//
// MazarinShepherd is the cross-.maz init point: it type-asserts to
// linksurface.LinkSurfaceInjector, stashes the Device + Allocator +
// RecvChan, and registers a TxChan. The stack itself is built in
// MazarinMain (not in MazarinShepherd — net.elf needs MazarinShepherd
// to return promptly so it can finish bring-up).
package main

import (
	"fmt"

	"mazzy/mazarin/linksurface"
	"mazzy/mazarin/mazhost"
)

// Plumbed from MazarinShepherd into MazarinMain. The MazarinShepherd
// call has to return promptly so net.elf can finish bring-up; the
// stack itself is built lazily when MazarinMain runs.
var (
	pendingDev   linksurface.Device
	pendingAlloc linksurface.Allocator
)

func init() { mazhost.PinEntry(MazarinMain, MazarinShepherd) }

// MazarinShepherd is called by net.elf's plugin loader. It type-asserts
// its `any` arg to linksurface.LinkSurfaceInjector (concrete
// struct assertions across .maz module boundaries are unreliable —
// fontcache rule), pulls Device/Allocator/RecvChan, creates the TxChan,
// and registers it back.
//
// Heavy work (gvisor Stack creation, NIC attach, address assignment,
// RX goroutine spawn) happens in MazarinMain, not here. This keeps the
// host's MazarinShepherd-return path fast.
//
//go:noinline
func MazarinShepherd(arg any) error {
	inj, ok := arg.(linksurface.LinkSurfaceInjector)
	if !ok {
		return fmt.Errorf("gvisor: expected LinkSurfaceInjector, got %T", arg)
	}
	pendingDev = inj.GetDevice()
	pendingAlloc = inj.GetAllocator()
	globalRecvChan = inj.GetRecvChan()
	globalTxChan = make(chan linksurface.TxEnvelope, txChanBuffer)
	inj.RegisterTxChan(globalTxChan)
	inj.RegisterNetIPCHandler(handleNetIPC)
	return nil
}

// MazarinMain is the .maz entry point. It builds the tcpip.Stack, hooks
// up the LinkEndpoint, configures the static IPv4 address, and spawns
// the RX-dispatch goroutine. Blocks forever once the stack is alive.
//
//go:noinline
func MazarinMain() {
	s, err := buildStack(pendingDev, pendingAlloc)
	if err != nil {
		fmt.Printf("[gvisor] buildStack failed: %v\n", err)
		return
	}
	globalStack = s
	fmt.Println("[gvisor] stack up; entering RX dispatch loop")
	go runEchoTest(s)
	go runUDPEchoServer(s)
	runRxDispatcher()
}

// main is run by mazgo's plugin init; for .maz plugins it never reaches
// the runtime entry point but it must exist for the linker. We use it
// to force the linker to keep the LinkEndpoint method implementations
// reachable through their interface itabs (without this, an unused
// method gets Ifn=-1 and panics at the first call site).
func main() {
	forceLinkEndpointMethods(&rawEndpoint{})
}
