package ksyscall

import (
	"sync/atomic"

	"mazzy/kmazarin/proc"
	_ "unsafe" // Required for go:linkname
)

// kernelSpanGroup is the fallback span group for kernel-context calls
// (when proc.CurrentShepherd() returns nil). Kernel threads do not perform
// userspace mmap, so this group is effectively unused in practice.
var kernelSpanGroup proc.LockedSpanGroup

// getCurrentSpanGroup returns the span group for the current process.
//
//go:nosplit
func getCurrentSpanGroup() *proc.LockedSpanGroup {
	p := proc.CurrentShepherd()
	if p == nil {
		return &kernelSpanGroup
	}
	return &p.Spans
}

// addSpan records a new VA reservation for the current process.
//
//go:nosplit
func addSpan(start, length uint64) bool {
	g := getCurrentSpanGroup()
	// [MAZ-179 tier-13b probe] a fresh non-FIXED grant overlapping an
	// existing span is a live-range double-grant — Add would silently
	// coalesce the evidence away, so witness it first. MAP_FIXED callers
	// run removeSpan beforehand and never trip this.
	if ov := g.FindOverlapEnd(start, length); ov != 0 {
		if atomic.AddUint32(&proc.GrantOverlapTrips, 1) == 1 {
			sid := int32(-1)
			if p := proc.CurrentShepherd(); p != nil {
				sid = int32(p.PID)
			}
			atomic.StoreInt32(&proc.GrantOverlapFirstSID, sid)
			atomic.StoreUint64(&proc.GrantOverlapFirstVA, start)
			atomic.StoreUint64(&proc.GrantOverlapFirstLen, length)
			atomic.StoreUint64(&proc.GrantOverlapFirstEnd, ov)
		}
	}
	return g.Add(start, length)
}

// removeSpan removes/splits spans overlapping the given range.
//
//go:nosplit
func removeSpan(start, length uint64) {
	getCurrentSpanGroup().Remove(start, length)
}

// IsAddressInSpan checks if an address falls within any reserved span.
// Used by the page fault handler to validate hint-based allocations.
//
//go:nosplit
func IsAddressInSpan(addr uint64) bool {
	return getCurrentSpanGroup().Contains(addr)
}

// tryReserveHint attempts to reserve a hint address if it doesn't overlap.
//
//go:nosplit
func tryReserveHint(hint, length uint64) uint64 {
	return getCurrentSpanGroup().TryReserve(hint, length)
}

// findSpanOverlapEnd returns the end of any span overlapping [start, start+length).
// Returns 0 if no overlap. Used by the bump allocator to skip past reserved regions.
//
//go:nosplit
func findSpanOverlapEnd(start, length uint64) uint64 {
	return getCurrentSpanGroup().FindOverlapEnd(start, length)
}

// GetCurrentSpanGroup returns the span group for the current process.
// Exported for use by kmem/paging.go via linkname (legacy — prefer proc directly).
//
//go:nosplit
func GetCurrentSpanGroup() *proc.LockedSpanGroup {
	return getCurrentSpanGroup()
}
