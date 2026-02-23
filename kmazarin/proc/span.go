package proc

import "sync/atomic"

// ============================================================================
// Span - represents a single VA reservation
// ============================================================================

// SpansPerProcess is the number of spans tracked per process.
// 256 entries × 24 bytes = 6KB per process.
const SpansPerProcess = 256

// spanImpl is a reserved virtual address range.
type spanImpl struct {
	start  uint64
	length uint64
	inUse  uint32 // 0 = free, 1 = in use
}

func (s *spanImpl) contains(addr uint64) bool {
	return s.inUse != 0 && addr >= s.start && addr < s.start+s.length
}

func (s *spanImpl) set(start, length uint64) {
	s.start = start
	s.length = length
	s.inUse = 1
}

func (s *spanImpl) clear() {
	s.inUse = 0
}

// ============================================================================
// LockedSpanGroup - manages a collection of spans for a process
// ============================================================================

type spanGroupImpl [SpansPerProcess]spanImpl

// LockedSpanGroup manages VA reservations for a single address space.
// Zero value is ready to use (all spans free, lock unlocked).
type LockedSpanGroup struct {
	spans spanGroupImpl
	lock  uint32 // spinlock: 0 = unlocked, 1 = locked
}

//go:nosplit
func (g *LockedSpanGroup) acquireLock() {
	for !atomic.CompareAndSwapUint32(&g.lock, 0, 1) {
		// spin
	}
}

//go:nosplit
func (g *LockedSpanGroup) releaseLock() {
	atomic.StoreUint32(&g.lock, 0)
}

// TryReserve attempts to reserve at the given address if it doesn't overlap.
// Returns the address on success, 0 if overlap or no free slots.
//
//go:nosplit
func (g *LockedSpanGroup) TryReserve(start, length uint64) uint64 {
	g.acquireLock()

	end := start + length

	for i := 0; i < SpansPerProcess; i++ {
		if g.spans[i].inUse != 0 {
			spanEnd := g.spans[i].start + g.spans[i].length
			if start < spanEnd && end > g.spans[i].start {
				g.releaseLock()
				return 0
			}
		}
	}

	for i := 0; i < SpansPerProcess; i++ {
		if g.spans[i].inUse == 0 {
			g.spans[i].set(start, length)
			g.releaseLock()
			return start
		}
	}

	g.releaseLock()
	return 0
}

// Add records a new VA reservation.
// Returns true on success, false if no free slots.
//
//go:nosplit
func (g *LockedSpanGroup) Add(start, length uint64) bool {
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
func (g *LockedSpanGroup) Remove(start, length uint64) {
	g.acquireLock()

	end := start + length

	for i := 0; i < SpansPerProcess; i++ {
		if g.spans[i].inUse == 0 {
			continue
		}
		spanStart := g.spans[i].start
		spanEnd := spanStart + g.spans[i].length

		if start >= spanEnd || end <= spanStart {
			continue
		}

		if start <= spanStart && end >= spanEnd {
			g.spans[i].clear()
			continue
		}

		if start <= spanStart && end < spanEnd {
			g.spans[i].start = end
			g.spans[i].length = spanEnd - end
			continue
		}

		if start > spanStart && end >= spanEnd {
			g.spans[i].length = start - spanStart
			continue
		}

		// Split: munmap in the middle of a span
		g.spans[i].length = start - spanStart
		for j := 0; j < SpansPerProcess; j++ {
			if g.spans[j].inUse == 0 {
				g.spans[j].set(end, spanEnd-end)
				break
			}
		}
	}

	g.releaseLock()
}

// Contains checks if an address falls within any reserved span.
//
//go:nosplit
func (g *LockedSpanGroup) Contains(addr uint64) bool {
	g.acquireLock()

	for i := 0; i < SpansPerProcess; i++ {
		if g.spans[i].contains(addr) {
			g.releaseLock()
			return true
		}
	}

	g.releaseLock()
	return false
}

// FindOverlapEnd returns the end address of any span overlapping [start, start+length).
// Returns 0 if no overlap. Used by the bump allocator to skip past reserved regions.
//
//go:nosplit
func (g *LockedSpanGroup) FindOverlapEnd(start, length uint64) uint64 {
	g.acquireLock()

	end := start + length

	for i := 0; i < SpansPerProcess; i++ {
		if g.spans[i].inUse == 0 {
			continue
		}
		spanStart := g.spans[i].start
		spanEnd := spanStart + g.spans[i].length

		if start < spanEnd && end > spanStart {
			g.releaseLock()
			return spanEnd
		}
	}

	g.releaseLock()
	return 0
}
