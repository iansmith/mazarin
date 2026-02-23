package ksyscall

import (
	"mazzy/kmazarin/proc"
	_ "unsafe" // Required for go:linkname
)

// kernelSpanGroup is the fallback span group for kernel-context calls
// (when proc.CurrentPriest() returns nil). Kernel threads do not perform
// userspace mmap, so this group is effectively unused in practice.
var kernelSpanGroup proc.LockedSpanGroup

// getCurrentSpanGroup returns the span group for the current process.
//
//go:nosplit
func getCurrentSpanGroup() *proc.LockedSpanGroup {
	p := proc.CurrentPriest()
	if p == nil {
		return &kernelSpanGroup
	}
	return &p.Spans
}

// addSpan records a new VA reservation for the current process.
//
//go:nosplit
func addSpan(start, length uint64) bool {
	return getCurrentSpanGroup().Add(start, length)
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
