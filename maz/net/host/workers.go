package host

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

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

// Dispatcher is the single CQE-drain goroutine for the net io_uring
// ring. It services both RX and TX completions:
//
//   - RX: Framing.ValidateReceivePacket peels the L2 header; an
//     RxEnvelope is non-blocking-sent on RecvChan (drop + counter bump
//     if full); a fresh page is pulled from Allocator and pinned to the
//     descriptor slot via a re-arm SQE.
//   - TX: pageVA is looked up in inflight; alloc.ReleaseRaw returns
//     the page to the pool.
//
// Single owner: only one Dispatcher per ring. Multiple TX worker
// goroutines share its sqMu + inflight map via SubmitTx.
type Dispatcher struct {
	Ring      *iouring.IORing
	RingID    int
	Allocator *Allocator
	Framing   linksurface.EthFraming
	RecvChan  chan<- linksurface.RxEnvelope

	// Rx slot state: slotPages[tag] is the page VA currently pinned
	// to RX descriptor `tag`. Set by PreArm + every re-arm; read on
	// RX completion to derive frame VA.
	slotPages []uintptr

	// Tx in-flight: txTag → pageVA. Populated by SubmitTx; consumed
	// on TX completion.
	txMu       sync.Mutex
	inflight   map[uint16]uintptr
	txTagSeq   atomic.Uint32

	// SQE submission mutex — serializes writers across the Dispatcher
	// (re-arm SQEs) and TxWorkers (submit SQEs).
	sqMu sync.Mutex

	DbgRxDropped uint64 // accessed via atomic
	DbgRxInvalid uint64
	DbgTxDropped uint64
}

// NewDispatcher constructs the dispatcher. armed is the number of RX
// descriptor slots; PreArm pulls that many pages from the allocator
// and submits the initial re-arms.
func NewDispatcher(ring *iouring.IORing, ringID, armed int, alloc *Allocator, framing linksurface.EthFraming, recvChan chan<- linksurface.RxEnvelope) *Dispatcher {
	return &Dispatcher{
		Ring:      ring,
		RingID:    ringID,
		Allocator: alloc,
		Framing:   framing,
		RecvChan:  recvChan,
		slotPages: make([]uintptr, armed),
		inflight:  make(map[uint16]uintptr, MaxInflightTx),
	}
}

// PreArm pulls `len(slotPages)` pages from the allocator, pins them to
// RX descriptor slots 0..N-1, and submits the initial re-arm SQEs.
// Must run once before Run.
func (d *Dispatcher) PreArm() error {
	d.sqMu.Lock()
	defer d.sqMu.Unlock()

	for tag := range d.slotPages {
		va, ok := d.Allocator.AllocRaw()
		if !ok {
			return fmt.Errorf("pre-arm: allocator empty at tag=%d", tag)
		}
		d.slotPages[tag] = va
		writeRearmSQE(d.Ring, uint32(tag), va)
	}
	n := uint32(len(d.slotPages))
	atomic.StoreUint32(&d.Ring.SQTail, n)
	if _, err := sys.IOUringEnter(d.RingID, n, 0, 0); err != nil {
		return fmt.Errorf("pre-arm IOUringEnter: %w", err)
	}
	return nil
}

// Run drains CQEs forever, dispatching RX to the plugin and releasing
// TX pages. Blocks; intended to be launched as `go dispatcher.Run()`.
func (d *Dispatcher) Run() {
	for {
		if _, err := sys.IOUringEnter(d.RingID, 0, 1, 0); err != nil {
			// Spurious wake / transient error — small backoff.
			time.Sleep(1 * time.Millisecond)
		}

		cqHead := atomic.LoadUint32(&d.Ring.CQHead)
		cqTail := atomic.LoadUint32(&d.Ring.CQTail)
		rearms := uint32(0)
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
				// Page is loaned to plugin; pull a fresh one for re-arm.
				newVA, ok := d.Allocator.AllocRaw()
				if !ok {
					// Pool exhausted — leave slot un-armed; will
					// recover when plugin Release()s a page.
					d.slotPages[tag] = 0
					continue
				}
				d.slotPages[tag] = newVA
			}
			// If dispatchRx returned false the page was kept (drop /
			// invalid frame) — re-arm with the same page.
			writeRearmSQE(d.Ring, uint32(tag), d.slotPages[tag])
			rearms++
		}
		atomic.StoreUint32(&d.Ring.CQHead, cqHead)

		if rearms > 0 {
			d.sqMu.Lock()
			_, _ = sys.IOUringEnter(d.RingID, rearms, 0, 0)
			d.sqMu.Unlock()
		}
	}
}

// dispatchRx validates the frame and tries to hand it to the plugin.
// Returns true if the page was loaned to the plugin (caller must
// allocate a fresh page for the slot); false if the page is to be
// re-armed in place (invalid frame or RecvChan full).
func (d *Dispatcher) dispatchRx(pageVA uintptr, usedLen int) bool {
	l3Offset, l3Len, srcMAC, ethertype, ok, _ := d.Framing.ValidateReceivePacket(pageVA, usedLen)
	if !ok {
		atomic.AddUint64(&d.DbgRxInvalid, 1)
		return false
	}
	env := linksurface.RxEnvelope{
		Packet:    NewRxPacket(pageVA, l3Offset, l3Len),
		SrcMAC:    srcMAC,
		Ethertype: ethertype,
	}
	select {
	case d.RecvChan <- env:
		return true
	default:
		atomic.AddUint64(&d.DbgRxDropped, 1)
		return false
	}
}

// SubmitTx writes a TX SQE for env, records the txTag→pageVA mapping
// in inflight, and kicks the kernel. Returns an error only when the
// SQE write or IOUringEnter fails — drop-on-full (inflight or pool
// exhausted) is reported via DbgTxDropped, not as an error.
func (d *Dispatcher) SubmitTx(env linksurface.TxEnvelope) error {
	if env.Packet == nil {
		return fmt.Errorf("SubmitTx: nil packet")
	}
	wireOffset, wireLen, err := d.Framing.AddSendBytes(env)
	if err != nil {
		return fmt.Errorf("Framing.AddSendBytes: %w", err)
	}
	pageVA := env.Packet.VABase()
	txTag := uint16(d.txTagSeq.Add(1))

	d.txMu.Lock()
	if len(d.inflight) >= MaxInflightTx {
		d.txMu.Unlock()
		atomic.AddUint64(&d.DbgTxDropped, 1)
		d.Allocator.ReleaseRaw(pageVA)
		return nil
	}
	d.inflight[txTag] = pageVA
	d.txMu.Unlock()

	d.sqMu.Lock()
	writeTxSQE(d.Ring, pageVA, uintptr(wireOffset), uint32(wireLen), txTag)
	if _, err := sys.IOUringEnter(d.RingID, 1, 0, 0); err != nil {
		d.sqMu.Unlock()
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
