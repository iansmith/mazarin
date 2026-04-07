// Package stack provides a generic stack backed by a slice. It is usable
// by both kernel and userspace code (no OS dependencies). The GC manages
// the backing slice lifetime. No synchronization — callers handle locking
// if needed.
package stack

// Stack is a LIFO container. The zero value is usable (empty stack).
type Stack[T any] struct {
	items []T
}

// New returns an empty stack.
func New[T any]() *Stack[T] {
	return &Stack[T]{}
}

// Push adds an element to the top of the stack.
func (s *Stack[T]) Push(v T) {
	s.items = append(s.items, v)
}

// Pop removes and returns the top element. Returns the zero value and
// false if the stack is empty.
func (s *Stack[T]) Pop() (T, bool) {
	if len(s.items) == 0 {
		var zero T
		return zero, false
	}
	top := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return top, true
}

// Peek returns the top element without removing it. Returns the zero
// value and false if the stack is empty.
func (s *Stack[T]) Peek() (T, bool) {
	if len(s.items) == 0 {
		var zero T
		return zero, false
	}
	return s.items[len(s.items)-1], true
}

// Len returns the number of elements.
func (s *Stack[T]) Len() int {
	return len(s.items)
}

// IsEmpty returns true if the stack has no elements.
func (s *Stack[T]) IsEmpty() bool {
	return len(s.items) == 0
}
