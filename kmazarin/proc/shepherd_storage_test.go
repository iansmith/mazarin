// Phase 0 red tests for MAZ-73 (sparse PID-keyed shepherd storage).
//
// These tests describe the expected behavior of ShepherdStorage. They
// fail against the stub implementation in shepherd_storage.go — that's
// the point. The Phase B implementation agent's job is to make them pass.

package proc

import (
	"errors"
	"testing"
)

// TestShepherdStorageNewIsEmpty verifies a fresh storage is empty.
func TestShepherdStorageNewIsEmpty(t *testing.T) {
	s := NewShepherdStorage()
	if got := s.Len(); got != 0 {
		t.Errorf("new ShepherdStorage Len() = %d, want 0", got)
	}
	if _, ok := s.Get(MinPID); ok {
		t.Errorf("Get on empty storage returned ok=true; want false")
	}
}

// TestShepherdStorageAllocateAndGet verifies Allocate returns a usable
// pointer and Get returns the same Shepherd.
func TestShepherdStorageAllocateAndGet(t *testing.T) {
	s := NewShepherdStorage()
	pid := ShepherdId(42)
	shep, err := s.Allocate(pid)
	if err != nil {
		t.Fatalf("Allocate(%d) returned %v, want nil", pid, err)
	}
	if shep == nil {
		t.Fatalf("Allocate returned nil shepherd")
	}
	got, ok := s.Get(pid)
	if !ok {
		t.Fatalf("Get(%d) after Allocate returned ok=false", pid)
	}
	if got != shep {
		t.Errorf("Get returned different pointer than Allocate; want same Shepherd")
	}
}

// TestShepherdStorageAllocateInUse verifies that allocating a PID
// already in storage returns ErrShepherdPIDInUse.
func TestShepherdStorageAllocateInUse(t *testing.T) {
	s := NewShepherdStorage()
	pid := ShepherdId(42)
	shep1, err := s.Allocate(pid)
	if err != nil {
		t.Fatalf("first Allocate returned %v", err)
	}
	if shep1 == nil {
		t.Fatalf("first Allocate returned nil shepherd")
	}
	shep1.PID = pid // marker
	shep2, err := s.Allocate(pid)
	if !errors.Is(err, ErrShepherdPIDInUse) {
		t.Errorf("second Allocate(%d) returned %v, want ErrShepherdPIDInUse", pid, err)
	}
	if shep2 != nil {
		t.Errorf("second Allocate returned non-nil shepherd; want nil")
	}
	// Original slot must be untouched.
	got, _ := s.Get(pid)
	if got != shep1 {
		t.Errorf("Get after failed second Allocate returned different pointer")
	}
}

// TestShepherdStorageAllocateOutOfRange verifies PIDs outside
// [MinPID, MaxPID] are rejected.
func TestShepherdStorageAllocateOutOfRange(t *testing.T) {
	s := NewShepherdStorage()
	for _, pid := range []ShepherdId{0, 1, MaxPID + 1, -1} {
		shep, err := s.Allocate(pid)
		if !errors.Is(err, ErrShepherdPIDOutOfRange) {
			t.Errorf("Allocate(%d) returned %v, want ErrShepherdPIDOutOfRange", pid, err)
		}
		if shep != nil {
			t.Errorf("Allocate(%d) returned non-nil shepherd; want nil", pid)
		}
	}
}

// TestShepherdStorageAllocateExhausted verifies that filling all
// MaxLiveShepherds slots causes subsequent Allocates to return
// ErrShepherdSlotExhausted.
func TestShepherdStorageAllocateExhausted(t *testing.T) {
	s := NewShepherdStorage()
	for i := 0; i < MaxLiveShepherds; i++ {
		pid := ShepherdId(int(MinPID) + i)
		if pid > MaxPID {
			t.Fatalf("test setup error: PID %d exceeds MaxPID %d at iteration %d", pid, MaxPID, i)
		}
		if _, err := s.Allocate(pid); err != nil {
			t.Fatalf("Allocate #%d (pid=%d) returned %v before capacity", i, pid, err)
		}
	}
	// Storage is full. Next Allocate of a NEW (not in use) PID must fail.
	overflow := ShepherdId(int(MinPID) + MaxLiveShepherds)
	shep, err := s.Allocate(overflow)
	if !errors.Is(err, ErrShepherdSlotExhausted) {
		t.Errorf("Allocate at capacity returned %v, want ErrShepherdSlotExhausted", err)
	}
	if shep != nil {
		t.Errorf("Allocate at capacity returned non-nil shepherd; want nil")
	}
}

// TestShepherdStorageReleaseFreesSlot verifies Release deletes a record
// and frees the slot for reuse.
func TestShepherdStorageReleaseFreesSlot(t *testing.T) {
	s := NewShepherdStorage()
	pid := ShepherdId(42)
	_, err := s.Allocate(pid)
	if err != nil {
		t.Fatalf("Allocate returned %v", err)
	}
	s.Release(pid)
	if _, ok := s.Get(pid); ok {
		t.Errorf("Get(%d) after Release returned ok=true; want false", pid)
	}
	if got := s.Len(); got != 0 {
		t.Errorf("Len after Release = %d, want 0", got)
	}
	// Slot should be reusable.
	if _, err := s.Allocate(pid); err != nil {
		t.Errorf("Allocate(%d) after Release returned %v, want nil (slot should be reusable)", pid, err)
	}
}

// TestShepherdStorageReleaseIdempotent verifies that Release on a PID
// not in storage is a no-op.
func TestShepherdStorageReleaseIdempotent(t *testing.T) {
	s := NewShepherdStorage()
	_, _ = s.Allocate(42)
	s.Release(99) // never allocated
	if _, ok := s.Get(42); !ok {
		t.Errorf("Get(42) after irrelevant Release(99) returned ok=false")
	}
	if got := s.Len(); got != 1 {
		t.Errorf("Len after irrelevant Release = %d, want 1", got)
	}
	// Out-of-range Release is also a no-op.
	s.Release(0)
	s.Release(MaxPID + 1)
	if got := s.Len(); got != 1 {
		t.Errorf("Len after out-of-range Releases = %d, want 1", got)
	}
}

// TestShepherdStorageLenTracksCount verifies Len reflects current
// shepherd count.
func TestShepherdStorageLenTracksCount(t *testing.T) {
	s := NewShepherdStorage()
	pids := []ShepherdId{10, 20, 30, 40, 50}
	for i, pid := range pids {
		_, _ = s.Allocate(pid)
		if got := s.Len(); got != int32(i+1) {
			t.Errorf("Len after Allocate #%d = %d, want %d", i, got, i+1)
		}
	}
	for i, pid := range pids {
		s.Release(pid)
		want := int32(len(pids) - i - 1)
		if got := s.Len(); got != want {
			t.Errorf("Len after Release #%d = %d, want %d", i, got, want)
		}
	}
}

// TestShepherdStorageForEachVisitsAll verifies ForEach iterates over
// every live Shepherd exactly once.
func TestShepherdStorageForEachVisitsAll(t *testing.T) {
	s := NewShepherdStorage()
	pids := []ShepherdId{10, 20, 30, 40}
	for _, pid := range pids {
		shep, err := s.Allocate(pid)
		if err != nil {
			t.Fatalf("Allocate(%d) returned %v", pid, err)
		}
		if shep == nil {
			t.Fatalf("Allocate(%d) returned nil shepherd", pid)
		}
		shep.PID = pid
	}
	seen := map[ShepherdId]int{}
	s.ForEach(func(shep *Shepherd) bool {
		seen[shep.PID]++
		return true
	})
	for _, pid := range pids {
		if seen[pid] != 1 {
			t.Errorf("ForEach visited PID %d %d times, want 1", pid, seen[pid])
		}
	}
	if len(seen) != len(pids) {
		t.Errorf("ForEach visited %d distinct shepherds, want %d", len(seen), len(pids))
	}
}

// TestShepherdStorageForEachStopsOnFalse verifies returning false halts
// iteration.
func TestShepherdStorageForEachStopsOnFalse(t *testing.T) {
	s := NewShepherdStorage()
	_, _ = s.Allocate(10)
	_, _ = s.Allocate(20)
	_, _ = s.Allocate(30)
	count := 0
	s.ForEach(func(_ *Shepherd) bool {
		count++
		return false // halt
	})
	if count != 1 {
		t.Errorf("ForEach with false-return called callback %d times, want 1", count)
	}
}
