package proc

import "testing"

// MAZ-129 Phase 0 — red tests for the vfork-failure-wake routing contract.
//
// DoCloneExecWork currently wakes the vfork parent only on the SUCCESS path.
// On failure (bad ELF, OOM, etc.) it returns errno early and run()'s generic
// wakeBlockedThread fires on the transient thread — the parked parent is never
// woken and hangs indefinitely.
//
// The fix introduces IsVforkCall() on CloneExecRequest so DoCloneExecWork can
// check at every early-return site whether it must route the wake to the parent.
// These tests lock in the detection contract; they are RED today because
// IsVforkCall() does not exist.

// TestIsVforkCallDetectsVforkSubmission verifies that a request with both
// ReservedPID and TransientTID set is identified as a vfork clone_exec — the
// condition under which DoCloneExecWork must wake the parent on failure.
func TestIsVforkCallDetectsVforkSubmission(t *testing.T) {
	req := &CloneExecRequest{
		ReservedPID:  42,
		TransientTID: 99,
	}
	if !req.IsVforkCall() {
		t.Error("request with ReservedPID + TransientTID must be identified as a vfork call")
	}
}

// TestIsVforkCallRejectsBootPath verifies that the boot-path request (zero
// ReservedPID, zero TransientTID) is NOT identified as a vfork call, so the
// parent-wake code is never triggered for non-vfork clone_exec invocations.
func TestIsVforkCallRejectsBootPath(t *testing.T) {
	req := &CloneExecRequest{}
	if req.IsVforkCall() {
		t.Error("boot-path request (zero ReservedPID + zero TransientTID) must not be a vfork call")
	}
}

// TestIsVforkCallRequiresBothFields verifies that setting only one of the two
// fields is not sufficient — both ReservedPID and TransientTID must be non-zero.
// A partial setup would indicate a bookkeeping inconsistency that should not
// silently trigger parent-wake logic.
func TestIsVforkCallRequiresBothFields(t *testing.T) {
	onlyPID := &CloneExecRequest{ReservedPID: 42}
	if onlyPID.IsVforkCall() {
		t.Error("request with ReservedPID but zero TransientTID must not be a vfork call")
	}

	onlyTID := &CloneExecRequest{TransientTID: 99}
	if onlyTID.IsVforkCall() {
		t.Error("request with TransientTID but zero ReservedPID must not be a vfork call")
	}
}
