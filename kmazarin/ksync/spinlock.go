// Package ksync provides the kernel's single canonical IRQ-atomic spinlock and
// the single canonical IRQ save/restore primitives (MAZ-167).
//
// Why this exists (MAZ-127 / MAZ-146): a spinlock acquired from BOTH preemptible
// context AND an IRQ-masked / fault-time context MUST mask IRQs for the whole
// hold. Otherwise a holder preempted mid-critical-section is never rescheduled
// once a fault-time acquirer starts spinning on the lock with IRQs masked — a
// permanent single-CPU hang. kmem's buddy allocator hit this (MAZ-127); proc's
// span lock hit it again (MAZ-146, the rachel boot stall). This package is the
// ONE place that locking logic lives, so every IRQ-atomic spinlock shares it.
//
// The arch IRQ save/restore asm lives HERE (spinlock_{arm64,amd64}.s), not in a
// client package: duplicated copies of it are how the MAZ-128 encoding typo
// half-survived its own fix and caused MAZ-166 (see irq_encoding_test.go).
// ksync stays a LEAF (the asm imports nothing), so proc, kmem, and others can
// all use Spinlock without a cycle.
package ksync

import "sync/atomic"

// SaveAndDisableIRQs masks IRQs and returns the prior DAIF/RFLAGS state.
// Implemented in spinlock_{arm64,amd64}.s. Safe from any context, including
// early boot before interrupts are enabled (masking there is a harmless no-op).
//
//go:nosplit
func SaveAndDisableIRQs() uint64

// RestoreIRQs restores the interrupt state saved by SaveAndDisableIRQs.
// Pairs nest: each Restore returns to the state its Save captured.
//
//go:nosplit
func RestoreIRQs(saved uint64)

// yieldProcessor executes PAUSE (amd64) / WFE (arm64) to relax a spin-wait.
//
//go:nosplit
func yieldProcessor()

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
	saved := SaveAndDisableIRQs()
	for !atomic.CompareAndSwapUint32(&s.locked, 0, 1) {
		yieldProcessor()
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
	RestoreIRQs(saved)
}

// TryLock attempts to acquire without spinning. On success the lock is held with
// IRQs masked until Unlock; on failure the prior IRQ state is left unchanged.
//
//go:nosplit
func (s *Spinlock) TryLock() bool {
	saved := SaveAndDisableIRQs()
	if atomic.CompareAndSwapUint32(&s.locked, 0, 1) {
		s.savedIRQ = saved
		return true
	}
	RestoreIRQs(saved)
	return false
}
