// Phase 0 red tests for MAZ-71 (kernel → linux-shepherd notification queue).
//
// These tests describe the expected behavior of NotificationQueue and the
// NotificationEvent payload. They fail against the stub implementation in
// notification.go — that's the point. The Phase B implementation agent's
// job is to make them pass.

package proc

import (
	"errors"
	"testing"
)

// TestNotificationQueueNewIsEmpty verifies a fresh queue starts empty.
func TestNotificationQueueNewIsEmpty(t *testing.T) {
	q := NewNotificationQueue()
	if got := q.Len(); got != 0 {
		t.Errorf("new queue Len() = %d, want 0", got)
	}
	if _, ok := q.Pop(); ok {
		t.Errorf("Pop on empty queue returned ok=true; want false")
	}
}

// TestNotificationQueueCap verifies Cap returns MaxNotificationEvents.
func TestNotificationQueueCap(t *testing.T) {
	q := NewNotificationQueue()
	if got := q.Cap(); got != MaxNotificationEvents {
		t.Errorf("Cap() = %d, want %d", got, MaxNotificationEvents)
	}
}

// TestNotificationQueuePushPopRoundtrip verifies Push then Pop preserves
// the event payload byte-for-byte.
func TestNotificationQueuePushPopRoundtrip(t *testing.T) {
	q := NewNotificationQueue()
	want := NotificationEvent{
		Type:       EventChildExit,
		Pid:        42,
		ParentPid:  7,
		ExitStatus: 0,
	}
	if err := q.Push(want); err != nil {
		t.Fatalf("Push returned %v, want nil", err)
	}
	got, ok := q.Pop()
	if !ok {
		t.Fatalf("Pop after Push returned ok=false; want true")
	}
	if got != want {
		t.Errorf("Pop returned %+v, want %+v", got, want)
	}
}

// TestNotificationQueueFIFOOrdering verifies the queue is FIFO across
// multiple events.
func TestNotificationQueueFIFOOrdering(t *testing.T) {
	q := NewNotificationQueue()
	events := []NotificationEvent{
		{Type: EventChildExit, Pid: 10, ParentPid: 1, ExitStatus: 0},
		{Type: EventExecComplete, Pid: 20},
		{Type: EventParentDeath, Pid: 30},
		{Type: EventChildExit, Pid: 40, ParentPid: 2, ExitStatus: 42},
	}
	for i, ev := range events {
		if err := q.Push(ev); err != nil {
			t.Fatalf("Push #%d returned %v", i, err)
		}
	}
	if got := q.Len(); got != len(events) {
		t.Errorf("Len after %d Pushes = %d, want %d", len(events), got, len(events))
	}
	for i, want := range events {
		got, ok := q.Pop()
		if !ok {
			t.Fatalf("Pop #%d returned ok=false", i)
		}
		if got != want {
			t.Errorf("Pop #%d = %+v, want %+v", i, got, want)
		}
	}
	if _, ok := q.Pop(); ok {
		t.Errorf("Pop on drained queue returned ok=true; want false")
	}
}

// TestNotificationQueueFullPushErr verifies Push returns ErrQueueFull
// after MaxNotificationEvents pushes.
func TestNotificationQueueFullPushErr(t *testing.T) {
	q := NewNotificationQueue()
	for i := 0; i < MaxNotificationEvents; i++ {
		ev := NotificationEvent{Type: EventChildExit, Pid: int16(i + 2)}
		if err := q.Push(ev); err != nil {
			t.Fatalf("Push #%d returned %v before capacity", i, err)
		}
	}
	overflow := NotificationEvent{Type: EventChildExit, Pid: 1000}
	err := q.Push(overflow)
	if !errors.Is(err, ErrQueueFull) {
		t.Errorf("Push at capacity returned %v, want ErrQueueFull", err)
	}
	if got := q.Len(); got != MaxNotificationEvents {
		t.Errorf("Len after overflow attempt = %d, want %d", got, MaxNotificationEvents)
	}
}

// TestNotificationQueueRingReusesSlots verifies that after partial drain,
// new pushes succeed up to the original capacity (ring-buffer semantics).
func TestNotificationQueueRingReusesSlots(t *testing.T) {
	q := NewNotificationQueue()
	// Push half capacity.
	half := MaxNotificationEvents / 2
	for i := 0; i < half; i++ {
		_ = q.Push(NotificationEvent{Type: EventChildExit, Pid: int16(i + 2)})
	}
	// Pop all of them.
	for i := 0; i < half; i++ {
		if _, ok := q.Pop(); !ok {
			t.Fatalf("Pop #%d returned ok=false during drain", i)
		}
	}
	if got := q.Len(); got != 0 {
		t.Errorf("Len after drain = %d, want 0", got)
	}
	// Push capacity worth of new events; all should succeed.
	for i := 0; i < MaxNotificationEvents; i++ {
		ev := NotificationEvent{Type: EventExecComplete, Pid: int16(i + 100)}
		if err := q.Push(ev); err != nil {
			t.Errorf("Push #%d after drain returned %v, want nil", i, err)
		}
	}
}

// TestNotificationQueueLenTracksDepth verifies Len returns the current
// queue depth, growing with Push and shrinking with Pop.
func TestNotificationQueueLenTracksDepth(t *testing.T) {
	q := NewNotificationQueue()
	for i := 0; i < 10; i++ {
		_ = q.Push(NotificationEvent{Type: EventChildExit, Pid: int16(i + 2)})
		if got := q.Len(); got != i+1 {
			t.Errorf("Len after Push #%d = %d, want %d", i, got, i+1)
		}
	}
	for i := 9; i >= 0; i-- {
		_, _ = q.Pop()
		if got := q.Len(); got != i {
			t.Errorf("Len after Pop #%d = %d, want %d", 9-i, got, i)
		}
	}
}

// TestNotificationEventDistinctTypes sanity-checks that the three event
// types are distinct (no enum collisions).
func TestNotificationEventDistinctTypes(t *testing.T) {
	if EventChildExit == EventParentDeath ||
		EventChildExit == EventExecComplete ||
		EventParentDeath == EventExecComplete {
		t.Errorf("event type enum collision: ChildExit=%d ParentDeath=%d ExecComplete=%d",
			EventChildExit, EventParentDeath, EventExecComplete)
	}
	if EventChildExit == 0 || EventParentDeath == 0 || EventExecComplete == 0 {
		t.Errorf("event type enum should not use 0 (reserved for zero-value); got ChildExit=%d ParentDeath=%d ExecComplete=%d",
			EventChildExit, EventParentDeath, EventExecComplete)
	}
}
