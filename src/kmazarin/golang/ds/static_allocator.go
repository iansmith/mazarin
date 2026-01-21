package ds

// StaticAllocator implements a simple ID allocator using a StaticStack.
//
// Usage pattern:
//   1. Create allocator with NewStaticAllocator(max)
//   2. Assign backing array: allocator.stack.Data = backingArray[:]
//   3. Call Init() to seed with IDs 0..max-1
//   4. Use Acquire() to get IDs, Release() to return them
//
// The allocator distributes unique IDs in the range [0, max-1].
// IDs are returned in order starting from 0 (first Acquire() returns 0).
//
// Example:
//   allocator := ds.NewStaticAllocator(4)
//   allocator.stack.Data = backingArray[:]
//   allocator.Init()  // Seeds with IDs: 0, 1, 2, 3
//   id := allocator.Acquire()  // Returns 0
//   allocator.Release(id)      // Returns 0 to pool
type StaticAllocator struct {
	stack StaticStack
	max   int16
}

// NewStaticAllocator creates a new ID allocator with the given maximum ID count.
// The allocator is NOT initialized - caller must:
//   1. Set stack.Data to a backing array
//   2. Call Init() to seed with IDs
//
//go:nosplit
func NewStaticAllocator(max int) *StaticAllocator {
	return &StaticAllocator{
		max: int16(max),
	}
}

// Init seeds the allocator with IDs 0..max-1.
// IDs are pushed in reverse order (max-1, max-2, ..., 1, 0) so that
// the first Acquire() returns ID 0.
//
// Panics if stack.Data is nil or has insufficient capacity.
//
//go:nosplit
func (a *StaticAllocator) Init() {
	if a.stack.Data == nil {
		panic("StaticAllocator.Init: stack.Data is nil")
	}
	if len(a.stack.Data) < int(a.max) {
		panic("StaticAllocator.Init: stack.Data capacity insufficient")
	}

	// Set stack capacity to max
	a.stack.capacity = int(a.max)
	a.stack.top = -1

	// Push IDs in reverse order: max-1, max-2, ..., 1, 0
	// This ensures first Acquire() returns 0
	for id := a.max - 1; id >= 0; id-- {
		a.stack.Push(id)
	}
}

// Acquire returns the next available ID.
// Panics if no IDs are available (allocator exhausted).
//
//go:nosplit
func (a *StaticAllocator) Acquire() int16 {
	if a.stack.IsEmpty() {
		panic("StaticAllocator.Acquire: no IDs available")
	}
	return a.stack.Pop()
}

// Release returns an ID to the allocator pool.
// The ID becomes available for future Acquire() calls.
//
// Note: Does NOT validate that the ID was previously acquired.
// Caller must ensure they don't double-release or release invalid IDs.
//
//go:nosplit
func (a *StaticAllocator) Release(id int16) {
	if a.stack.IsFull() {
		panic("StaticAllocator.Release: stack is full")
	}
	a.stack.Push(id)
}

// Available returns the number of IDs currently available for allocation.
//
//go:nosplit
func (a *StaticAllocator) Available() int {
	return a.stack.Size()
}

// Capacity returns the total number of IDs managed by this allocator.
//
//go:nosplit
func (a *StaticAllocator) Capacity() int {
	return int(a.max)
}
