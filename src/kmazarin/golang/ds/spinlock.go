//go:build arm64

package ds

// Spinlock implements a simple spinlock with calibrated backoff.
//
// Lock acquisition strategy:
// - Try CAS 5 times with 500ns backoff between attempts
// - Total wait: 4 × 500ns = 2 microseconds
// - Panic if unable to acquire after 5 attempts
//
// Timing (at 62.5MHz QEMU default):
// - SpinBackoffTicks = 31 = 500 nanoseconds
//
// Safety:
// - All methods are //go:nosplit safe
// - Uses ARMv8.0 compatible atomic instructions (LDAXRW/STLXRW)
// - Compatible with all ARM64 variants
type Spinlock struct {
	locked uint32 // 0 = unlocked, 1 = locked
}

const (
	SpinAttempts     = 5  // Total CAS attempts before giving up
	SpinBackoffTicks = 31 // 500ns at 62.5MHz between attempts
)

// Lock acquires the spinlock.
//
// Makes 5 attempts to acquire the lock, waiting 500ns between each attempt.
// Panics if unable to acquire after all attempts (total ~2μs wait).
//
// Flow:
//   Attempt 1: CAS
//   Wait 500ns
//   Attempt 2: CAS
//   Wait 500ns
//   Attempt 3: CAS
//   Wait 500ns
//   Attempt 4: CAS
//   Wait 500ns
//   Attempt 5: CAS
//   → Panic if failed
//
//go:nosplit
func (s *Spinlock) Lock() {
	for attempt := 0; attempt < SpinAttempts; attempt++ {
		if CompareAndSwapUint32(&s.locked, 0, 1) {
			return // Successfully acquired
		}

		// Wait 500ns before next attempt (except after last attempt)
		if attempt < SpinAttempts-1 {
			nanoWait(SpinBackoffTicks)
		}
	}

	// Failed to acquire after all attempts
	panic("spinlock: failed to acquire after 2μs timeout")
}

// Unlock releases the spinlock.
//
// Uses atomic store with release semantics to ensure all prior
// memory operations are visible before the lock is released.
//
//go:nosplit
func (s *Spinlock) Unlock() {
	StoreUint32(&s.locked, 0)
}

// Assembly functions implemented in spinlock_arm64.s
//
// CompareAndSwapUint32 atomically compares *addr to old, and if equal,
// stores new. Returns true if the swap was performed.
// Uses LDAXRW/STLXRW (ARMv8.0 compatible).
func CompareAndSwapUint32(addr *uint32, old, new uint32) bool

// StoreUint32 atomically stores val to *addr with release semantics.
// Uses STLRW (ARMv8.0 compatible).
func StoreUint32(addr *uint32, val uint32)

// nanoWait busy-waits for the specified number of clock ticks.
// Uses CNTVCT_EL0 (virtual counter) for precise timing.
// Does NOT yield CPU - spins in tight loop.
func nanoWait(ticks uint64)
