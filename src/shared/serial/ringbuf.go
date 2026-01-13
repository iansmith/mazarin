// ringbuf.go - Thread-safe ring buffer for serial I/O
//
// This ring buffer is shared between Cardinal and Kmazarin for UART I/O.
// It uses a spinlock for synchronization and supports both polling and
// IRQ-based operation.

package serial

import "sync/atomic"

// RingBuffer is a thread-safe circular buffer for serial I/O
// Must be initialized before use
type RingBuffer struct {
	buf  [4096]byte // Buffer storage
	head uint32     // Write position (next write index)
	tail uint32     // Read position (next read index)
	lock uint32     // Spinlock (0=unlocked, 1=locked)
}

// Lock acquires the spinlock
// Spins until the lock is acquired
//
//go:nosplit
func (r *RingBuffer) Lock() {
	for !atomic.CompareAndSwapUint32(&r.lock, 0, 1) {
		// Spin - could add a yield hint here for efficiency
	}
}

// Unlock releases the spinlock
//
//go:nosplit
func (r *RingBuffer) Unlock() {
	atomic.StoreUint32(&r.lock, 0)
}

// Write writes a single byte to the ring buffer
// Returns true if successful, false if buffer is full
// Caller must hold the lock
//
//go:nosplit
func (r *RingBuffer) Write(b byte) bool {
	head := atomic.LoadUint32(&r.head)
	tail := atomic.LoadUint32(&r.tail)

	// Calculate next head position
	nextHead := (head + 1) % uint32(len(r.buf))

	// Check if buffer is full
	if nextHead == tail {
		return false // Buffer full
	}

	// Write byte
	r.buf[head] = b
	atomic.StoreUint32(&r.head, nextHead)
	return true
}

// WriteLocked writes a single byte to the ring buffer with locking
// Returns true if successful, false if buffer is full
//
//go:nosplit
func (r *RingBuffer) WriteLocked(b byte) bool {
	r.Lock()
	success := r.Write(b)
	r.Unlock()
	return success
}

// Read reads a single byte from the ring buffer
// Returns (byte, true) if successful, (0, false) if buffer is empty
// Caller must hold the lock
//
//go:nosplit
func (r *RingBuffer) Read() (byte, bool) {
	head := atomic.LoadUint32(&r.head)
	tail := atomic.LoadUint32(&r.tail)

	// Check if buffer is empty
	if head == tail {
		return 0, false // Buffer empty
	}

	// Read byte
	b := r.buf[tail]
	nextTail := (tail + 1) % uint32(len(r.buf))
	atomic.StoreUint32(&r.tail, nextTail)
	return b, true
}

// ReadLocked reads a single byte from the ring buffer with locking
// Returns (byte, true) if successful, (0, false) if buffer is empty
//
//go:nosplit
func (r *RingBuffer) ReadLocked() (byte, bool) {
	r.Lock()
	b, ok := r.Read()
	r.Unlock()
	return b, ok
}

// Available returns the number of bytes available to read
// Does not acquire the lock - use for approximate size only
//
//go:nosplit
func (r *RingBuffer) Available() uint32 {
	head := atomic.LoadUint32(&r.head)
	tail := atomic.LoadUint32(&r.tail)

	if head >= tail {
		return head - tail
	}
	return uint32(len(r.buf)) - tail + head
}

// Space returns the number of bytes available for writing
// Does not acquire the lock - use for approximate size only
//
//go:nosplit
func (r *RingBuffer) Space() uint32 {
	// Reserve 1 byte to distinguish full from empty
	return uint32(len(r.buf)) - r.Available() - 1
}

// Reset clears the ring buffer
// Caller must hold the lock
//
//go:nosplit
func (r *RingBuffer) Reset() {
	atomic.StoreUint32(&r.head, 0)
	atomic.StoreUint32(&r.tail, 0)
}
