package proc

import "mazzy/kmazarin/ksync"

// ============================================================================
// Span - represents a single VA reservation
// ============================================================================

// SpansPerProcess is the number of spans tracked per process.
// 1024 entries × 24 bytes = 24KB per process.
const SpansPerProcess = 1024

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
	// IRQ-atomic (MAZ-146): the span lock is taken from BOTH preemptible context
	// (mmap / share-pages → Add/Remove) AND the IRQ-masked page-fault handler
	// (inAllocatedUserRegion → Contains). A bare CAS spinlock let a holder be
	// preempted mid-section; a fault-time acquirer then spun on it with IRQs
	// masked, so the holder was never rescheduled → permanent boot hang. The
	// shared IRQ-atomic ksync.Spinlock keeps every hold atomic w.r.t. preemption
	// (same class + fix as MAZ-127's buddy kmem.Spinlock).
	lk ksync.Spinlock
}

//go:nosplit
func (g *LockedSpanGroup) acquireLock() { g.lk.Lock() }

//go:nosplit
func (g *LockedSpanGroup) releaseLock() { g.lk.Unlock() }

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

// Add records a new VA reservation. Adjacent spans are coalesced to
// avoid exhausting the fixed-size span table. This is critical because
// the Go runtime's sysMap (MAP_FIXED) repeatedly splits reservation
// spans — without coalescing, each heap growth consumes a slot and the
// table fills up, causing a fatal OOM even with plenty of free pages.
// Returns true on success, false if no free slots.
//
//go:nosplit
func (g *LockedSpanGroup) Add(start, length uint64) bool {
	g.acquireLock()

	end := start + length
	leftIdx := -1
	rightIdx := -1

	// Find adjacent spans to coalesce with.
	for i := 0; i < SpansPerProcess; i++ {
		if g.spans[i].inUse == 0 {
			continue
		}
		spanEnd := g.spans[i].start + g.spans[i].length
		if spanEnd == start {
			leftIdx = i
		}
		if g.spans[i].start == end {
			rightIdx = i
		}
	}

	if leftIdx >= 0 && rightIdx >= 0 {
		// Merge left + new + right into left, free right slot.
		rightEnd := g.spans[rightIdx].start + g.spans[rightIdx].length
		g.spans[leftIdx].length = rightEnd - g.spans[leftIdx].start
		g.spans[rightIdx].clear()
		g.releaseLock()
		return true
	}
	if leftIdx >= 0 {
		// Extend left neighbor to cover new range.
		g.spans[leftIdx].length += length
		g.releaseLock()
		return true
	}
	if rightIdx >= 0 {
		// Extend right neighbor backward to cover new range.
		g.spans[rightIdx].start = start
		g.spans[rightIdx].length += length
		g.releaseLock()
		return true
	}

	// No adjacent spans — allocate a free slot.
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

// SpanVisitor is a callback type for ForEach.
// Called once per active span with (start, length) in bytes.
// The callback must NOT call any LockedSpanGroup methods (would deadlock).
type SpanVisitor func(start, length uint64)

// ForEach snapshots the span array under the lock, then iterates
// the copy without holding it. This prevents deadlock when a page
// fault handler calls Contains on the same LockedSpanGroup while
// the ForEach callback (e.g. A/D bit scan) is running on another
// thread — the lock is held only for the copy, not the callback.
func (g *LockedSpanGroup) ForEach(fn SpanVisitor) {
	var buf [SpansPerProcess]spanImpl
	g.acquireLock()
	buf = g.spans
	g.releaseLock()
	for i := 0; i < SpansPerProcess; i++ {
		if buf[i].inUse != 0 {
			fn(buf[i].start, buf[i].length)
		}
	}
}
