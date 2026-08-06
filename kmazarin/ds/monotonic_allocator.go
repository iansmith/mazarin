//go:build arm64 || amd64

package ds

// MonotonicAllocator is a fixed-range ID allocator that issues IDs
// monotonically forward and never reissues a freed ID until the cursor has
// wrapped the whole range. It is the shepherd-PID allocation strategy
// (originally proc.PIDAllocator, MAZ-150) generalized so every kernel ID space
// — shepherd PIDs, thread IDs, channel IDs — shares one definition.
//
// It replaces the LIFO ds.StaticAllocator for ID allocation. StaticAllocator
// pops the most-recently-freed ID as the very next allocation; that immediate
// reissue is the MAZ-179 bug: a thread that dies with an in-flight delegate
// call frees its TID, which is instantly handed to a new thread that collides
// on the same TID-indexed slot. Monotonic issue closes that window.
//
// The allocatable range is [reserved, max) where max == len(inUse). IDs in
// [0, reserved) are never issued. Storage is a caller-provided []bool indexed
// by raw ID, so there is no heap allocation and every method is nosplit-safe.
//
// Not goroutine-safe on its own; callers hold the appropriate lock (the same
// contract as StaticAllocator and PIDAllocator).
type MonotonicAllocator[T StackElement] struct {
	inUse     []bool // indexed by raw ID; len defines max
	cursor    T      // next ID position TryAcquire will consider
	reserved  T      // IDs in [0, reserved) are never issued
	max       T      // == T(len(inUse)); IDs issued from [reserved, max)
	freeCount int    // number of free slots in [reserved, max); 0 == exhausted
}

// Init initializes the allocator over IDs [0, len(inUse)) with no reserved IDs.
//
//go:nosplit
func (a *MonotonicAllocator[T]) Init(inUse []bool) {
	a.InitWithReserved(inUse, 0)
}

// InitWithReserved initializes the allocator with the given backing array.
// IDs [0, reserved) are reserved and never issued; IDs [reserved, len(inUse))
// are allocatable. The backing array is cleared. The first Acquire returns
// the reserved boundary (the lowest allocatable ID).
//
//go:nosplit
func (a *MonotonicAllocator[T]) InitWithReserved(inUse []bool, reserved int) {
	a.inUse = inUse
	a.max = T(len(inUse))
	if reserved < 0 {
		reserved = 0
	}
	a.reserved = T(reserved)
	for i := range a.inUse {
		a.inUse[i] = false
	}
	if a.reserved >= a.max {
		a.freeCount = 0 // all reserved, nothing to allocate
		a.cursor = a.reserved
		return
	}
	a.cursor = a.reserved
	a.freeCount = int(a.max - a.reserved)
}

// TryAcquire returns the next available ID and true, or (0, false) if the
// allocator is exhausted. Allocation is monotonic forward: each call returns an
// ID strictly after the previous one until the cursor wraps at max back to
// reserved, at which point it reuses freed IDs while skipping in-use ones. A
// freed ID is not reissued until that wrap.
//
//go:nosplit
func (a *MonotonicAllocator[T]) TryAcquire() (T, bool) {
	if a.freeCount == 0 {
		return 0, false
	}
	// freeCount > 0 guarantees a free slot exists, so this scan terminates.
	for a.inUse[int(a.cursor)] {
		a.cursor++
		if a.cursor >= a.max {
			a.cursor = a.reserved
		}
	}
	id := a.cursor
	a.inUse[int(id)] = true
	a.freeCount--
	// Advance past the just-issued ID so the next call starts strictly after
	// it — the monotonic-forward guarantee that prevents immediate reuse.
	a.cursor++
	if a.cursor >= a.max {
		a.cursor = a.reserved
	}
	return id, true
}

// Acquire returns the next available ID, panicking if the allocator is
// exhausted. This matches the StaticAllocator.Acquire contract so it is a
// drop-in replacement for ID spaces that treat exhaustion as a fatal invariant
// violation (thread IDs, channel IDs). ID spaces that must fail gracefully
// (shepherd PIDs → EAGAIN) call TryAcquire instead.
//
//go:nosplit
func (a *MonotonicAllocator[T]) Acquire() T {
	id, ok := a.TryAcquire()
	if !ok {
		panic("MonotonicAllocator.Acquire: no IDs available")
	}
	return id
}

// Release returns an ID to the allocatable pool. Idempotent: releasing an ID
// that is not currently allocated is a no-op. IDs outside [reserved, max) are
// ignored. Release deliberately does not touch the cursor — that is what makes
// the freed ID re-enter the pool only after wraparound, not immediately.
//
//go:nosplit
func (a *MonotonicAllocator[T]) Release(id T) {
	if id < a.reserved || id >= a.max {
		return
	}
	if !a.inUse[int(id)] {
		return
	}
	a.inUse[int(id)] = false
	a.freeCount++
}

// InUse reports whether the given ID is currently allocated. Returns false for
// IDs outside [reserved, max).
//
//go:nosplit
func (a *MonotonicAllocator[T]) InUse(id T) bool {
	if id < a.reserved || id >= a.max {
		return false
	}
	return a.inUse[int(id)]
}

// Available returns the number of IDs currently free for allocation.
//
//go:nosplit
func (a *MonotonicAllocator[T]) Available() int { return a.freeCount }

// Capacity returns the total number of allocatable IDs (excludes reserved).
//
//go:nosplit
func (a *MonotonicAllocator[T]) Capacity() int { return int(a.max - a.reserved) }

// Reserved returns the number of reserved IDs.
//
//go:nosplit
func (a *MonotonicAllocator[T]) Reserved() int { return int(a.reserved) }

// TotalCapacity returns the total range size including reserved IDs.
//
//go:nosplit
func (a *MonotonicAllocator[T]) TotalCapacity() int { return int(a.max) }
