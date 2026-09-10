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
	if v := Check(true, 5, 5, 3, 3, 7, 7); v != Accept {
		t.Fatalf("Check(matching) = %v, want Accept", v)
	}
}

// TestCheckRejectsSlotFree — no delegate is in flight at this TID (the slot
// was already cleaned by death cleanup): a reply must be rejected, NOT waked
// through. Today's code wakes the target thread unconditionally in this case,
// poking a return value into whatever that TID is blocked on now.
func TestCheckRejectsSlotFree(t *testing.T) {
	if v := Check(false, 5, 5, 3, 3, 7, 7); v != RejectSlotFree {
		t.Fatalf("Check(slot free) = %v, want RejectSlotFree", v)
	}
}

// TestCheckRejectsCallerMismatch — the TID was reused: the slot now belongs
// to a different caller incarnation (slot CallerSID 9, reply for the dead
// caller 5). The reply must be rejected WITHOUT touching the slot — it
// belongs to the new delegate, whose real reply is still coming.
func TestCheckRejectsCallerMismatch(t *testing.T) {
	if v := Check(true, 9, 5, 3, 3, 7, 7); v != RejectCallerMismatch {
		t.Fatalf("Check(caller mismatch) = %v, want RejectCallerMismatch", v)
	}
}

// TestCheckCallerMismatchPrecedesHandlerMismatch — when the slot belongs to a
// different caller AND the replier isn't its handler, the reply is classified
// as a stale-caller reply (the slot isn't the replier's delegate at all), so
// the stale-reply counter stays an accurate measure of the TID-reuse hazard.
func TestCheckCallerMismatchPrecedesHandlerMismatch(t *testing.T) {
	if v := Check(true, 9, 5, 4, 3, 7, 7); v != RejectCallerMismatch {
		t.Fatalf("Check(both mismatch) = %v, want RejectCallerMismatch", v)
	}
}

// TestCheckSlotFreePrecedesCallerMismatch — slot free AND caller mismatch:
// slot-free must win, so the stale-reply counter's classification stays
// accurate (a freed slot is not evidence of TID reuse).
func TestCheckSlotFreePrecedesCallerMismatch(t *testing.T) {
	if v := Check(false, 9, 5, 3, 3, 7, 7); v != RejectSlotFree {
		t.Fatalf("Check(slot free + caller mismatch) = %v, want RejectSlotFree", v)
	}
}

// TestCheckSlotFreePrecedesHandlerMismatch — slot free AND handler mismatch
// (caller matches): slot-free must win.
func TestCheckSlotFreePrecedesHandlerMismatch(t *testing.T) {
	if v := Check(false, 5, 5, 3, 4, 7, 7); v != RejectSlotFree {
		t.Fatalf("Check(slot free + handler mismatch) = %v, want RejectSlotFree", v)
	}
}

// TestCheckRejectsHandlerMismatch — right caller incarnation, wrong replier:
// a shepherd that guesses a caller TID must not be able to forge a reply
// (the pre-existing HandlerSID security check, preserved).
func TestCheckRejectsHandlerMismatch(t *testing.T) {
	if v := Check(true, 5, 5, 3, 4, 7, 7); v != RejectHandlerMismatch {
		t.Fatalf("Check(handler mismatch) = %v, want RejectHandlerMismatch", v)
	}
}

// MAZ-201 — SysID witness. The SID+handler gate cannot distinguish two
// SUCCESSIVE delegates from the SAME thread: a stray reply (late, duplicated,
// or mis-laned inside the handler) carrying the caller's own SID and TID
// passes every check above and lands as the return value of whatever delegate
// that thread is blocked on NOW. Observed as MAZ-201's boot fatal: a fork/exec
// child's runtime checkfds fcntl(0) returned ENOENT — a value no fcntl path
// in kernel or linux shepherd can produce — because a stray same-identity
// reply was delivered to it. The reply now carries the SysID of the request
// the handler processed (SyscallReply arg3); a mismatch against the slot's
// recorded SysID rejects the reply. replySysID 0 (sysid.Invalid) means the
// replier sent no witness — accepted for compatibility.

// TestCheckAcceptsMatchingSysID — full match including the SysID witness.
func TestCheckAcceptsMatchingSysID(t *testing.T) {
	if v := Check(true, 5, 5, 3, 3, 7, 7); v != Accept {
		t.Fatalf("Check(matching sysid) = %v, want Accept", v)
	}
}

// TestCheckRejectsSysIDMismatch — same caller, same handler, but the reply is
// for a DIFFERENT syscall than the one in flight at this slot: the stray-reply
// case MAZ-155's SID witness cannot see. Must be rejected without touching
// the slot — the in-flight delegate's genuine reply is still coming.
func TestCheckRejectsSysIDMismatch(t *testing.T) {
	if v := Check(true, 5, 5, 3, 3, 6, 7); v != RejectSysIDMismatch {
		t.Fatalf("Check(sysid mismatch) = %v, want RejectSysIDMismatch", v)
	}
}

// TestCheckAcceptsLegacyNoWitness — replySysID 0 = no witness supplied
// (sysid.Invalid is never a real delegated syscall): accept on the SID and
// handler checks alone, preserving compatibility with repliers that predate
// the witness.
func TestCheckAcceptsLegacyNoWitness(t *testing.T) {
	if v := Check(true, 5, 5, 3, 3, 6, 0); v != Accept {
		t.Fatalf("Check(no witness) = %v, want Accept", v)
	}
}

// TestCheckCallerMismatchPrecedesSysIDMismatch — a reused slot with a stale
// caller AND a differing SysID classifies as caller mismatch: the SID witness
// is the stronger incarnation signal and keeps the stale-reply counter
// accurate.
func TestCheckCallerMismatchPrecedesSysIDMismatch(t *testing.T) {
	if v := Check(true, 9, 5, 3, 3, 6, 7); v != RejectCallerMismatch {
		t.Fatalf("Check(caller + sysid mismatch) = %v, want RejectCallerMismatch", v)
	}
}

// TestCheckHandlerMismatchPrecedesSysIDMismatch — wrong replier AND wrong
// SysID: the forged-reply security classification wins.
func TestCheckHandlerMismatchPrecedesSysIDMismatch(t *testing.T) {
	if v := Check(true, 5, 5, 3, 4, 6, 7); v != RejectHandlerMismatch {
		t.Fatalf("Check(handler + sysid mismatch) = %v, want RejectHandlerMismatch", v)
	}
}
