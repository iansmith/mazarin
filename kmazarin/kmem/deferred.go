// deferred.go - Lock-free queues for top-half to bottom-half page tracking
//
// The top-half (page fault handler, nosplit context) cannot call TrackPage
// or UntrackPage directly because it runs on the exception stack with
// limited stack space and no Go runtime guarantees. Instead it enqueues
// into a fixed-size ring buffer using atomic operations.
//
// The bottom-half goroutine drains the ring buffers and calls TrackPage /
// UntrackPage in normal Go context.
//
// Track and untrack each get their own ring (MAZ-163) rather than sharing
// one: BuddyFreeTyped enqueues an untrack on every free, including frees of
// pages that were never tracked (a harmless no-op once drained, but still a
// ring slot spent) — sharing one ring meant untrack's much higher volume
// could starve pending track records, and vice versa, during a burst (e.g.
// ordinary boot-time page-table churn, before the bottom-half goroutine has
// had a chance to run and drain anything). Separate rings mean a burst on
// one side can't drop records on the other.

package kmem

import "sync/atomic"

// DeferredPageRecord is the data queued by the top-half for later processing
// by TrackPage.
type DeferredPageRecord struct {
	PA         uintptr
	VA         uintptr
	Type       PageAllocType
	ShepherdID int16
	ThreadID   int16
	Order      uint8
}

// MaxDeferredRecords is the track ring's capacity. Must be a power of 2.
const MaxDeferredRecords = 1024

// MaxDeferredUntrackRecords is the untrack ring's capacity. Must be a power
// of 2. Larger than the track ring: every BuddyFreeTyped call enqueues an
// untrack, including frees of untracked pages, so its volume runs well
// above the five paging.go TrackPage sites'.
const MaxDeferredUntrackRecords = 2048

var (
	deferredQueue [MaxDeferredRecords]DeferredPageRecord
	deferredHead  uint32 // Read position (bottom-half)
	deferredTail  uint32 // Write position (top-half)

	deferredUntrackQueue [MaxDeferredUntrackRecords]uintptr
	deferredUntrackHead  uint32
	deferredUntrackTail  uint32

	// Flag checked by the event poller to wake the bottom-half processor
	PageTrackingPending uint32

	// Overflow counters for diagnostics
	deferredOverflows        uint64
	deferredUntrackOverflows uint64
)

// QueueDeferredRecord enqueues a track record from the top-half (nosplit
// context). Returns true if the record was enqueued, false if the queue is
// full.
//
//go:nosplit
func QueueDeferredRecord(rec DeferredPageRecord) bool {
	tail := atomic.LoadUint32(&deferredTail)
	head := atomic.LoadUint32(&deferredHead)

	// Check if full (one slot wasted to distinguish full from empty)
	next := (tail + 1) & (MaxDeferredRecords - 1)
	if next == head {
		deferredOverflows++
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
// in value.
//
//go:nosplit
func signalPageTrackingPending() {
	if atomic.LoadUint32(&PageTrackingPending) == 0 {
		atomic.StoreUint32(&PageTrackingPending, 1)
	}
}

// QueueDeferredUntrack enqueues a PA for UntrackPage from the top-half
// (nosplit context) — BuddyFreeTyped's shed point (MAZ-163). Returns true
// if enqueued, false if the untrack ring is full.
//
//go:nosplit
func QueueDeferredUntrack(pa uintptr) bool {
	tail := atomic.LoadUint32(&deferredUntrackTail)
	head := atomic.LoadUint32(&deferredUntrackHead)

	next := (tail + 1) & (MaxDeferredUntrackRecords - 1)
	if next == head {
		deferredUntrackOverflows++
		return false
	}

	deferredUntrackQueue[tail&(MaxDeferredUntrackRecords-1)] = pa
	atomic.StoreUint32(&deferredUntrackTail, next)

	signalPageTrackingPending()
	return true
}

// ProcessDeferredRecords drains both deferred queues — track records to
// TrackPage, untrack PAs to UntrackPage. Called from the bottom-half
// goroutine in normal Go context.
func ProcessDeferredRecords() {
	for {
		head := atomic.LoadUint32(&deferredHead)
		tail := atomic.LoadUint32(&deferredTail)

		if head == tail {
			break // Empty
		}

		rec := deferredQueue[head&(MaxDeferredRecords-1)]
		atomic.StoreUint32(&deferredHead, (head+1)&(MaxDeferredRecords-1))

		TrackPage(PageAllocInfo{
			PA:         rec.PA,
			VA:         rec.VA,
			Type:       rec.Type,
			ShepherdID: rec.ShepherdID,
			ThreadID:   rec.ThreadID,
			Order:      rec.Order,
		})
	}

	for {
		head := atomic.LoadUint32(&deferredUntrackHead)
		tail := atomic.LoadUint32(&deferredUntrackTail)

		if head == tail {
			return // Empty
		}

		pa := deferredUntrackQueue[head&(MaxDeferredUntrackRecords-1)]
		atomic.StoreUint32(&deferredUntrackHead, (head+1)&(MaxDeferredUntrackRecords-1))

		UntrackPage(pa)
	}
}

// GetDeferredOverflows returns the number of dropped track records due to
// queue overflow.
//
//go:nosplit
func GetDeferredOverflows() uint64 {
	return deferredOverflows
}

// GetDeferredUntrackOverflows returns the number of dropped untrack records
// due to queue overflow. A dropped untrack silently reintroduces the leak
// this ticket fixes — a caller (LogMemoryStats) should treat any nonzero
// value as an active problem, not just a diagnostic curiosity.
//
//go:nosplit
func GetDeferredUntrackOverflows() uint64 {
	return deferredUntrackOverflows
}
