package replygate

import "testing"

// MAZ-155 — delegate-reply acceptance spec. SyscallReply must validate the
// caller INCARNATION, not just the handler: delegateCallInfos is keyed by
// caller TID, and TIDs recycle LIFO immediately at thread death (MAZ-150 made
// only shepherd PIDs monotonic). A handler that holds a request past its
// caller's death and replies late would otherwise fulfill an UNRELATED
// delegate that reused the TID — corrupting its return value and unmapping
// the wrong data page. The reply already carries the replier's recorded
// caller SID (SyscallReply arg0); comparing it against the slot's CallerSID
// is the incarnation check: shepherd PIDs are monotonic, so a dead caller's
// SID can never match a reused slot.

// TestCheckAcceptsMatchingReply — in-use slot, caller SID matches the reply,
// replier is the registered handler: the reply is genuine.
func TestCheckAcceptsMatchingReply(t *testing.T) {
	if v := Check(true, 5, 5, 3, 3); v != Accept {
		t.Fatalf("Check(matching) = %v, want Accept", v)
	}
}

// TestCheckRejectsSlotFree — no delegate is in flight at this TID (the slot
// was already cleaned by death cleanup): a reply must be rejected, NOT waked
// through. Today's code wakes the target thread unconditionally in this case,
// poking a return value into whatever that TID is blocked on now.
func TestCheckRejectsSlotFree(t *testing.T) {
	if v := Check(false, 5, 5, 3, 3); v != RejectSlotFree {
		t.Fatalf("Check(slot free) = %v, want RejectSlotFree", v)
	}
}

// TestCheckRejectsCallerMismatch — the TID was reused: the slot now belongs
// to a different caller incarnation (slot CallerSID 9, reply for the dead
// caller 5). The reply must be rejected WITHOUT touching the slot — it
// belongs to the new delegate, whose real reply is still coming.
func TestCheckRejectsCallerMismatch(t *testing.T) {
	if v := Check(true, 9, 5, 3, 3); v != RejectCallerMismatch {
		t.Fatalf("Check(caller mismatch) = %v, want RejectCallerMismatch", v)
	}
}

// TestCheckCallerMismatchPrecedesHandlerMismatch — when the slot belongs to a
// different caller AND the replier isn't its handler, the reply is classified
// as a stale-caller reply (the slot isn't the replier's delegate at all), so
// the stale-reply counter stays an accurate measure of the TID-reuse hazard.
func TestCheckCallerMismatchPrecedesHandlerMismatch(t *testing.T) {
	if v := Check(true, 9, 5, 4, 3); v != RejectCallerMismatch {
		t.Fatalf("Check(both mismatch) = %v, want RejectCallerMismatch", v)
	}
}

// TestCheckSlotFreePrecedesCallerMismatch — slot free AND caller mismatch:
// slot-free must win, so the stale-reply counter's classification stays
// accurate (a freed slot is not evidence of TID reuse).
func TestCheckSlotFreePrecedesCallerMismatch(t *testing.T) {
	if v := Check(false, 9, 5, 3, 3); v != RejectSlotFree {
		t.Fatalf("Check(slot free + caller mismatch) = %v, want RejectSlotFree", v)
	}
}

// TestCheckSlotFreePrecedesHandlerMismatch — slot free AND handler mismatch
// (caller matches): slot-free must win.
func TestCheckSlotFreePrecedesHandlerMismatch(t *testing.T) {
	if v := Check(false, 5, 5, 3, 4); v != RejectSlotFree {
		t.Fatalf("Check(slot free + handler mismatch) = %v, want RejectSlotFree", v)
	}
}

// TestCheckRejectsHandlerMismatch — right caller incarnation, wrong replier:
// a shepherd that guesses a caller TID must not be able to forge a reply
// (the pre-existing HandlerSID security check, preserved).
func TestCheckRejectsHandlerMismatch(t *testing.T) {
	if v := Check(true, 5, 5, 3, 4); v != RejectHandlerMismatch {
		t.Fatalf("Check(handler mismatch) = %v, want RejectHandlerMismatch", v)
	}
}
