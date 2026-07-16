// Spec-lock tests for the clone_exec SVC params wire format (MAZ-120). These
// pin the single-source-of-truth encoding the userspace marshaler
// (mazarin/sys.CloneExec) and the kernel unmarshaler (kmazarin/ksyscall) both
// depend on, plus the argv/envp packing the proc-side helpers re-export.

package linuxabi

import (
	"bytes"
	"testing"
)

func equalBlobs(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// TestPackArgvRoundtrip — UnpackArgv is the exact inverse of PackArgv for a
// vector with no empty elements; argv[0] survives byte-identical.
func TestPackArgvRoundtrip(t *testing.T) {
	argv := [][]byte{[]byte("compile"), []byte("-o"), []byte("x.a")}
	got := UnpackArgv(PackArgv(argv))
	if !equalBlobs(got, argv) {
		t.Fatalf("roundtrip mismatch: argv=%q got=%q", argv, got)
	}
}

// TestPackArgvEmpty — an empty vector packs to a nil blob and unpacks to empty
// (not a one-element [""] slice).
func TestPackArgvEmpty(t *testing.T) {
	if b := PackArgv(nil); b != nil {
		t.Errorf("PackArgv(nil) = %q, want nil", b)
	}
	if got := UnpackArgv(nil); len(got) != 0 {
		t.Errorf("UnpackArgv(nil) yielded %d elements, want 0", len(got))
	}
}

// TestMarshalCloneExecParamsRoundtrip — a full params region (argv + envp +
// intent + cwd + filename) survives Marshal→Unmarshal byte-for-byte.
func TestMarshalCloneExecParamsRoundtrip(t *testing.T) {
	argv := [][]byte{[]byte("compile"), []byte("-o"), []byte("out.a")}
	envp := [][]byte{[]byte("GOROOT=/go"), []byte("GODEBUG=gccheckmark=1")}
	intent := []IntentOp{
		{Kind: IntentDup3, Arg0: 7, Arg1: 1, Arg2: 0},
		{Kind: IntentClose, Arg0: 8},
		{Kind: IntentFSetFD, Arg0: 9, Arg1: 1},
	}
	cwd := []byte("/work/src")
	filename := []byte("/usr/bin/compile")

	const wantTID int16 = 42
	const wantSID int16 = 7
	const wantMask uint8 = 0b11 // both fd 1 and fd 2 redirected
	blob, err := MarshalCloneExecParams(PackArgv(argv), PackArgv(envp), intent, cwd, filename, wantTID, wantSID, wantMask)
	if err != nil {
		t.Fatalf("MarshalCloneExecParams: %v", err)
	}
	got, err := UnmarshalCloneExecParams(blob)
	if err != nil {
		t.Fatalf("UnmarshalCloneExecParams: %v", err)
	}
	if !equalBlobs(got.Argv, argv) {
		t.Errorf("argv mismatch: got=%q want=%q", got.Argv, argv)
	}
	if !equalBlobs(got.Envp, envp) {
		t.Errorf("envp mismatch: got=%q want=%q", got.Envp, envp)
	}
	if !bytes.Equal(got.Cwd, cwd) {
		t.Errorf("cwd mismatch: got=%q want=%q", got.Cwd, cwd)
	}
	if !bytes.Equal(got.Filename, filename) {
		t.Errorf("filename mismatch: got=%q want=%q", got.Filename, filename)
	}
	if len(got.Intent) != len(intent) {
		t.Fatalf("intent count = %d, want %d", len(got.Intent), len(intent))
	}
	for i := range intent {
		if got.Intent[i] != intent[i] {
			t.Errorf("intent[%d] = %+v, want %+v", i, got.Intent[i], intent[i])
		}
	}
	if got.VforkCallerTID != wantTID {
		t.Errorf("VforkCallerTID = %d, want %d", got.VforkCallerTID, wantTID)
	}
	if got.VforkCallerSID != wantSID {
		t.Errorf("VforkCallerSID = %d, want %d", got.VforkCallerSID, wantSID)
	}
	if got.StdioRedirectMask != wantMask {
		t.Errorf("StdioRedirectMask = %#b, want %#b", got.StdioRedirectMask, wantMask)
	}
}

// TestMarshalCloneExecParamsEmpty — a fully-empty request (no argv/envp/intent/
// cwd/filename) still produces a valid header-only blob that round-trips.
func TestMarshalCloneExecParamsEmpty(t *testing.T) {
	blob, err := MarshalCloneExecParams(nil, nil, nil, nil, nil, 0, 0, 0)
	if err != nil {
		t.Fatalf("MarshalCloneExecParams(empty): %v", err)
	}
	if len(blob) != CloneExecParamsHeaderSize {
		t.Errorf("empty blob len = %d, want %d", len(blob), CloneExecParamsHeaderSize)
	}
	got, err := UnmarshalCloneExecParams(blob)
	if err != nil {
		t.Fatalf("UnmarshalCloneExecParams(empty): %v", err)
	}
	if len(got.Argv) != 0 || len(got.Envp) != 0 || len(got.Intent) != 0 ||
		len(got.Cwd) != 0 || len(got.Filename) != 0 {
		t.Errorf("empty round-trip non-empty: %+v", got)
	}
	if got.StdioRedirectMask != 0 {
		t.Errorf("empty StdioRedirectMask = %#b, want 0", got.StdioRedirectMask)
	}
}

// TestCloneExecParamsHeaderCarriesStdioRedirectMask (MAZ-149 Phase 0 RED) —
// the params header reserves room for the per-process stdio redirect mask
// (bit 0 = fd 1 redirected, bit 1 = fd 2 redirected), which sysExecve computes
// from the child's final FD-table state and the kernel stores on the child
// Shepherd at creation (SetStartupState) so SyscallWrite can split console
// (fast path) from redirected (blocking delegate) without consulting the
// shepherd. Wire layout: header grows 24 → 32 (stays 8-byte aligned), mask
// byte at [24], [25:32] reserved-zero.
//
// This locks only the header sizing; the mask round-trip assertions land with
// the implementation (they cannot compile until the field exists). The
// behavioral acceptance gate is the forkexectest stage-2 boot smoke.
func TestCloneExecParamsHeaderCarriesStdioRedirectMask(t *testing.T) {
	if CloneExecParamsHeaderSize != 32 {
		t.Fatalf("CloneExecParamsHeaderSize = %d, want 32 (room for StdioRedirectMask at byte [24])",
			CloneExecParamsHeaderSize)
	}
}

// TestUnmarshalCloneExecParamsRejectsShort — a blob shorter than the header is
// rejected, not silently decoded from garbage.
func TestUnmarshalCloneExecParamsRejectsShort(t *testing.T) {
	if _, err := UnmarshalCloneExecParams(make([]byte, CloneExecParamsHeaderSize-1)); err != ErrCloneExecParamsMalformed {
		t.Errorf("short blob err = %v, want ErrCloneExecParamsMalformed", err)
	}
}

// TestUnmarshalCloneExecParamsRejectsOverrun — a header that declares section
// lengths past the blob end is rejected.
func TestUnmarshalCloneExecParamsRejectsOverrun(t *testing.T) {
	blob, err := MarshalCloneExecParams([]byte("a\x00b"), nil, nil, nil, nil, 0, 0, 0)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Truncate the body so the declared argvLen runs past the blob.
	truncated := blob[:len(blob)-1]
	if _, err := UnmarshalCloneExecParams(truncated); err != ErrCloneExecParamsMalformed {
		t.Errorf("overrun blob err = %v, want ErrCloneExecParamsMalformed", err)
	}
}

// TestMarshalCloneExecParamsRejectsOversize — a params region past
// CloneExecArgMax is rejected with ErrCloneExecArgTooBig (E2BIG at the SVC).
func TestMarshalCloneExecParamsRejectsOversize(t *testing.T) {
	huge := make([]byte, CloneExecArgMax+1)
	if _, err := MarshalCloneExecParams(huge, nil, nil, nil, nil, 0, 0, 0); err != ErrCloneExecArgTooBig {
		t.Errorf("oversize err = %v, want ErrCloneExecArgTooBig", err)
	}
}
