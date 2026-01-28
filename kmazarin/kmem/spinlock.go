// spinlock.go - Simple spinlock for protecting shared state
package kmem

import "sync/atomic"

// Spinlock is a simple spinlock using atomic compare-and-swap.
// Use for short critical sections only. For longer waits, prefer
// other synchronization mechanisms.
type Spinlock struct {
	locked uint32
}

// Lock acquires the spinlock, spinning until successful.
// Uses ARM64 WFE (Wait For Event) instruction to reduce power
// consumption while spinning.
//
//go:nosplit
func (s *Spinlock) Lock() {
	for !atomic.CompareAndSwapUint32(&s.locked, 0, 1) {
		yieldProcessor() // ARM64 WFE instruction
	}
}

// Unlock releases the spinlock.
// Uses store with release semantics to ensure all prior writes
// are visible before the lock is released.
//
//go:nosplit
func (s *Spinlock) Unlock() {
	atomic.StoreUint32(&s.locked, 0)
}

// TryLock attempts to acquire the spinlock without blocking.
// Returns true if the lock was acquired, false otherwise.
//
//go:nosplit
func (s *Spinlock) TryLock() bool {
	return atomic.CompareAndSwapUint32(&s.locked, 0, 1)
}

// yieldProcessor is implemented in assembly (spinlock_arm64.s)
// It executes the ARM64 WFE instruction which puts the core in a
// low-power state until an event (such as another core releasing
// a lock via SEV) occurs.
//
//go:nosplit
func yieldProcessor()
