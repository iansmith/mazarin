// Phase-0 spec-lock tests for the canonical clone_exec intent encoding
// (MAZ-118). These pin the single-source-of-truth contract that BOTH
// kmazarin/proc and maz/linux/internal/cloneexec import. They mirror the
// invariants the proc-side TestCloneExec* tests used to assert locally —
// now they assert them on the shared definition, and the proc/shepherd sides
// inherit them by importing (see proc's TestProcIntentOpIsLinuxabiAlias and
// cloneexec's window_test.go).

package linuxabi

import (
	"testing"
	"unsafe"
)

// TestIntentKindsAreDistinct — the four kinds must be distinct values so a
// consumer can switch on Kind without collisions.
func TestIntentKindsAreDistinct(t *testing.T) {
	kinds := map[IntentKind]string{
		IntentNone:   "IntentNone",
		IntentDup3:   "IntentDup3",
		IntentClose:  "IntentClose",
		IntentFSetFD: "IntentFSetFD",
	}
	if len(kinds) != 4 {
		t.Fatalf("intent-kind set deduped to %d entries; expected 4 distinct values", len(kinds))
	}
}

// TestIntentNoneIsZeroValue — a zero-initialized op must read as IntentNone
// so the kernel's zeroed trailing StartupIntent slots are not mistaken for
// real ops.
func TestIntentNoneIsZeroValue(t *testing.T) {
	var op IntentOp
	if op.Kind != IntentNone {
		t.Errorf("zero-value IntentOp.Kind = %d, want IntentNone (%d)", op.Kind, IntentNone)
	}
}

// TestIntentOpSize — size is part of the contract; the kernel's per-Shepherd
// StartupIntent array budget assumes 16-byte ops.
func TestIntentOpSize(t *testing.T) {
	const want = 16
	if got := unsafe.Sizeof(IntentOp{}); got != want {
		t.Errorf("sizeof(IntentOp) = %d, want %d", got, want)
	}
}

// TestCapsMatchKernelBudget — the caps are the values the kernel re-checks;
// they are pinned here so a change is a deliberate, reviewed edit.
func TestCapsMatchKernelBudget(t *testing.T) {
	if MaxStartupIntentOps != 16 {
		t.Errorf("MaxStartupIntentOps = %d, want 16", MaxStartupIntentOps)
	}
	if MaxStartupCwdBytes != 256 {
		t.Errorf("MaxStartupCwdBytes = %d, want 256", MaxStartupCwdBytes)
	}
}
