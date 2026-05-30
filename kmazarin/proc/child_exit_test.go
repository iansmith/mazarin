// Phase 0 RED test for MAZ-77 (kernel → parent child-exit notification).
//
// These tests pin the contract of proc.BuildChildExitEvent — the pure,
// host-testable helper that turns a dying child Shepherd + its raw exit
// status into the NotificationEvent the kernel publishes to the parent's
// ring (via KernelPublishProcessNotify in the kmazarin package).
//
// They FAIL on current code because BuildChildExitEvent does not exist yet
// (compile error = RED). That is the point: the test locks the payload
// contract before any threads.go wire-up is written. The threads.go call
// site then becomes a thin, untestable-in-isolation adapter around this
// pure function.
//
// Contract (from the ticket):
//   - signature: BuildChildExitEvent(child *Shepherd, status int32)
//                  (NotificationEvent, ShepherdId, bool)
//   - Type        = EventChildExit
//   - Pid         = child's own PID
//   - ParentPid   = child's ParentPID
//   - ExitStatus  = the RAW exit code (kernel does NOT apply the Linux <<8
//                   encoding — that stays in the linux shepherd, MAZ-80)
//   - target SID  = child's ParentPID
//   - ok          = true  when ParentPID != 0
//                 = false when ParentPID == 0 (boot shepherds: no parent to
//                   notify; caller must skip delivery)

package proc

import "testing"

// TestBuildChildExitEvent_HasParent verifies the happy path: a child with a
// real parent yields a fully-populated EventChildExit event, the parent PID
// as the target SID, and ok=true.
func TestBuildChildExitEvent_HasParent(t *testing.T) {
	child := &Shepherd{
		PID:       40,
		ParentPID: 7,
	}

	ev, target, ok := BuildChildExitEvent(child, 42)

	if !ok {
		t.Fatalf("ok = false, want true (child has ParentPID=7)")
	}
	if target != 7 {
		t.Errorf("target SID = %d, want 7 (the parent PID)", target)
	}
	want := NotificationEvent{
		Type:       EventChildExit,
		Pid:        40,
		ParentPid:  7,
		ExitStatus: 42,
	}
	if ev != want {
		t.Errorf("event = %+v, want %+v", ev, want)
	}
}

// TestBuildChildExitEvent_RawExitCode pins the ExitStatus contract: the
// kernel carries the RAW exit code, NOT the Linux-encoded (status<<8) form.
// A raw status of 1 must stay 1 in the payload (0x100 would mean the kernel
// wrongly applied the Linux encoding that belongs in MAZ-80).
func TestBuildChildExitEvent_RawExitCode(t *testing.T) {
	child := &Shepherd{
		PID:       100,
		ParentPID: 3,
	}

	ev, _, ok := BuildChildExitEvent(child, 1)

	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if ev.ExitStatus != 1 {
		t.Errorf("ExitStatus = %d, want 1 (raw exit code, not Linux <<8 encoded)", ev.ExitStatus)
	}
}

// TestBuildChildExitEvent_NoParent verifies that a boot-time shepherd
// (ParentPID == 0) yields ok=false so the caller skips delivery — there is
// no parent ring to notify.
func TestBuildChildExitEvent_NoParent(t *testing.T) {
	child := &Shepherd{
		PID:       40,
		ParentPID: 0,
	}

	_, _, ok := BuildChildExitEvent(child, 42)

	if ok {
		t.Errorf("ok = true for ParentPID=0; want false (no parent to notify)")
	}
}
