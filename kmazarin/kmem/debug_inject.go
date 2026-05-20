package kmem

import "sync/atomic"

// MapPageInProcessFailNext arms a one-shot failure: when true, the next call
// to MapPageInProcess will clear the flag and return false without doing any
// work.
//
// One-shot rather than a counter because every rollback path also calls
// MapPageInProcess to restore the caller's mapping — a counter > 1 would
// inject failures into the rollback itself and trip KernelPanic. The only
// safe arming is "fire the next forward call, then disarm."
//
// Used by xfertest's TransferDMAClump rollback stage to provoke the
// mid-transfer failure path. Production cost: one atomic.Load + branch per
// MapPageInProcess call; default-false → predicted-never-taken.
//
// Set via SyscallDebugPrint marker DebugMarkerSetMapFailNext (0xDB8).
var MapPageInProcessFailNext atomic.Bool

// ConsumeMapFailInjection returns true (and disarms) if the one-shot flag
// is set; otherwise returns false. Nosplit so it can be called from inside
// transferDMAClumpInner's IRQ-disabled critical section.
//
//go:nosplit
func ConsumeMapFailInjection() bool {
	return MapPageInProcessFailNext.CompareAndSwap(true, false)
}
