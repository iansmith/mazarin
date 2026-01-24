package ds

import "mazzy/kmazarin/util"

// OrderedList is a doubly linked list that maintains elements in sorted order
// Elements are ordered by their OrderedBy() value, either ascending or descending
type OrderedList[T util.OrderedByer] struct {
	head      *node[T]
	tail      *node[T]
	size      int
	ascending bool // true for ascending (smallest first), false for descending (largest first)
}

// node is an internal node in the doubly linked list
type node[T util.OrderedByer] struct {
	value T
	prev  *node[T]
	next  *node[T]
}

// NewOrderedList creates a new ordered list
// ascending: true for ascending order (smallest OrderedBy() first)
//           false for descending order (largest OrderedBy() first)
func NewOrderedList[T util.OrderedByer](ascending bool) *OrderedList[T] {
	return &OrderedList[T]{
		ascending: ascending,
	}
}

// Insert adds an element to the list, maintaining sorted order
// Dispatches to the appropriate internal insertion method based on order
func (l *OrderedList[T]) Insert(value T) {
	if l.ascending {
		l.insertAscending(value)
	} else {
		l.insertDescending(value)
	}
}

// insertAscending inserts a value maintaining ascending order (smallest first)
func (l *OrderedList[T]) insertAscending(value T) {
	newNode := &node[T]{value: value}

	// Empty list - this is the first element
	if l.head == nil {
		l.head = newNode
		l.tail = newNode
		l.size++
		return
	}

	// Find insertion point - insert before first node with larger OrderedBy()
	orderBy := value.OrderedBy()
	current := l.head

	for current != nil {
		if orderBy < current.value.OrderedBy() {
			// Insert before current node
			newNode.next = current
			newNode.prev = current.prev

			if current.prev != nil {
				current.prev.next = newNode
			} else {
				// Inserting at head
				l.head = newNode
			}
			current.prev = newNode

			l.size++
			return
		}

		current = current.next
	}

	// Reached end of list - insert at tail
	newNode.prev = l.tail
	l.tail.next = newNode
	l.tail = newNode
	l.size++
}

// insertDescending inserts a value maintaining descending order (largest first)
func (l *OrderedList[T]) insertDescending(value T) {
	newNode := &node[T]{value: value}

	// Empty list - this is the first element
	if l.head == nil {
		l.head = newNode
		l.tail = newNode
		l.size++
		return
	}

	// Find insertion point - insert before first node with smaller OrderedBy()
	orderBy := value.OrderedBy()
	current := l.head

	for current != nil {
		if orderBy > current.value.OrderedBy() {
			// Insert before current node
			newNode.next = current
			newNode.prev = current.prev

			if current.prev != nil {
				current.prev.next = newNode
			} else {
				// Inserting at head
				l.head = newNode
			}
			current.prev = newNode

			l.size++
			return
		}

		current = current.next
	}

	// Reached end of list - insert at tail
	newNode.prev = l.tail
	l.tail.next = newNode
	l.tail = newNode
	l.size++
}

// Peek returns the OrderedBy() value of the first element without removing it
// Panics if the list is empty
func (l *OrderedList[T]) Peek() uint64 {
	if l.head == nil {
		panic("OrderedList.Peek: list is empty")
	}
	return l.head.value.OrderedBy()
}

// IsEmpty returns true if the list contains no elements
func (l *OrderedList[T]) IsEmpty() bool {
	return l.head == nil
}

// Pop removes and returns the first element from the list
// The first element has the smallest OrderedBy() value (ascending)
// or largest OrderedBy() value (descending)
// Panics if the list is empty
func (l *OrderedList[T]) Pop() T {
	if l.head == nil {
		panic("OrderedList.Pop: list is empty")
	}

	value := l.head.value

	if l.head == l.tail {
		// Only one element in the list
		l.head = nil
		l.tail = nil
	} else {
		// Move head forward
		l.head = l.head.next
		l.head.prev = nil
	}

	l.size--
	return value
}

// Size returns the number of elements in the list
func (l *OrderedList[T]) Size() int {
	return l.size
}
