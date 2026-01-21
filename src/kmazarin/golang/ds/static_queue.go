package ds

import "kmazarin/golang/console"

// StaticQueue is a FIFO queue with fixed capacity using circular buffer.
// Push adds to back, Pop removes from front.
// Panics (via KernelPanic) on overflow or underflow.
//
// ZERO VALUE IS READY TO USE with backing array:
//
//	var queueData [256]int32
//	var queue = StaticQueue[int32]{Data: queueData[:]}
type StaticQueue[T any] struct {
	Data []T // Slice backed by static array (public for zero-value init)
	head int // Index of front element
	tail int // Index of next slot to fill
	size int // Current number of elements
}

// Push adds value to the back of the queue.
// Panics if the queue is full.
func (q *StaticQueue[T]) Push(value T) {
	if q.size >= len(q.Data) {
		console.KernelPanic("StaticQueue overflow: capacity exceeded")
	}
	q.Data[q.tail] = value
	q.tail = (q.tail + 1) % len(q.Data)
	q.size++
}

// Pop removes and returns the front element.
// Panics if the queue is empty.
func (q *StaticQueue[T]) Pop() T {
	if q.size == 0 {
		console.KernelPanic("StaticQueue underflow: pop from empty queue")
	}
	value := q.Data[q.head]
	q.head = (q.head + 1) % len(q.Data)
	q.size--
	return value
}

// Size returns the current number of elements in the queue.
// Implements Sizer interface.
func (q *StaticQueue[T]) Size() int {
	return q.size
}

// IsEmpty returns true if the queue has no elements.
func (q *StaticQueue[T]) IsEmpty() bool {
	return q.size == 0
}

// Clear removes all elements from the queue.
func (q *StaticQueue[T]) Clear() {
	q.head = 0
	q.tail = 0
	q.size = 0
}
