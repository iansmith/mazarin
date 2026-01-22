//go:build arm64

package ds

import "testing"

// TestStaticOrderedListInsert tests basic insert and size operations
func TestStaticOrderedListInsert(t *testing.T) {
	var dataArray [10]int16
	var orderByArray [10]uint64
	list := StaticOrderedList{
		data:    dataArray[:],
		orderBy: orderByArray[:],
	}

	// Insert in random order
	list.Insert(5, 500)
	list.Insert(2, 200)
	list.Insert(7, 700)
	list.Insert(1, 100)

	if list.Size() != 4 {
		t.Errorf("Expected size 4, got %d", list.Size())
	}

	if list.IsEmpty() {
		t.Error("List should not be empty")
	}
}

// TestStaticOrderedListPopIfLess tests deadline expiration logic
func TestStaticOrderedListPopIfLess(t *testing.T) {
	var dataArray [10]int16
	var orderByArray [10]uint64
	list := StaticOrderedList{
		data:    dataArray[:],
		orderBy: orderByArray[:],
	}

	// Insert threads with different deadlines
	list.Insert(10, 1000) // tid=10, deadline=1000
	list.Insert(20, 2000) // tid=20, deadline=2000
	list.Insert(30, 3000) // tid=30, deadline=3000
	list.Insert(15, 1500) // tid=15, deadline=1500

	// Current time = 1200 (only tid=10 should be expired)
	tid, deadline := list.PopIfLess(1200)
	if tid != 10 {
		t.Errorf("Expected tid=10, got tid=%d", tid)
	}
	if deadline != 1000 {
		t.Errorf("Expected deadline=1000, got %d", deadline)
	}
	if list.Size() != 3 {
		t.Errorf("Expected size 3 after pop, got %d", list.Size())
	}

	// Current time = 1200 again (tid=15 deadline=1500 not yet expired)
	tid, deadline = list.PopIfLess(1200)
	if tid != -1 {
		t.Errorf("Expected tid=-1 (no expired), got tid=%d", tid)
	}
	if deadline != 0 {
		t.Errorf("Expected deadline=0, got %d", deadline)
	}

	// Current time = 2500 (tid=15 and tid=20 should be expired)
	tid, deadline = list.PopIfLess(2500)
	if tid != 15 {
		t.Errorf("Expected tid=15, got tid=%d", tid)
	}
	if deadline != 1500 {
		t.Errorf("Expected deadline=1500, got %d", deadline)
	}

	tid, deadline = list.PopIfLess(2500)
	if tid != 20 {
		t.Errorf("Expected tid=20, got tid=%d", tid)
	}
	if deadline != 2000 {
		t.Errorf("Expected deadline=2000, got %d", deadline)
	}

	// One item left (tid=30, deadline=3000)
	if list.Size() != 1 {
		t.Errorf("Expected size 1, got %d", list.Size())
	}
}

// TestStaticOrderedListSortOrder tests that insertions maintain sort order
func TestStaticOrderedListSortOrder(t *testing.T) {
	var dataArray [10]int16
	var orderByArray [10]uint64
	list := StaticOrderedList{
		data:    dataArray[:],
		orderBy: orderByArray[:],
	}

	// Insert in descending order
	list.Insert(9, 900)
	list.Insert(8, 800)
	list.Insert(7, 700)
	list.Insert(6, 600)
	list.Insert(5, 500)

	// Pop them - should come out in ascending deadline order
	expected := []struct {
		tid      int16
		deadline uint64
	}{
		{5, 500},
		{6, 600},
		{7, 700},
		{8, 800},
		{9, 900},
	}

	for _, exp := range expected {
		tid, deadline := list.PopIfLess(10000) // High current time
		if tid != exp.tid {
			t.Errorf("Expected tid=%d, got tid=%d", exp.tid, tid)
		}
		if deadline != exp.deadline {
			t.Errorf("Expected deadline=%d, got %d", exp.deadline, deadline)
		}
	}

	if !list.IsEmpty() {
		t.Error("List should be empty after popping all elements")
	}
}

// TestStaticOrderedListFull tests panic on overflow
func TestStaticOrderedListFull(t *testing.T) {
	var dataArray [3]int16
	var orderByArray [3]uint64
	list := StaticOrderedList{
		data:    dataArray[:],
		orderBy: orderByArray[:],
	}

	list.Insert(1, 100)
	list.Insert(2, 200)
	list.Insert(3, 300)

	// Should panic on 4th insert
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when list is full")
		}
	}()

	list.Insert(4, 400) // Should panic
}

// TestStaticOrderedListEmpty tests panic on empty pop
func TestStaticOrderedListEmpty(t *testing.T) {
	var dataArray [3]int16
	var orderByArray [3]uint64
	list := StaticOrderedList{
		data:    dataArray[:],
		orderBy: orderByArray[:],
	}

	// Should panic when popping from empty list
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when popping from empty list")
		}
	}()

	list.PopIfLess(1000) // Should panic
}

// TestStaticOrderedListDuplicateDeadlines tests handling of duplicate deadline values
func TestStaticOrderedListDuplicateDeadlines(t *testing.T) {
	var dataArray [10]int16
	var orderByArray [10]uint64
	list := StaticOrderedList{
		data:    dataArray[:],
		orderBy: orderByArray[:],
	}

	// Insert multiple threads with same deadline
	list.Insert(10, 1000)
	list.Insert(20, 1000)
	list.Insert(30, 1000)
	list.Insert(40, 2000)

	// Pop all with deadline <= 1000
	currentTime := uint64(1000)
	count := 0
	for {
		tid, deadline := list.PopIfLess(currentTime)
		if tid == -1 {
			break
		}
		count++
		if deadline != 1000 {
			t.Errorf("Expected deadline=1000, got %d", deadline)
		}
	}

	if count != 3 {
		t.Errorf("Expected to pop 3 threads with deadline=1000, got %d", count)
	}

	if list.Size() != 1 {
		t.Errorf("Expected 1 thread remaining, got %d", list.Size())
	}
}
