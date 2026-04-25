//go:build arm64 || amd64

package ds

// StaticAllocator implements a simple ID allocator using a StaticStack.
//
// Usage:
//   var allocator StaticAllocator[ThreadId]  // Zero-value, not initialized
//   allocator.Init(backingArray)             // Initialize and seed with IDs
//   id := allocator.Acquire()                // Returns first available ID
//   allocator.Release(id)                    // Return ID to pool
//
// The allocator distributes unique IDs in the range [reserved, max-1].
// IDs 0 to reserved-1 are reserved and never allocated.
// IDs are shuffled during initialization for unpredictable ordering.
//
// Safety:
// - NO allocations - safe for interrupt handlers
// - All methods are //go:nosplit safe
type StaticAllocator[T StackElement] struct {
	stack    StaticStack[T] // Embedded by value (not pointer)
	max      T
	reserved T // Number of reserved IDs at the beginning
}

// Init initializes the allocator with the given backing array and seeds it
// with IDs 0..max-1. No reserved IDs.
//
//go:nosplit
func (a *StaticAllocator[T]) Init(data []T) {
	a.InitWithReserved(data, 0)
}

// InitWithReserved initializes the allocator with reserved IDs.
// IDs 0 to reserved-1 are never allocated.
// IDs reserved to max-1 are shuffled and placed in the allocation pool.
//
//go:nosplit
func (a *StaticAllocator[T]) InitWithReserved(data []T, reserved int) {
	// Initialize the embedded stack
	a.stack.Init(data)
	a.max = T(len(data))
	a.reserved = T(reserved)

	if reserved < 0 {
		a.reserved = 0
	}
	if a.reserved >= a.max {
		// All IDs are reserved, nothing to allocate
		return
	}

	// Number of IDs available for allocation
	numIDs := int(a.max - a.reserved)

	// Create temporary array for IDs (in the data array itself)
	// We'll use the first numIDs slots as scratch space
	for i := 0; i < numIDs; i++ {
		data[i] = a.reserved + T(i)
	}

	// Shuffle using Fisher-Yates with timer-seeded PRNG
	seed := readSeed()
	for i := numIDs - 1; i > 0; i-- {
		// Generate pseudo-random index j where 0 <= j <= i
		seed = lcgNext(seed)
		j := int(seed % uint64(i+1))
		// Swap
		data[i], data[j] = data[j], data[i]
	}

	// Push shuffled IDs onto stack (in reverse order so first pop is data[0])
	for i := numIDs - 1; i >= 0; i-- {
		a.stack.Push(data[i])
	}
}

// readSeed returns a seed value for the PRNG.
// Uses timer counter if available, otherwise returns a constant.
//
//go:nosplit
func readSeed() uint64 {
	// Try to read CNTVCT_EL0 (virtual timer counter)
	// This provides good entropy from boot time
	return CurrentTime(0)
}

// lcgNext implements a Linear Congruential Generator for pseudo-random numbers.
// Uses the Numerical Recipes LCG parameters.
//
//go:nosplit
func lcgNext(seed uint64) uint64 {
	// LCG parameters from Numerical Recipes
	return seed*6364136223846793005 + 1442695040888963407
}

// Acquire returns the next available ID.
// Returns a shuffled ID from the range [reserved, max-1].
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
// Releasing a reserved ID (< reserved) is undefined behavior.
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

// Capacity returns the total number of allocatable IDs (excludes reserved).
//
//go:nosplit
func (a *StaticAllocator[T]) Capacity() int {
	return int(a.max - a.reserved)
}

// Reserved returns the number of reserved IDs.
//
//go:nosplit
func (a *StaticAllocator[T]) Reserved() int {
	return int(a.reserved)
}

// TotalCapacity returns the total size including reserved IDs.
//
//go:nosplit
func (a *StaticAllocator[T]) TotalCapacity() int {
	return int(a.max)
}
