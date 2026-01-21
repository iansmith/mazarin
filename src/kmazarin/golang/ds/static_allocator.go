//go:build arm64

package ds

// StaticAllocator implements a simple ID allocator using a StaticStack.
//
// Usage pattern:
//   1. Create allocator with NewStaticAllocator(backingArray)
//   2. Call Init() to seed with IDs 0..max-1
//   3. Use Acquire() to get IDs, Release() to return them
//
// The allocator distributes unique IDs in the range [0, max-1].
// IDs are returned in order starting from 0 (first Acquire() returns 0).
//
// Example:
//   backingArray := make([]ThreadId, 32)
//   allocator := ds.NewStaticAllocator(backingArray)
//   allocator.Init()  // Seeds with IDs: 0, 1, 2, ...31
//   id := allocator.Acquire()  // Returns 0
//   allocator.Release(id)      // Returns 0 to pool
type StaticAllocator[T StackElement] struct {
	stack *StaticStack[T]
	max   T
}

// NewStaticAllocator creates a new ID allocator with the given backing array.
// The allocator is NOT initialized - caller must call Init() to seed with IDs.
//
//go:nosplit
func NewStaticAllocator[T StackElement](data []T) *StaticAllocator[T] {
	return &StaticAllocator[T]{
		stack: NewStaticStack(data),
		max:   T(len(data)),
	}
}

// Init seeds the allocator with IDs 0..max-1.
// IDs are pushed in reverse order (max-1, max-2, ..., 1, 0) so that
// the first Acquire() returns ID 0.
//
//go:nosplit
func (a *StaticAllocator[T]) Init() {
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
func (a *StaticAllocator[T]) Acquire() T {
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
func (a *StaticAllocator[T]) Release(id T) {
	if a.stack.IsFull() {
		panic("StaticAllocator.Release: stack is full")
	}
	a.stack.Push(id)
}

// Available returns the number of IDs currently available for allocation.
//
//go:nosplit
func (a *StaticAllocator[T]) Available() int {
	return a.stack.Size()
}

// Capacity returns the total number of IDs managed by this allocator.
//
//go:nosplit
func (a *StaticAllocator[T]) Capacity() int {
	return int(a.max)
}
