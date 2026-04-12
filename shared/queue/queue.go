// Package queue provides a generic thread-safe FIFO queue.
// It is usable by both kernel and userspace code.
package queue

import (
	"sync"

	"mazzy/shared/dlist"
)

// Queue is a thread-safe FIFO queue backed by a doubly-linked list.
// Multiple goroutines may call Enqueue and Dequeue concurrently.
// The Wake channel is signaled (non-blocking) on each Enqueue so that
// a drainer goroutine can use select to wait for items.
type Queue[T any] struct {
	mu   sync.Mutex
	list *dlist.List[T]
	wake chan struct{}
}

// New creates an empty queue.
func New[T any]() *Queue[T] {
	return &Queue[T]{
		list: dlist.New[T](),
		wake: make(chan struct{}, 1),
	}
}

// Enqueue adds an item to the back of the queue and signals Wake.
func (q *Queue[T]) Enqueue(v T) {
	q.mu.Lock()
	q.list.PushBack(v)
	q.mu.Unlock()
	// Non-blocking signal — if the channel already has a pending
	// signal the drainer will see it on its next iteration.
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Dequeue removes and returns the item at the front of the queue.
// Returns the zero value of T and false if the queue is empty.
func (q *Queue[T]) Dequeue() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	front := q.list.Front()
	if front == nil {
		var zero T
		return zero, false
	}
	return q.list.Remove(front), true
}

// Wake returns a channel that receives a signal when an item is enqueued.
// Use this in a select statement to wait for items without polling.
func (q *Queue[T]) Wake() <-chan struct{} {
	return q.wake
}

// Len returns the number of items in the queue.
func (q *Queue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.list.Len()
}

// RemoveWhere removes all items matching the predicate and returns them.
func (q *Queue[T]) RemoveWhere(fn func(T) bool) []T {
	q.mu.Lock()
	defer q.mu.Unlock()
	var removed []T
	q.list.Range(func(n *dlist.Node[T]) bool {
		if fn(n.Value) {
			removed = append(removed, q.list.Remove(n))
		}
		return true
	})
	return removed
}
