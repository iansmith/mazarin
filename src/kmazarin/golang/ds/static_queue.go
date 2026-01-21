//go:build arm64

package ds

// StaticQueue is a FIFO queue with fixed capacity using circular buffer.
// Supports Pluck() to remove elements from the middle (creates holes).
// Push adds to back, Pop removes from front (skipping holes).
// Panics (via KernelPanic) on overflow or underflow.
//
// ZERO VALUE IS READY TO USE with backing arrays:
//
//	var queueData [256]int16
//	var queueInUse [256]bool
//	var queue = StaticQueue[int16]{Data: queueData[:], InUse: queueInUse[:]}
type StaticQueue[T comparable] struct {
	Data  []T             // Slice backed by static array (public for zero-value init)
	InUse []bool          // Slice backed by static array (public for zero-value init)
	buf   StaticRingBuf[T]
	Lock  Spinlock        // Protects all fields for concurrent access
}

// ensureInit initializes the internal ring buffer if needed
func (q *StaticQueue[T]) ensureInit() {
	if q.buf.Data == nil {
		q.buf.Data = q.Data
		q.buf.InUse = q.InUse
	}
}

// Push adds value to the back of the queue.
// Panics if the queue is full.
func (q *StaticQueue[T]) Push(value T) {
	q.Lock.Lock()
	defer q.Lock.Unlock()

	q.ensureInit()
	q.buf.Push(value)
}

// Pop removes and returns the front element, skipping holes.
// Panics if the queue is empty.
func (q *StaticQueue[T]) Pop() T {
	q.Lock.Lock()
	defer q.Lock.Unlock()

	q.ensureInit()
	return q.buf.Pop()
}

// Pluck finds and removes an element from the queue by value.
// Returns true if found and removed, false otherwise.
// Creates a "hole" in the queue which is skipped by Pop.
func (q *StaticQueue[T]) Pluck(value T) bool {
	q.Lock.Lock()
	defer q.Lock.Unlock()

	q.ensureInit()
	return q.buf.Pluck(value)
}

// Size returns the current number of elements in the queue (not including holes).
// Implements Sizer interface.
func (q *StaticQueue[T]) Size() int {
	q.Lock.Lock()
	defer q.Lock.Unlock()

	q.ensureInit()
	return q.buf.Size()
}

// IsEmpty returns true if the queue has no elements.
func (q *StaticQueue[T]) IsEmpty() bool {
	q.Lock.Lock()
	defer q.Lock.Unlock()

	q.ensureInit()
	return q.buf.IsEmpty()
}

// Clear removes all elements from the queue.
func (q *StaticQueue[T]) Clear() {
	q.Lock.Lock()
	defer q.Lock.Unlock()

	q.ensureInit()
	q.buf.Clear()
}
