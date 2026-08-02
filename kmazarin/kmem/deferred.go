// deferred.go - Lock-free queue for top-half to bottom-half page tracking
//
// The top-half (page fault handler, nosplit context) cannot call TrackPage
// or UntrackPage directly because it runs on the exception stack with
// limited stack space and no Go runtime guarantees. Instead it enqueues a
// DeferredPageRecord into a fixed-size ring buffer using atomic operations.
//
// The bottom-half goroutine drains the ring buffer and calls TrackPage /
// UntrackPage in normal Go context.
//
// Track and untrack share ONE ring (MAZ-163) — this is load-bearing, not
// incidental. FIFO ordering across a single ring is what guarantees a PA's
// track record is processed before a later untrack of the same PA, and
// (after a subsequent realloc) before the next track of that PA. A version
// of this file briefly split them into separate rings to address ring
// pressure — that broke the ordering guarantee: with two rings, a rapid
// alloc→free→realloc of the same PA (which the buddy allocator's LIFO free
// list makes near-certain, not a corner case) could drain as
// track(X)→track(X)→untrack(X), permanently orphaning the first track's
// slot (TrackPage has no dedup; the second track's index write clobbers the
// first's). Cycle C in page_tracker_selftest.go exists specifically to
// catch this class of ordering bug. The fix for ring pressure is capacity
// (MaxDeferredRecords), not splitting.

package kmem

import "sync/atomic"

// DeferredOp identifies what ProcessDeferredRecords should do with a queued
// record. Zero value (DeferredOpTrack) keeps every pre-MAZ-163 enqueue site
// in paging.go — none of which set Op — behaving exactly as before.
type DeferredOp uint8

const (
	// DeferredOpTrack calls TrackPage with the record's fields. Zero value.
	DeferredOpTrack DeferredOp = iota
	// DeferredOpUntrack calls UntrackPage(PA); only PA is meaningful.
	DeferredOpUntrack
)

// DeferredPageRecord is the data queued by the top-half for later processing.
type DeferredPageRecord struct {
	Op         DeferredOp
	PA         uintptr
	VA         uintptr
	Type       PageAllocType
	ShepherdID int16
	ThreadID   int16
	Order      uint8
}

// MaxDeferredRecords is the ring buffer capacity. Must be a power of 2.
// 4096 (up from the original 1024): BuddyFreeTyped now enqueues an untrack
// on every free, including frees of pages that were never tracked, roughly
// doubling traffic through this one ring versus the original five
// paging.go TrackPage sites alone.
const MaxDeferredRecords = 4096

var (
	deferredQueue [MaxDeferredRecords]DeferredPageRecord
	deferredHead  uint32 // Read position (bottom-half)
	deferredTail  uint32 // Write position (top-half)

	// Flag checked by the event poller to wake the bottom-half processor
	PageTrackingPending uint32

	// Overflow counter for diagnostics. Atomic: incremented from nosplit
	// top-half context, which may run concurrently on multiple CPUs.
	deferredOverflows uint64
)

// QueueDeferredRecord enqueues a record from the top-half (nosplit context).
// Returns true if the record was enqueued, false if the queue is full.
//
//go:nosplit
func QueueDeferredRecord(rec DeferredPageRecord) bool {
	tail := atomic.LoadUint32(&deferredTail)
	head := atomic.LoadUint32(&deferredHead)

	// Check if full (one slot wasted to distinguish full from empty)
	next := (tail + 1) & (MaxDeferredRecords - 1)
	if next == head {
		atomic.AddUint64(&deferredOverflows, 1)
		return false
	}

	// Index with the mask to give the compiler a provable bound: avoids
	// the bounds-check chain on the nosplit stack-budget path from
	// SyscallWrite → CopyFromUser → ... → allocPTPage → QueueDeferredRecord.
	deferredQueue[tail&(MaxDeferredRecords-1)] = rec
	atomic.StoreUint32(&deferredTail, next)

	signalPageTrackingPending()
	return true
}

// signalPageTrackingPending sets PageTrackingPending, skipping the store
// when it's already set. Load-then-store instead of an unconditional store:
// on this now-hotter path (BuddyFreeTyped enqueues on every free, not just
// the five original TrackPage sites), an unconditional store means every
// enqueue dirties the cache line even when the flag is already 1 and the
// bottom-half hasn't consumed it yet — pure coherence traffic for no change
// in value. The tail store above happens-before this check, and the
// consumer re-checks the ring after clearing its wait, so there's no
// lost-wakeup window: a producer that sees Pending==1 skips the store
// because a wakeup is already pending; a producer that sees Pending==0 sets
// it after its own tail store is visible.
//
//go:nosplit
func signalPageTrackingPending() {
	if atomic.LoadUint32(&PageTrackingPending) == 0 {
		atomic.StoreUint32(&PageTrackingPending, 1)
	}
}

// ProcessDeferredRecords drains the deferred queue and calls TrackPage or
// UntrackPage for each record, in FIFO order. Called from the bottom-half
// goroutine in normal Go context.
func ProcessDeferredRecords() {
	for {
		head := atomic.LoadUint32(&deferredHead)
		tail := atomic.LoadUint32(&deferredTail)

		if head == tail {
			return // Empty
		}

		rec := deferredQueue[head&(MaxDeferredRecords-1)]
		atomic.StoreUint32(&deferredHead, (head+1)&(MaxDeferredRecords-1))

		switch rec.Op {
		case DeferredOpUntrack:
			UntrackPage(rec.PA)
		default: // DeferredOpTrack
			TrackPage(PageAllocInfo{
				PA:         rec.PA,
				VA:         rec.VA,
				Type:       rec.Type,
				ShepherdID: rec.ShepherdID,
				ThreadID:   rec.ThreadID,
				Order:      rec.Order,
			})
		}
	}
}

// GetDeferredOverflows returns the number of dropped records due to queue overflow.
//
//go:nosplit
func GetDeferredOverflows() uint64 {
	return atomic.LoadUint64(&deferredOverflows)
}
