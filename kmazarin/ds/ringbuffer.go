// Package ds provides data structures for kmazarin
package ds

// RingBuffer is a fixed-capacity buffer that stores elements in a circular array.
// It uses static allocation (no pointers for content, no dynamic growth).
// When full, Push() returns false and the element is not added.
//
// This is NOT a traditional ring buffer that overwrites old data - it's a
// fixed-size collection that fills up and stops accepting new elements.
type RingBuffer[T any] struct {
	data     []T // Pre-allocated array
	capacity int // Maximum number of elements
	count    int // Current number of elements
}

// NewRingBuffer creates a new ring buffer with the specified capacity.
// The buffer is pre-allocated to the given size and never grows.
//
//go:nosplit
func NewRingBuffer[T any](capacity int) *RingBuffer[T] {
	return &RingBuffer[T]{
		data:     make([]T, capacity),
		capacity: capacity,
		count:    0,
	}
}

// Clear resets the ring buffer to empty state.
// Does not free memory, just resets the count to 0.
//
//go:nosplit
func (rb *RingBuffer[T]) Clear() {
	rb.count = 0
}

// Push adds an element to the ring buffer.
// Returns true if successful, false if buffer is full.
//
//go:nosplit
func (rb *RingBuffer[T]) Push(value T) bool {
	if rb.count >= rb.capacity {
		return false // Buffer full
	}
	rb.data[rb.count] = value
	rb.count++
	return true
}

// Len returns the number of elements currently in the buffer.
//
//go:nosplit
func (rb *RingBuffer[T]) Len() int {
	return rb.count
}

// IsEmpty returns true if the buffer contains no elements.
//
//go:nosplit
func (rb *RingBuffer[T]) IsEmpty() bool {
	return rb.count == 0
}

// Get returns the element at the specified index.
// Index must be in range [0, Len()-1].
// Panics if index is out of bounds (should be checked with Len() first).
//
//go:nosplit
func (rb *RingBuffer[T]) Get(index int) T {
	if index < 0 || index >= rb.count {
		panic("ringbuffer: index out of bounds")
	}
	return rb.data[index]
}

// Capacity returns the maximum number of elements the buffer can hold.
//
//go:nosplit
func (rb *RingBuffer[T]) Capacity() int {
	return rb.capacity
}

// GetRandom returns a random element from the buffer.
// Requires randFunc to return a value in range [0, max).
// Returns the zero value of T if buffer is empty.
//
//go:nosplit
func (rb *RingBuffer[T]) GetRandom(randFunc func(max int) int) T {
	if rb.count == 0 {
		var zero T
		return zero
	}
	idx := randFunc(rb.count)
	return rb.data[idx]
}
