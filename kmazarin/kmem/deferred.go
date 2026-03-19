// deferred.go - Lock-free queue for top-half to bottom-half page tracking
//
// The top-half (page fault handler, nosplit context) cannot call TrackPage
// directly because it runs on the exception stack with limited stack space
// and no Go runtime guarantees. Instead it enqueues a DeferredPageRecord
// into a fixed-size ring buffer using atomic operations.
//
// The bottom-half goroutine drains the ring buffer and calls TrackPage
// in normal Go context.

package kmem

import "sync/atomic"

// DeferredPageRecord is the data queued by the top-half for later processing.
type DeferredPageRecord struct {
	PA       uintptr
	VA       uintptr
	Type     PageAllocType
	ShepherdID int16
	ThreadID int16
	Order    uint8
}

// MaxDeferredRecords is the ring buffer capacity. Must be a power of 2.
const MaxDeferredRecords = 1024

var (
	deferredQueue [MaxDeferredRecords]DeferredPageRecord
	deferredHead  uint32 // Read position (bottom-half)
	deferredTail  uint32 // Write position (top-half)

	// Flag checked by the event poller to wake the bottom-half processor
	PageTrackingPending uint32

	// Overflow counter for diagnostics
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
		deferredOverflows++
		return false
	}

	deferredQueue[tail] = rec
	atomic.StoreUint32(&deferredTail, next)

	// Signal the event poller
	atomic.StoreUint32(&PageTrackingPending, 1)
	return true
}

// ProcessDeferredRecords drains the deferred queue and calls TrackPage
// for each record. Called from the bottom-half goroutine in normal Go context.
func ProcessDeferredRecords() {
	for {
		head := atomic.LoadUint32(&deferredHead)
		tail := atomic.LoadUint32(&deferredTail)

		if head == tail {
			return // Empty
		}

		rec := deferredQueue[head]
		atomic.StoreUint32(&deferredHead, (head+1)&(MaxDeferredRecords-1))

		TrackPage(PageAllocInfo{
			PA:       rec.PA,
			VA:       rec.VA,
			Type:     rec.Type,
			ShepherdID: rec.ShepherdID,
			ThreadID: rec.ThreadID,
			Order:    rec.Order,
		})
	}
}

// GetDeferredOverflows returns the number of dropped records due to queue overflow.
//
//go:nosplit
func GetDeferredOverflows() uint64 {
	return deferredOverflows
}
