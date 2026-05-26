// share.go — Mode 2 Share surface (MAZ-53).
//
// Sender side: Slab.ShareRange / Slab.Share publish a kernel-level R/W mapping
// of a Slab (or sub-range) into a consumer's address space and notify the
// consumer via ProtoShareReq IPC. The sender retains ownership; kernel RefCount
// tracks total active mappings.
//
// Consumer side: ReceiveShare blocks on the per-sender channel populated by a
// Dispatcher wired with RegisterShareConsumer. Share.Release sends a fire-and-
// forget ProtoShareRelease IPC so the sender can call sys.UnshareFromTarget
// (via RegisterShareRelease) and decrement the RefCount.
//
// Dispatcher wiring (sender calls RegisterShareRelease; consumer calls RegisterShareConsumer,
// both before d.Start()):
//
//	// In the sender shepherd:
//	transfer.RegisterShareRelease(dispatcher)
//
//	// In the consumer shepherd:
//	transfer.RegisterShareConsumer(dispatcher)
//
// See design/mazarin-transfer-state-machine.md for the full lifecycle.
package transfer

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"unsafe"

	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
)

// ShareID correlates a Share publication with the consumer's eventual Release.
// Sender-assigned; consumers echo it back in the Release IPC.
type ShareID uint32

// ErrShareRangeInvalid is returned when ShareRange's offset/length are out
// of bounds (negative, zero length, or extending past the Slab's total capacity).
var ErrShareRangeInvalid = errors.New("transfer: share range offset/length out of bounds")

// ErrShareReleased is returned when Release is called on an already-released
// Share. Idempotent semantic: a second Release returns nil, not this error.
// This sentinel exists for explicit checks if a caller wants to distinguish.
var ErrShareReleased = errors.New("transfer: share already released")

// Share is the consumer-side view of a server's published mapping.
//
// VA + Bytes describe the byte range the consumer has R/W access to. For a
// whole-Slab Share, Bytes equals the Slab's total capacity. For ShareRange,
// Bytes equals the length argument and may be sub-page sized.
type Share struct {
	VA    uintptr
	Bytes int

	mu           sync.Mutex
	id           ShareID
	senderSID    ShepherdID
	released     bool
	kernelMapped bool // true only when established via ReceiveShare (real kernel mapping)
}

// ============================================================
// Sender side — ShareRange, outstanding-shares table, Release handler
// ============================================================

type shareOutstanding struct {
	consumerSID ShepherdID
	consumerVA  uintptr // page-aligned base in consumer's address space
	numPages    int
	callerVA    uintptr // page-aligned base in the caller's own address space (proof-of-possession for UnshareFromTarget)
}

var (
	shareTableMu sync.Mutex
	shareTable   = make(map[ShareID]shareOutstanding)
	nextShareID  atomic.Uint32
)

// ShareRange exposes [offset, offset+length) bytes of the Slab as a R/W
// mapping in the consumer's address space. The Slab stays in the sender's
// address space; kernel RefCount tracks total active mappings.
//
// Page granularity means non-aligned (offset, length) may expose neighboring
// bytes on boundary pages — see "v1 trust model" in MAZ-53.
//
// Returns a ShareID the sender uses to track outstanding shares.
func (s *Slab) ShareRange(with ShepherdID, offset, length int) (ShareID, error) {
	capacity := s.pages * PageSize
	if offset < 0 || length <= 0 || offset+length > capacity {
		return 0, ErrShareRangeInvalid
	}

	s.mu.Lock()
	if s.state == slabReleased {
		s.mu.Unlock()
		return 0, ErrSlabReleased
	}
	pageBase := s.va + uintptr((offset/PageSize)*PageSize)
	s.mu.Unlock()

	startPage := offset / PageSize
	endPage := (offset + length + PageSize - 1) / PageSize
	numPages := endPage - startPage

	return publishShare(with, pageBase, numPages, offset%PageSize, length)
}

// Share is the whole-Slab convenience wrapper. Equivalent to:
//
//	s.ShareRange(with, 0, s.pages*PageSize)
func (s *Slab) Share(with ShepherdID) (ShareID, error) {
	return s.ShareRange(with, 0, s.pages*PageSize)
}

// publishShare maps numPages of the caller's pages starting at pageBase into
// the consumer's address space, records the outstanding-shares entry, and
// fires the ProtoShareReq notification. intraOffset is the byte offset into
// the first shared page that bytes counts from; bytes is the logical byte
// count visible to the consumer.
func publishShare(with ShepherdID, pageBase uintptr, numPages, intraOffset, bytes int) (ShareID, error) {
	consumerVABase, err := sys.SharePagesWithTarget(int(with), pageBase, numPages)
	if err != nil {
		return 0, err
	}
	consumerVA := consumerVABase + uintptr(intraOffset)

	id := ShareID(nextShareID.Add(1))
	shareTableMu.Lock()
	shareTable[id] = shareOutstanding{
		consumerSID: with,
		consumerVA:  consumerVABase,
		numPages:    numPages,
		callerVA:    pageBase,
	}
	shareTableMu.Unlock()

	p := ipc.ShareReqPayload{
		ShareID: uint32(id),
		Bytes:   uint32(bytes),
		VA:      uint64(consumerVA),
	}
	msg := ipc.EncodeShareReq(&p, int16(os.Getpid()))
	_ = uring.Send(int(with), &msg) // fire-and-forget; consumer must be listening
	return id, nil
}

// releaseHook, if non-nil, is called after each successful unshare with the
// ShareID that was just released. Set via SetReleaseHook before boot tests or
// other callers that need per-release notification.
var releaseHook func(ShareID)

// SetReleaseHook installs an after-unshare callback. Called at most once;
// not thread-safe — set before d.Start() in boot integration tests.
func SetReleaseHook(fn func(ShareID)) { releaseHook = fn }

// RegisterShareRelease wires the sender-side ProtoShareRelease handler into a
// Dispatcher. Incoming Release IPCs from consumers trigger sys.UnshareFromTarget
// and remove the entry from the outstanding-shares table. Call before d.Start().
func RegisterShareRelease(d *uring.Dispatcher) {
	d.OnFunc(ipc.ProtoShareRelease, decodeShareReleaseTagged, handleShareRelease)
}

type shareReleaseTagged struct {
	payload   ipc.ShareReleasePayload
	senderSID ShepherdID // the consumer that sent this Release
}

func decodeShareReleaseTagged(msg *ipc.UringIPCMsg) any {
	return shareReleaseTagged{
		payload:   *ipc.DecodeShareRelease(msg),
		senderSID: ShepherdID(msg.SenderSID),
	}
}

func handleShareRelease(v any) {
	tagged := v.(shareReleaseTagged)
	id := ShareID(tagged.payload.ShareID)

	shareTableMu.Lock()
	rec, ok := shareTable[id]
	shareTableMu.Unlock()
	if !ok {
		return // already released or unknown — drop
	}
	// Auth: only the shepherd we originally shared with may release.
	if tagged.senderSID != rec.consumerSID {
		return
	}

	if err := sys.UnshareFromTarget(int(rec.consumerSID), rec.consumerVA, rec.numPages, rec.callerVA); err != nil {
		return // unshare failed — leave table entry intact for retry or cleanup on death
	}
	shareTableMu.Lock()
	delete(shareTable, id)
	shareTableMu.Unlock()
	if releaseHook != nil {
		releaseHook(id)
	}
}

// ============================================================
// Consumer side — ReceiveShare, per-sender channels, AsBytes, Release
// ============================================================

var (
	shareConsumersMu sync.Mutex
	shareConsumers   = make(map[ShepherdID]chan any)
)

func shareReqCh(sender ShepherdID) chan any {
	shareConsumersMu.Lock()
	defer shareConsumersMu.Unlock()
	if ch, ok := shareConsumers[sender]; ok {
		return ch
	}
	ch := make(chan any, 4)
	shareConsumers[sender] = ch
	return ch
}

// RegisterShareConsumer wires the consumer-side ProtoShareReq handler into a
// Dispatcher. Routes incoming ShareReq messages to per-sender channels that
// ReceiveShare reads from. Call before d.Start().
func RegisterShareConsumer(d *uring.Dispatcher) {
	d.OnFunc(ipc.ProtoShareReq, decodeShareReqTagged, routeShareReq)
}

type shareReqTagged struct {
	payload   ipc.ShareReqPayload
	senderSID ShepherdID
}

func decodeShareReqTagged(msg *ipc.UringIPCMsg) any {
	return shareReqTagged{
		payload:   *ipc.DecodeShareReq(msg),
		senderSID: ShepherdID(msg.SenderSID),
	}
}

func routeShareReq(v any) {
	tagged := v.(shareReqTagged)
	ch := shareReqCh(tagged.senderSID)
	select {
	case ch <- tagged:
	default:
		// consumer's channel full — drop; sender won't be notified
	}
}

// ReceiveShare blocks until the next Share IPC from `from` arrives. Returns a
// Share giving the consumer R/W access to the bytes the sender published.
// Requires RegisterShareConsumer to be wired into the dispatcher before d.Start().
func ReceiveShare(from ShepherdID) (*Share, error) {
	ch := shareReqCh(from)
	v, ok := <-ch
	if !ok || v == nil {
		return nil, errors.New("transfer: share channel closed")
	}
	tagged, ok := v.(shareReqTagged)
	if !ok {
		return nil, errors.New("transfer: unexpected type on share receive channel")
	}
	return &Share{
		VA:           uintptr(tagged.payload.VA),
		Bytes:        int(tagged.payload.Bytes),
		id:           ShareID(tagged.payload.ShareID),
		senderSID:    from,
		kernelMapped: true,
	}, nil
}

// Reshare maps the same pages this Share covers into another shepherd's
// address space and sends a ProtoShareReq notification. This implements
// chained sharing (A → B → C): B calls Reshare to give C access to pages
// B received from A.
//
// The kernel permits this because the MAZ-53 audit relaxed
// SyscallSharePagesWithTarget: a caller with PD_SHARED set on a page (i.e.
// a valid shared mapping, even without ownership) may re-share that page.
// The matching relaxation on SyscallUnshareFromTarget allows B to later
// revoke C's mapping when C's Release IPC arrives.
//
// The caller must have RegisterShareRelease wired in its dispatcher — the
// outstanding-shares table records the new (B→C) entry, and the dispatcher
// calls UnshareFromTarget when C releases.
func (s *Share) Reshare(with ShepherdID) (ShareID, error) {
	s.mu.Lock()
	if s.released {
		s.mu.Unlock()
		return 0, ErrShareReleased
	}
	va := s.VA
	bytes := s.Bytes
	s.mu.Unlock()

	// Reconstruct the page-aligned base and page count from the byte-granular VA.
	// VA may be sub-page-offset for a sub-range ShareRange.
	pageBase := va &^ uintptr(PageSize-1)
	intraOffset := int(va - pageBase)
	numPages := (intraOffset + bytes + PageSize - 1) / PageSize

	return publishShare(with, pageBase, numPages, intraOffset, bytes)
}

// AsBytes returns a R/W byte-slice view over the shared region.
// len(AsBytes()) == s.Bytes. The slice aliases the kernel-mapped memory;
// the caller must not retain it past Release.
func (s *Share) AsBytes() []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(s.VA)), s.Bytes)
}

// Release tells the sender we're done with the share. Sends a fire-and-forget
// ProtoShareRelease IPC; the sender's RegisterShareRelease handler revokes the
// mapping via sys.UnshareFromTarget. Idempotent: a second Release returns nil.
//
// IPC is sent only for kernel-established shares (those created by ReceiveShare).
// A Share constructed directly (e.g. in tests) has no kernel mapping to revoke.
func (s *Share) Release() error {
	s.mu.Lock()
	if s.released {
		s.mu.Unlock()
		return nil
	}
	s.released = true
	km := s.kernelMapped
	s.mu.Unlock()

	if km {
		p := ipc.ShareReleasePayload{ShareID: uint32(s.id)}
		msg := ipc.EncodeShareRelease(&p, int16(os.Getpid()))
		_ = uring.Send(int(s.senderSID), &msg) // fire-and-forget
	}
	return nil
}
