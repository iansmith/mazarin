// diplomat/main/span.go - Memory span tracking for mmap
//
// Spans track virtual address ranges that have been allocated via mmap.
// This is used to:
// - Validate MAP_FIXED requests don't overlap existing allocations
// - Honor address hints when possible
// - Track what memory has been allocated
//
// Based on Cardinal's span implementation, but simplified for diplomat's needs.

package main

import "unsafe"

// Span represents a contiguous virtual address range allocated via mmap.
// The Go runtime may:
// 1. Request a specific address (MAP_FIXED) - must not overlap existing spans
// 2. Provide a hint address - we try to honor it if available
// 3. Request any address (addr=0) - we allocate from bump region
type Span interface {
	// Register records a new allocated range.
	// Returns true on success, false if overlaps or no space.
	Register(startVA, endVA uintptr) bool

	// Contains checks if an address is within any registered span.
	// Used for validation and debugging.
	Contains(va uintptr) bool

	// Overlaps checks if a proposed range overlaps any existing span.
	// Used for MAP_FIXED validation.
	Overlaps(startVA, endVA uintptr) bool

	// FindFreeRegion finds a free region of at least size bytes.
	// Returns startVA on success, 0 if not found.
	// Used for honoring hints - search near the hint.
	FindFreeRegion(hint uintptr, size uintptr) uintptr
}

// simpleSpan implements Span using a fixed array of ranges.
// This matches Cardinal's approach but with an interface.
type simpleSpan struct {
	spans    [maxSpans]spanEntry
	nextFree int // Optimization: hint for next free slot
}

type spanEntry struct {
	startVA uintptr
	endVA   uintptr // Exclusive
	inUse   bool
}

const maxSpans = 64 // Diplomat needs fewer spans than Cardinal (no device regions)

// Global span tracker - used by mmap implementation
var globalSpanTracker Span = &simpleSpan{}

// NewSimpleSpan creates a new simpleSpan tracker.
func NewSimpleSpan() Span {
	return &simpleSpan{nextFree: 0}
}

// Register implements Span.Register
func (s *simpleSpan) Register(startVA, endVA uintptr) bool {
	// Validate parameters
	if startVA >= endVA {
		return false
	}

	// Check for overlaps (critical for MAP_FIXED)
	if s.Overlaps(startVA, endVA) {
		return false
	}

	// Find first free slot
	for i := 0; i < maxSpans; i++ {
		if !s.spans[i].inUse {
			s.spans[i].startVA = startVA
			s.spans[i].endVA = endVA
			s.spans[i].inUse = true
			s.nextFree = i + 1 // Hint for next allocation
			return true
		}
	}

	return false // All spans exhausted
}

// Contains implements Span.Contains
func (s *simpleSpan) Contains(va uintptr) bool {
	for i := 0; i < maxSpans; i++ {
		if s.spans[i].inUse && va >= s.spans[i].startVA && va < s.spans[i].endVA {
			return true
		}
	}
	return false
}

// Overlaps implements Span.Overlaps
func (s *simpleSpan) Overlaps(startVA, endVA uintptr) bool {
	for i := 0; i < maxSpans; i++ {
		if !s.spans[i].inUse {
			continue
		}

		existingStart := s.spans[i].startVA
		existingEnd := s.spans[i].endVA

		// Check for overlap:
		// New range overlaps if:
		//   new.start < existing.end AND new.end > existing.start
		if startVA < existingEnd && endVA > existingStart {
			return true
		}
	}
	return false
}

// FindFreeRegion implements Span.FindFreeRegion
// Strategy: If hint is provided and free, use it.
// Otherwise, search sequentially from hint for a free region.
func (s *simpleSpan) FindFreeRegion(hint uintptr, size uintptr) uintptr {
	if size == 0 {
		return 0
	}

	// If no hint, can't help (caller should use bump allocator)
	if hint == 0 {
		return 0
	}

	// Round hint down to page boundary
	pageSize := uintptr(4096)
	alignedHint := hint &^ (pageSize - 1)
	endAddr := alignedHint + size

	// Check if hint range is free
	if !s.Overlaps(alignedHint, endAddr) {
		return alignedHint
	}

	// Hint overlaps - for now, return 0 (caller will use bump allocator)
	// Future: could search for nearby free region
	return 0
}

// verifySpansMapped ensures the spans array is accessible
// Called during early boot to verify memory layout
//
//go:nosplit
func verifySpansMapped() bool {
	// Get address of global span tracker
	tracker := globalSpanTracker.(*simpleSpan)
	spansAddr := uintptr(unsafe.Pointer(&tracker.spans[0]))

	// In UEFI, all memory allocated via AllocatePages is mapped
	// Just verify it's not a null pointer
	return spansAddr != 0
}
