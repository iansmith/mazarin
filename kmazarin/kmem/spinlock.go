// spinlock.go - kmem's IRQ-atomic spinlock is the shared ksync.Spinlock.
//
// kmem owns the arch IRQ-disable asm (spinlock_{arm64,amd64}.s) — its natural
// home as the kernel-memory package — and registers it into ksync at init, so
// the SINGLE canonical Spinlock (used by the buddy allocator, proc's span lock,
// …) is IRQ-atomic everywhere from one implementation. See package ksync for the
// rationale (MAZ-127 / MAZ-146): a lock taken from both preemptible and
// IRQ-masked/fault contexts must mask IRQs for the whole hold.
package kmem

import "mazzy/kmazarin/ksync"

// Spinlock is the kernel's canonical IRQ-atomic spinlock (defined in ksync).
// Aliased here so existing kmem.Spinlock callers are unchanged.
type Spinlock = ksync.Spinlock

// init registers kmem's arch IRQ primitives with ksync. Runs during package
// init — before main()/boot enables interrupts or acquires any of these locks at
// runtime — so every Spinlock hold is IRQ-atomic by the time it matters.
func init() {
	ksync.SetIRQPrimitives(saveAndDisableIRQsLocal, restoreIRQsLocal, yieldProcessor)
}

// --- arch primitives (implemented in spinlock_{arm64,amd64}.s) ---

// yieldProcessor executes PAUSE (amd64) / WFE (arm64) to relax a spin-wait.
//
//go:nosplit
func yieldProcessor()

// saveAndDisableIRQsLocal masks IRQs and returns the prior DAIF/RFLAGS state;
// restoreIRQsLocal restores it. Kept local to kmem (the kernel cannot be
// imported here — import cycle) and shared via ksync.SetIRQPrimitives.
//
//go:nosplit
func saveAndDisableIRQsLocal() uint64

//go:nosplit
func restoreIRQsLocal(saved uint64)
