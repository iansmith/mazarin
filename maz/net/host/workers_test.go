package host

import (
	"sync/atomic"
	"testing"

	"mazzy/mazarin/linksurface"
	"mazzy/shared/iouring"
)

// newTestDispatcher builds a Dispatcher with a buffered RecvChan and the
// given armed-slot count. Allocator is nil — dispatchRx-only tests don't
// touch it; PreArm tests pass their own allocator explicitly.
func newTestDispatcher(armed int, alloc *Allocator) *Dispatcher {
	recv := make(chan linksurface.RxEnvelope, 1)
	return NewDispatcher(&iouring.IORing{}, 0, armed, alloc, recv)
}

// TestDispatchRxRejectsOversizeUsedLen — dispatchRx today only rejects
// usedLen <= VirtIONetHdrSize (runt frames). An oversize usedLen sneaks
// past, and NewRxPacket constructs a packet view that reads past the
// backing page.
//
// RED today: dispatchRx returns true, DbgRxInvalid stays at 0.
// GREEN after fix: dispatchRx returns false and DbgRxInvalid increments.
func TestDispatchRxRejectsOversizeUsedLen(t *testing.T) {
	d := newTestDispatcher(1, nil)

	const oversize = VirtIONetHdrSize + PageSize + 1
	const pageVA = uintptr(0x10000)

	ok := d.dispatchRx(pageVA, oversize)
	if ok {
		t.Fatalf("dispatchRx accepted oversize usedLen=%d (PageSize=%d, hdr=%d); fix must reject",
			oversize, PageSize, VirtIONetHdrSize)
	}
	if d.DbgRxInvalid == 0 {
		t.Errorf("expected DbgRxInvalid to be incremented on oversize rejection, got 0")
	}
}

// TestDispatchRxAcceptsValidLength guards against a too-aggressive upper
// bound. A normal-sized frame (e.g. 1500-byte MTU's worth) must still be
// delivered. Passes today (the runt-check doesn't reject it) and must
// keep passing after the upper-bound fix.
func TestDispatchRxAcceptsValidLength(t *testing.T) {
	d := newTestDispatcher(1, nil)

	const normal = VirtIONetHdrSize + 1500
	const pageVA = uintptr(0x10000)

	ok := d.dispatchRx(pageVA, normal)
	if !ok {
		t.Fatalf("dispatchRx rejected normal-sized frame usedLen=%d (MTU-sized); fix is too aggressive",
			normal)
	}
	if d.DbgRxInvalid != 0 {
		t.Errorf("DbgRxInvalid incremented on valid frame: %d", d.DbgRxInvalid)
	}
}

// TestDispatchRxRejectsExactlyOnePastBound — boundary case. The kernel
// writes vhdr at page base + frame at offset 12; the descriptor's DMA
// target is the page base, so usedLen counts both. The largest valid
// usedLen is PageSize (page completely full, vhdr + 4084 frame bytes);
// PageSize+1 is the smallest invalid value. A bound of
// VirtIONetHdrSize+PageSize (=4108) would be too loose — it'd let
// usedLen=4097 through, and NewRxPacket would read one byte past the
// page. Use the tight bound.
func TestDispatchRxRejectsExactlyOnePastBound(t *testing.T) {
	d := newTestDispatcher(1, nil)

	const justOver = PageSize + 1
	const pageVA = uintptr(0x10000)

	ok := d.dispatchRx(pageVA, justOver)
	if ok {
		t.Fatalf("dispatchRx accepted one byte past page bound usedLen=%d; fix must reject", justOver)
	}
	if d.DbgRxInvalid == 0 {
		t.Errorf("expected DbgRxInvalid increment on +1-past-bound, got 0")
	}
}

// TestPreArmRollsBackOnAllocFailure — when AllocRaw fails mid-PreArm
// (allocator pool exhausted), PreArm currently returns the error
// without rolling back already-allocated slot pages. d.slotPages stays
// populated and the allocator's outstanding count stays elevated.
//
// The natural-exhaustion approach avoids polluting production code with
// a fail-injection knob: construct an allocator with N-1 pages and ask
// PreArm to fill N slots. The Nth AllocRaw call returns (0, false) via
// the existing empty-pool path.
//
// RED today: post-failure, slotPages non-zero, outstanding > 0, free
// list shorter than initial.
// GREEN after fix: all slotPages == 0, outstanding == 0, free restored.
func TestPreArmRollsBackOnAllocFailure(t *testing.T) {
	// Allocator with 2 pages; dispatcher asks for 4. Tags 0, 1 succeed;
	// tag 2's AllocRaw returns (0, false) — rollback must clean up the
	// work done for tags 0 and 1.
	initialPages := []uintptr{0xa000, 0xb000}
	alloc := &Allocator{free: append([]uintptr(nil), initialPages...)}
	d := newTestDispatcher(4, alloc)

	err := d.PreArm()
	if err == nil {
		t.Fatalf("PreArm should fail when allocator has 2 pages and dispatcher asks for 4")
	}

	for i, va := range d.slotPages {
		if va != 0 {
			t.Errorf("after PreArm failure: slotPages[%d] = 0x%x, want 0 (rollback should clear)", i, va)
		}
	}
	if got := alloc.outstanding.Load(); got != 0 {
		t.Errorf("after PreArm failure: allocator.outstanding = %d, want 0 (rollback should release pages)", got)
	}
	if got := len(alloc.free); got != len(initialPages) {
		t.Errorf("after PreArm failure: allocator.free has %d pages, want %d (rollback should restore pool)",
			got, len(initialPages))
	}
	// SQTail must stay at its pre-call value. PreArm currently only
	// bumps SQTail after the AllocRaw loop succeeds, so on AllocRaw
	// failure SQTail is naturally 0 — but assert it as a regression
	// guard against future code that bumps SQTail inside the loop.
	if got := atomic.LoadUint32(&d.Ring.SQTail); got != 0 {
		t.Errorf("after PreArm failure: ring.SQTail = %d, want 0 (rollback should restore)", got)
	}
}
