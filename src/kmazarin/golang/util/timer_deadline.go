package util

// TimerDeadline is a concrete implementation of Timed that combines
// a deadline timestamp with an action to execute.
//
// This is useful for timer queues, delayed task execution, and
// deadline-based scheduling systems.
type TimerDeadline struct {
	deadline uint64
	action   Action
}

// NewTimerDeadline creates a new TimerDeadline with the given deadline and action.
//
// Parameters:
//   - deadline: The time (in counter ticks or other time units) when the action should execute
//   - action: The action to execute when the deadline is reached
//
// The deadline value will be returned by both OrderedBy() and Deadline() methods,
// allowing TimerDeadline to be used in ordered collections like OrderedList.
func NewTimerDeadline(deadline uint64, action Action) *TimerDeadline {
	return &TimerDeadline{
		deadline: deadline,
		action:   action,
	}
}

// OrderedBy returns the deadline timestamp.
// This method is used by ordered collections for sorting.
func (t *TimerDeadline) OrderedBy() uint64 {
	return t.deadline
}

// Deadline returns the deadline timestamp.
// This method provides semantic clarity for time-based code.
// As required by the Timed interface, it returns the same value as OrderedBy().
func (t *TimerDeadline) Deadline() uint64 {
	return t.deadline
}

// GetAction returns the action associated with this deadline.
// This allows the action to be retrieved and executed when the deadline is reached.
func (t *TimerDeadline) GetAction() Action {
	return t.action
}

// Execute runs the associated action.
// This is a convenience method that calls Run() on the stored action.
func (t *TimerDeadline) Execute() {
	if t.action != nil {
		t.action.Run()
	}
}
