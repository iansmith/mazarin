// diplomat/main/futex.go - Futex stub for Go runtime
//
// Implements minimal futex support without actual threading.
// The Go runtime uses futex for locks and synchronization primitives.
// We stub these to return values that allow initialization to proceed.

package main

import "unsafe"

// Futex operations (from Linux)
const (
	FUTEX_WAIT         = 0
	FUTEX_WAKE         = 1
	FUTEX_FD           = 2
	FUTEX_REQUEUE      = 3
	FUTEX_CMP_REQUEUE  = 4
	FUTEX_WAKE_OP      = 5
	FUTEX_PRIVATE_FLAG = 128
	EAGAIN             = 11
)

// DiplomatFutex implements futex for the Go runtime (stub version).
//
// Without threading support, we:
// - FUTEX_WAIT: Return -EAGAIN (spurious wakeup)
// - FUTEX_WAKE: Return 0 (no threads woken)
//
// This is sufficient for Go runtime to initialize its locking primitives.
// The runtime will handle spurious wakeups gracefully.
//
// Parameters match Linux futex(2):
//   uaddr   - pointer to futex word
//   op      - futex operation (FUTEX_WAIT, FUTEX_WAKE, etc.)
//   val     - operation-specific value
//   timeout - timeout (ignored)
//   uaddr2  - second futex address (for REQUEUE, ignored)
//   val3    - operation-specific value (ignored)
//
// Returns:
//   >= 0: success (number of waiters woken for FUTEX_WAKE)
//   <  0: -errno (e.g., -EAGAIN)
//
//go:nosplit
func DiplomatFutex(uaddr unsafe.Pointer, op int32, val uint32, timeout unsafe.Pointer, uaddr2 unsafe.Pointer, val3 uint32) int64 {
	// Mask off private flag
	opMasked := op & 0x7F

	switch opMasked {
	case FUTEX_WAIT:
		// FUTEX_WAIT: Block until woken
		// Without threads, we can't actually block
		// Return -EAGAIN to indicate spurious wakeup
		// This tells the caller to retry the operation
		return -EAGAIN

	case FUTEX_WAKE:
		// FUTEX_WAKE: Wake up to 'val' threads
		// Without threads, no one to wake
		// Return 0 (number of threads woken)
		return 0

	default:
		// Other futex operations not implemented
		// Return success for now (may need to add more later)
		return 0
	}
}
