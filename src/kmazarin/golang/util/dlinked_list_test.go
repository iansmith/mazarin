package util

import "testing"

func TestNewDLinkedList(t *testing.T) {
	list := NewDLinkedList[int]()
	if list == nil {
		t.Fatal("NewDLinkedList returned nil")
	}
	if !list.IsEmpty() {
		t.Error("new list should be empty")
	}
	if list.Size() != 0 {
		t.Errorf("new list size should be 0, got %d", list.Size())
	}
	if list.Front() != nil {
		t.Error("new list Front() should be nil")
	}
	if list.Back() != nil {
		t.Error("new list Back() should be nil")
	}
}

func TestPushBack(t *testing.T) {
	list := NewDLinkedList[int]()

	node1 := list.PushBack(1)
	if list.Size() != 1 {
		t.Errorf("expected size 1, got %d", list.Size())
	}
	if list.Front() != node1 {
		t.Error("Front() should return first node")
	}
	if list.Back() != node1 {
		t.Error("Back() should return first node (only element)")
	}
	if node1.Value != 1 {
		t.Errorf("expected value 1, got %d", node1.Value)
	}

	node2 := list.PushBack(2)
	if list.Size() != 2 {
		t.Errorf("expected size 2, got %d", list.Size())
	}
	if list.Front() != node1 {
		t.Error("Front() should still return first node")
	}
	if list.Back() != node2 {
		t.Error("Back() should return second node")
	}

	node3 := list.PushBack(3)
	if list.Size() != 3 {
		t.Errorf("expected size 3, got %d", list.Size())
	}
	if list.Back() != node3 {
		t.Error("Back() should return third node")
	}

	// Verify traversal
	if node1.Next() != node2 {
		t.Error("node1.Next() should be node2")
	}
	if node2.Next() != node3 {
		t.Error("node2.Next() should be node3")
	}
	if node3.Next() != nil {
		t.Error("node3.Next() should be nil")
	}
	if node1.Prev() != nil {
		t.Error("node1.Prev() should be nil")
	}
	if node2.Prev() != node1 {
		t.Error("node2.Prev() should be node1")
	}
	if node3.Prev() != node2 {
		t.Error("node3.Prev() should be node2")
	}
}

func TestPushFront(t *testing.T) {
	list := NewDLinkedList[int]()

	node1 := list.PushFront(1)
	if list.Size() != 1 {
		t.Errorf("expected size 1, got %d", list.Size())
	}
	if list.Front() != node1 {
		t.Error("Front() should return first node")
	}

	node2 := list.PushFront(2)
	if list.Size() != 2 {
		t.Errorf("expected size 2, got %d", list.Size())
	}
	if list.Front() != node2 {
		t.Error("Front() should return newly added node")
	}
	if list.Back() != node1 {
		t.Error("Back() should return first inserted node")
	}

	// Verify traversal
	if node2.Next() != node1 {
		t.Error("node2.Next() should be node1")
	}
	if node1.Prev() != node2 {
		t.Error("node1.Prev() should be node2")
	}
}

func TestRemove(t *testing.T) {
	list := NewDLinkedList[int]()

	// Test remove from single-element list
	node1 := list.PushBack(1)
	list.Remove(node1)
	if !list.IsEmpty() {
		t.Error("list should be empty after removing only element")
	}
	if list.Front() != nil || list.Back() != nil {
		t.Error("Front() and Back() should be nil after removing only element")
	}
	if node1.List() != nil {
		t.Error("removed node's List() should be nil")
	}

	// Test remove middle element
	node1 = list.PushBack(1)
	node2 := list.PushBack(2)
	node3 := list.PushBack(3)

	list.Remove(node2)
	if list.Size() != 2 {
		t.Errorf("expected size 2, got %d", list.Size())
	}
	if node1.Next() != node3 {
		t.Error("node1.Next() should be node3 after removing node2")
	}
	if node3.Prev() != node1 {
		t.Error("node3.Prev() should be node1 after removing node2")
	}

	// Test remove head
	list.Remove(node1)
	if list.Size() != 1 {
		t.Errorf("expected size 1, got %d", list.Size())
	}
	if list.Front() != node3 {
		t.Error("Front() should be node3 after removing head")
	}
	if node3.Prev() != nil {
		t.Error("new head's Prev() should be nil")
	}

	// Test remove tail (last remaining)
	list.Remove(node3)
	if !list.IsEmpty() {
		t.Error("list should be empty")
	}
}

func TestRemoveTail(t *testing.T) {
	list := NewDLinkedList[int]()
	node1 := list.PushBack(1)
	node2 := list.PushBack(2)
	node3 := list.PushBack(3)

	list.Remove(node3)
	if list.Size() != 2 {
		t.Errorf("expected size 2, got %d", list.Size())
	}
	if list.Back() != node2 {
		t.Error("Back() should be node2 after removing tail")
	}
	if node2.Next() != nil {
		t.Error("new tail's Next() should be nil")
	}
	_ = node1 // suppress unused warning
}

func TestRemoveInvalidNode(t *testing.T) {
	list1 := NewDLinkedList[int]()
	list2 := NewDLinkedList[int]()

	node1 := list1.PushBack(1)
	node2 := list2.PushBack(2)

	// Try to remove node from wrong list - should do nothing
	originalSize := list1.Size()
	list1.Remove(node2)
	if list1.Size() != originalSize {
		t.Error("removing node from wrong list should do nothing")
	}

	// Try to remove nil node - should do nothing
	list1.Remove(nil)
	if list1.Size() != originalSize {
		t.Error("removing nil node should do nothing")
	}

	_ = node1 // suppress unused warning
}

func TestPopFront(t *testing.T) {
	list := NewDLinkedList[int]()
	list.PushBack(1)
	list.PushBack(2)
	list.PushBack(3)

	val := list.PopFront()
	if val != 1 {
		t.Errorf("expected 1, got %d", val)
	}
	if list.Size() != 2 {
		t.Errorf("expected size 2, got %d", list.Size())
	}
	if list.Front().Value != 2 {
		t.Error("Front() value should be 2")
	}

	val = list.PopFront()
	if val != 2 {
		t.Errorf("expected 2, got %d", val)
	}

	val = list.PopFront()
	if val != 3 {
		t.Errorf("expected 3, got %d", val)
	}
	if !list.IsEmpty() {
		t.Error("list should be empty")
	}
}

func TestPopFrontPanic(t *testing.T) {
	list := NewDLinkedList[int]()

	defer func() {
		if r := recover(); r == nil {
			t.Error("PopFront on empty list should panic")
		}
	}()

	list.PopFront()
}

func TestPopBack(t *testing.T) {
	list := NewDLinkedList[int]()
	list.PushBack(1)
	list.PushBack(2)
	list.PushBack(3)

	val := list.PopBack()
	if val != 3 {
		t.Errorf("expected 3, got %d", val)
	}
	if list.Size() != 2 {
		t.Errorf("expected size 2, got %d", list.Size())
	}
	if list.Back().Value != 2 {
		t.Error("Back() value should be 2")
	}

	val = list.PopBack()
	if val != 2 {
		t.Errorf("expected 2, got %d", val)
	}

	val = list.PopBack()
	if val != 1 {
		t.Errorf("expected 1, got %d", val)
	}
	if !list.IsEmpty() {
		t.Error("list should be empty")
	}
}

func TestPopBackPanic(t *testing.T) {
	list := NewDLinkedList[int]()

	defer func() {
		if r := recover(); r == nil {
			t.Error("PopBack on empty list should panic")
		}
	}()

	list.PopBack()
}

func TestNodeListPointer(t *testing.T) {
	list := NewDLinkedList[int]()
	node := list.PushBack(1)

	if node.List() != list {
		t.Error("node.List() should return the list it belongs to")
	}

	list.Remove(node)
	if node.List() != nil {
		t.Error("removed node's List() should be nil")
	}
}

func TestIterateForward(t *testing.T) {
	list := NewDLinkedList[int]()
	list.PushBack(1)
	list.PushBack(2)
	list.PushBack(3)
	list.PushBack(4)
	list.PushBack(5)

	expected := []int{1, 2, 3, 4, 5}
	i := 0
	for node := list.Front(); node != nil; node = node.Next() {
		if node.Value != expected[i] {
			t.Errorf("at index %d: expected %d, got %d", i, expected[i], node.Value)
		}
		i++
	}
	if i != 5 {
		t.Errorf("expected to iterate 5 times, got %d", i)
	}
}

func TestIterateBackward(t *testing.T) {
	list := NewDLinkedList[int]()
	list.PushBack(1)
	list.PushBack(2)
	list.PushBack(3)
	list.PushBack(4)
	list.PushBack(5)

	expected := []int{5, 4, 3, 2, 1}
	i := 0
	for node := list.Back(); node != nil; node = node.Prev() {
		if node.Value != expected[i] {
			t.Errorf("at index %d: expected %d, got %d", i, expected[i], node.Value)
		}
		i++
	}
	if i != 5 {
		t.Errorf("expected to iterate 5 times, got %d", i)
	}
}

func TestMixedOperations(t *testing.T) {
	list := NewDLinkedList[int]()

	// Build: [2, 1, 3]
	node1 := list.PushBack(1)
	list.PushFront(2)
	list.PushBack(3)

	if list.Size() != 3 {
		t.Errorf("expected size 3, got %d", list.Size())
	}

	// Remove middle
	list.Remove(node1)
	if list.Size() != 2 {
		t.Errorf("expected size 2, got %d", list.Size())
	}

	// Pop front (removes 2)
	val := list.PopFront()
	if val != 2 {
		t.Errorf("expected 2, got %d", val)
	}

	// Pop back (removes 3)
	val = list.PopBack()
	if val != 3 {
		t.Errorf("expected 3, got %d", val)
	}

	if !list.IsEmpty() {
		t.Error("list should be empty")
	}
}

// Test with struct type to verify generics work properly
type testStruct struct {
	ID   int
	Name string
}

func TestWithStructType(t *testing.T) {
	list := NewDLinkedList[testStruct]()

	node1 := list.PushBack(testStruct{ID: 1, Name: "first"})
	node2 := list.PushBack(testStruct{ID: 2, Name: "second"})

	if list.Size() != 2 {
		t.Errorf("expected size 2, got %d", list.Size())
	}

	if node1.Value.ID != 1 || node1.Value.Name != "first" {
		t.Error("node1 value incorrect")
	}

	if node2.Value.ID != 2 || node2.Value.Name != "second" {
		t.Error("node2 value incorrect")
	}

	// Modify value through node
	node1.Value.Name = "modified"
	if list.Front().Value.Name != "modified" {
		t.Error("value modification should persist")
	}
}

// Test with pointer type
func TestWithPointerType(t *testing.T) {
	list := NewDLinkedList[*testStruct]()

	s1 := &testStruct{ID: 1, Name: "first"}
	s2 := &testStruct{ID: 2, Name: "second"}

	node1 := list.PushBack(s1)
	list.PushBack(s2)

	if node1.Value != s1 {
		t.Error("node should store exact pointer")
	}

	s1.Name = "modified"
	if node1.Value.Name != "modified" {
		t.Error("pointer modification should be visible")
	}
}

// Test using list as a FIFO queue
func TestAsQueue(t *testing.T) {
	queue := NewDLinkedList[int]()

	// Enqueue
	queue.PushBack(1)
	queue.PushBack(2)
	queue.PushBack(3)

	// Dequeue
	if queue.PopFront() != 1 {
		t.Error("expected 1")
	}
	if queue.PopFront() != 2 {
		t.Error("expected 2")
	}

	// More enqueue
	queue.PushBack(4)
	queue.PushBack(5)

	// More dequeue
	if queue.PopFront() != 3 {
		t.Error("expected 3")
	}
	if queue.PopFront() != 4 {
		t.Error("expected 4")
	}
	if queue.PopFront() != 5 {
		t.Error("expected 5")
	}

	if !queue.IsEmpty() {
		t.Error("queue should be empty")
	}
}

// Test using list as a stack (LIFO)
func TestAsStack(t *testing.T) {
	stack := NewDLinkedList[int]()

	// Push
	stack.PushBack(1)
	stack.PushBack(2)
	stack.PushBack(3)

	// Pop
	if stack.PopBack() != 3 {
		t.Error("expected 3")
	}
	if stack.PopBack() != 2 {
		t.Error("expected 2")
	}
	if stack.PopBack() != 1 {
		t.Error("expected 1")
	}

	if !stack.IsEmpty() {
		t.Error("stack should be empty")
	}
}
