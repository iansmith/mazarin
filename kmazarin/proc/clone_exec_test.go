package proc

import (
	"testing"
	"unsafe"
)

// MAZ-75 Phase 0 — spec-locking tests for the clone_exec request types
// and the new Shepherd.StartupIntent / StartupCwd storage fields. These
// tests are intentionally light: they assert struct shape + zero-value
// invariants. The end-to-end behavior of clone_exec is verified by the
// xfertest stage landed in Item 7 of the Plan.

func TestCloneExecIntentKindsAreDistinct(t *testing.T) {
	kinds := map[CloneExecIntentKind]string{
		IntentNone:   "IntentNone",
		IntentDup3:   "IntentDup3",
		IntentClose:  "IntentClose",
		IntentFSetFD: "IntentFSetFD",
	}
	if len(kinds) != 4 {
		t.Fatalf("intent-kind set deduped to %d entries; expected 4 distinct values", len(kinds))
	}
}

func TestIntentNoneIsZeroValue(t *testing.T) {
	// A zero-initialized op must be safely interpretable as IntentNone so
	// that unused trailing slots in Shepherd.StartupIntent don't get
	// misinterpreted as real ops.
	var op CloneExecIntentOp
	if op.Kind != IntentNone {
		t.Errorf("zero-value CloneExecIntentOp.Kind = %d, want IntentNone (%d)",
			op.Kind, IntentNone)
	}
}

func TestCloneExecIntentOpSize(t *testing.T) {
	// Size is part of the contract — the per-Shepherd StartupIntent array
	// budget assumes 16-byte ops. If the size changes, the assertion
	// changes deliberately + the Shepherd struct size grows accordingly.
	const want = 16
	if got := unsafe.Sizeof(CloneExecIntentOp{}); got != want {
		t.Errorf("sizeof(CloneExecIntentOp) = %d, want %d", got, want)
	}
}

func TestCloneExecRequestFieldsCompile(t *testing.T) {
	// Construct a CloneExecRequest with every field set by name. Failure
	// to compile is the RED-state signal that a field is missing — the
	// test body itself doesn't assert behavior, only the spec shape.
	var parent Shepherd
	_ = CloneExecRequest{
		ELFStartVA:     0x4000_0000,
		ELFNumBytes:    65536,
		ELFNumPages:    16,
		CallerL0PA:     0x1000,
		CallerShepherd: &parent,
		Argv:           [][]byte{[]byte("hello")},
		Envp:           [][]byte{[]byte("PATH=/bin")},
		Intent: []CloneExecIntentOp{
			{Kind: IntentDup3, Arg0: 3, Arg1: 1, Arg2: 0},
			{Kind: IntentClose, Arg0: 4},
			{Kind: IntentFSetFD, Arg0: 5, Arg1: 0},
		},
		Cwd:      []byte("/tmp"),
		Filename: []byte("hello.elf"),
	}
}

func TestShepherdStartupIntentZeroOnAllocate(t *testing.T) {
	// A freshly-allocated Shepherd via ShepherdStorage must have empty
	// StartupIntent / StartupCwd. Relies on Allocate's slot-zeroing
	// behavior (the storage clears the slot to zero before returning) —
	// if that ever changes, this catches it.
	s := NewShepherdStorage()
	pid := MinPID
	sh, err := s.Allocate(pid)
	if err != nil {
		t.Fatalf("Allocate(%d) returned err: %v", pid, err)
	}
	if sh.NumStartupIntent != 0 {
		t.Errorf("fresh Shepherd.NumStartupIntent = %d, want 0", sh.NumStartupIntent)
	}
	if sh.StartupCwdLen != 0 {
		t.Errorf("fresh Shepherd.StartupCwdLen = %d, want 0", sh.StartupCwdLen)
	}
	for i, op := range sh.StartupIntent {
		if op.Kind != IntentNone {
			t.Errorf("fresh Shepherd.StartupIntent[%d].Kind = %d, want IntentNone (0)",
				i, op.Kind)
		}
	}
	for i, b := range sh.StartupCwd {
		if b != 0 {
			t.Errorf("fresh Shepherd.StartupCwd[%d] = 0x%02x, want 0", i, b)
		}
	}
}
