//go:build arm64

package ds

// StaticQueue is a FIFO queue with fixed capacity using circular buffer.
// Supports Pluck() to remove elements from the middle (creates holes).
// Push adds to back, Pop removes from front (skipping holes).
// Panics on overflow or underflow.
//
// Uses embedded ring buffer logic - all operations are nosplit-safe.
//
// ZERO VALUE IS READY TO USE with backing arrays:
//
//	var queueData [256]int16
//	var queueInUse [256]bool
//	var queue = StaticQueue[int16]{Data: queueData[:], InUse: queueInUse[:]}
type StaticQueue[T comparable] struct {
	Data  []T    // Slice backed by static array (public for zero-value init)
	InUse []bool // Slice backed by static array (public for zero-value init)
	head  int    // Index of front element (private)
	tail  int    // Index of next slot to fill (private)
	count int    // Number of in-use elements (private)
	// NOTE: No lock - protected by external schedulerLock
}

// Push adds value to the back of the queue.
// Panics if the queue is full.
//
//go:nosplit
func (q *StaticQueue[T]) Push(value T) {
	if q.count >= len(q.Data) {
		panic("StaticQueue overflow: capacity exceeded")
	}

	// Find next free slot from tail
	attempts := 0
	for q.InUse[q.tail] && attempts < len(q.Data) {
		q.tail = (q.tail + 1) % len(q.Data)
		attempts++
	}

	if attempts >= len(q.Data) {
		panic("StaticQueue overflow: no free slots")
	}

	q.Data[q.tail] = value
	q.InUse[q.tail] = true
	q.tail = (q.tail + 1) % len(q.Data)
	q.count++
}

// Pop removes and returns the front element, skipping holes.
// Panics if the queue is empty.
//
//go:nosplit
func (q *StaticQueue[T]) Pop() T {
	if q.count == 0 {
		panic("StaticQueue underflow: pop from empty queue")
	}

	// Skip holes to find next valid element
	attempts := 0
	for !q.InUse[q.head] && attempts < len(q.Data) {
		q.head = (q.head + 1) % len(q.Data)
		attempts++
	}

	if attempts >= len(q.Data) || !q.InUse[q.head] {
		panic("StaticQueue corruption: count > 0 but no valid elements")
	}

	value := q.Data[q.head]
	q.InUse[q.head] = false
	q.head = (q.head + 1) % len(q.Data)
	q.count--
	return value
}

// Pluck finds and removes an element from the queue by value.
// Returns true if found and removed, false otherwise.
// Creates a "hole" in the queue which is skipped by Pop.
//
//go:nosplit
func (q *StaticQueue[T]) Pluck(value T) bool {
	// Scan entire buffer looking for matching value
	for i := 0; i < len(q.Data); i++ {
		if q.InUse[i] && q.Data[i] == value {
			// Found it - mark as hole
			q.InUse[i] = false
			q.count--
			return true
		}
	}
	return false
}

// Size returns the current number of elements in the queue (not including holes).
// Implements Sizer interface.
//
//go:nosplit
func (q *StaticQueue[T]) Size() int {
	return q.count
}

// IsEmpty returns true if the queue has no elements.
//
//go:nosplit
func (q *StaticQueue[T]) IsEmpty() bool {
	return q.count == 0
}

// Clear removes all elements from the queue.
//
//go:nosplit
func (q *StaticQueue[T]) Clear() {
	for i := 0; i < len(q.InUse); i++ {
		q.InUse[i] = false
	}
	q.head = 0
	q.tail = 0
	q.count = 0
}

// PushHead adds value to the FRONT of the queue (priority insertion).
// Used for soft IRQ dispatcher to get immediate scheduling.
// Panics if the queue is full.
//
// MULTICORE SAFETY: Caller must hold external lock (schedulerLock).
//
//go:nosplit
func (q *StaticQueue[T]) PushHead(value T) {
	if q.count >= len(q.Data) {
		panic("StaticQueue overflow: capacity exceeded")
	}

	// Find free slot searching backward from head
	newHead := (q.head - 1 + len(q.Data)) % len(q.Data)
	attempts := 0
	for q.InUse[newHead] && attempts < len(q.Data) {
		newHead = (newHead - 1 + len(q.Data)) % len(q.Data)
		attempts++
	}

	if attempts >= len(q.Data) {
		panic("StaticQueue overflow: no free slots for PushHead")
	}

	q.Data[newHead] = value
	q.InUse[newHead] = true
	q.head = newHead
	q.count++
}
