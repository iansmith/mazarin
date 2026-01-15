package util

import "testing"

// testAction implements Action for testing
type testAction struct {
	executed bool
	value    int
}

func (a *testAction) Run() {
	a.executed = true
}

// countAction counts how many times it was executed
type countAction struct {
	count int
}

func (a *countAction) Run() {
	a.count++
}

func TestNewTimerDeadline(t *testing.T) {
	action := &testAction{value: 42}
	td := NewTimerDeadline(1000, action)

	if td == nil {
		t.Fatal("NewTimerDeadline returned nil")
	}

	if td.deadline != 1000 {
		t.Errorf("Expected deadline 1000, got %d", td.deadline)
	}

	if td.action != action {
		t.Error("Action not stored correctly")
	}
}

func TestTimerDeadlineOrderedBy(t *testing.T) {
	action := &testAction{}
	td := NewTimerDeadline(5000, action)

	if got := td.OrderedBy(); got != 5000 {
		t.Errorf("OrderedBy(): expected 5000, got %d", got)
	}
}

func TestTimerDeadlineDeadline(t *testing.T) {
	action := &testAction{}
	td := NewTimerDeadline(7000, action)

	if got := td.Deadline(); got != 7000 {
		t.Errorf("Deadline(): expected 7000, got %d", got)
	}
}

func TestTimerDeadlineOrderedByAndDeadlineMatch(t *testing.T) {
	action := &testAction{}
	td := NewTimerDeadline(9000, action)

	orderBy := td.OrderedBy()
	deadline := td.Deadline()

	if orderBy != deadline {
		t.Errorf("OrderedBy(%d) and Deadline(%d) must return same value", orderBy, deadline)
	}
}

func TestTimerDeadlineGetAction(t *testing.T) {
	action := &testAction{value: 123}
	td := NewTimerDeadline(2000, action)

	retrievedAction := td.GetAction()

	if retrievedAction == nil {
		t.Fatal("GetAction returned nil")
	}

	if retrievedAction != action {
		t.Error("GetAction did not return the same action")
	}

	// Verify it's the same action by checking its value
	if ta, ok := retrievedAction.(*testAction); ok {
		if ta.value != 123 {
			t.Errorf("Action value: expected 123, got %d", ta.value)
		}
	} else {
		t.Error("GetAction did not return correct type")
	}
}

func TestTimerDeadlineExecute(t *testing.T) {
	action := &testAction{}
	td := NewTimerDeadline(3000, action)

	if action.executed {
		t.Error("Action should not be executed yet")
	}

	td.Execute()

	if !action.executed {
		t.Error("Action should be executed after Execute()")
	}
}

func TestTimerDeadlineExecuteMultipleTimes(t *testing.T) {
	action := &countAction{}
	td := NewTimerDeadline(4000, action)

	for i := 1; i <= 5; i++ {
		td.Execute()
		if action.count != i {
			t.Errorf("Iteration %d: expected count %d, got %d", i, i, action.count)
		}
	}
}

func TestTimerDeadlineExecuteWithNilAction(t *testing.T) {
	// Should not panic with nil action
	td := NewTimerDeadline(6000, nil)

	// This should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Execute with nil action should not panic: %v", r)
		}
	}()

	td.Execute()
}

func TestTimerDeadlineImplementsTimed(t *testing.T) {
	action := &testAction{}
	td := NewTimerDeadline(8000, action)

	// Compile-time check that TimerDeadline implements Timed
	var _ Timed = td

	// Runtime check through interface
	var timed Timed = td
	if timed.OrderedBy() != 8000 {
		t.Errorf("Through Timed interface: OrderedBy expected 8000, got %d", timed.OrderedBy())
	}
	if timed.Deadline() != 8000 {
		t.Errorf("Through Timed interface: Deadline expected 8000, got %d", timed.Deadline())
	}
}

func TestTimerDeadlineImplementsOrderedByer(t *testing.T) {
	action := &testAction{}
	td := NewTimerDeadline(9000, action)

	// Since Timed embeds OrderedByer, TimerDeadline should also implement OrderedByer
	var _ OrderedByer = td

	// Runtime check through interface
	var ordered OrderedByer = td
	if ordered.OrderedBy() != 9000 {
		t.Errorf("Through OrderedByer interface: expected 9000, got %d", ordered.OrderedBy())
	}
}

func TestActionInterface(t *testing.T) {
	action := &testAction{value: 999}

	// Compile-time check that testAction implements Action
	var _ Action = action

	// Verify Run can be called through interface
	var a Action = action
	if action.executed {
		t.Error("Action should not be executed initially")
	}

	a.Run()

	if !action.executed {
		t.Error("Action should be executed after Run()")
	}
}

func TestMultipleTimerDeadlines(t *testing.T) {
	// Create multiple timer deadlines to verify they're independent
	action1 := &countAction{}
	action2 := &countAction{}
	action3 := &countAction{}

	td1 := NewTimerDeadline(100, action1)
	td2 := NewTimerDeadline(200, action2)
	td3 := NewTimerDeadline(300, action3)

	// Execute them in different orders
	td3.Execute()
	td1.Execute()
	td2.Execute()

	if action1.count != 1 {
		t.Errorf("Action1 count: expected 1, got %d", action1.count)
	}
	if action2.count != 1 {
		t.Errorf("Action2 count: expected 1, got %d", action2.count)
	}
	if action3.count != 1 {
		t.Errorf("Action3 count: expected 1, got %d", action3.count)
	}

	// Verify deadlines are correct
	if td1.Deadline() != 100 {
		t.Errorf("TD1 deadline: expected 100, got %d", td1.Deadline())
	}
	if td2.Deadline() != 200 {
		t.Errorf("TD2 deadline: expected 200, got %d", td2.Deadline())
	}
	if td3.Deadline() != 300 {
		t.Errorf("TD3 deadline: expected 300, got %d", td3.Deadline())
	}
}
