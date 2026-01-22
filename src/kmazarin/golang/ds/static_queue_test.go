package ds

import "testing"

// TestQueuePushPop tests basic push and pop operations
func TestQueuePushPop(t *testing.T) {
	data := make([]int16, 5)
	inUse := make([]bool, 5)
	q := StaticQueue[int16]{Data: data, InUse: inUse}

	// Initially empty
	if !q.IsEmpty() {
		t.Error("new queue should be empty")
	}
	if q.Size() != 0 {
		t.Errorf("new queue size should be 0, got %d", q.Size())
	}

	// Push values
	q.Push(10)
	if q.IsEmpty() {
		t.Error("queue should not be empty after push")
	}
	if q.Size() != 1 {
		t.Errorf("size should be 1, got %d", q.Size())
	}

	q.Push(20)
	q.Push(30)
	if q.Size() != 3 {
		t.Errorf("size should be 3, got %d", q.Size())
	}

	// Pop values (FIFO order)
	val := q.Pop()
	if val != 10 {
		t.Errorf("expected 10, got %d", val)
	}

	val = q.Pop()
	if val != 20 {
		t.Errorf("expected 20, got %d", val)
	}

	val = q.Pop()
	if val != 30 {
		t.Errorf("expected 30, got %d", val)
	}

	// Should be empty now
	if !q.IsEmpty() {
		t.Error("queue should be empty after popping all")
	}
}

// TestQueuePushHead tests PushHead for priority insertion
func TestQueuePushHead(t *testing.T) {
	data := make([]int16, 5)
	inUse := make([]bool, 5)
	q := StaticQueue[int16]{Data: data, InUse: inUse}

	// Push some values normally
	q.Push(10)
	q.Push(20)
	q.Push(30)

	// PushHead should insert at front
	q.PushHead(99)
	if q.Size() != 4 {
		t.Errorf("size should be 4, got %d", q.Size())
	}

	// Pop should return the priority value first
	val := q.Pop()
	if val != 99 {
		t.Errorf("PushHead value should be first, expected 99, got %d", val)
	}

	// Then the original order
	val = q.Pop()
	if val != 10 {
		t.Errorf("expected 10, got %d", val)
	}
	val = q.Pop()
	if val != 20 {
		t.Errorf("expected 20, got %d", val)
	}
	val = q.Pop()
	if val != 30 {
		t.Errorf("expected 30, got %d", val)
	}
}

// TestQueuePushHeadEmpty tests PushHead on empty queue
func TestQueuePushHeadEmpty(t *testing.T) {
	data := make([]int16, 5)
	inUse := make([]bool, 5)
	q := StaticQueue[int16]{Data: data, InUse: inUse}

	// PushHead on empty queue
	q.PushHead(42)
	if q.Size() != 1 {
		t.Errorf("size should be 1, got %d", q.Size())
	}

	val := q.Pop()
	if val != 42 {
		t.Errorf("expected 42, got %d", val)
	}
}

// TestQueuePushHeadMultiple tests multiple PushHead operations
func TestQueuePushHeadMultiple(t *testing.T) {
	data := make([]int16, 5)
	inUse := make([]bool, 5)
	q := StaticQueue[int16]{Data: data, InUse: inUse}

	// Push normally
	q.Push(100)

	// Multiple PushHead operations
	q.PushHead(3)
	q.PushHead(2)
	q.PushHead(1)

	// Should pop in order: 1, 2, 3, 100
	vals := []int16{1, 2, 3, 100}
	for _, expected := range vals {
		val := q.Pop()
		if val != expected {
			t.Errorf("expected %d, got %d", expected, val)
		}
	}
}

// TestQueuePushHeadPanicOnFull tests that PushHead panics when full
func TestQueuePushHeadPanicOnFull(t *testing.T) {
	data := make([]int16, 3)
	inUse := make([]bool, 3)
	q := StaticQueue[int16]{Data: data, InUse: inUse}

	q.Push(1)
	q.Push(2)
	q.Push(3)

	defer func() {
		if r := recover(); r == nil {
			t.Error("PushHead on full queue should panic")
		}
	}()
	q.PushHead(99)
}

// TestQueuePluck tests removing elements from the middle
func TestQueuePluck(t *testing.T) {
	data := make([]int16, 5)
	inUse := make([]bool, 5)
	q := StaticQueue[int16]{Data: data, InUse: inUse}

	q.Push(10)
	q.Push(20)
	q.Push(30)

	// Pluck middle element
	if !q.Pluck(20) {
		t.Error("Pluck should return true for existing element")
	}
	if q.Size() != 2 {
		t.Errorf("size should be 2, got %d", q.Size())
	}

	// Pop should skip the hole
	val := q.Pop()
	if val != 10 {
		t.Errorf("expected 10, got %d", val)
	}
	val = q.Pop()
	if val != 30 {
		t.Errorf("expected 30, got %d", val)
	}
}

// TestQueuePluckNotFound tests plucking non-existent element
func TestQueuePluckNotFound(t *testing.T) {
	data := make([]int16, 5)
	inUse := make([]bool, 5)
	q := StaticQueue[int16]{Data: data, InUse: inUse}

	q.Push(10)
	q.Push(20)

	if q.Pluck(99) {
		t.Error("Pluck should return false for non-existent element")
	}
	if q.Size() != 2 {
		t.Errorf("size should still be 2, got %d", q.Size())
	}
}

// TestQueueClear tests clearing the queue
func TestQueueClear(t *testing.T) {
	data := make([]int16, 5)
	inUse := make([]bool, 5)
	q := StaticQueue[int16]{Data: data, InUse: inUse}

	q.Push(10)
	q.Push(20)
	q.Push(30)

	q.Clear()
	if !q.IsEmpty() {
		t.Error("queue should be empty after Clear")
	}
	if q.Size() != 0 {
		t.Errorf("size should be 0, got %d", q.Size())
	}
}

// TestQueuePushHeadAfterPluck tests PushHead reuses holes
func TestQueuePushHeadAfterPluck(t *testing.T) {
	data := make([]int16, 4)
	inUse := make([]bool, 4)
	q := StaticQueue[int16]{Data: data, InUse: inUse}

	// Fill queue
	q.Push(1)
	q.Push(2)
	q.Push(3)
	q.Push(4)

	// Pluck one element (creates a hole)
	q.Pluck(2)

	// PushHead should succeed (uses the hole)
	q.PushHead(99)
	if q.Size() != 4 {
		t.Errorf("size should be 4, got %d", q.Size())
	}

	// Pop order should be: 99, 1, 3, 4
	expected := []int16{99, 1, 3, 4}
	for _, exp := range expected {
		val := q.Pop()
		if val != exp {
			t.Errorf("expected %d, got %d", exp, val)
		}
	}
}

// TestQueuePanicOnPopEmpty tests that Pop panics when empty
func TestQueuePanicOnPopEmpty(t *testing.T) {
	data := make([]int16, 5)
	inUse := make([]bool, 5)
	q := StaticQueue[int16]{Data: data, InUse: inUse}

	defer func() {
		if r := recover(); r == nil {
			t.Error("Pop on empty queue should panic")
		}
	}()
	q.Pop()
}

// TestQueuePanicOnPushFull tests that Push panics when full
func TestQueuePanicOnPushFull(t *testing.T) {
	data := make([]int16, 2)
	inUse := make([]bool, 2)
	q := StaticQueue[int16]{Data: data, InUse: inUse}

	q.Push(1)
	q.Push(2)

	defer func() {
		if r := recover(); r == nil {
			t.Error("Push on full queue should panic")
		}
	}()
	q.Push(3)
}
