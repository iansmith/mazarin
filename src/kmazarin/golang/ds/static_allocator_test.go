package ds

import "testing"

// TestAllocatorInit verifies that Init() seeds IDs 0..max-1 in correct order
func TestAllocatorInit(t *testing.T) {
	data := make([]int16, 5)
	a := NewStaticAllocator(5)
	a.stack.Data = data
	a.Init()

	// Should have all 5 IDs available
	if a.Available() != 5 {
		t.Errorf("expected 5 IDs available, got %d", a.Available())
	}

	// Should allocate in order: 0, 1, 2, 3, 4
	for expected := int16(0); expected < 5; expected++ {
		id := a.Acquire()
		if id != expected {
			t.Errorf("expected ID %d, got %d", expected, id)
		}
	}

	// Should be exhausted now
	if a.Available() != 0 {
		t.Errorf("expected 0 IDs available, got %d", a.Available())
	}
}

// TestAllocatorAcquireRelease tests basic acquire/release cycle
func TestAllocatorAcquireRelease(t *testing.T) {
	data := make([]int16, 3)
	a := NewStaticAllocator(3)
	a.stack.Data = data
	a.Init()

	// Acquire all IDs
	id0 := a.Acquire()
	id1 := a.Acquire()
	id2 := a.Acquire()

	if id0 != 0 || id1 != 1 || id2 != 2 {
		t.Errorf("expected IDs 0,1,2, got %d,%d,%d", id0, id1, id2)
	}

	// Release one
	a.Release(id1) // Release 1

	// Should be able to acquire again
	id := a.Acquire()
	if id != 1 {
		t.Errorf("expected to get back ID 1, got %d", id)
	}
}

// TestAllocatorExhaustion tests that Acquire panics when exhausted
func TestAllocatorExhaustion(t *testing.T) {
	data := make([]int16, 2)
	a := NewStaticAllocator(2)
	a.stack.Data = data
	a.Init()

	// Acquire all
	a.Acquire()
	a.Acquire()

	// Next acquire should panic
	defer func() {
		if r := recover(); r == nil {
			t.Error("Acquire on exhausted allocator should panic")
		}
	}()
	a.Acquire()
}

// TestAllocatorDoubleRelease tests releasing more than capacity
func TestAllocatorDoubleRelease(t *testing.T) {
	data := make([]int16, 2)
	a := NewStaticAllocator(2)
	a.stack.Data = data
	a.Init()

	id0 := a.Acquire()
	id1 := a.Acquire()

	// Release both
	a.Release(id0)
	a.Release(id1)

	// Trying to release one more should panic (stack full)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Release when full should panic")
		}
	}()
	a.Release(99) // Doesn't matter what ID, stack is full
}

// TestAllocatorIdUniqueness verifies no duplicate IDs are allocated
func TestAllocatorIdUniqueness(t *testing.T) {
	data := make([]int16, 10)
	a := NewStaticAllocator(10)
	a.stack.Data = data
	a.Init()

	// Track allocated IDs
	allocated := make(map[int16]bool)

	// Acquire all IDs
	for i := 0; i < 10; i++ {
		id := a.Acquire()
		if allocated[id] {
			t.Errorf("duplicate ID %d allocated", id)
		}
		allocated[id] = true
	}

	// Should have IDs 0-9
	for i := int16(0); i < 10; i++ {
		if !allocated[i] {
			t.Errorf("ID %d was not allocated", i)
		}
	}
}

// TestAllocatorStartsAtZero verifies first ID is 0
func TestAllocatorStartsAtZero(t *testing.T) {
	data := make([]int16, 5)
	a := NewStaticAllocator(5)
	a.stack.Data = data
	a.Init()

	id := a.Acquire()
	if id != 0 {
		t.Errorf("first ID should be 0, got %d", id)
	}
}

// TestAllocatorReleaseOrder tests LIFO behavior after releases
func TestAllocatorReleaseOrder(t *testing.T) {
	data := make([]int16, 5)
	a := NewStaticAllocator(5)
	a.stack.Data = data
	a.Init()

	// Acquire IDs 0, 1, 2
	id0 := a.Acquire()
	id1 := a.Acquire()
	id2 := a.Acquire()

	if id0 != 0 || id1 != 1 || id2 != 2 {
		t.Fatalf("expected 0,1,2, got %d,%d,%d", id0, id1, id2)
	}

	// Release in order: 1, 0, 2
	a.Release(id1) // Release 1
	a.Release(id0) // Release 0
	a.Release(id2) // Release 2

	// Acquire should return in LIFO order: 2, 0, 1
	if a.Acquire() != 2 {
		t.Error("expected 2 (last released)")
	}
	if a.Acquire() != 0 {
		t.Error("expected 0")
	}
	if a.Acquire() != 1 {
		t.Error("expected 1 (first released)")
	}
}

// TestAllocatorCapacity tests Capacity() method
func TestAllocatorCapacity(t *testing.T) {
	data := make([]int16, 7)
	a := NewStaticAllocator(7)
	a.stack.Data = data
	a.Init()

	if a.Capacity() != 7 {
		t.Errorf("expected capacity 7, got %d", a.Capacity())
	}

	// Capacity should not change after allocations
	a.Acquire()
	a.Acquire()
	if a.Capacity() != 7 {
		t.Errorf("capacity should remain 7, got %d", a.Capacity())
	}
}

// TestAllocatorAvailable tests Available() method
func TestAllocatorAvailable(t *testing.T) {
	data := make([]int16, 4)
	a := NewStaticAllocator(4)
	a.stack.Data = data
	a.Init()

	if a.Available() != 4 {
		t.Errorf("initially should have 4 available, got %d", a.Available())
	}

	a.Acquire()
	if a.Available() != 3 {
		t.Errorf("after 1 acquire, should have 3 available, got %d", a.Available())
	}

	a.Acquire()
	a.Acquire()
	if a.Available() != 1 {
		t.Errorf("after 3 acquires, should have 1 available, got %d", a.Available())
	}

	id := a.Acquire()
	if a.Available() != 0 {
		t.Errorf("after all acquired, should have 0 available, got %d", a.Available())
	}

	a.Release(id)
	if a.Available() != 1 {
		t.Errorf("after 1 release, should have 1 available, got %d", a.Available())
	}
}

// TestAllocatorInitWithNilData tests that Init panics with nil data
func TestAllocatorInitWithNilData(t *testing.T) {
	a := NewStaticAllocator(5)
	// Don't set stack.Data

	defer func() {
		if r := recover(); r == nil {
			t.Error("Init with nil Data should panic")
		}
	}()
	a.Init()
}

// TestAllocatorInitWithInsufficientCapacity tests Init panic with small array
func TestAllocatorInitWithInsufficientCapacity(t *testing.T) {
	data := make([]int16, 2)
	a := NewStaticAllocator(5) // Need 5, but only provide 2
	a.stack.Data = data

	defer func() {
		if r := recover(); r == nil {
			t.Error("Init with insufficient capacity should panic")
		}
	}()
	a.Init()
}

// TestAllocatorMultipleCycles tests repeated acquire/release cycles
func TestAllocatorMultipleCycles(t *testing.T) {
	data := make([]int16, 3)
	a := NewStaticAllocator(3)
	a.stack.Data = data
	a.Init()

	// Cycle 1: acquire all, release all
	ids1 := []int16{a.Acquire(), a.Acquire(), a.Acquire()}
	for _, id := range ids1 {
		a.Release(id)
	}

	// Cycle 2: acquire all again
	ids2 := []int16{a.Acquire(), a.Acquire(), a.Acquire()}

	// Should have gotten all IDs back (possibly in different order due to LIFO)
	idMap := make(map[int16]bool)
	for _, id := range ids2 {
		idMap[id] = true
	}
	if len(idMap) != 3 {
		t.Error("should have 3 unique IDs")
	}
	for i := int16(0); i < 3; i++ {
		if !idMap[i] {
			t.Errorf("ID %d not allocated in cycle 2", i)
		}
	}
}
