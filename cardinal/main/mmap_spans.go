package main

// Mmap span tracking - records which virtual address ranges have been mmap'd.
// Used by page fault handler to validate that faulting addresses are legitimate.
const MAX_MMAP_SPANS = 32

type mmapSpan struct {
	startVA uintptr
	endVA   uintptr
	inUse   bool
}

var mmapSpans [MAX_MMAP_SPANS]mmapSpan

// registerMmapSpan records a new mmap'd region.
//
//go:nosplit
func registerMmapSpan(startVA, endVA uintptr) bool {
	for i := 0; i < MAX_MMAP_SPANS; i++ {
		if !mmapSpans[i].inUse {
			mmapSpans[i].startVA = startVA
			mmapSpans[i].endVA = endVA
			mmapSpans[i].inUse = true
			return true
		}
	}
	return false
}

// isInMmapSpan checks if a virtual address is within any registered mmap span.
//
//go:nosplit
func isInMmapSpan(va uintptr) bool {
	for i := 0; i < MAX_MMAP_SPANS; i++ {
		if mmapSpans[i].inUse && va >= mmapSpans[i].startVA && va < mmapSpans[i].endVA {
			return true
		}
	}
	return false
}
