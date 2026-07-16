package pipe

import "testing"

// MAZ-155 — parked-request ownership registry spec. Every request the shepherd
// parks (pipe readers on an empty buffer, pipe writers on a full one) is
// registered in a ParkSet keyed by the opaque token with its owner shepherd's
// SID. Removal from the set IS the abandon signal (the MAZ-149 writer-registry
// protocol, now shared): when the owner dies, DropOwner sweeps its tokens, and
// the wake paths skip any token that is no longer live instead of replying —
// a late Reply would target a stale-or-REUSED caller TID and corrupt an
// unrelated in-flight delegate.

type parkTok struct{ name string }

// TestParkSetTakeLiveOnce — TakeLive removes the token and reports it was
// live; a second take (or a take of an unknown token) reports abandoned.
// This is the fulfilled-or-abandoned signal the wake loops rely on.
func TestParkSetTakeLiveOnce(t *testing.T) {
	s := NewParkSet()
	tok := &parkTok{"r1"}
	s.Park(tok, 5)
	if !s.TakeLive(tok) {
		t.Fatalf("TakeLive(parked token) = false, want true")
	}
	if s.TakeLive(tok) {
		t.Fatalf("second TakeLive = true, want false (already taken)")
	}
	if s.TakeLive(&parkTok{"never-parked"}) {
		t.Fatalf("TakeLive(unknown token) = true, want false")
	}
}

// TestParkSetDropOwnerAbandons — DropOwner abandons exactly the dying SID's
// tokens; other owners' parks stay live. Mirrors dropParkedPipeWritersForSID /
// the new reader sweep at shepherd teardown.
func TestParkSetDropOwnerAbandons(t *testing.T) {
	s := NewParkSet()
	dead1, dead2, live := &parkTok{"d1"}, &parkTok{"d2"}, &parkTok{"l"}
	s.Park(dead1, 5)
	s.Park(dead2, 5)
	s.Park(live, 7)
	s.DropOwner(5)
	if s.TakeLive(dead1) || s.TakeLive(dead2) {
		t.Fatalf("dropped owner's tokens still live, want abandoned")
	}
	if !s.TakeLive(live) {
		t.Fatalf("other owner's token abandoned by DropOwner(5), want live")
	}
}

// TestParkSetSweepCollectsMatching — Sweep removes and returns the tokens the
// predicate selects, leaving the rest live. This is the watchdog's expiry
// primitive (predicate = parked longer than the timeout).
func TestParkSetSweepCollectsMatching(t *testing.T) {
	s := NewParkSet()
	old1, old2, fresh := &parkTok{"o1"}, &parkTok{"o2"}, &parkTok{"f"}
	s.Park(old1, 5)
	s.Park(old2, 6)
	s.Park(fresh, 7)
	expired := s.Sweep(func(tok any) bool {
		return tok == old1 || tok == old2
	})
	if len(expired) != 2 {
		t.Fatalf("Sweep returned %d tokens, want 2", len(expired))
	}
	got := map[any]bool{expired[0]: true, expired[1]: true}
	if !got[old1] || !got[old2] {
		t.Fatalf("Sweep returned %v, want {o1, o2}", expired)
	}
	if s.TakeLive(old1) || s.TakeLive(old2) {
		t.Fatalf("swept tokens still live, want removed")
	}
	if !s.TakeLive(fresh) {
		t.Fatalf("unswept token abandoned, want live")
	}
}

// TestParkSetAbandonedReaderSkippedAtWake — end-to-end shape of the reader
// wake loop: two readers from different shepherds park on an empty pipe; one
// shepherd dies (DropOwner) BEFORE data arrives. A write releases both tokens
// from the pipe's waiter list (the pipe package doesn't know about owners),
// and the ParkSet liveness filter is what keeps the dead shepherd's request
// from being replied to.
func TestParkSetAbandonedReaderSkippedAtWake(t *testing.T) {
	r, w := New(0)
	s := NewParkSet()
	deadRdr, liveRdr := &parkTok{"dead-sid-5"}, &parkTok{"live-sid-7"}
	s.Park(deadRdr, 5)
	r.Park(deadRdr)
	s.Park(liveRdr, 7)
	r.Park(liveRdr)

	s.DropOwner(5) // SID 5 dies while parked.

	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatalf("write err: %v", err)
	}
	woken := r.TakeWaiters()
	if len(woken) != 2 {
		t.Fatalf("TakeWaiters returned %d tokens, want 2 (pipe layer is owner-blind)", len(woken))
	}
	var fulfilled []any
	for _, tok := range woken {
		if s.TakeLive(tok) {
			fulfilled = append(fulfilled, tok)
		}
	}
	if len(fulfilled) != 1 || fulfilled[0] != liveRdr {
		t.Fatalf("fulfilled = %v, want exactly [live-sid-7] (dead shepherd's read must be dropped, not replied)", fulfilled)
	}
}
