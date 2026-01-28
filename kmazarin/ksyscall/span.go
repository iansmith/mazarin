
package ksyscall

import (
	"sync/atomic"
	_ "unsafe" // Required for go:linkname
)

// ============================================================================
// Span - represents a single VA reservation
// ============================================================================

// Span represents a reserved virtual address range.
type Span interface {
	Start() uint64
	Length() uint64
	End() uint64
	InUse() bool
	Contains(addr uint64) bool
}

// spanImpl is the concrete implementation of Span.
// Uses fixed-size fields to avoid allocation.
type spanImpl struct {
	start  uint64
	length uint64
	inUse  uint32 // 0 = free, 1 = in use
}

func (s *spanImpl) Start() uint64  { return s.start }
func (s *spanImpl) Length() uint64 { return s.length }
func (s *spanImpl) End() uint64    { return s.start + s.length }
func (s *spanImpl) InUse() bool    { return s.inUse != 0 }

func (s *spanImpl) Contains(addr uint64) bool {
	return s.inUse != 0 && addr >= s.start && addr < s.start+s.length
}

// set configures the span (internal use)
func (s *spanImpl) set(start, length uint64) {
	s.start = start
	s.length = length
	s.inUse = 1
}

// clear marks the span as unused (internal use)
func (s *spanImpl) clear() {
	s.inUse = 0
}

// ============================================================================
// SpanGroup - manages a collection of spans for a process
// ============================================================================

// SpanGroup manages VA reservations for a single address space (process).
type SpanGroup interface {
	// TryReserve attempts to reserve at the given address.
	// Returns the address on success, 0 if it overlaps existing spans or no slots.
	TryReserve(start, length uint64) uint64

	// Add records a new VA reservation.
	// Returns true on success, false if no free slots.
	Add(start, length uint64) bool

	// Remove removes/splits spans overlapping the given range.
	Remove(start, length uint64)

	// Contains checks if an address falls within any reserved span.
	Contains(addr uint64) bool

	// FindOverlapEnd returns the end address of any span that overlaps [start, start+length).
	// Returns 0 if no overlap found. Used by bump allocator to skip past reserved regions.
	FindOverlapEnd(start, length uint64) uint64
}

// SpansPerProcess is the number of spans tracked per process.
// 256 entries × 24 bytes = 6KB per process.
const SpansPerProcess = 256

// spanGroupImpl is the concrete implementation of SpanGroup.
// It's a fixed-size array to avoid allocation during mmap.
type spanGroupImpl [SpansPerProcess]spanImpl

// spanGroupLock provides per-SpanGroup locking.
// In a full implementation, this would be embedded in the process struct.
// For now, we use a simple spinlock per group.
type lockedSpanGroup struct {
	spans spanGroupImpl
	lock  uint32 // spinlock: 0 = unlocked, 1 = locked
}

//go:nosplit
func (g *lockedSpanGroup) acquireLock() {
	for !atomic.CompareAndSwapUint32(&g.lock, 0, 1) {
		// spin
	}
}

//go:nosplit
func (g *lockedSpanGroup) releaseLock() {
	atomic.StoreUint32(&g.lock, 0)
}

// TryReserve attempts to reserve at the given address if it doesn't overlap.
// Returns the address on success, 0 if overlap or no free slots.
//
//go:nosplit
func (g *lockedSpanGroup) TryReserve(start, length uint64) uint64 {
	g.acquireLock()

	end := start + length

	// Check for overlap with existing spans
	for i := 0; i < SpansPerProcess; i++ {
		if g.spans[i].inUse != 0 {
			spanEnd := g.spans[i].start + g.spans[i].length
			if start < spanEnd && end > g.spans[i].start {
				// Overlap detected
				g.releaseLock()
				return 0
			}
		}
	}

	// No overlap - find a free slot and reserve
	for i := 0; i < SpansPerProcess; i++ {
		if g.spans[i].inUse == 0 {
			g.spans[i].set(start, length)
			g.releaseLock()
			return start
		}
	}

	// No free slots
	g.releaseLock()
	return 0
}

// Add records a new VA reservation.
// Returns true on success, false if no free slots.
//
//go:nosplit
func (g *lockedSpanGroup) Add(start, length uint64) bool {
	g.acquireLock()

	for i := 0; i < SpansPerProcess; i++ {
		if g.spans[i].inUse == 0 {
			g.spans[i].set(start, length)
			g.releaseLock()
			return true
		}
	}

	g.releaseLock()
	return false
}

// Remove removes/splits spans overlapping the given range.
//
//go:nosplit
func (g *lockedSpanGroup) Remove(start, length uint64) {
	g.acquireLock()

	end := start + length

	for i := 0; i < SpansPerProcess; i++ {
		if g.spans[i].inUse == 0 {
			continue
		}
		spanStart := g.spans[i].start
		spanEnd := spanStart + g.spans[i].length

		// No overlap?
		if start >= spanEnd || end <= spanStart {
			continue
		}

		// Case 1: Full removal (munmap covers entire span)
		if start <= spanStart && end >= spanEnd {
			g.spans[i].clear()
			continue
		}

		// Case 2: Partial removal from start of span
		if start <= spanStart && end < spanEnd {
			g.spans[i].start = end
			g.spans[i].length = spanEnd - end
			continue
		}

		// Case 3: Partial removal from end of span
		if start > spanStart && end >= spanEnd {
			g.spans[i].length = start - spanStart
			continue
		}

		// Case 4: Split - munmap in middle of span
		// Shrink existing span to first part
		g.spans[i].length = start - spanStart

		// Find a free slot for the second part
		for j := 0; j < SpansPerProcess; j++ {
			if g.spans[j].inUse == 0 {
				g.spans[j].set(end, spanEnd-end)
				break
			}
		}
		// If no free slot, we lose tracking of the second part
	}

	g.releaseLock()
}

// Contains checks if an address falls within any reserved span.
//
//go:nosplit
func (g *lockedSpanGroup) Contains(addr uint64) bool {
	g.acquireLock()

	for i := 0; i < SpansPerProcess; i++ {
		if g.spans[i].Contains(addr) {
			g.releaseLock()
			return true
		}
	}

	g.releaseLock()
	return false
}

// FindOverlapEnd returns the end address of any span that overlaps [start, start+length).
// Returns 0 if no overlap found. Used by bump allocator to skip past reserved regions.
//
//go:nosplit
func (g *lockedSpanGroup) FindOverlapEnd(start, length uint64) uint64 {
	g.acquireLock()

	end := start + length

	for i := 0; i < SpansPerProcess; i++ {
		if g.spans[i].inUse == 0 {
			continue
		}
		spanStart := g.spans[i].start
		spanEnd := spanStart + g.spans[i].length

		// Check for overlap: ranges overlap if start < spanEnd AND end > spanStart
		if start < spanEnd && end > spanStart {
			g.releaseLock()
			return spanEnd
		}
	}

	g.releaseLock()
	return 0
}

// ============================================================================
// Per-process span groups
// ============================================================================

// MaxPriests must match the value in main package threads.go
const maxPriests = 32

// priestSpanGroups holds span groups for each priest, indexed by PID.
// PID 0 is reserved for the kernel (not used for mmap tracking).
var priestSpanGroups [maxPriests]lockedSpanGroup

// GetCurrentSpanGroup returns the span group for the current process.
// Uses the current thread's PID to look up the correct span group.
// Falls back to PID 0's group if no current thread (early init).
//
//go:nosplit
func GetCurrentSpanGroup() *lockedSpanGroup {
	// Get the current thread's PID via linkname
	pid := getCurrentThreadPIDForSpan()
	if pid < 0 || pid >= maxPriests {
		pid = 0 // Fallback to kernel group for safety
	}
	return &priestSpanGroups[pid]
}

// getCurrentThreadPIDForSpan gets the current thread's PID for span lookup.
// This is a linkname to main.getCurrentThreadPIDForSpan.
//
//go:linkname getCurrentThreadPIDForSpan main.getCurrentThreadPIDForSpan
func getCurrentThreadPIDForSpan() int16
