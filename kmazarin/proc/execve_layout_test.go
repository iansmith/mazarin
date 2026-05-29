package proc

import (
	"bytes"
	"testing"
)

// MAZ-120 Phase 0 — spec-locking RED tests for the host-testable core of the
// execve→clone_exec integration: the argv/envp wire-format roundtrip used by
// the SyscallCloneExec SVC transport, and the env-merge policy the kernel
// stack layout must enforce.
//
// These tests are RED today because the helpers they exercise
// (PackArgv/UnpackArgv/MergeExecEnv) do not exist yet — exactly the
// compile-failure RED signal used by clone_exec_test.go's spec-lock tests.
// They will turn GREEN once MAZ-120 implements the helpers and routes the SVC
// transport + setupUserStack through them.
//
// Scope note: the real SVC entry, delegate packing, maz/linux dispatch, and
// the in-child stack write are kernel-only (package ksyscall is not host-
// testable) and are verified by the boot/xfertest stage described in
// task_plan.md, not here. This file locks ONLY the pure, host-testable
// contract: faithful argv/envp transport + the mandatory-env merge policy.

// equalBlobs compares two [][]byte for element-wise byte equality.
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

// TestExecveArgvPackRoundtrip locks the wire format the SyscallCloneExec SVC
// uses to ferry the ragged argv/envp arrays across the SVC boundary in a
// single packed buffer: NUL-separated elements, unpack is the exact inverse of
// pack. This is the faithful-argv guarantee at the transport layer — argv[0]
// ("compile") survives the round trip unmodified.
func TestExecveArgvPackRoundtrip(t *testing.T) {
	argv := [][]byte{
		[]byte("compile"),
		[]byte("-o"),
		[]byte("x.a"),
	}
	blob := PackArgv(argv)
	got := UnpackArgv(blob)
	if !equalBlobs(got, argv) {
		t.Fatalf("PackArgv/UnpackArgv roundtrip mismatch:\n  argv = %q\n  got  = %q", argv, got)
	}
	// argv[0] specifically must be byte-identical — faithful execve argv[0]
	// is the whole point of MAZ-120's stack-layout work.
	if len(got) == 0 || !bytes.Equal(got[0], []byte("compile")) {
		t.Fatalf("argv[0] not preserved through pack/unpack: got %q", got)
	}
}

// TestExecveEnvpPackRoundtrip locks the same transport contract for envp,
// whose entries carry '=' and arbitrary path bytes. envp must survive the
// SVC transport so the merge step downstream sees the real caller env.
func TestExecveEnvpPackRoundtrip(t *testing.T) {
	envp := [][]byte{
		[]byte("GOROOT=/go"),
		[]byte("PATH=/usr/bin:/bin"),
		[]byte("HOME=/root"),
	}
	got := UnpackArgv(PackArgv(envp))
	if !equalBlobs(got, envp) {
		t.Fatalf("envp pack/unpack roundtrip mismatch:\n  envp = %q\n  got  = %q", envp, got)
	}
}

// TestExecveArgvPackEmpty locks the empty-input edge: an empty argv packs to an
// empty blob and unpacks back to empty (not a one-element [""] slice). Trailing
// behavior must match the kernel's existing readPackedArgs (skip empty runs).
func TestExecveArgvPackEmpty(t *testing.T) {
	if got := UnpackArgv(PackArgv(nil)); len(got) != 0 {
		t.Fatalf("empty argv roundtrip yielded %d elements, want 0: %q", len(got), got)
	}
}

// TestEnvMergeMandatoryWins locks the env-merge policy stated in the ticket:
// "caller envp ∪ mandatory mazzy env, mandatory wins". The merged env must
//   1. contain every mandatory entry verbatim (mandatory wins on key conflict),
//   2. contain GODEBUG=gccheckmark=1 (the non-negotiable runtime invariant),
//   3. carry through caller-only keys untouched,
//   4. NOT duplicate a key that appears in both (one entry, mandatory value).
func TestEnvMergeMandatoryWins(t *testing.T) {
	caller := [][]byte{
		[]byte("GOROOT=/go"),
		[]byte("GODEBUG=madvdontneed=1"), // conflicts with mandatory — mandatory must win
		[]byte("PATH=/usr/bin"),
	}
	mandatory := [][]byte{
		[]byte("GODEBUG=gccheckmark=1"), // mandatory runtime invariant
		[]byte("GOMAXPROCS=1"),
	}

	merged := MergeExecEnv(caller, mandatory)
	m := envToMap(merged)

	// 1+2. Mandatory wins on the conflicting key, and it's the gccheckmark value.
	if got := m["GODEBUG"]; got != "gccheckmark=1" {
		t.Errorf("GODEBUG = %q, want %q (mandatory must win over caller)", got, "gccheckmark=1")
	}
	// 2. The literal invariant entry must be present.
	if !containsEntry(merged, "GODEBUG=gccheckmark=1") {
		t.Errorf("merged env missing mandatory GODEBUG=gccheckmark=1: %q", merged)
	}
	// 3. Caller-only keys survive.
	if got := m["GOROOT"]; got != "/go" {
		t.Errorf("caller-only GOROOT = %q, want /go", got)
	}
	if got := m["PATH"]; got != "/usr/bin" {
		t.Errorf("caller-only PATH = %q, want /usr/bin", got)
	}
	// 3. Mandatory-only keys present.
	if got := m["GOMAXPROCS"]; got != "1" {
		t.Errorf("mandatory-only GOMAXPROCS = %q, want 1", got)
	}
	// 4. No duplicate keys — exactly one entry per distinct key.
	seen := map[string]int{}
	for _, e := range merged {
		k := keyOf(e)
		seen[k]++
		if seen[k] > 1 {
			t.Errorf("merged env has duplicate key %q: %q", k, merged)
		}
	}
}

// envToMap parses "key=value" entries into a map for assertion convenience.
func envToMap(env [][]byte) map[string]string {
	out := make(map[string]string, len(env))
	for _, e := range env {
		s := string(e)
		for i := 0; i < len(s); i++ {
			if s[i] == '=' {
				out[s[:i]] = s[i+1:]
				break
			}
		}
	}
	return out
}

// keyOf returns the key portion of a "key=value" entry (whole entry if no '=').
func keyOf(e []byte) string {
	for i := 0; i < len(e); i++ {
		if e[i] == '=' {
			return string(e[:i])
		}
	}
	return string(e)
}

// containsEntry reports whether env contains a verbatim "key=value" entry.
func containsEntry(env [][]byte, want string) bool {
	for _, e := range env {
		if string(e) == want {
			return true
		}
	}
	return false
}
