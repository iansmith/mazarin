package util

import "testing"

// testOrderedItem implements OrderedByer
type testOrderedItem struct {
	order uint64
}

func (t testOrderedItem) OrderedBy() uint64 {
	return t.order
}

// testTimedItem implements Timed
type testTimedItem struct {
	time uint64
}

func (t testTimedItem) OrderedBy() uint64 {
	return t.time
}

func (t testTimedItem) Deadline() uint64 {
	return t.time
}

func TestOrderedByer(t *testing.T) {
	item := testOrderedItem{order: 100}

	// Verify it implements the interface
	var _ OrderedByer = item

	if got := item.OrderedBy(); got != 100 {
		t.Errorf("OrderedBy(): expected 100, got %d", got)
	}
}

func TestTimed(t *testing.T) {
	item := testTimedItem{time: 200}

	// Verify it implements both interfaces
	var _ Timed = item
	var _ OrderedByer = item // Timed embeds OrderedByer

	if got := item.OrderedBy(); got != 200 {
		t.Errorf("OrderedBy(): expected 200, got %d", got)
	}

	if got := item.Deadline(); got != 200 {
		t.Errorf("Deadline(): expected 200, got %d", got)
	}
}

func TestTimedIsOrderedByer(t *testing.T) {
	item := testTimedItem{time: 300}

	// A Timed can be assigned to an OrderedByer variable
	var ordered OrderedByer = item

	if got := ordered.OrderedBy(); got != 300 {
		t.Errorf("OrderedBy() through OrderedByer interface: expected 300, got %d", got)
	}
}

func TestTimedOrderedByAndDeadlineMatch(t *testing.T) {
	// This test verifies that implementations return the same value
	// from both methods (as documented)
	item := testTimedItem{time: 500}

	orderBy := item.OrderedBy()
	deadline := item.Deadline()

	if orderBy != deadline {
		t.Errorf("OrderedBy() and Deadline() must return same value: got %d and %d", orderBy, deadline)
	}
}
