package main

import (
	"sync/atomic"
	"unsafe"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"mazzy/mazarin/linksurface"
)

// rawEndpoint is the inner raw-L3 LinkEndpoint wrapped by gvisor's
// link/ethernet. The wrapper handles all L2 framing; we just shuttle
// bytes between gvisor's PacketBuffer model and net.elf's DMA pool /
// io_uring TX (via WritePackets / deliverRx).
type rawEndpoint struct {
	// dispatcher is set by Attach. Stored as atomic.Pointer so RX
	// can read it lock-free.
	dispatcher atomic.Pointer[stack.NetworkDispatcher]

	mtu uint32
	mac tcpip.LinkAddress

	alloc  linksurface.Allocator
	txChan chan<- linksurface.TxEnvelope
}

// wireMTU is the link-layer MTU (1514 = 1500 L3 payload + 14 byte eth
// header). gvisor's link/ethernet wrapper subtracts 14 before reporting
// up the stack, so the IP layer sees a 1500-byte MTU.
const wireMTU = 1514

// --- NetworkLinkEndpoint interface ---

func (e *rawEndpoint) MTU() uint32                    { return e.mtu }
func (e *rawEndpoint) SetMTU(m uint32)                { e.mtu = m }
func (e *rawEndpoint) MaxHeaderLength() uint16        { return 0 }
func (e *rawEndpoint) LinkAddress() tcpip.LinkAddress { return e.mac }
func (e *rawEndpoint) SetLinkAddress(a tcpip.LinkAddress) {
	e.mac = a
}

func (e *rawEndpoint) Capabilities() stack.LinkEndpointCapabilities {
	// Neither loopback nor offload. The link/ethernet wrapper adds
	// CapabilityResolutionRequired automatically when our capabilities
	// don't include CapabilityLoopback.
	return 0
}

func (e *rawEndpoint) Attach(d stack.NetworkDispatcher) {
	if d == nil {
		e.dispatcher.Store(nil)
		return
	}
	// Explicit heap holder — readability over relying on escape analysis.
	dp := new(stack.NetworkDispatcher)
	*dp = d
	e.dispatcher.Store(dp)
}

func (e *rawEndpoint) IsAttached() bool {
	return e.dispatcher.Load() != nil
}

func (e *rawEndpoint) Wait() {}

func (e *rawEndpoint) ARPHardwareType() header.ARPHardwareType {
	// Returning ARPHardwareNone lets the link/ethernet wrapper above
	// us claim ARPHardwareEther (its method substitutes when the inner
	// returns None — see gvisor link/ethernet.go).
	return header.ARPHardwareNone
}

func (e *rawEndpoint) AddHeader(*stack.PacketBuffer)      {}
func (e *rawEndpoint) ParseHeader(*stack.PacketBuffer) bool { return true }
func (e *rawEndpoint) Close()                              {}
func (e *rawEndpoint) SetOnCloseAction(func())             {}

// --- LinkWriter interface ---

// WritePackets is called by link/ethernet for every outbound packet,
// with the eth header already pushed onto pb.LinkHeader. We delegate
// per-packet to writeOne which copies the assembled wire bytes into a
// net.elf TX page and sends a TxEnvelope.
func (e *rawEndpoint) WritePackets(pkts stack.PacketBufferList) (int, tcpip.Error) {
	written := 0
	for _, pb := range pkts.AsSlice() {
		if !e.writeOne(pb) {
			break
		}
		written++
	}
	return written, nil
}

// writeOne handles a single PacketBuffer. Returns false if we should
// stop the batch (allocator exhausted); true if the packet was either
// queued or dropped (caller keeps going).
func (e *rawEndpoint) writeOne(pb *stack.PacketBuffer) bool {
	total := pb.Size()
	sp := e.alloc.AllocTx()
	if sp == nil {
		atomic.AddUint64(&dbgTxAllocFail, 1)
		return false
	}
	if total > pageSize-sp.Offset() {
		atomic.AddUint64(&dbgTxOversize, 1)
		e.alloc.ReleaseTx(sp)
		return true
	}

	base := sp.VABase() + uintptr(sp.Offset())
	written := 0
	// Iterate the underlying view list directly to avoid the per-packet
	// [][]byte allocation that pb.AsSlices() makes. AsViewList returns the
	// raw ViewList plus a header offset that may fall inside the first
	// view (mirrors gvisor's own fdbased TX path in
	// pkg/tcpip/link/fdbased/endpoint.go:695-730). Caller must not save
	// or modify the list.
	views, offset := pb.AsViewList()
	view := views.Front()
	for ; view != nil && offset >= view.Size(); view = view.Next() {
		offset -= view.Size()
	}
	for ; view != nil; view = view.Next() {
		s := view.AsSlice()[offset:]
		offset = 0
		dst := unsafe.Slice((*byte)(unsafe.Pointer(base+uintptr(written))), len(s))
		copy(dst, s)
		written += len(s)
	}

	env := linksurface.TxEnvelope{Packet: sp, Len: total}
	select {
	case e.txChan <- env:
	default:
		atomic.AddUint64(&dbgTxChanFull, 1)
		e.alloc.ReleaseTx(sp)
	}
	return true
}

// deliverRx is called by runRxDispatcher with each incoming RxEnvelope.
// We build a PacketBuffer carrying eth + L3 and hand it to the wrapper's
// NetworkDispatcher. The wrapper parses the eth header and forwards.
//
// Today this copies the page contents into a fresh buffer.Buffer (gvisor
// owns it past DeliverNetworkPacket, and the net.elf page must be
// released synchronously to re-arm the descriptor). See MAZ-32 for the
// zero-copy follow-up.
func (e *rawEndpoint) deliverRx(env linksurface.RxEnvelope) {
	dpPtr := e.dispatcher.Load()
	if dpPtr == nil || *dpPtr == nil {
		// No NIC attached yet. Drop the packet (and release the page).
		atomic.AddUint64(&dbgRxNoDispatcher, 1)
		e.alloc.Release(env.Packet)
		return
	}

	src := unsafe.Slice(
		(*byte)(unsafe.Pointer(env.Packet.VABase()+uintptr(env.Packet.Offset()))),
		env.Packet.Len(),
	)
	// MAZ-32: zero-copy candidate. The copy happens here.
	view := buffer.NewViewWithData(src)
	pb := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithView(view),
	})
	e.alloc.Release(env.Packet)
	(*dpPtr).DeliverNetworkPacket(0 /* protocol — wrapper parses ethertype */, pb)
	pb.DecRef()
}

// forceLinkEndpointMethods keeps the linker from dropping the
// stack.LinkEndpoint method implementations on rawEndpoint. Mirrors
// keymapper.maz's forceKeyMapperMethods pattern. Called from main().
//
//go:noinline
func forceLinkEndpointMethods(e *rawEndpoint) {
	var le stack.LinkEndpoint = e
	le.MTU()
	le.SetMTU(0)
	le.MaxHeaderLength()
	le.LinkAddress()
	le.SetLinkAddress(tcpip.LinkAddress(""))
	le.Capabilities()
	le.Attach(nil)
	le.IsAttached()
	le.Wait()
	le.ARPHardwareType()
	le.AddHeader(nil)
	le.ParseHeader(nil)
	le.Close()
	le.SetOnCloseAction(nil)
	_, _ = le.WritePackets(stack.PacketBufferList{})
}
