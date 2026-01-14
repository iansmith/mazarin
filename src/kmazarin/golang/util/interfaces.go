package util

// OrderedByer is the interface that types must implement to be stored in ordered collections
// that sort by a uint64 key value.
type OrderedByer interface {
	OrderedBy() uint64
}

// Timed is an interface for time-based ordered elements.
// Types implementing Timed can be used in ordered collections sorted by time.
//
// IMPORTANT: OrderedBy() and Deadline() MUST return the same underlying value.
// The two methods exist for semantic clarity:
//   - OrderedBy() is used by generic ordered collections for sorting
//   - Deadline() is used by time-sensitive code for deadline checking
//
// Example implementation:
//
//	type TimedEvent struct {
//	    deadline uint64
//	}
//
//	func (t TimedEvent) OrderedBy() uint64 { return t.deadline }
//	func (t TimedEvent) Deadline() uint64  { return t.deadline }
//
// Since Timed includes OrderedBy(), any Timed type is also an OrderedByer
// and can be used in OrderedList or other ordered collections.
type Timed interface {
	OrderedByer // Embedded interface - provides OrderedBy() method
	Deadline() uint64
}

// Action is an interface for executable actions.
// Types implementing Action can be scheduled, queued, or triggered.
type Action interface {
	Run()
}
