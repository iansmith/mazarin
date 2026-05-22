package host

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"mazzy/mazarin/linksurface"
	"mazzy/mazarin/sys"
	"mazzy/shared/iouring"
)

// TxWorkers is the default number of TX worker goroutines per plugin.
// 64 matches the design doc — enough to absorb a busy gvisor TX burst
// without serializing on the SQE mutex.
const TxWorkers = 64

// MaxInflightTx caps the number of un-completed TX submissions. Each
// txTag→pageVA entry sits in inflight; bounded by the ring's SQ size
// (256). Refusing to submit when full beats overflowing the SQ.
const MaxInflightTx = 200

// VirtIONetHdrSize is the 12-byte virtio-net hardware header that prefixes
// every RX and TX buffer. Net.elf owns it; the L3 plugin never sees it
// (RX pages are handed out with Offset=12 already past it; TX pages
// reserve 12 bytes at the front for net.elf to zero before submission).
const VirtIONetHdrSize = 12

// Dispatcher is the single CQE-drain goroutine for the net io_uring
// ring. It services both RX and TX completions:
//
//   - RX: hand the page (advanced past virtio_net_hdr) to the plugin via
//     a non-blocking send on RecvChan (drop + counter bump if full); a
//     fresh page is pulled from Allocator and pinned to the descriptor
//     slot via a re-arm SQE.
//   - TX: pageVA is looked up in inflight; alloc.ReleaseRaw returns
//     the page to the pool.
//
// Single owner: only one Dispatcher per ring. Multiple TX worker
// goroutines share its sqMu + inflight map via SubmitTx.
type Dispatcher struct {
	Ring      *iouring.IORing
	RingID    int
	Allocator *Allocator
	RecvChan  chan<- linksurface.RxEnvelope

	// Rx slot state: slotPages[tag] is the page VA currently pinned
	// to RX descriptor `tag`. Set by PreArm + every re-arm; read on
	// RX completion to derive frame VA. Zero means the slot is parked
	// (allocator was empty when we tried to re-arm). Only touched from Run.
	slotPages []uintptr

	// slotsParked signals that at least one slotPages[tag] is 0. Run
	// scans for zero entries and re-arms them after every CQE-drain
	// pass. Only touched from Run.
	slotsParked bool

	// Tx in-flight: txTag → pageVA. Populated by SubmitTx; consumed
	// on TX completion.
	txMu     sync.Mutex
	inflight map[uint16]uintptr
	txTagSeq atomic.Uint32

	// SQE submission mutex — serializes writers across the Dispatcher
	// (re-arm SQEs) and TxWorkers (submit SQEs).
	sqMu sync.Mutex

	// unkickedSQEs counts SQEs queued in the ring (SQTail advanced) but
	// not yet successfully submitted to the kernel because a prior
	// IOUringEnter failed. The next successful IOUringEnter from any path
	// adds this count to its toSubmit so the kernel catches up; without
	// it, queued SQEs cause head-of-line blocking for the next caller.
	unkickedSQEs atomic.Uint32

	// Diagnostic counters, all accessed via atomic.AddUint64.
	DbgRxDropped  uint64
	DbgRxInvalid  uint64
	DbgTxDropped  uint64
	DbgTxOversize uint64
	DbgKickFailed uint64
}

// NewDispatcher constructs the dispatcher. armed is the number of RX
// descriptor slots; PreArm pulls that many pages from the allocator
// and submits the initial re-arms.
func NewDispatcher(ring *iouring.IORing, ringID, armed int, alloc *Allocator, recvChan chan<- linksurface.RxEnvelope) *Dispatcher {
	return &Dispatcher{
		Ring:      ring,
		RingID:    ringID,
		Allocator: alloc,
		RecvChan:  recvChan,
		slotPages: make([]uintptr, armed),
		inflight:  make(map[uint16]uintptr, MaxInflightTx),
	}
}

// PreArm pulls `len(slotPages)` pages from the allocator, pins them to
// RX descriptor slots 0..N-1, and submits the initial re-arm SQEs.
// Must run once before Run. Rolls back on failure — a partial PreArm
// would leave the SQ ring, allocator outstanding count, and slotPages
// inconsistent for retry/teardown.
func (d *Dispatcher) PreArm() error {
	d.sqMu.Lock()
	defer d.sqMu.Unlock()

	oldTail := atomic.LoadUint32(&d.Ring.SQTail)
	for tag := range d.slotPages {
		va, ok := d.Allocator.AllocRaw()
		if !ok {
			d.rollbackPreArm(tag, oldTail)
			return fmt.Errorf("pre-arm: allocator empty at tag=%d", tag)
		}
		d.slotPages[tag] = va
		// RX descriptor addr MUST be page-aligned per the kernel's
		// handleNetRearmDesc check — the device DMAs the full 4 KiB
		// starting at the page base, with virtio_net_hdr landing at
		// offset 0 and the eth header at offset 12.
		writeRearmSQE(d.Ring, uint32(tag), va)
	}
	n := uint32(len(d.slotPages))
	// writeRearmSQE already advanced SQTail to oldTail+n.
	if _, err := sys.IOUringEnter(d.RingID, n, 0, 0); err != nil {
		d.rollbackPreArm(len(d.slotPages), oldTail)
		return fmt.Errorf("pre-arm IOUringEnter: %w", err)
	}
	return nil
}

// rollbackPreArm undoes the partial work of a failed PreArm. allocatedTags
// is the number of slots that successfully got an AllocRaw (the loop
// index where allocation failed, or len(slotPages) if every AllocRaw
// succeeded and IOUringEnter is what failed). oldTail is the SQTail
// snapshot taken at PreArm entry. Caller MUST hold sqMu.
//
// SQE slots in the ring buffer that writeRearmSQE populated stay
// populated; the kernel only reads up to SQTail, so restoring SQTail
// is sufficient to make them invisible.
func (d *Dispatcher) rollbackPreArm(allocatedTags int, oldTail uint32) {
	for tag := range allocatedTags {
		d.Allocator.ReleaseRaw(d.slotPages[tag])
		d.slotPages[tag] = 0
	}
	atomic.StoreUint32(&d.Ring.SQTail, oldTail)
}

// pendingRearm is a deferred SQE write batched and flushed under sqMu after
// the CQE-drain pass. Batching keeps the sqMu critical section short and
// race-free against TxWorkers, which hold sqMu when writing TX SQEs.
type pendingRearm struct {
	tag    uint32
	pageVA uintptr
}

// Run drains CQEs forever, dispatching RX to the plugin and releasing
// TX pages. Blocks; intended to be launched as `go dispatcher.Run()`.
//
// Uses IOUringEnterBlocking (NOT IOUringEnter) for the wait so the
// Go P is released while parked in the kernel — other goroutines
// (RxConsumer, TxWorkers) need the P to make progress.
func (d *Dispatcher) Run() {
	var pending []pendingRearm
	for {
		if _, err := sys.IOUringEnterBlocking(d.RingID, 0, 1, 0); err != nil {
			// Spurious wake / transient error — small backoff.
			time.Sleep(1 * time.Millisecond)
		}

		cqHead := atomic.LoadUint32(&d.Ring.CQHead)
		cqTail := atomic.LoadUint32(&d.Ring.CQTail)
		pending = pending[:0]
		for cqHead != cqTail {
			cqe := d.Ring.CQEntries[cqHead&iouring.CQMask]
			ud := cqe.UserData
			cqHead++

			if iouring.NetIsTxCQE(ud) {
				txTag := iouring.NetDecodeTag(ud)
				d.txMu.Lock()
				pageVA, ok := d.inflight[txTag]
				if ok {
					delete(d.inflight, txTag)
				}
				d.txMu.Unlock()
				if ok {
					d.Allocator.ReleaseRaw(pageVA)
				}
				continue
			}

			tag := iouring.NetDecodeTag(ud)
			if int(tag) >= len(d.slotPages) {
				atomic.AddUint64(&d.DbgRxInvalid, 1)
				continue
			}
			pageVA := d.slotPages[tag]
			usedLen := int(cqe.Res)

			if d.dispatchRx(pageVA, usedLen) {
				newVA, ok := d.Allocator.AllocRaw()
				if !ok {
					// Pool exhausted — park; recovery scan below retries.
					d.slotPages[tag] = 0
					d.slotsParked = true
					continue
				}
				d.slotPages[tag] = newVA
			}
			// dispatchRx==false → re-arm same page; ==true → re-arm new page.
			pending = append(pending, pendingRearm{uint32(tag), d.slotPages[tag]})
		}
		atomic.StoreUint32(&d.Ring.CQHead, cqHead)

		// Recovery: scan for parked slots and re-arm them. A TX completion
		// or plugin Release during this pass may have returned pages to the
		// pool. Bail on the first AllocRaw failure to avoid hammering the
		// allocator lock under sustained exhaustion.
		if d.slotsParked {
			d.slotsParked = false
			for tag, va := range d.slotPages {
				if va != 0 {
					continue
				}
				newVA, ok := d.Allocator.AllocRaw()
				if !ok {
					d.slotsParked = true
					break
				}
				d.slotPages[tag] = newVA
				pending = append(pending, pendingRearm{uint32(tag), newVA})
			}
		}

		// Flush if either this pass produced re-arms OR a prior failed kick
		// left unkickedSQEs queued in the ring. Gating only on
		// len(pending) > 0 would let queued rearm SQEs sit indefinitely
		// during a steady-state RX flow with no slot churn. Snapshot
		// unkickedSQEs with Load to decide; the authoritative Swap happens
		// inside sqMu, matching SubmitTx's pattern.
		if len(pending) > 0 || d.unkickedSQEs.Load() > 0 {
			d.sqMu.Lock()
			for _, p := range pending {
				writeRearmSQE(d.Ring, p.tag, p.pageVA)
			}
			toSubmit := uint32(len(pending)) + d.unkickedSQEs.Swap(0)
			if toSubmit > 0 {
				if _, err := sys.IOUringEnter(d.RingID, toSubmit, 0, 0); err != nil {
					// SQEs are queued in the ring (writeRearmSQE advanced
					// SQTail); the next successful kick from any path will
					// flush them.
					d.unkickedSQEs.Add(toSubmit)
					atomic.AddUint64(&d.DbgKickFailed, 1)
				}
			}
			d.sqMu.Unlock()
		}
	}
}

// dispatchRx hands the frame to the plugin. Returns true if the page
// was loaned to the plugin (caller must allocate a fresh page for the
// slot); false if the page is to be re-armed in place (runt frame or
// RecvChan full).
//
// usedLen is the CQE's reported byte count — bytes the kernel wrote
// starting at the descriptor address (the page base). We hand the
// plugin a ReceivePacket with Offset=VirtIONetHdrSize so it sees the
// eth header at the start of its view; the plugin's link/ethernet
// layer parses from there.
func (d *Dispatcher) dispatchRx(pageVA uintptr, usedLen int) bool {
	// Defense-in-depth: kernel top-half is trusted (MAZ-28), but a bad
	// usedLen would make NewRxPacket read past the page. Mirrors the
	// MaxTxPayload guard in SubmitTx on the egress side.
	if usedLen <= VirtIONetHdrSize || usedLen-VirtIONetHdrSize > MaxRxPayload {
		atomic.AddUint64(&d.DbgRxInvalid, 1)
		return false
	}
	env := linksurface.RxEnvelope{
		Packet: NewRxPacket(pageVA, VirtIONetHdrSize, usedLen-VirtIONetHdrSize),
	}
	select {
	case d.RecvChan <- env:
		return true
	default:
		atomic.AddUint64(&d.DbgRxDropped, 1)
		return false
	}
}

// MaxTxPayload caps the L3+ plugin bytes per TX page. A page holds
// virtio_net_hdr (12 B) followed by the plugin's bytes; oversize would
// overflow the page on the wire side and corrupt the next clump slot.
const MaxTxPayload = PageSize - VirtIONetHdrSize

// MaxRxPayload caps the L3+ frame bytes the kernel may report on an RX
// completion. Same math as MaxTxPayload — the page holds vhdr + frame,
// so payload-after-vhdr can fill at most PageSize-VirtIONetHdrSize.
const MaxRxPayload = PageSize - VirtIONetHdrSize

// SubmitTx writes the 12-byte virtio_net_hdr at the front of the page,
// emits a TX SQE for env, records the txTag→pageVA mapping in inflight,
// and kicks the kernel. Returns an error only when the SQE write or
// IOUringEnter fails — drop-on-full (inflight or pool exhausted) is
// reported via DbgTxDropped, not as an error.
func (d *Dispatcher) SubmitTx(env linksurface.TxEnvelope) error {
	if env.Packet == nil {
		return fmt.Errorf("SubmitTx: nil packet")
	}
	pageVA := env.Packet.VABase()

	if env.Len < 0 || env.Len > MaxTxPayload {
		atomic.AddUint64(&d.DbgTxOversize, 1)
		d.Allocator.ReleaseRaw(pageVA)
		return nil
	}

	// Zero the 12-byte virtio_net_hdr at the front of the page.
	vhdr := unsafe.Slice((*byte)(unsafe.Pointer(pageVA)), VirtIONetHdrSize)
	for i := range vhdr {
		vhdr[i] = 0
	}

	d.txMu.Lock()
	if len(d.inflight) >= MaxInflightTx {
		d.txMu.Unlock()
		atomic.AddUint64(&d.DbgTxDropped, 1)
		d.Allocator.ReleaseRaw(pageVA)
		return nil
	}
	// Find an unused uint16 tag. The wire encoding (NetEncodeTxUserData) caps
	// the tag at uint16, so after 65536 submissions txTagSeq wraps. With
	// MaxInflightTx (200) << 65536 the loop is O(1) on average; the
	// MaxInflightTx guard above bounds it (≥65336 tags are always unused).
	var txTag uint16
	for {
		txTag = uint16(d.txTagSeq.Add(1))
		if _, busy := d.inflight[txTag]; !busy {
			break
		}
	}
	d.inflight[txTag] = pageVA
	d.txMu.Unlock()

	d.sqMu.Lock()
	// Wire range: addr = page base, off = 0, len = virtio_net_hdr + plugin bytes.
	writeTxSQE(d.Ring, pageVA, 0, uint32(VirtIONetHdrSize+env.Len), txTag)
	toSubmit := uint32(1) + d.unkickedSQEs.Swap(0)
	if _, err := sys.IOUringEnter(d.RingID, toSubmit, 0, 0); err != nil {
		// Roll back our SQE (sqMu held, no concurrent SQTail writers) so
		// the kernel never sees this TX. Re-park the prior unkicked
		// count for next attempt.
		atomic.AddUint32(&d.Ring.SQTail, ^uint32(0))
		d.unkickedSQEs.Add(toSubmit - 1)
		d.sqMu.Unlock()
		d.txMu.Lock()
		delete(d.inflight, txTag)
		d.txMu.Unlock()
		d.Allocator.ReleaseRaw(pageVA)
		atomic.AddUint64(&d.DbgKickFailed, 1)
		return fmt.Errorf("IOUringEnter: %w", err)
	}
	d.sqMu.Unlock()
	return nil
}

// RunTxWorkers launches n goroutines that drain txChan and call
// SubmitTx for every envelope. Blocks (one of the goroutines is the
// caller); call as `go host.RunTxWorkers(...)` if you want non-blocking.
func RunTxWorkers(n int, d *Dispatcher, txChan <-chan linksurface.TxEnvelope) {
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			for env := range txChan {
				if err := d.SubmitTx(env); err != nil {
					// Submission error (not drop-on-full) — log and continue.
					fmt.Printf("[net] TX submit error: %v\n", err)
				}
			}
		}()
	}
	wg.Wait()
}

// writeRearmSQE writes an IOUringOpNetRearmDesc SQE at SQTail and
// advances the tail. Caller MUST hold sqMu.
func writeRearmSQE(ring *iouring.IORing, tag uint32, pageVA uintptr) {
	sqTail := atomic.LoadUint32(&ring.SQTail)
	idx := sqTail & iouring.SQMask
	ring.SQEntries[idx] = iouring.SQEntry{
		Opcode:   iouring.IOUringOpNetRearmDesc,
		FD:       int32(tag),
		Addr:     uint64(pageVA),
		UserData: iouring.NetEncodeRxUserData(uint16(tag)),
	}
	atomic.StoreUint32(&ring.SQTail, sqTail+1)
}

// writeTxSQE writes an IOUringOpNetSubmitTx SQE at SQTail and advances
// the tail. Caller MUST hold sqMu.
func writeTxSQE(ring *iouring.IORing, pageVA, offset uintptr, length uint32, txTag uint16) {
	sqTail := atomic.LoadUint32(&ring.SQTail)
	idx := sqTail & iouring.SQMask
	ring.SQEntries[idx] = iouring.SQEntry{
		Opcode:   iouring.IOUringOpNetSubmitTx,
		Addr:     uint64(pageVA),
		Off:      uint64(offset),
		Len:      length,
		UserData: iouring.NetEncodeTxUserData(txTag),
	}
	atomic.StoreUint32(&ring.SQTail, sqTail+1)
}
