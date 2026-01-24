package util

// DLinkedList is a generic doubly-linked list that can hold any type.
// Unlike OrderedList, elements are not sorted - they maintain insertion order.
// This is useful for queues (ready queue, blocked lists) where FIFO order matters.
type DLinkedList[T any] struct {
	head *DNode[T]
	tail *DNode[T]
	size int
}

// DNode is a node in the doubly-linked list.
// The node itself can serve as an ID/handle for the element, enabling O(1) removal.
type DNode[T any] struct {
	Value T
	prev  *DNode[T]
	next  *DNode[T]
	list  *DLinkedList[T] // Back-pointer for O(1) removal and validation
}

// NewDLinkedList creates a new empty doubly-linked list.
func NewDLinkedList[T any]() *DLinkedList[T] {
	return &DLinkedList[T]{}
}

// PushBack adds an element to the end of the list.
// Returns the node (can be used as handle for O(1) removal).
func (l *DLinkedList[T]) PushBack(value T) *DNode[T] {
	node := &DNode[T]{
		Value: value,
		list:  l,
	}

	if l.tail == nil {
		// Empty list
		l.head = node
		l.tail = node
	} else {
		// Add after tail
		node.prev = l.tail
		l.tail.next = node
		l.tail = node
	}

	l.size++
	return node
}

// PushFront adds an element to the front of the list.
// Returns the node (can be used as handle for O(1) removal).
func (l *DLinkedList[T]) PushFront(value T) *DNode[T] {
	node := &DNode[T]{
		Value: value,
		list:  l,
	}

	if l.head == nil {
		// Empty list
		l.head = node
		l.tail = node
	} else {
		// Add before head
		node.next = l.head
		l.head.prev = node
		l.head = node
	}

	l.size++
	return node
}

// Remove removes a node from the list in O(1) time.
// The node must belong to this list (validated via back-pointer).
// After removal, the node's list pointer is cleared.
func (l *DLinkedList[T]) Remove(node *DNode[T]) {
	if node == nil || node.list != l {
		return // Node doesn't belong to this list
	}

	// Update prev pointer
	if node.prev != nil {
		node.prev.next = node.next
	} else {
		// Node was head
		l.head = node.next
	}

	// Update next pointer
	if node.next != nil {
		node.next.prev = node.prev
	} else {
		// Node was tail
		l.tail = node.prev
	}

	// Clear node's pointers
	node.prev = nil
	node.next = nil
	node.list = nil
	l.size--
}

// PopFront removes and returns the first element's value.
// Panics if the list is empty.
func (l *DLinkedList[T]) PopFront() T {
	if l.head == nil {
		panic("DLinkedList.PopFront: list is empty")
	}

	node := l.head
	value := node.Value

	l.head = node.next
	if l.head != nil {
		l.head.prev = nil
	} else {
		// List is now empty
		l.tail = nil
	}

	node.next = nil
	node.list = nil
	l.size--

	return value
}

// PopBack removes and returns the last element's value.
// Panics if the list is empty.
func (l *DLinkedList[T]) PopBack() T {
	if l.tail == nil {
		panic("DLinkedList.PopBack: list is empty")
	}

	node := l.tail
	value := node.Value

	l.tail = node.prev
	if l.tail != nil {
		l.tail.next = nil
	} else {
		// List is now empty
		l.head = nil
	}

	node.prev = nil
	node.list = nil
	l.size--

	return value
}

// Front returns the first node, or nil if empty.
func (l *DLinkedList[T]) Front() *DNode[T] {
	return l.head
}

// Back returns the last node, or nil if empty.
func (l *DLinkedList[T]) Back() *DNode[T] {
	return l.tail
}

// Size returns the number of elements in the list.
func (l *DLinkedList[T]) Size() int {
	return l.size
}

// IsEmpty returns true if the list contains no elements.
func (l *DLinkedList[T]) IsEmpty() bool {
	return l.head == nil
}

// Next returns the next node in the list, or nil if this is the last node.
func (n *DNode[T]) Next() *DNode[T] {
	if n == nil {
		return nil
	}
	return n.next
}

// Prev returns the previous node in the list, or nil if this is the first node.
func (n *DNode[T]) Prev() *DNode[T] {
	if n == nil {
		return nil
	}
	return n.prev
}

// List returns the list this node belongs to, or nil if removed.
func (n *DNode[T]) List() *DLinkedList[T] {
	if n == nil {
		return nil
	}
	return n.list
}
