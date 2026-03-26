// Package dlist provides a generic doubly-linked list. It is usable by both
// kernel and userspace code (no OS dependencies). The GC manages node lifetime.
package dlist

// Node is an element of the list.
type Node[T any] struct {
	Value T
	next  *Node[T]
	prev  *Node[T]
	list  *List[T]
}

// Next returns the next node, or nil if this is the last node.
func (n *Node[T]) Next() *Node[T] {
	if n.next != nil && n.next != &n.list.sentinel {
		return n.next
	}
	return nil
}

// Prev returns the previous node, or nil if this is the first node.
func (n *Node[T]) Prev() *Node[T] {
	if n.prev != nil && n.prev != &n.list.sentinel {
		return n.prev
	}
	return nil
}

// List is a doubly-linked list. The zero value is not usable; call New.
type List[T any] struct {
	sentinel Node[T] // sentinel.next = head, sentinel.prev = tail
	len      int
}

// New creates an empty list.
func New[T any]() *List[T] {
	l := &List[T]{}
	l.sentinel.next = &l.sentinel
	l.sentinel.prev = &l.sentinel
	l.sentinel.list = l
	return l
}

// Len returns the number of elements.
func (l *List[T]) Len() int { return l.len }

// Front returns the first node, or nil if empty.
func (l *List[T]) Front() *Node[T] {
	if l.len == 0 {
		return nil
	}
	return l.sentinel.next
}

// Back returns the last node, or nil if empty.
func (l *List[T]) Back() *Node[T] {
	if l.len == 0 {
		return nil
	}
	return l.sentinel.prev
}

// insertAfter inserts a new node with value val after at.
func (l *List[T]) insertAfter(val T, at *Node[T]) *Node[T] {
	n := &Node[T]{Value: val, prev: at, next: at.next, list: l}
	at.next.prev = n
	at.next = n
	l.len++
	return n
}

// PushBack adds val to the end of the list and returns the new node.
func (l *List[T]) PushBack(val T) *Node[T] {
	return l.insertAfter(val, l.sentinel.prev)
}

// PushFront adds val to the front of the list and returns the new node.
func (l *List[T]) PushFront(val T) *Node[T] {
	return l.insertAfter(val, &l.sentinel)
}

// Remove removes n from the list and returns its value.
// n must belong to this list; calling Remove on a node from another list
// or a node that has already been removed is undefined.
func (l *List[T]) Remove(n *Node[T]) T {
	n.prev.next = n.next
	n.next.prev = n.prev
	n.next = nil
	n.prev = nil
	n.list = nil
	l.len--
	return n.Value
}

// Range calls fn for each node front-to-back. It is safe to Remove the
// current node inside fn. If fn returns false, iteration stops.
func (l *List[T]) Range(fn func(*Node[T]) bool) {
	for n := l.sentinel.next; n != &l.sentinel; {
		next := n.next // capture before fn might Remove n
		if !fn(n) {
			return
		}
		n = next
	}
}
