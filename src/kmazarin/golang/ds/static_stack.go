//go:build arm64

package ds

// StaticStack implements a fixed-capacity stack backed by a static array.
// The backing array (Data) must be provided by the caller.
//
// This is used by StaticAllocator for ID management, where the stack
// holds available IDs (int16 values).
//
// Invariants:
// - top == -1 means empty
// - top == capacity-1 means full
// - Valid elements: Data[0..top]
type StaticStack struct {
	Data     []int16  // Backing array (must be provided by caller)
	top      int      // Index of top element (-1 = empty)
	capacity int      // Max capacity (length of Data)
	Lock     Spinlock // Protects all fields for concurrent access
}

// NewStaticStack creates a new stack with the given backing array.
// The stack starts empty (top = -1).
func NewStaticStack(data []int16) *StaticStack {
	return &StaticStack{
		Data:     data,
		top:      -1,
		capacity: len(data),
	}
}

// Push adds a value to the top of the stack.
// Panics if the stack is full.
//
//go:nosplit
func (s *StaticStack) Push(val int16) {
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
func (s *StaticStack) Pop() int16 {
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
func (s *StaticStack) Peek() int16 {
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
func (s *StaticStack) IsEmpty() bool {
	s.Lock.Lock()
	defer s.Lock.Unlock()

	return s.top == -1
}

// IsFull returns true if the stack is at capacity.
//
//go:nosplit
func (s *StaticStack) IsFull() bool {
	s.Lock.Lock()
	defer s.Lock.Unlock()

	return s.top == s.capacity-1
}

// Size returns the number of elements currently in the stack.
//
//go:nosplit
func (s *StaticStack) Size() int {
	s.Lock.Lock()
	defer s.Lock.Unlock()

	return s.top + 1
}

// Capacity returns the maximum number of elements the stack can hold.
//
//go:nosplit
func (s *StaticStack) Capacity() int {
	s.Lock.Lock()
	defer s.Lock.Unlock()

	return s.capacity
}
