package ds

import "testing"

// TestStackPushPop tests basic push and pop operations
func TestStackPushPop(t *testing.T) {
	data := make([]int16, 5)
	var s StaticStack[int16]; s.Init(data)

	// Initially empty
	if !s.IsEmpty() {
		t.Error("new stack should be empty")
	}
	if s.Size() != 0 {
		t.Errorf("new stack size should be 0, got %d", s.Size())
	}

	// Push values
	s.Push(10)
	if s.IsEmpty() {
		t.Error("stack should not be empty after push")
	}
	if s.Size() != 1 {
		t.Errorf("size should be 1, got %d", s.Size())
	}

	s.Push(20)
	s.Push(30)
	if s.Size() != 3 {
		t.Errorf("size should be 3, got %d", s.Size())
	}

	// Pop values (LIFO order)
	val := s.Pop()
	if val != 30 {
		t.Errorf("expected 30, got %d", val)
	}

	val = s.Pop()
	if val != 20 {
		t.Errorf("expected 20, got %d", val)
	}

	val = s.Pop()
	if val != 10 {
		t.Errorf("expected 10, got %d", val)
	}

	// Should be empty now
	if !s.IsEmpty() {
		t.Error("stack should be empty after popping all")
	}
}

// TestStackCapacity tests capacity limits
func TestStackCapacity(t *testing.T) {
	data := make([]int16, 3)
	var s StaticStack[int16]; s.Init(data)

	if s.Capacity() != 3 {
		t.Errorf("capacity should be 3, got %d", s.Capacity())
	}

	// Fill to capacity
	s.Push(1)
	s.Push(2)
	s.Push(3)

	if !s.IsFull() {
		t.Error("stack should be full")
	}
	if s.Size() != s.Capacity() {
		t.Errorf("size %d should equal capacity %d", s.Size(), s.Capacity())
	}
}

// TestStackPanicOnEmpty tests that Pop and Peek panic when empty
func TestStackPanicOnEmpty(t *testing.T) {
	data := make([]int16, 5)
	var s StaticStack[int16]; s.Init(data)

	// Test Pop on empty
	defer func() {
		if r := recover(); r == nil {
			t.Error("Pop on empty stack should panic")
		}
	}()
	s.Pop()
}

// TestStackPanicOnEmptyPeek tests that Peek panics when empty
func TestStackPanicOnEmptyPeek(t *testing.T) {
	data := make([]int16, 5)
	var s StaticStack[int16]; s.Init(data)

	defer func() {
		if r := recover(); r == nil {
			t.Error("Peek on empty stack should panic")
		}
	}()
	s.Peek()
}

// TestStackPanicOnFull tests that Push panics when full
func TestStackPanicOnFull(t *testing.T) {
	data := make([]int16, 2)
	var s StaticStack[int16]; s.Init(data)

	s.Push(1)
	s.Push(2)

	defer func() {
		if r := recover(); r == nil {
			t.Error("Push on full stack should panic")
		}
	}()
	s.Push(3)
}

// TestStackPeek tests Peek without removing
func TestStackPeek(t *testing.T) {
	data := make([]int16, 5)
	var s StaticStack[int16]; s.Init(data)

	s.Push(42)
	s.Push(99)

	// Peek should return top without removing
	val := s.Peek()
	if val != 99 {
		t.Errorf("Peek should return 99, got %d", val)
	}

	// Size should not change
	if s.Size() != 2 {
		t.Errorf("Size should still be 2 after Peek, got %d", s.Size())
	}

	// Peek again - should still be 99
	val = s.Peek()
	if val != 99 {
		t.Errorf("Peek should still return 99, got %d", val)
	}

	// Now pop - should get 99
	val = s.Pop()
	if val != 99 {
		t.Errorf("Pop should return 99, got %d", val)
	}

	// Peek now should return 42
	val = s.Peek()
	if val != 42 {
		t.Errorf("Peek should now return 42, got %d", val)
	}
}

// TestStackMultipleCycles tests push/pop cycles
func TestStackMultipleCycles(t *testing.T) {
	data := make([]int16, 4)
	var s StaticStack[int16]; s.Init(data)

	// Cycle 1
	s.Push(1)
	s.Push(2)
	if s.Pop() != 2 || s.Pop() != 1 {
		t.Error("Cycle 1 failed")
	}

	// Cycle 2
	s.Push(10)
	s.Push(20)
	s.Push(30)
	if s.Pop() != 30 || s.Pop() != 20 || s.Pop() != 10 {
		t.Error("Cycle 2 failed")
	}

	// Cycle 3 - fill to capacity
	s.Push(100)
	s.Push(200)
	s.Push(300)
	s.Push(400)
	if !s.IsFull() {
		t.Error("Stack should be full")
	}
	if s.Pop() != 400 || s.Pop() != 300 || s.Pop() != 200 || s.Pop() != 100 {
		t.Error("Cycle 3 failed")
	}
	if !s.IsEmpty() {
		t.Error("Stack should be empty")
	}
}

// TestStackZeroValues tests handling of zero values
func TestStackZeroValues(t *testing.T) {
	data := make([]int16, 3)
	var s StaticStack[int16]; s.Init(data)

	s.Push(0)
	s.Push(-1)
	s.Push(0)

	if s.Pop() != 0 {
		t.Error("Should pop 0")
	}
	if s.Pop() != -1 {
		t.Error("Should pop -1")
	}
	if s.Pop() != 0 {
		t.Error("Should pop 0")
	}
}
