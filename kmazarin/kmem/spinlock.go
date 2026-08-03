// spinlock.go - kmem's IRQ-atomic spinlock is the shared ksync.Spinlock.
//
// See package ksync for the rationale (MAZ-127 / MAZ-146): a lock taken from
// both preemptible and IRQ-masked/fault contexts must mask IRQs for the whole
// hold. The arch IRQ save/restore asm lives in ksync itself (MAZ-167) — kmem's
// former local copy was one of the duplicates that let the MAZ-128 encoding
// typo survive half-fixed and cause MAZ-166.
package kmem

import "mazzy/kmazarin/ksync"

// Spinlock is the kernel's canonical IRQ-atomic spinlock (defined in ksync).
// Aliased here so existing kmem.Spinlock callers are unchanged.
type Spinlock = ksync.Spinlock
