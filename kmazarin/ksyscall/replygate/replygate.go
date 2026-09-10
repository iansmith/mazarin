// Package replygate holds the pure acceptance decision for delegate replies
// (MAZ-155). delegateCallInfos is keyed by caller TID, and TIDs recycle LIFO
// immediately at thread death, so SyscallReply must validate the caller
// INCARNATION — not just the handler — before touching a slot: a handler that
// holds a request past its caller's death and replies late would otherwise
// fulfill an unrelated delegate that reused the TID, corrupting its return
// value and unmapping the wrong data page.
//
// The logic lives in this leaf package (no kernel imports) so it is
// host-testable, following the kmazarin/ksync pattern.
package replygate

// Verdict classifies a delegate reply against the slot it targets.
type Verdict uint8

const (
	// Accept — in-use slot, caller SID matches the reply, replier is the
	// registered handler: the reply is genuine.
	Accept Verdict = iota
	// RejectSlotFree — no delegate is in flight at this TID (already cleaned
	// by death cleanup). Waking would poke a return value into whatever the
	// TID is blocked on now.
	RejectSlotFree
	// RejectCallerMismatch — the TID was reused: the slot belongs to a
	// different caller incarnation. The slot must not be touched — it belongs
	// to the new delegate, whose real reply is still coming. Shepherd PIDs
	// are monotonic (MAZ-150), so a dead caller's SID can never match.
	RejectCallerMismatch
	// RejectHandlerMismatch — right caller incarnation, wrong replier: a
	// shepherd that guesses a caller TID must not be able to forge a reply
	// (the pre-existing HandlerSID security check).
	RejectHandlerMismatch
	// RejectGenerationMismatch — right caller, right replier, but the reply
	// echoes a different slot GENERATION than the delegate in flight
	// (MAZ-201). The SID witness cannot distinguish two successive delegates
	// from the SAME thread, so a stray reply — late, duplicated, or mis-laned
	// inside the handler — carrying the caller's own identity would land as
	// the return value of whatever delegate the thread is blocked on now. A
	// syscall-type witness would only narrow the hole (two consecutive Reads
	// stay indistinguishable), so the witness is the per-claim generation the
	// kernel stamped into the request, echoed back by the handler.
	RejectGenerationMismatch
)

// Check decides whether a reply carrying replyCallerSID from replierSID may
// fulfill the slot state (inUse, infoCallerSID, infoHandlerSID, infoGen).
// Precedence: slot-free, then caller mismatch, then handler mismatch, then
// generation mismatch — so the stale-reply counter classifies accurately (a
// freed slot is not evidence of TID reuse, and a reused slot is stale
// regardless of who the new handler is). replyGen 0 means the replier
// supplied no witness (slot generations are always nonzero): accepted on the
// SID and handler checks alone, for compatibility.
func Check(inUse bool, infoCallerSID, replyCallerSID, infoHandlerSID, replierSID int16, infoGen, replyGen uint32) Verdict {
	if !inUse {
		return RejectSlotFree
	}
	if infoCallerSID != replyCallerSID {
		return RejectCallerMismatch
	}
	if infoHandlerSID != replierSID {
		return RejectHandlerMismatch
	}
	if replyGen != 0 && replyGen != infoGen {
		return RejectGenerationMismatch
	}
	return Accept
}
