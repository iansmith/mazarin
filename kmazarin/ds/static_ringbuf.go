//go:build arm64 || amd64

package ds

// StaticRingBuf is a low-level circular buffer for small comparable types.
// Supports "holes" (plucked elements) for efficient removal from middle.
// Uses InUse markers to track valid slots vs holes.
//
// This is an internal primitive. Use StaticQueue for the public API.
//
// ZERO VALUE IS READY TO USE with backing arrays:
//
//	var bufData [256]int16
//	var bufInUse [256]bool
//	var buf = staticRingBuf[int16]{data: bufData[:], inUse: bufInUse[:]}
type staticRingBuf[T comparable] struct {
	data  []T    // Slice backed by static array (internal)
	inUse []bool // Slice backed by static array (internal)
	head  int    // Index of front element
	tail  int    // Index of next slot to fill
	count int    // Number of in-use elements (not including holes)
}

// push adds value to the back of the buffer.
// Panics if the buffer is full.
//
//go:nosplit
func (rb *staticRingBuf[T]) push(value T) {
	if rb.count >= len(rb.data) {
		panic("staticRingBuf overflow: capacity exceeded")
	}

	// Find next free slot from tail
	attempts := 0
	for rb.inUse[rb.tail] && attempts < len(rb.data) {
		rb.tail = (rb.tail + 1) % len(rb.data)
		attempts++
	}

	if attempts >= len(rb.data) {
		panic("staticRingBuf overflow: no free slots")
	}

	rb.data[rb.tail] = value
	rb.inUse[rb.tail] = true
	rb.tail = (rb.tail + 1) % len(rb.data)
	rb.count++
}

// pop removes and returns the front element, skipping holes.
// Panics if the buffer is empty.
//
//go:nosplit
func (rb *staticRingBuf[T]) pop() T {
	if rb.count == 0 {
		panic("staticRingBuf underflow: pop from empty buffer")
	}

	// Skip holes to find next valid element
	attempts := 0
	for !rb.inUse[rb.head] && attempts < len(rb.data) {
		rb.head = (rb.head + 1) % len(rb.data)
		attempts++
	}

	if attempts >= len(rb.data) || !rb.inUse[rb.head] {
		panic("staticRingBuf corruption: count > 0 but no valid elements")
	}

	value := rb.data[rb.head]
	rb.inUse[rb.head] = false
	rb.head = (rb.head + 1) % len(rb.data)
	rb.count--
	return value
}

// pluck finds an element by value, marks it as not in use (creating a hole), and returns true.
// Returns false if element is not found.
//
//go:nosplit
func (rb *staticRingBuf[T]) pluck(value T) bool {
	// Scan entire buffer looking for matching value
	for i := 0; i < len(rb.data); i++ {
		if rb.inUse[i] && rb.data[i] == value {
			// Found it - mark as hole
			rb.inUse[i] = false
			rb.count--
			return true
		}
	}
	return false
}

// size returns the number of in-use elements (not including holes).
//
//go:nosplit
func (rb *staticRingBuf[T]) size() int {
	return rb.count
}

// isEmpty returns true if there are no in-use elements.
//
//go:nosplit
func (rb *staticRingBuf[T]) isEmpty() bool {
	return rb.count == 0
}

// clear removes all elements and resets head/tail pointers.
//
//go:nosplit
func (rb *staticRingBuf[T]) clear() {
	for i := 0; i < len(rb.inUse); i++ {
		rb.inUse[i] = false
	}
	rb.head = 0
	rb.tail = 0
	rb.count = 0
}

// init initializes the ring buffer with backing arrays.
// This is called by higher-level structures to set up the arrays.
//
//go:nosplit
func (rb *staticRingBuf[T]) init(data []T, inUse []bool) {
	rb.data = data
	rb.inUse = inUse
	rb.head = 0
	rb.tail = 0
	rb.count = 0
}
