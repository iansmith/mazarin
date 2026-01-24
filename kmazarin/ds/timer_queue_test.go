package ds

import (
	"testing"

	"mazzy/kmazarin/util"
)

// testTimerAction is a simple action that records execution
type testTimerAction struct {
	name     string
	executed bool
}

func (a *testTimerAction) Run() {
	a.executed = true
}

// TestTimerDeadlineInOrderedList demonstrates using TimerDeadline
// in an OrderedList to create a timer queue
func TestTimerDeadlineInOrderedList(t *testing.T) {
	// Create ascending list (earliest deadline first)
	timerQueue := NewOrderedList[*util.TimerDeadline](true)

	// Create timer deadlines with actions
	action1 := &testTimerAction{name: "task1"}
	action2 := &testTimerAction{name: "task2"}
	action3 := &testTimerAction{name: "task3"}

	td1 := util.NewTimerDeadline(300, action1)
	td2 := util.NewTimerDeadline(100, action2)
	td3 := util.NewTimerDeadline(200, action3)

	// Insert in random order
	timerQueue.Insert(td1)
	timerQueue.Insert(td2)
	timerQueue.Insert(td3)

	// Verify they come out in deadline order: 100, 200, 300
	expected := []struct {
		deadline uint64
		name     string
	}{
		{100, "task2"},
		{200, "task3"},
		{300, "task1"},
	}

	for i, exp := range expected {
		if timerQueue.IsEmpty() {
			t.Fatalf("Queue empty at position %d", i)
		}

		// Peek to verify deadline
		if got := timerQueue.Peek(); got != exp.deadline {
			t.Errorf("Position %d: Peek expected %d, got %d", i, exp.deadline, got)
		}

		// Pop and execute
		td := timerQueue.Pop()

		if td.Deadline() != exp.deadline {
			t.Errorf("Position %d: expected deadline %d, got %d", i, exp.deadline, td.Deadline())
		}

		// Execute the action
		td.Execute()

		// Verify the correct action was executed
		action := td.GetAction().(*testTimerAction)
		if action.name != exp.name {
			t.Errorf("Position %d: expected action %s, got %s", i, exp.name, action.name)
		}
		if !action.executed {
			t.Errorf("Position %d: action %s was not executed", i, action.name)
		}
	}

	if !timerQueue.IsEmpty() {
		t.Error("Queue should be empty after processing all timers")
	}
}

// TestTimerQueueProcessing demonstrates a typical timer queue usage pattern
func TestTimerQueueProcessing(t *testing.T) {
	timerQueue := NewOrderedList[*util.TimerDeadline](true)

	// Track execution order
	var executionOrder []string

	// Create actions that record execution order
	action1 := &recordingAction{name: "timer1", executionOrder: &executionOrder}
	action2 := &recordingAction{name: "timer2", executionOrder: &executionOrder}
	action3 := &recordingAction{name: "timer3", executionOrder: &executionOrder}
	action4 := &recordingAction{name: "timer4", executionOrder: &executionOrder}

	// Insert timers with different deadlines
	timerQueue.Insert(util.NewTimerDeadline(400, action1))
	timerQueue.Insert(util.NewTimerDeadline(100, action2))
	timerQueue.Insert(util.NewTimerDeadline(300, action3))
	timerQueue.Insert(util.NewTimerDeadline(200, action4))

	// Simulate processing timers until queue is empty
	currentTime := uint64(0)
	maxTime := uint64(500)

	for !timerQueue.IsEmpty() && currentTime <= maxTime {
		nextDeadline := timerQueue.Peek()

		// Advance time to next deadline
		if nextDeadline > currentTime {
			currentTime = nextDeadline
		}

		// Process all timers that have expired
		for !timerQueue.IsEmpty() && timerQueue.Peek() <= currentTime {
			timer := timerQueue.Pop()
			timer.Execute()
		}
	}

	// Verify execution order matches deadline order
	expectedOrder := []string{"timer2", "timer4", "timer3", "timer1"}
	if len(executionOrder) != len(expectedOrder) {
		t.Fatalf("Expected %d executions, got %d", len(expectedOrder), len(executionOrder))
	}

	for i, expected := range expectedOrder {
		if executionOrder[i] != expected {
			t.Errorf("Position %d: expected %s, got %s", i, expected, executionOrder[i])
		}
	}
}

// recordingAction records execution in a slice
type recordingAction struct {
	name           string
	executionOrder *[]string
}

func (a *recordingAction) Run() {
	*a.executionOrder = append(*a.executionOrder, a.name)
}

// TestTimerQueueSize demonstrates Size() with TimerDeadline
func TestTimerQueueSize(t *testing.T) {
	timerQueue := NewOrderedList[*util.TimerDeadline](true)

	if timerQueue.Size() != 0 {
		t.Errorf("Empty queue: expected size 0, got %d", timerQueue.Size())
	}

	// Add timers
	timerQueue.Insert(util.NewTimerDeadline(100, &testTimerAction{}))
	timerQueue.Insert(util.NewTimerDeadline(200, &testTimerAction{}))
	timerQueue.Insert(util.NewTimerDeadline(300, &testTimerAction{}))

	if timerQueue.Size() != 3 {
		t.Errorf("After 3 inserts: expected size 3, got %d", timerQueue.Size())
	}

	timerQueue.Pop()

	if timerQueue.Size() != 2 {
		t.Errorf("After 1 pop: expected size 2, got %d", timerQueue.Size())
	}
}

// TestTimerQueueDuplicateDeadlines demonstrates handling duplicate deadlines
func TestTimerQueueDuplicateDeadlines(t *testing.T) {
	timerQueue := NewOrderedList[*util.TimerDeadline](true)

	action1 := &testTimerAction{name: "first-100"}
	action2 := &testTimerAction{name: "second-100"}
	action3 := &testTimerAction{name: "first-200"}

	timerQueue.Insert(util.NewTimerDeadline(100, action1))
	timerQueue.Insert(util.NewTimerDeadline(200, action3))
	timerQueue.Insert(util.NewTimerDeadline(100, action2))

	// Both 100-deadline timers should come before 200-deadline timer
	first := timerQueue.Pop()
	if first.Deadline() != 100 {
		t.Errorf("First: expected deadline 100, got %d", first.Deadline())
	}

	second := timerQueue.Pop()
	if second.Deadline() != 100 {
		t.Errorf("Second: expected deadline 100, got %d", second.Deadline())
	}

	third := timerQueue.Pop()
	if third.Deadline() != 200 {
		t.Errorf("Third: expected deadline 200, got %d", third.Deadline())
	}
}
