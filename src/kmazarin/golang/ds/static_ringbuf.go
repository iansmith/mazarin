package ds

import "kmazarin/golang/console"

// StaticRingBuf is a circular buffer with fixed capacity that supports "holes" (plucked elements).
// Uses InUse markers to track which slots contain valid data vs holes.
//
// ZERO VALUE IS READY TO USE with backing arrays:
//
//	var bufData [256]int16
//	var bufInUse [256]bool
//	var buf = StaticRingBuf[int16]{Data: bufData[:], InUse: bufInUse[:]}
type StaticRingBuf[T comparable] struct {
	Data  []T    // Slice backed by static array
	InUse []bool // Slice backed by static array (true = slot has valid data)
	head  int    // Index of front element
	tail  int    // Index of next slot to fill
	count int    // Number of in-use elements (not including holes)
}

// Push adds value to the back of the buffer.
// Panics if the buffer is full.
func (rb *StaticRingBuf[T]) Push(value T) {
	if rb.count >= len(rb.Data) {
		console.KernelPanic("StaticRingBuf overflow: capacity exceeded")
	}

	// Find next free slot from tail
	// We need to skip holes and find an unused slot
	attempts := 0
	for rb.InUse[rb.tail] && attempts < len(rb.Data) {
		rb.tail = (rb.tail + 1) % len(rb.Data)
		attempts++
	}

	if attempts >= len(rb.Data) {
		console.KernelPanic("StaticRingBuf overflow: no free slots")
	}

	rb.Data[rb.tail] = value
	rb.InUse[rb.tail] = true
	rb.tail = (rb.tail + 1) % len(rb.Data)
	rb.count++
}

// Pop removes and returns the front element, skipping holes.
// Panics if the buffer is empty.
func (rb *StaticRingBuf[T]) Pop() T {
	if rb.count == 0 {
		console.KernelPanic("StaticRingBuf underflow: pop from empty buffer")
	}

	// Skip holes to find next valid element
	attempts := 0
	for !rb.InUse[rb.head] && attempts < len(rb.Data) {
		rb.head = (rb.head + 1) % len(rb.Data)
		attempts++
	}

	if attempts >= len(rb.Data) || !rb.InUse[rb.head] {
		console.KernelPanic("StaticRingBuf corruption: count > 0 but no valid elements")
	}

	value := rb.Data[rb.head]
	rb.InUse[rb.head] = false
	rb.head = (rb.head + 1) % len(rb.Data)
	rb.count--
	return value
}

// Pluck finds an element by value, marks it as not in use (creating a hole), and returns true.
// Returns false if element is not found.
// This allows removing an element from the middle of the queue (e.g., when blocking a thread).
func (rb *StaticRingBuf[T]) Pluck(value T) bool {
	// Scan entire buffer looking for matching value
	for i := 0; i < len(rb.Data); i++ {
		if rb.InUse[i] && rb.Data[i] == value {
			// Found it - mark as hole
			rb.InUse[i] = false
			rb.count--
			return true
		}
	}
	return false
}

// Size returns the number of in-use elements (not including holes).
// Implements Sizer interface.
func (rb *StaticRingBuf[T]) Size() int {
	return rb.count
}

// IsEmpty returns true if there are no in-use elements.
func (rb *StaticRingBuf[T]) IsEmpty() bool {
	return rb.count == 0
}

// Clear removes all elements and resets head/tail pointers.
func (rb *StaticRingBuf[T]) Clear() {
	for i := 0; i < len(rb.InUse); i++ {
		rb.InUse[i] = false
	}
	rb.head = 0
	rb.tail = 0
	rb.count = 0
}
