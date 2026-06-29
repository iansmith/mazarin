// Package ksync provides the kernel's single canonical IRQ-atomic spinlock.
//
// Why this exists (MAZ-127 / MAZ-146): a spinlock acquired from BOTH preemptible
// context AND an IRQ-masked / fault-time context MUST mask IRQs for the whole
// hold. Otherwise a holder preempted mid-critical-section is never rescheduled
// once a fault-time acquirer starts spinning on the lock with IRQs masked — a
// permanent single-CPU hang. kmem's buddy allocator hit this (MAZ-127); proc's
// span lock hit it again (MAZ-146, the rachel boot stall). This package is the
// ONE place that locking logic lives, so every IRQ-atomic spinlock shares it.
//
// Import-cycle break: ksync is a LEAF (it imports nothing kernel-specific), so
// proc, kmem, and others can all use Spinlock without a cycle. The arch
// IRQ-disable asm lives in its natural home — the kernel-memory package (kmem) —
// and is INJECTED here via SetIRQPrimitives at boot, before interrupts are
// enabled. Until injected (the single-threaded, IRQs-off early-boot window) Lock
// falls back to a bare CAS, which is correct there because no preemption is
// possible yet.
package ksync

import "sync/atomic"

// IRQ-primitive hooks, injected once at boot by the package that owns the arch
// asm (kmem). nil only during the pre-injection early-boot window.
var (
	irqSave    func() uint64
	irqRestore func(uint64)
	cpuYield   func()
)

// SetIRQPrimitives installs the arch IRQ save/disable, restore, and spin-yield
// primitives. Call once, early in boot, before interrupts are enabled. This is
// the dependency-inversion seam that keeps ksync a leaf: the impl lives in the
// kernel-memory/interrupt layer and registers itself here.
func SetIRQPrimitives(save func() uint64, restore func(uint64), yield func()) {
	irqSave = save
	irqRestore = restore
	cpuYield = yield
}

// Spinlock is an IRQ-atomic spinlock for SHORT critical sections. Lock masks IRQs
// for the entire hold (so the holder cannot be preempted mid-section); Unlock
// restores the prior IRQ state. Single holder: savedIRQ is written only by the
// winning acquirer, under IRQ-masked exclusion. The zero value is ready to use.
type Spinlock struct {
	locked   uint32
	savedIRQ uint64 // prior IRQ state captured at Lock, restored at Unlock
}

// Lock acquires the spinlock with IRQs masked, spinning until successful.
//
//go:nosplit
func (s *Spinlock) Lock() {
	var saved uint64
	if irqSave != nil {
		saved = irqSave()
	}
	for !atomic.CompareAndSwapUint32(&s.locked, 0, 1) {
		if cpuYield != nil {
			cpuYield()
		}
	}
	// Only the winning acquirer reaches here, IRQs masked, so this store to the
	// shared field is uncontended.
	s.savedIRQ = saved
}

// Unlock releases the spinlock and restores the IRQ state captured by Lock.
//
//go:nosplit
func (s *Spinlock) Unlock() {
	saved := s.savedIRQ
	atomic.StoreUint32(&s.locked, 0)
	if irqRestore != nil {
		irqRestore(saved)
	}
}

// TryLock attempts to acquire without spinning. On success the lock is held with
// IRQs masked until Unlock; on failure the prior IRQ state is left unchanged.
//
//go:nosplit
func (s *Spinlock) TryLock() bool {
	var saved uint64
	if irqSave != nil {
		saved = irqSave()
	}
	if atomic.CompareAndSwapUint32(&s.locked, 0, 1) {
		s.savedIRQ = saved
		return true
	}
	if irqRestore != nil {
		irqRestore(saved)
	}
	return false
}
