package proc

import "testing"

// MAZ-112 Phase 0 — RED spec for the clone_exec intent-visibility race fix.
//
// The race (DoCloneExecWork, kmazarin/ksyscall/clone_exec.go): the child
// thread is enqueued to the ready queue by CreateUserspaceThread BEFORE the
// race-sensitive fields (ParentPID, StartupIntent/NumStartupIntent,
// StartupCwd/StartupCwdLen) are populated — so a consumer racing the child's
// first instruction could observe them unset (MAZ-113 is the future reader).
//
// The fix introduces CreateCloneExecThread (kmazarin/kmazarin/threads.go),
// which populates those fields UNDER schedulerLock, BEFORE enqueue. To keep
// the lock-held critical section minimal (nosplit discipline; the live
// x86_64 nosplit-overflow bug means the new in-lock code must stay tiny),
// the population is done via ONE proc-side helper:
//
//	func (s *Shepherd) SetStartupState(parentPID ShepherdId,
//	    intent []CloneExecIntentOp, cwd []byte) (intentCopied, cwdCopied int)
//
// This method does NOT exist on current code, so these tests are RED (the
// package fails to compile). They turn GREEN once the helper lands with the
// contract asserted below. The deterministic end-to-end RED→GREEN (the child
// thread observed with populated fields at the StateCheck hook) lives in the
// boot self-test kmazarin/kmazarin/clone_exec_race_selftest.go — that package
// is not host-testable, so this proc-level helper is the host-testable seam
// that locks the populate-as-a-unit contract the in-lock fix depends on.

// TestSetStartupStatePopulatesAllRaceSensitiveFields is the core RED: the
// single helper must set ParentPID + StartupIntent/NumStartupIntent +
// StartupCwd/StartupCwdLen together, so CreateCloneExecThread can populate
// every race-sensitive field in one nosplit call under schedulerLock.
func TestSetStartupStatePopulatesAllRaceSensitiveFields(t *testing.T) {
	s := NewShepherdStorage()
	sh, err := s.Allocate(MinPID)
	if err != nil {
		t.Fatalf("Allocate(%d) returned err: %v", MinPID, err)
	}

	intent := []CloneExecIntentOp{
		{Kind: IntentDup3, Arg0: 3, Arg1: 1, Arg2: 0},
		{Kind: IntentClose, Arg0: 4},
		{Kind: IntentFSetFD, Arg0: 5, Arg1: 1},
	}
	cwd := []byte("/work/dir")
	const parent ShepherdId = MinPID + 1
	const mask uint8 = 0b01 // fd 1 redirected (MAZ-149)

	nIntent, nCwd := sh.SetStartupState(parent, intent, cwd, mask)

	if sh.ParentPID != parent {
		t.Errorf("ParentPID = %d, want %d", sh.ParentPID, parent)
	}
	if sh.StdioRedirectMask != mask {
		t.Errorf("StdioRedirectMask = %#b, want %#b", sh.StdioRedirectMask, mask)
	}
	if int(sh.NumStartupIntent) != len(intent) {
		t.Errorf("NumStartupIntent = %d, want %d", sh.NumStartupIntent, len(intent))
	}
	if nIntent != len(intent) {
		t.Errorf("returned intentCopied = %d, want %d", nIntent, len(intent))
	}
	for i, op := range intent {
		if sh.StartupIntent[i] != op {
			t.Errorf("StartupIntent[%d] = %+v, want %+v", i, sh.StartupIntent[i], op)
		}
	}
	// Trailing slots must remain IntentNone (zeroed by Allocate, untouched).
	for i := len(intent); i < MaxStartupIntentOps; i++ {
		if sh.StartupIntent[i].Kind != IntentNone {
			t.Errorf("trailing StartupIntent[%d].Kind = %d, want IntentNone", i, sh.StartupIntent[i].Kind)
		}
	}
	if int(sh.StartupCwdLen) != len(cwd) {
		t.Errorf("StartupCwdLen = %d, want %d", sh.StartupCwdLen, len(cwd))
	}
	if nCwd != len(cwd) {
		t.Errorf("returned cwdCopied = %d, want %d", nCwd, len(cwd))
	}
	if got := string(sh.StartupCwd[:sh.StartupCwdLen]); got != string(cwd) {
		t.Errorf("StartupCwd = %q, want %q", got, string(cwd))
	}
}

// TestSetStartupStateEmptyIntentAndCwd locks the no-op shape: a child with no
// buffered intent and no chdir (the common case) must end up with zeroed
// counts and only ParentPID set.
func TestSetStartupStateEmptyIntentAndCwd(t *testing.T) {
	s := NewShepherdStorage()
	sh, err := s.Allocate(MinPID)
	if err != nil {
		t.Fatalf("Allocate(%d) returned err: %v", MinPID, err)
	}
	const parent ShepherdId = MinPID + 2

	nIntent, nCwd := sh.SetStartupState(parent, nil, nil, 0)

	if sh.ParentPID != parent {
		t.Errorf("ParentPID = %d, want %d", sh.ParentPID, parent)
	}
	if sh.NumStartupIntent != 0 || nIntent != 0 {
		t.Errorf("NumStartupIntent = %d (returned %d), want 0", sh.NumStartupIntent, nIntent)
	}
	if sh.StartupCwdLen != 0 || nCwd != 0 {
		t.Errorf("StartupCwdLen = %d (returned %d), want 0", sh.StartupCwdLen, nCwd)
	}
	if sh.StdioRedirectMask != 0 {
		t.Errorf("StdioRedirectMask = %#b, want 0 (console stdio)", sh.StdioRedirectMask)
	}
}

// TestSetStartupStateCapBoundedCopy locks the cap-clamping contract. The
// ksyscall caller cap-checks Intent/Cwd before calling (clone_exec.go:77-84),
// but the helper itself MUST NOT overrun the fixed arrays even when handed an
// over-cap slice — copy() into a fixed array clamps, and the recorded counts
// must reflect what was actually copied, never the (larger) input length.
func TestSetStartupStateCapBoundedCopy(t *testing.T) {
	s := NewShepherdStorage()
	sh, err := s.Allocate(MinPID)
	if err != nil {
		t.Fatalf("Allocate(%d) returned err: %v", MinPID, err)
	}

	overIntent := make([]CloneExecIntentOp, MaxStartupIntentOps+4)
	for i := range overIntent {
		overIntent[i] = CloneExecIntentOp{Kind: IntentClose, Arg0: int32(i)}
	}
	overCwd := make([]byte, MaxStartupCwdBytes+10)
	for i := range overCwd {
		overCwd[i] = 'a'
	}

	nIntent, nCwd := sh.SetStartupState(MinPID+3, overIntent, overCwd, 0)

	if nIntent != MaxStartupIntentOps {
		t.Errorf("intentCopied = %d, want clamp to %d", nIntent, MaxStartupIntentOps)
	}
	if int(sh.NumStartupIntent) != MaxStartupIntentOps {
		t.Errorf("NumStartupIntent = %d, want clamp to %d", sh.NumStartupIntent, MaxStartupIntentOps)
	}
	if nCwd != MaxStartupCwdBytes {
		t.Errorf("cwdCopied = %d, want clamp to %d", nCwd, MaxStartupCwdBytes)
	}
	if int(sh.StartupCwdLen) != MaxStartupCwdBytes {
		t.Errorf("StartupCwdLen = %d, want clamp to %d", sh.StartupCwdLen, MaxStartupCwdBytes)
	}
}
