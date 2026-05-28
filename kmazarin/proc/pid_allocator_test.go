// Phase 0 red tests for MAZ-69 (kernel PID allocator).
//
// These tests describe the expected post-implementation behavior of
// PIDAllocator per the MAZ-68 + MAZ-73 design decisions. They fail
// against the stub implementation in pid_allocator.go — that's the
// point. The Phase B implementation agent's job is to make them pass.

package proc

import (
	"errors"
	"testing"
)

// TestPIDAllocatorAllocReturnsSequentialFromMin checks the first three
// Alloc() calls return MinPID, MinPID+1, MinPID+2 in order.
// (Sequential monotonic forward from MIN — MAZ-68 #4.)
func TestPIDAllocatorAllocReturnsSequentialFromMin(t *testing.T) {
	a := NewPIDAllocator()
	for i := ShepherdId(0); i < 3; i++ {
		pid, err := a.Alloc()
		if err != nil {
			t.Fatalf("Alloc #%d returned error %v, want nil", i, err)
		}
		want := MinPID + i
		if pid != want {
			t.Errorf("Alloc #%d = %d, want %d", i, pid, want)
		}
	}
}

// TestPIDAllocatorNeverReturnsPID0Or1 checks that across a full exhaustion
// of the allocator, PID 0 (invalid) and PID 1 (kernel sentinel) never
// appear in the returned set. (MAZ-68 #5: PID 1 reserved; PID 0 invalid.)
func TestPIDAllocatorNeverReturnsPID0Or1(t *testing.T) {
	a := NewPIDAllocator()
	total := int(MaxPID-MinPID) + 1
	for i := 0; i < total; i++ {
		pid, err := a.Alloc()
		if err != nil {
			t.Fatalf("Alloc #%d returned error %v before exhaustion", i, err)
		}
		if pid == 0 {
			t.Errorf("Alloc returned PID 0 (invalid)")
		}
		if pid == 1 {
			t.Errorf("Alloc returned PID 1 (kernel sentinel)")
		}
	}
}

// TestPIDAllocatorDoesNotImmediatelyReuse checks that freeing a PID does
// NOT make it next-to-issue. (MAZ-68 #4: prevents the "kill(pid) hits a
// new process" race; freed PIDs re-enter the pool only after wraparound.)
func TestPIDAllocatorDoesNotImmediatelyReuse(t *testing.T) {
	a := NewPIDAllocator()
	p1, err := a.Alloc()
	if err != nil {
		t.Fatalf("first Alloc returned error %v", err)
	}
	a.Free(p1)
	p2, err := a.Alloc()
	if err != nil {
		t.Fatalf("second Alloc returned error %v", err)
	}
	if p1 == p2 {
		t.Errorf("Alloc reissued freed PID %d immediately; expected wraparound-only reuse", p1)
	}
}

// TestPIDAllocatorExhaustionReturnsErr checks that after every PID in
// [MinPID, MaxPID] is allocated and none freed, the next Alloc returns
// (0, ErrPIDExhausted). (MAZ-73 decision: hard fail with EAGAIN.)
func TestPIDAllocatorExhaustionReturnsErr(t *testing.T) {
	a := NewPIDAllocator()
	total := int(MaxPID-MinPID) + 1
	for i := 0; i < total; i++ {
		if _, err := a.Alloc(); err != nil {
			t.Fatalf("Alloc #%d returned error %v before exhaustion", i, err)
		}
	}
	pid, err := a.Alloc()
	if !errors.Is(err, ErrPIDExhausted) {
		t.Errorf("Alloc on exhausted allocator: err=%v, want ErrPIDExhausted", err)
	}
	if pid != 0 {
		t.Errorf("Alloc on exhausted allocator: pid=%d, want 0 (invalid sentinel)", pid)
	}
}

// TestPIDAllocatorWraparoundReusesFreedAfterFull checks that once the
// allocator has issued every PID once and some have since been freed, a
// subsequent Alloc wraps around and returns one of the freed PIDs.
func TestPIDAllocatorWraparoundReusesFreedAfterFull(t *testing.T) {
	a := NewPIDAllocator()
	total := int(MaxPID-MinPID) + 1
	allocated := make([]ShepherdId, 0, total)
	for i := 0; i < total; i++ {
		pid, err := a.Alloc()
		if err != nil {
			t.Fatalf("Alloc #%d returned error %v", i, err)
		}
		allocated = append(allocated, pid)
	}
	// Free two specific PIDs.
	a.Free(allocated[0])
	a.Free(allocated[5])
	pid, err := a.Alloc()
	if err != nil {
		t.Fatalf("Alloc after Free returned error %v, want success via wraparound", err)
	}
	if pid != allocated[0] && pid != allocated[5] {
		t.Errorf("Alloc after wraparound = %d; want one of {%d, %d}", pid, allocated[0], allocated[5])
	}
}

// TestPIDAllocatorWraparoundSkipsInUse checks that wraparound visits only
// freed PIDs and never reissues a still-in-use PID.
func TestPIDAllocatorWraparoundSkipsInUse(t *testing.T) {
	a := NewPIDAllocator()
	total := int(MaxPID-MinPID) + 1
	allocated := make([]ShepherdId, 0, total)
	for i := 0; i < total; i++ {
		pid, _ := a.Alloc()
		allocated = append(allocated, pid)
	}
	// Free three non-adjacent PIDs; expect those three (and only those) to
	// come back from the next three Allocs.
	freedSet := map[ShepherdId]bool{
		allocated[10]:   true,
		allocated[200]:  true,
		allocated[1000]: true,
	}
	for p := range freedSet {
		a.Free(p)
	}
	seen := map[ShepherdId]bool{}
	for i := 0; i < len(freedSet); i++ {
		pid, err := a.Alloc()
		if err != nil {
			t.Fatalf("Alloc #%d after wraparound returned %v", i, err)
		}
		if !freedSet[pid] {
			t.Errorf("Alloc returned %d; not in freed set %v", pid, freedSet)
		}
		if seen[pid] {
			t.Errorf("Alloc returned %d twice; should skip in-use", pid)
		}
		seen[pid] = true
	}
}

// TestPIDAllocatorFreeIdempotent checks that double-Free is a no-op and
// doesn't corrupt allocator state.
func TestPIDAllocatorFreeIdempotent(t *testing.T) {
	a := NewPIDAllocator()
	pid, err := a.Alloc()
	if err != nil {
		t.Fatalf("Alloc returned %v", err)
	}
	a.Free(pid)
	a.Free(pid) // second free — must not panic
	// Allocator should still be usable
	if _, err = a.Alloc(); err != nil {
		t.Errorf("Alloc after double-Free returned %v, want nil", err)
	}
}

// TestPIDAllocatorInUseTracksAllocFree checks the InUse predicate.
func TestPIDAllocatorInUseTracksAllocFree(t *testing.T) {
	a := NewPIDAllocator()
	pid, err := a.Alloc()
	if err != nil {
		t.Fatalf("Alloc returned %v", err)
	}
	if !a.InUse(pid) {
		t.Errorf("InUse(%d) = false after Alloc; want true", pid)
	}
	a.Free(pid)
	if a.InUse(pid) {
		t.Errorf("InUse(%d) = true after Free; want false", pid)
	}
}

// TestPIDAllocatorNeverReturnsPIDOutOfRange checks all allocated PIDs lie
// in [MinPID, MaxPID].
func TestPIDAllocatorNeverReturnsPIDOutOfRange(t *testing.T) {
	a := NewPIDAllocator()
	total := int(MaxPID-MinPID) + 1
	for i := 0; i < total; i++ {
		pid, err := a.Alloc()
		if err != nil {
			t.Fatalf("Alloc #%d returned %v", i, err)
		}
		if pid < MinPID || pid > MaxPID {
			t.Errorf("Alloc #%d returned PID %d outside [%d, %d]", i, pid, MinPID, MaxPID)
		}
	}
}
