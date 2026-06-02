// spinlock.go - Simple spinlock for protecting shared state
package kmem

import "sync/atomic"

// Spinlock is a simple IRQ-atomic spinlock using atomic compare-and-swap.
// Use for short critical sections only.
//
// IRQ discipline: Lock masks IRQs for the duration of the hold (Unlock restores
// the prior state). This is REQUIRED, not optional: the locks built on this type
// (the buddy physical-page allocator, the unified pool, the page-descriptor
// tracker) are acquired from BOTH preemptible kernel context (e.g. the thread-0
// deferred-reclamation drain, freeing pages with IRQs enabled) AND the IRQ-masked
// demand-fault allocator (the data-abort handler runs with DAIF.I set). If a
// holder could be preempted mid-critical-section, a fault-time acquirer would spin
// on it with IRQs masked and never let the holder be rescheduled — a hard hang
// (observed as BuddyAllocTyped → yieldProcessor spinning forever). Masking IRQs
// while held keeps every hold atomic w.r.t. preemption; the cost is only the
// microseconds of one free-list op, so callers (e.g. the drain) remain freely
// preemptible BETWEEN acquisitions.
type Spinlock struct {
	locked    uint32
	savedDAIF uint64 // IRQ state captured at Lock, restored at Unlock (single holder)
}

// Lock acquires the spinlock with IRQs masked, spinning until successful.
//
//go:nosplit
func (s *Spinlock) Lock() {
	saved := saveAndDisableIRQsLocal()
	for !atomic.CompareAndSwapUint32(&s.locked, 0, 1) {
		yieldProcessor() // ARM64 WFE instruction
	}
	// Only the winning acquirer reaches here, and IRQs are masked, so this store
	// to the shared field is uncontended.
	s.savedDAIF = saved
}

// Unlock releases the spinlock and restores the IRQ state captured by Lock.
//
//go:nosplit
func (s *Spinlock) Unlock() {
	saved := s.savedDAIF
	atomic.StoreUint32(&s.locked, 0)
	restoreIRQsLocal(saved)
}

// TryLock attempts to acquire the spinlock without blocking. Returns true if
// acquired (with IRQs masked until Unlock); false leaves the IRQ state unchanged.
//
//go:nosplit
func (s *Spinlock) TryLock() bool {
	saved := saveAndDisableIRQsLocal()
	if atomic.CompareAndSwapUint32(&s.locked, 0, 1) {
		s.savedDAIF = saved
		return true
	}
	restoreIRQsLocal(saved)
	return false
}

// yieldProcessor is implemented in assembly (spinlock_arm64.s)
// It executes the ARM64 WFE instruction which puts the core in a
// low-power state until an event (such as another core releasing
// a lock via SEV) occurs.
//
//go:nosplit
func yieldProcessor()

// saveAndDisableIRQsLocal masks IRQs and returns the prior DAIF/RFLAGS state.
// restoreIRQsLocal restores it. Implemented in spinlock_{arm64,amd64}.s — kmem
// cannot import the main package's SaveAndDisableIRQs (import cycle).
//
//go:nosplit
func saveAndDisableIRQsLocal() uint64

//go:nosplit
func restoreIRQsLocal(saved uint64)
