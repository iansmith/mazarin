// Phase-0 RED drift guard for MAZ-118's single-source-of-truth decision.
//
// MAZ-118 moves the canonical clone_exec intent encoding into
// shared/linuxabi and reconciles kmazarin/proc to it via Go TYPE ALIASES +
// const re-exports. This external test (package proc_test so it can import
// both proc and linuxabi) asserts that reconciliation actually happened:
//
//   - proc.CloneExecIntentOp is the SAME type as linuxabi.IntentOp
//     (a type alias makes reflect.TypeOf compare equal — a fresh `type X
//     struct{...}` redefinition would NOT).
//   - proc.CloneExecIntentKind is the SAME type as linuxabi.IntentKind.
//   - the kind sentinels and the caps are the same values.
//
// RED today: proc currently defines its own independent struct/kind/consts,
// so the type-identity assertions fail. The test runs (it does not panic /
// compile-error) and reports a clean per-assertion FAIL — that is the RED
// state this commit locks. The implementation phase turns these green by
// replacing proc's definitions with aliases + re-exports of linuxabi's.

package proc_test

import (
	"reflect"
	"testing"

	"mazzy/kmazarin/proc"
	"mazzy/shared/linuxabi"
)

// TestProcIntentOpIsLinuxabiAlias — the proc op type must be the shared type
// itself, not a structural twin. reflect.TypeOf equality holds iff
// CloneExecIntentOp is an alias for linuxabi.IntentOp.
func TestProcIntentOpIsLinuxabiAlias(t *testing.T) {
	got := reflect.TypeOf(proc.CloneExecIntentOp{})
	want := reflect.TypeOf(linuxabi.IntentOp{})
	if got != want {
		t.Errorf("proc.CloneExecIntentOp resolves to %v, want it aliased to linuxabi.IntentOp (%v)", got, want)
	}
}

// TestProcIntentKindIsLinuxabiAlias — same for the discriminant type.
func TestProcIntentKindIsLinuxabiAlias(t *testing.T) {
	got := reflect.TypeOf(proc.CloneExecIntentKind(0))
	want := reflect.TypeOf(linuxabi.IntentKind(0))
	if got != want {
		t.Errorf("proc.CloneExecIntentKind resolves to %v, want it aliased to linuxabi.IntentKind (%v)", got, want)
	}
}

// TestProcIntentSentinelsMatchShared — the kind sentinels must carry the
// shared values (caught even if the alias is forgotten and only the const is
// re-exported, or vice versa).
func TestProcIntentSentinelsMatchShared(t *testing.T) {
	cases := []struct {
		name      string
		procKind  proc.CloneExecIntentKind
		sharedVal linuxabi.IntentKind
	}{
		{"IntentNone", proc.IntentNone, linuxabi.IntentNone},
		{"IntentDup3", proc.IntentDup3, linuxabi.IntentDup3},
		{"IntentClose", proc.IntentClose, linuxabi.IntentClose},
		{"IntentFSetFD", proc.IntentFSetFD, linuxabi.IntentFSetFD},
	}
	for _, c := range cases {
		if uint8(c.procKind) != uint8(c.sharedVal) {
			t.Errorf("proc.%s = %d, want shared value %d", c.name, c.procKind, c.sharedVal)
		}
	}
}

// TestProcCapsMatchShared — the caps proc exposes (used as array dimensions
// and ksyscall cap-checks) must be the shared values.
func TestProcCapsMatchShared(t *testing.T) {
	if proc.MaxStartupIntentOps != linuxabi.MaxStartupIntentOps {
		t.Errorf("proc.MaxStartupIntentOps = %d, want shared %d", proc.MaxStartupIntentOps, linuxabi.MaxStartupIntentOps)
	}
	if proc.MaxStartupCwdBytes != linuxabi.MaxStartupCwdBytes {
		t.Errorf("proc.MaxStartupCwdBytes = %d, want shared %d", proc.MaxStartupCwdBytes, linuxabi.MaxStartupCwdBytes)
	}
}
