//go:build arm64

package ds

// StackElement is a type constraint for elements that can be stored in StaticStack.
// Supports small integer types commonly used for IDs and indices.
type StackElement interface {
	~byte | ~int8 | ~uint16 | ~int16
}

// StaticStack implements a fixed-capacity stack backed by a static array.
// The backing array must be provided to the constructor.
//
// This is used by StaticAllocator for ID management, where the stack
// holds available IDs.
//
// Invariants:
// - top == -1 means empty
// - top == capacity-1 means full
// - Valid elements: Data[0..top]
type StaticStack[T StackElement] struct {
	Data     []T      // Backing array (provided at construction)
	top      int      // Index of top element (-1 = empty)
	capacity int      // Max capacity (length of Data)
	Lock     Spinlock // Protects all fields for concurrent access
}

// NewStaticStack creates a new stack with the given backing array.
// The stack starts empty (top = -1).
func NewStaticStack[T StackElement](data []T) *StaticStack[T] {
	return &StaticStack[T]{
		Data:     data,
		top:      -1,
		capacity: len(data),
	}
}

// Push adds a value to the top of the stack.
// Panics if the stack is full.
//
//go:nosplit
func (s *StaticStack[T]) Push(val T) {
	s.Lock.Lock()
	defer s.Lock.Unlock()

	if s.top == s.capacity-1 { // IsFull check inlined to avoid nested lock
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
	s.Lock.Lock()
	defer s.Lock.Unlock()

	if s.top == -1 { // IsEmpty check inlined to avoid nested lock
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
	s.Lock.Lock()
	defer s.Lock.Unlock()

	if s.top == -1 { // IsEmpty check inlined to avoid nested lock
		panic("StaticStack.Peek: stack is empty")
	}
	return s.Data[s.top]
}

// IsEmpty returns true if the stack has no elements.
//
//go:nosplit
func (s *StaticStack[T]) IsEmpty() bool {
	s.Lock.Lock()
	defer s.Lock.Unlock()

	return s.top == -1
}

// IsFull returns true if the stack is at capacity.
//
//go:nosplit
func (s *StaticStack[T]) IsFull() bool {
	s.Lock.Lock()
	defer s.Lock.Unlock()

	return s.top == s.capacity-1
}

// Size returns the number of elements currently in the stack.
//
//go:nosplit
func (s *StaticStack[T]) Size() int {
	s.Lock.Lock()
	defer s.Lock.Unlock()

	return s.top + 1
}

// Capacity returns the maximum number of elements the stack can hold.
//
//go:nosplit
func (s *StaticStack[T]) Capacity() int {
	s.Lock.Lock()
	defer s.Lock.Unlock()

	return s.capacity
}
