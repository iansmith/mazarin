package ds

import (
	"testing"

	"kmazarin/util"
)

// testItem implements util.OrderedByer for testing
type testItem struct {
	id    int
	order uint64
}

func (t testItem) OrderedBy() uint64 {
	return t.order
}

// Compile-time assertion that testItem implements util.OrderedByer
var _ util.OrderedByer = testItem{}

func TestNewOrderedList(t *testing.T) {
	listAsc := NewOrderedList[testItem](true)
	if listAsc == nil {
		t.Fatal("NewOrderedList returned nil")
	}
	if !listAsc.ascending {
		t.Error("Expected ascending=true")
	}

	listDesc := NewOrderedList[testItem](false)
	if listDesc.ascending {
		t.Error("Expected ascending=false")
	}
}

func TestIsEmpty(t *testing.T) {
	list := NewOrderedList[testItem](true)
	if !list.IsEmpty() {
		t.Error("New list should be empty")
	}

	list.Insert(testItem{id: 1, order: 100})
	if list.IsEmpty() {
		t.Error("List with one element should not be empty")
	}
}

func TestInsertAscending(t *testing.T) {
	list := NewOrderedList[testItem](true)

	// Insert in random order
	list.Insert(testItem{id: 3, order: 300})
	list.Insert(testItem{id: 1, order: 100})
	list.Insert(testItem{id: 5, order: 500})
	list.Insert(testItem{id: 2, order: 200})
	list.Insert(testItem{id: 4, order: 400})

	// Should be ordered: 100, 200, 300, 400, 500
	expected := []uint64{100, 200, 300, 400, 500}
	for i, exp := range expected {
		if list.IsEmpty() {
			t.Fatalf("List empty at position %d", i)
		}
		got := list.Pop().OrderedBy()
		if got != exp {
			t.Errorf("Position %d: expected %d, got %d", i, exp, got)
		}
	}

	if !list.IsEmpty() {
		t.Error("List should be empty after popping all elements")
	}
}

func TestInsertDescending(t *testing.T) {
	list := NewOrderedList[testItem](false)

	// Insert in random order
	list.Insert(testItem{id: 3, order: 300})
	list.Insert(testItem{id: 1, order: 100})
	list.Insert(testItem{id: 5, order: 500})
	list.Insert(testItem{id: 2, order: 200})
	list.Insert(testItem{id: 4, order: 400})

	// Should be ordered: 500, 400, 300, 200, 100
	expected := []uint64{500, 400, 300, 200, 100}
	for i, exp := range expected {
		if list.IsEmpty() {
			t.Fatalf("List empty at position %d", i)
		}
		got := list.Pop().OrderedBy()
		if got != exp {
			t.Errorf("Position %d: expected %d, got %d", i, exp, got)
		}
	}

	if !list.IsEmpty() {
		t.Error("List should be empty after popping all elements")
	}
}

func TestPeek(t *testing.T) {
	list := NewOrderedList[testItem](true)

	list.Insert(testItem{id: 2, order: 200})
	list.Insert(testItem{id: 1, order: 100})
	list.Insert(testItem{id: 3, order: 300})

	// Peek should return 100 (smallest in ascending list)
	if got := list.Peek(); got != 100 {
		t.Errorf("Peek: expected 100, got %d", got)
	}

	// Peek should not remove the element
	if list.Size() != 3 {
		t.Errorf("Size after Peek: expected 3, got %d", list.Size())
	}

	// Verify first element is still 100
	if got := list.Pop().OrderedBy(); got != 100 {
		t.Errorf("Pop after Peek: expected 100, got %d", got)
	}
}

func TestPeekPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Peek on empty list should panic")
		}
	}()

	list := NewOrderedList[testItem](true)
	list.Peek() // Should panic
}

func TestPopPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Pop on empty list should panic")
		}
	}()

	list := NewOrderedList[testItem](true)
	list.Pop() // Should panic
}

func TestSize(t *testing.T) {
	list := NewOrderedList[testItem](true)

	if list.Size() != 0 {
		t.Errorf("Empty list size: expected 0, got %d", list.Size())
	}

	list.Insert(testItem{id: 1, order: 100})
	if list.Size() != 1 {
		t.Errorf("Size after 1 insert: expected 1, got %d", list.Size())
	}

	list.Insert(testItem{id: 2, order: 200})
	list.Insert(testItem{id: 3, order: 300})
	if list.Size() != 3 {
		t.Errorf("Size after 3 inserts: expected 3, got %d", list.Size())
	}

	list.Pop()
	if list.Size() != 2 {
		t.Errorf("Size after 1 pop: expected 2, got %d", list.Size())
	}

	list.Pop()
	list.Pop()
	if list.Size() != 0 {
		t.Errorf("Size after all pops: expected 0, got %d", list.Size())
	}
}

func TestDuplicateOrders(t *testing.T) {
	list := NewOrderedList[testItem](true)

	// Insert items with duplicate order values
	list.Insert(testItem{id: 1, order: 100})
	list.Insert(testItem{id: 2, order: 200})
	list.Insert(testItem{id: 3, order: 100}) // Duplicate
	list.Insert(testItem{id: 4, order: 200}) // Duplicate

	if list.Size() != 4 {
		t.Errorf("Expected size 4, got %d", list.Size())
	}

	// All items with order 100 should come before items with order 200
	first := list.Pop().OrderedBy()
	if first != 100 {
		t.Errorf("First: expected 100, got %d", first)
	}

	second := list.Pop().OrderedBy()
	if second != 100 {
		t.Errorf("Second: expected 100, got %d", second)
	}

	third := list.Pop().OrderedBy()
	if third != 200 {
		t.Errorf("Third: expected 200, got %d", third)
	}

	fourth := list.Pop().OrderedBy()
	if fourth != 200 {
		t.Errorf("Fourth: expected 200, got %d", fourth)
	}
}

func TestSingleElement(t *testing.T) {
	list := NewOrderedList[testItem](true)

	list.Insert(testItem{id: 1, order: 100})

	if list.IsEmpty() {
		t.Error("List should not be empty after insert")
	}

	if list.Size() != 1 {
		t.Errorf("Size: expected 1, got %d", list.Size())
	}

	if got := list.Peek(); got != 100 {
		t.Errorf("Peek: expected 100, got %d", got)
	}

	item := list.Pop()
	if item.OrderedBy() != 100 {
		t.Errorf("Pop: expected 100, got %d", item.OrderedBy())
	}

	if !list.IsEmpty() {
		t.Error("List should be empty after popping single element")
	}
}
