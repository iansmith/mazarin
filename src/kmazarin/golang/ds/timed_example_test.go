package ds

import (
	"testing"

	"kmazarin/util"
)

// timedEvent implements util.Timed interface
type timedEvent struct {
	name     string
	deadline uint64
}

func (e timedEvent) OrderedBy() uint64 {
	return e.deadline
}

func (e timedEvent) Deadline() uint64 {
	return e.deadline
}

// Compile-time assertion that timedEvent implements util.Timed
var _ util.Timed = timedEvent{}

// TestOrderedListWithTimed demonstrates that types implementing util.Timed
// can be used in OrderedList since Timed embeds OrderedByer
func TestOrderedListWithTimed(t *testing.T) {
	// Create an ascending list (earliest deadline first)
	list := NewOrderedList[timedEvent](true)

	// Insert events with different deadlines
	list.Insert(timedEvent{name: "event3", deadline: 300})
	list.Insert(timedEvent{name: "event1", deadline: 100})
	list.Insert(timedEvent{name: "event4", deadline: 400})
	list.Insert(timedEvent{name: "event2", deadline: 200})

	// Verify they come out in deadline order
	expected := []struct {
		name     string
		deadline uint64
	}{
		{"event1", 100},
		{"event2", 200},
		{"event3", 300},
		{"event4", 400},
	}

	for i, exp := range expected {
		if list.IsEmpty() {
			t.Fatalf("List empty at position %d", i)
		}

		event := list.Pop()

		if event.name != exp.name {
			t.Errorf("Position %d: expected name %s, got %s", i, exp.name, event.name)
		}

		if event.Deadline() != exp.deadline {
			t.Errorf("Position %d: expected deadline %d, got %d", i, exp.deadline, event.Deadline())
		}

		// Verify OrderedBy() and Deadline() return the same value
		if event.OrderedBy() != event.Deadline() {
			t.Errorf("Position %d: OrderedBy(%d) != Deadline(%d)", i, event.OrderedBy(), event.Deadline())
		}
	}

	if !list.IsEmpty() {
		t.Error("List should be empty after popping all events")
	}
}

// TestOrderedListWithTimedDescending demonstrates descending order
// (latest deadline first) which is useful for "most urgent" queues
func TestOrderedListWithTimedDescending(t *testing.T) {
	// Create a descending list (latest deadline first)
	list := NewOrderedList[timedEvent](false)

	list.Insert(timedEvent{name: "event2", deadline: 200})
	list.Insert(timedEvent{name: "event4", deadline: 400})
	list.Insert(timedEvent{name: "event1", deadline: 100})
	list.Insert(timedEvent{name: "event3", deadline: 300})

	// Should come out: 400, 300, 200, 100
	expected := []uint64{400, 300, 200, 100}

	for i, exp := range expected {
		event := list.Pop()
		if event.Deadline() != exp {
			t.Errorf("Position %d: expected deadline %d, got %d", i, exp, event.Deadline())
		}
	}
}

// TestTimedInterfacePeek demonstrates using Peek with Timed elements
func TestTimedInterfacePeek(t *testing.T) {
	list := NewOrderedList[timedEvent](true)

	list.Insert(timedEvent{name: "late", deadline: 500})
	list.Insert(timedEvent{name: "early", deadline: 100})
	list.Insert(timedEvent{name: "middle", deadline: 300})

	// Peek should return the earliest deadline (100)
	if got := list.Peek(); got != 100 {
		t.Errorf("Peek: expected 100, got %d", got)
	}

	// Pop and verify it's the "early" event
	event := list.Pop()
	if event.name != "early" {
		t.Errorf("Expected 'early', got '%s'", event.name)
	}
	if event.Deadline() != 100 {
		t.Errorf("Expected deadline 100, got %d", event.Deadline())
	}
}
