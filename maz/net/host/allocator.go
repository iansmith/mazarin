package host

import (
	"fmt"
	"sync"
	"sync/atomic"

	"mazzy/mazarin/linksurface"
	"mazzy/mazarin/mem"
)

const PageSize = 4096

// SoftWatermark is the outstanding-page count above which Allocator logs
// a warning and bumps DbgPagesHigh. Matches the design doc's 32 always-
// armed RX slice — once outstanding crosses 32 the plugin has more
// pages in-flight than the always-on RX armed set, which is a slowdown
// signal worth surfacing.
const SoftWatermark = 32

// HardWatermark is the outstanding-page count at which AllocTx starts
// returning nil (drop-on-full). 96 leaves 32 pages in reserve out of
// the 128-page default pool — matches the doc's "32 armed + 32
// client-loan + 64 reserve" split inverted from the consumer side.
const HardWatermark = 96

// packetView is the host-side concrete impl of both linksurface.ReceivePacket
// and linksurface.SendPacket. The same shape works for both because RX
// envelopes carry length (set by the RxDispatcher after Framing validates),
// and TX envelopes carry length out-of-band in TxEnvelope.Len.
type packetView struct {
	vaBase uintptr
	offset int
	length int
}

// VABase returns the page base VA — the address Release uses to return
// the page to the pool.
func (p *packetView) VABase() uintptr { return p.vaBase }

// Offset returns the byte position where L3 starts.
func (p *packetView) Offset() int { return p.offset }

// Len returns the L3 byte count (RX only; SendPacket carries length in
// TxEnvelope.Len).
func (p *packetView) Len() int { return p.length }

// Allocator is a fixed-size, LIFO free-list over a contiguous DMA pool.
// All pages share the same Headroom; AllocTx hands out a SendPacket
// with Offset == Headroom and Len == 0 (plugin writes its L3 bytes
// at VABase+Offset).
//
// The allocator backs both the RX descriptor pool (slot-pinned by the
// Dispatcher) and the TX page pool (handed to plugins). RX slot-pin
// state lives in the Dispatcher; the Allocator does not distinguish.
type Allocator struct {
	pool     *mem.ContiguousBuffer
	headroom int

	mu   sync.Mutex
	free []uintptr // LIFO stack of free page VAs

	outstanding atomic.Int64

	DbgPagesHigh    atomic.Uint64
	DbgPagesDropped atomic.Uint64
}

// NewAllocator allocates a contiguous DMA pool of pages*PageSize bytes
// and seeds the free list with all pages. headroom is the L3 offset
// every TX page is handed out with (Device.Headroom).
func NewAllocator(pages, headroom int) (*Allocator, error) {
	buf, err := mem.AllocContiguous(pages * PageSize)
	if err != nil {
		return nil, fmt.Errorf("mem.AllocContiguous(%d pages): %w", pages, err)
	}
	free := make([]uintptr, 0, pages)
	for i := range pages {
		free = append(free, buf.Addr+uintptr(i)*PageSize)
	}
	return &Allocator{
		pool:     buf,
		headroom: headroom,
		free:     free,
	}, nil
}

// PoolBase returns the base VA of the DMA pool — used by the Dispatcher
// to derive page VA from descIdx (RX slot-pin).
func (a *Allocator) PoolBase() uintptr { return a.pool.Addr }

// PageCount returns the total number of pages in the pool.
func (a *Allocator) PageCount() int { return a.pool.Size / PageSize }

// Headroom returns the L3 offset Device.Headroom() reports.
func (a *Allocator) Headroom() int { return a.headroom }

// AllocTx implements linksurface.Allocator. Returns nil when the hard
// watermark is exceeded or the pool is empty — caller must drop the
// outbound frame and bump its own dropped counter.
func (a *Allocator) AllocTx() linksurface.SendPacket {
	out := a.outstanding.Load()
	if out >= HardWatermark {
		a.DbgPagesDropped.Add(1)
		return nil
	}

	a.mu.Lock()
	if len(a.free) == 0 {
		a.mu.Unlock()
		a.DbgPagesDropped.Add(1)
		return nil
	}
	n := len(a.free) - 1
	va := a.free[n]
	a.free = a.free[:n]
	a.mu.Unlock()

	if a.outstanding.Add(1) > SoftWatermark {
		a.DbgPagesHigh.Add(1)
	}
	return &packetView{vaBase: va, offset: a.headroom, length: 0}
}

// Release implements linksurface.Allocator. Returns a page (typically
// an RX page the plugin is done with, or a TX page the Dispatcher
// drained on TX completion) to the free list.
func (a *Allocator) Release(pkt linksurface.ReceivePacket) {
	if pkt == nil {
		return
	}
	a.ReleaseRaw(pkt.VABase())
}

// ReleaseTx implements linksurface.Allocator. Symmetric drop-path
// helper for SendPacket — same outcome as Release(ReceivePacket).
func (a *Allocator) ReleaseTx(pkt linksurface.SendPacket) {
	if pkt == nil {
		return
	}
	a.ReleaseRaw(pkt.VABase())
}

// AllocRaw is the host-internal allocation primitive used by the
// Dispatcher to pin pages onto RX descriptors. It bypasses the
// SoftWatermark / HardWatermark counters (RX-housekeeping allocations
// shouldn't trip the plugin-pressure thresholds) but still bumps the
// outstanding-pages counter so Release stays balanced.
//
// Returns (0, false) when the free list is empty — the caller must
// decide whether the dispatcher can keep running or must shut down RX.
func (a *Allocator) AllocRaw() (uintptr, bool) {
	a.mu.Lock()
	if len(a.free) == 0 {
		a.mu.Unlock()
		return 0, false
	}
	n := len(a.free) - 1
	va := a.free[n]
	a.free = a.free[:n]
	a.mu.Unlock()
	a.outstanding.Add(1)
	return va, true
}

// ReleaseRaw is the symmetric primitive for AllocRaw / TX-completion
// release. Used by the Dispatcher; same effect as Release but takes a
// raw VA so the Dispatcher doesn't need to build a ReceivePacket just
// to free a TX page on completion.
func (a *Allocator) ReleaseRaw(vaBase uintptr) {
	a.mu.Lock()
	a.free = append(a.free, vaBase)
	a.mu.Unlock()
	a.outstanding.Add(-1)
}

// NewRxPacket builds a ReceivePacket view over a kernel-owned RX page
// at the given VA/offset/length. Used by the RxDispatcher after
// Framing.ValidateReceivePacket returns the L3 boundary. The page is
// already in the pool (pinned to its descIdx slot); Release does the
// re-arm via the Dispatcher's tag→VA mapping.
func NewRxPacket(vaBase uintptr, offset, length int) linksurface.ReceivePacket {
	return &packetView{vaBase: vaBase, offset: offset, length: length}
}
