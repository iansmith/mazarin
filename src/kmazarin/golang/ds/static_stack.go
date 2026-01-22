//go:build arm64

package ds

// StackElement is a type constraint for elements that can be stored in StaticStack.
// Supports small integer types commonly used for IDs and indices.
type StackElement interface {
	~byte | ~int8 | ~uint16 | ~int16
}

// StaticStack implements a fixed-capacity stack backed by a static array.
//
// Usage:
//   var stack StaticStack[ThreadId]  // Zero-value, not initialized
//   stack.Init(backingArray)         // Initialize with backing array
//   stack.Push(value)                // Ready to use
//
// This is used by StaticAllocator for ID management, where the stack
// holds available IDs.
//
// Invariants:
// - top == -1 means empty
// - top == capacity-1 means full
// - Valid elements: Data[0..top]
//
// Safety:
// - NO allocations - safe for interrupt handlers
// - All methods are //go:nosplit safe
type StaticStack[T StackElement] struct {
	Data     []T // Backing array (set via Init)
	top      int // Index of top element (-1 = empty)
	capacity int // Max capacity (length of Data)
	// NOTE: No lock - protected by external schedulerLock
}

// Init initializes the stack with the given backing array.
// The stack starts empty (top = -1).
//
//go:nosplit
func (s *StaticStack[T]) Init(data []T) {
	s.Data = data
	s.top = -1
	s.capacity = len(data)
}

// Push adds a value to the top of the stack.
// Panics if the stack is full.
//
//go:nosplit
func (s *StaticStack[T]) Push(val T) {
	if s.top == s.capacity-1 {
		panic("StaticStack.Push: stack is full")
	}
	s.top++
	s.Data[s.top] = val
}

// Pop removes and returns the value at the top of the stack.
// Panics if the stack is empty.
//
//go:nosplit
func (s *StaticStack[T]) Pop() T {
	if s.top == -1 {
		panic("StaticStack.Pop: stack is empty")
	}
	val := s.Data[s.top]
	s.top--
	return val
}

// Peek returns the value at the top of the stack without removing it.
// Panics if the stack is empty.
//
//go:nosplit
func (s *StaticStack[T]) Peek() T {
	if s.top == -1 {
		panic("StaticStack.Peek: stack is empty")
	}
	return s.Data[s.top]
}

// IsEmpty returns true if the stack has no elements.
//
//go:nosplit
func (s *StaticStack[T]) IsEmpty() bool {
	return s.top == -1
}

// IsFull returns true if the stack is at capacity.
//
//go:nosplit
func (s *StaticStack[T]) IsFull() bool {
	return s.top == s.capacity-1
}

// Size returns the number of elements currently in the stack.
//
//go:nosplit
func (s *StaticStack[T]) Size() int {
	return s.top + 1
}

// Capacity returns the maximum number of elements the stack can hold.
//
//go:nosplit
func (s *StaticStack[T]) Capacity() int {
	return s.capacity
}
