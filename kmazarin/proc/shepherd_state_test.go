// Phase 0 red tests for MAZ-70 (kernel process state record).
//
// These tests describe the expected post-implementation behavior of the
// new Shepherd struct fields + manipulation methods added for Linux
// emulation. They fail against the stub implementations in
// shepherd_state.go — that's the point. The Phase B implementation
// agent's job is to make them pass.

package proc

import (
	"bytes"
	"errors"
	"testing"
)

// TestShepherdNewHasNoParent verifies that a freshly-constructed Shepherd
// has ParentPID = 0 (= no parent; boot-time / kernel-launched marker).
func TestShepherdNewHasNoParent(t *testing.T) {
	var s Shepherd
	if s.ParentPID != 0 {
		t.Errorf("fresh Shepherd.ParentPID = %d, want 0", s.ParentPID)
	}
}

// TestShepherdAddChildAndQuery verifies AddChild stores PIDs and HasChild +
// ChildCount reflect them correctly.
func TestShepherdAddChildAndQuery(t *testing.T) {
	var s Shepherd
	if err := s.AddChild(42); err != nil {
		t.Fatalf("AddChild(42) returned %v, want nil", err)
	}
	if err := s.AddChild(43); err != nil {
		t.Fatalf("AddChild(43) returned %v, want nil", err)
	}
	if !s.HasChild(42) {
		t.Errorf("HasChild(42) = false after AddChild(42)")
	}
	if !s.HasChild(43) {
		t.Errorf("HasChild(43) = false after AddChild(43)")
	}
	if s.HasChild(99) {
		t.Errorf("HasChild(99) = true; 99 was never added")
	}
	if got := s.ChildCount(); got != 2 {
		t.Errorf("ChildCount = %d, want 2", got)
	}
}

// TestShepherdAddChildIdempotent verifies that adding the same child twice
// is a no-op (no double-counting, no error).
func TestShepherdAddChildIdempotent(t *testing.T) {
	var s Shepherd
	if err := s.AddChild(42); err != nil {
		t.Fatalf("first AddChild(42) returned %v", err)
	}
	if err := s.AddChild(42); err != nil {
		t.Errorf("second AddChild(42) returned %v, want nil (idempotent)", err)
	}
	if got := s.ChildCount(); got != 1 {
		t.Errorf("ChildCount after double-add = %d, want 1", got)
	}
}

// TestShepherdAddChildCapacity verifies AddChild returns ErrTooManyChildren
// after MaxChildrenPerShepherd PIDs have been added.
func TestShepherdAddChildCapacity(t *testing.T) {
	var s Shepherd
	for i := 0; i < MaxChildrenPerShepherd; i++ {
		if err := s.AddChild(ShepherdId(100 + i)); err != nil {
			t.Fatalf("AddChild #%d returned %v before capacity", i, err)
		}
	}
	err := s.AddChild(ShepherdId(100 + MaxChildrenPerShepherd))
	if !errors.Is(err, ErrTooManyChildren) {
		t.Errorf("AddChild at capacity returned %v, want ErrTooManyChildren", err)
	}
	if got := s.ChildCount(); got != int32(MaxChildrenPerShepherd) {
		t.Errorf("ChildCount after overflow attempt = %d, want %d", got, MaxChildrenPerShepherd)
	}
}

// TestShepherdRemoveChild verifies RemoveChild correctly removes a child
// and updates HasChild + ChildCount.
func TestShepherdRemoveChild(t *testing.T) {
	var s Shepherd
	_ = s.AddChild(42)
	_ = s.AddChild(43)
	s.RemoveChild(42)
	if s.HasChild(42) {
		t.Errorf("HasChild(42) = true after RemoveChild(42)")
	}
	if !s.HasChild(43) {
		t.Errorf("HasChild(43) = false; RemoveChild(42) shouldn't affect 43")
	}
	if got := s.ChildCount(); got != 1 {
		t.Errorf("ChildCount = %d, want 1", got)
	}
}

// TestShepherdRemoveChildIdempotent verifies removing a PID not in the
// list is a no-op (no panic, no count change).
func TestShepherdRemoveChildIdempotent(t *testing.T) {
	var s Shepherd
	_ = s.AddChild(42)
	s.RemoveChild(99) // never added
	if !s.HasChild(42) {
		t.Errorf("HasChild(42) = false after irrelevant RemoveChild(99)")
	}
	if got := s.ChildCount(); got != 1 {
		t.Errorf("ChildCount after irrelevant RemoveChild = %d, want 1", got)
	}
	// RemoveChild on already-removed PID
	s.RemoveChild(42)
	s.RemoveChild(42) // double remove
	if s.HasChild(42) {
		t.Errorf("HasChild(42) = true after double RemoveChild")
	}
}

// TestShepherdEachChildIteratesAll verifies EachChild visits every added
// child PID exactly once (order is implementation-defined).
func TestShepherdEachChildIteratesAll(t *testing.T) {
	var s Shepherd
	want := []ShepherdId{10, 20, 30, 40}
	for _, c := range want {
		_ = s.AddChild(c)
	}
	seen := map[ShepherdId]int{}
	s.EachChild(func(child ShepherdId) bool {
		seen[child]++
		return true
	})
	for _, c := range want {
		if seen[c] != 1 {
			t.Errorf("EachChild visited child %d %d times, want 1", c, seen[c])
		}
	}
	if len(seen) != len(want) {
		t.Errorf("EachChild visited %d distinct children, want %d", len(seen), len(want))
	}
}

// TestShepherdEachChildStopsOnFalse verifies that returning false from the
// callback halts iteration.
func TestShepherdEachChildStopsOnFalse(t *testing.T) {
	var s Shepherd
	_ = s.AddChild(10)
	_ = s.AddChild(20)
	_ = s.AddChild(30)
	count := 0
	s.EachChild(func(child ShepherdId) bool {
		count++
		return false // halt after one
	})
	if count != 1 {
		t.Errorf("EachChild with false-return called callback %d times, want 1", count)
	}
}

// TestShepherdMarkZombie verifies MarkZombie sets the zombie flag and
// records the exit status.
func TestShepherdMarkZombie(t *testing.T) {
	var s Shepherd
	if s.IsZombie() {
		t.Errorf("fresh Shepherd is already zombie")
	}
	s.MarkZombie(42)
	if !s.IsZombie() {
		t.Errorf("IsZombie() = false after MarkZombie(42)")
	}
	if s.ExitStatus != 42 {
		t.Errorf("ExitStatus = %d after MarkZombie(42), want 42", s.ExitStatus)
	}
}

// TestShepherdReap verifies Reap clears the zombie state and returns the
// exit status.
func TestShepherdReap(t *testing.T) {
	var s Shepherd
	s.MarkZombie(7)
	status := s.Reap()
	if status != 7 {
		t.Errorf("Reap returned %d, want 7", status)
	}
	if s.IsZombie() {
		t.Errorf("IsZombie() = true after Reap")
	}
}

// TestShepherdReapNonZombie verifies that calling Reap on a non-zombie
// shepherd returns 0 and does not mutate state.
func TestShepherdReapNonZombie(t *testing.T) {
	var s Shepherd
	if status := s.Reap(); status != 0 {
		t.Errorf("Reap on non-zombie returned %d, want 0", status)
	}
	if s.IsZombie() {
		t.Errorf("IsZombie() = true after Reap on non-zombie")
	}
}

// TestShepherdSetEnvironRoundtrip verifies SetEnviron stores bytes that
// GetEnviron returns intact.
func TestShepherdSetEnvironRoundtrip(t *testing.T) {
	var s Shepherd
	env := []byte("PATH=/bin\x00HOME=/root\x00")
	if err := s.SetEnviron(env); err != nil {
		t.Fatalf("SetEnviron returned %v, want nil", err)
	}
	got := s.GetEnviron()
	if !bytes.Equal(got, env) {
		t.Errorf("GetEnviron = %q, want %q", got, env)
	}
}

// TestShepherdSetEnvironTooLarge verifies SetEnviron rejects oversized env.
func TestShepherdSetEnvironTooLarge(t *testing.T) {
	var s Shepherd
	env := make([]byte, MaxEnvironBytes+1)
	err := s.SetEnviron(env)
	if !errors.Is(err, ErrEnvironTooLarge) {
		t.Errorf("SetEnviron(%d bytes) returned %v, want ErrEnvironTooLarge", len(env), err)
	}
}

// TestShepherdSetEnvironEmpty verifies that an empty env is allowed and
// GetEnviron returns an empty slice.
func TestShepherdSetEnvironEmpty(t *testing.T) {
	var s Shepherd
	if err := s.SetEnviron(nil); err != nil {
		t.Errorf("SetEnviron(nil) returned %v, want nil", err)
	}
	if got := s.GetEnviron(); len(got) != 0 {
		t.Errorf("GetEnviron after SetEnviron(nil) = %q, want empty", got)
	}
}

// TestShepherdSetEnvironOverwrite verifies that a second SetEnviron call
// replaces the previous environ entirely.
func TestShepherdSetEnvironOverwrite(t *testing.T) {
	var s Shepherd
	first := []byte("OLD=1\x00")
	second := []byte("NEW=2\x00")
	if err := s.SetEnviron(first); err != nil {
		t.Fatalf("first SetEnviron returned %v", err)
	}
	if err := s.SetEnviron(second); err != nil {
		t.Fatalf("second SetEnviron returned %v", err)
	}
	got := s.GetEnviron()
	if !bytes.Equal(got, second) {
		t.Errorf("GetEnviron after overwrite = %q, want %q", got, second)
	}
}
