package pipe

import (
	"testing"
	"time"
)

// MAZ-155 review follow-up — wake-window exclusion. A wake loop's
// TakeLive→re-Park window must be atomic with respect to DropOwner:
// without exclusion, DropOwner can run between TakeLive removing the token
// and Park re-adding it, leaving a zombie registration for a dead owner
// that no future DropOwner will ever clean (SIDs are monotonic). The
// wake loop brackets each token's window with BeginWake/EndWake;
// DropOwner waits out every in-flight window before sweeping, so a
// re-park either lands before the sweep (and is swept) or the token was
// already removed by the sweep (TakeLive returned false, no re-park).

// TestParkSetDropOwnerWaitsForWakeWindow — DropOwner must not complete
// while a wake window is open, and a re-park inside the window must be
// visible to (and swept by) the waiting DropOwner. This is the zombie-token
// TOCTOU from the PR #90 review, made deterministic.
func TestParkSetDropOwnerWaitsForWakeWindow(t *testing.T) {
	s := NewParkSet()
	tok := &parkTok{"limbo"}
	s.Park(tok, 5)

	s.BeginWake()
	if !s.TakeLive(tok) {
		t.Fatalf("TakeLive(parked token) = false, want true")
	}

	done := make(chan struct{})
	go func() {
		s.DropOwner(5)
		close(done)
	}()

	select {
	case <-done:
		t.Fatalf("DropOwner completed while a wake window was open, want blocked")
	case <-time.After(50 * time.Millisecond):
	}

	// Re-park inside the window (the drain found the pipe still empty).
	s.Park(tok, 5)
	s.EndWake()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("DropOwner still blocked after EndWake, want completed")
	}

	if s.TakeLive(tok) {
		t.Fatalf("re-parked token survived DropOwner, want swept (zombie)")
	}
}

// TestParkSetWakeWindowsDoNotBlockEachOther — concurrent wake windows on
// the same set must run in parallel (reader side of the exclusion); only
// DropOwner excludes them.
func TestParkSetWakeWindowsDoNotBlockEachOther(t *testing.T) {
	s := NewParkSet()
	s.BeginWake()
	second := make(chan struct{})
	go func() {
		s.BeginWake()
		s.EndWake()
		close(second)
	}()
	select {
	case <-second:
	case <-time.After(2 * time.Second):
		t.Fatalf("second wake window blocked by the first, want concurrent")
	}
	s.EndWake()
}
