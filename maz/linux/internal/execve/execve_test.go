// Phase 0 RED tests for MAZ-79 (linux shepherd execve dispatch: argv/envp
// marshaling, path resolution, CloneExecRequest builder, coreutils seam).
//
// These tests describe the EXPECTED post-implementation behavior. They fail
// against the STUB in execve.go — that is the point (RED state). The
// implementation phase makes them pass without weakening any assertion.
//
// Test matrix (mirrors the ticket's RED test plan):
//   - TestExecveArgvUnpack            — null-separated blob → [][]byte
//   - TestExecvePathResolution        — absolute pass-through; relative vs CWD
//   - TestExecveBuildsCloneExecRequest— argv/envp/intent/cwd → request fields
//   - TestExecveRoutingElfVsCoreutils — CoreutilsRoute ("",false) in v1
//   - TestIntentCapEnforcement        — >16 ops / >256-byte cwd → error, no truncation

package execve

import (
	"bytes"
	"errors"
	"testing"
)

// TestExecveArgvUnpack: "compile\0-o\0x.a\0" → ["compile","-o","x.a"].
// Inverse of MAZ-120's kernel-side packing / readPackedArgs.
func TestExecveArgvUnpack(t *testing.T) {
	blob := []byte("compile\x00-o\x00x.a\x00")
	got := UnpackArgs(blob)
	want := [][]byte{[]byte("compile"), []byte("-o"), []byte("x.a")}
	if len(got) != len(want) {
		t.Fatalf("UnpackArgs returned %d args, want %d (got=%q)", len(got), len(want), got)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("arg[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// A blob without a trailing NUL still yields its last element.
	got2 := UnpackArgs([]byte("ls\x00-l"))
	want2 := [][]byte{[]byte("ls"), []byte("-l")}
	if len(got2) != 2 || !bytes.Equal(got2[0], want2[0]) || !bytes.Equal(got2[1], want2[1]) {
		t.Errorf("UnpackArgs(no trailing NUL) = %q, want %q", got2, want2)
	}

	// Empty blob → no args.
	if got3 := UnpackArgs(nil); len(got3) != 0 {
		t.Errorf("UnpackArgs(nil) = %q, want empty", got3)
	}
}

// TestExecvePathResolution: absolute paths pass through unchanged; relative
// paths resolve against the per-PID CWD. No $PATH search in v1.
func TestExecvePathResolution(t *testing.T) {
	// Absolute path passes through (cmd/go hands absolute tool paths).
	abs := "/go/pkg/tool/linux_arm64/compile"
	if got := ResolvePath(abs, "/work"); got != abs {
		t.Errorf("ResolvePath(absolute) = %q, want %q (unchanged)", got, abs)
	}

	// Relative path resolves against CWD.
	if got := ResolvePath("a.out", "/work"); got != "/work/a.out" {
		t.Errorf("ResolvePath(\"a.out\", \"/work\") = %q, want \"/work/a.out\"", got)
	}

	// Relative path with a parent ref is cleaned against CWD.
	if got := ResolvePath("../bin/ls", "/work/sub"); got != "/work/bin/ls" {
		t.Errorf("ResolvePath(\"../bin/ls\", \"/work/sub\") = %q, want \"/work/bin/ls\"", got)
	}
}

// TestExecveBuildsCloneExecRequest: a path + argv + envp + buffered intent
// [dup3(3,1,0), close(4)] + cwd "/work" produce a request whose
// Argv[0]=="compile", Envp carries the caller env, Intent matches, Cwd=="/work",
// and Filename is the resolved path.
func TestExecveBuildsCloneExecRequest(t *testing.T) {
	filename := "/go/pkg/tool/linux_arm64/compile"
	argv := [][]byte{[]byte("compile"), []byte("-o"), []byte("x.a")}
	envp := [][]byte{[]byte("GOROOT=/go"), []byte("PATH=/usr/bin")}
	intent := []IntentOp{
		{Kind: IntentDup3, Arg0: 3, Arg1: 1, Arg2: 0},
		{Kind: IntentClose, Arg0: 4},
	}
	cwd := []byte("/work")

	req, err := Build(filename, argv, envp, intent, cwd)
	if err != nil {
		t.Fatalf("Build returned error %v, want nil", err)
	}
	if req.Filename != filename {
		t.Errorf("req.Filename = %q, want %q", req.Filename, filename)
	}
	if len(req.Argv) != 3 || !bytes.Equal(req.Argv[0], []byte("compile")) {
		t.Errorf("req.Argv = %q, want argv[0]==compile (3 args)", req.Argv)
	}
	if len(req.Envp) != 2 || !bytes.Equal(req.Envp[0], []byte("GOROOT=/go")) {
		t.Errorf("req.Envp = %q, want caller env carried through", req.Envp)
	}
	if len(req.Intent) != 2 {
		t.Fatalf("req.Intent has %d ops, want 2", len(req.Intent))
	}
	if req.Intent[0].Kind != IntentDup3 || req.Intent[0].Arg0 != 3 || req.Intent[0].Arg1 != 1 {
		t.Errorf("req.Intent[0] = %+v, want Dup3{3,1,0}", req.Intent[0])
	}
	if req.Intent[1].Kind != IntentClose || req.Intent[1].Arg0 != 4 {
		t.Errorf("req.Intent[1] = %+v, want Close{4}", req.Intent[1])
	}
	if !bytes.Equal(req.Cwd, []byte("/work")) {
		t.Errorf("req.Cwd = %q, want \"/work\"", req.Cwd)
	}

	// Empty argv is rejected — argv[0] is required.
	if _, err := Build(filename, nil, envp, nil, nil); !errors.Is(err, ErrEmptyArgv) {
		t.Errorf("Build(empty argv) err = %v, want ErrEmptyArgv", err)
	}
}

// TestExecveRoutingElfVsCoreutils: in v1 the coreutils table is empty, so
// CoreutilsRoute returns ("", false) for every target — both a go-tool path
// and a /usr/bin tool take the ELF branch. MAZ-67 fills the table later.
func TestExecveRoutingElfVsCoreutils(t *testing.T) {
	for _, p := range []string{
		"/go/pkg/tool/linux_arm64/compile",
		"/usr/bin/ls",
	} {
		mazPath, ok := CoreutilsRoute(p)
		if ok {
			t.Errorf("CoreutilsRoute(%q) = (%q, true), want (\"\", false) in v1", p, mazPath)
		}
		if mazPath != "" {
			t.Errorf("CoreutilsRoute(%q) mazPath = %q, want \"\"", p, mazPath)
		}
	}
}

// TestIntentCapEnforcement: >16 intent ops or a >256-byte cwd must produce a
// clean cap error (matching the kernel's ceE2BIG / ceENAMETOOLONG re-check),
// NOT a silently truncated request.
func TestIntentCapEnforcement(t *testing.T) {
	argv := [][]byte{[]byte("compile")}

	// 17 ops exceeds MaxStartupIntentOps (16) → ErrIntentOverflow.
	tooManyOps := make([]IntentOp, MaxStartupIntentOps+1)
	for i := range tooManyOps {
		tooManyOps[i] = IntentOp{Kind: IntentClose, Arg0: int32(i)}
	}
	if _, err := Build("/bin/x", argv, nil, tooManyOps, nil); !errors.Is(err, ErrIntentOverflow) {
		t.Errorf("Build(17 ops) err = %v, want ErrIntentOverflow", err)
	}

	// Exactly 16 ops is accepted (boundary).
	exactOps := make([]IntentOp, MaxStartupIntentOps)
	for i := range exactOps {
		exactOps[i] = IntentOp{Kind: IntentClose, Arg0: int32(i)}
	}
	if req, err := Build("/bin/x", argv, nil, exactOps, nil); err != nil {
		t.Errorf("Build(16 ops) err = %v, want nil (boundary)", err)
	} else if len(req.Intent) != MaxStartupIntentOps {
		t.Errorf("Build(16 ops) kept %d ops, want %d (no truncation)", len(req.Intent), MaxStartupIntentOps)
	}

	// A 257-byte cwd exceeds MaxStartupCwdBytes (256) → ErrCwdOverflow.
	longCwd := bytes.Repeat([]byte("a"), MaxStartupCwdBytes+1)
	if _, err := Build("/bin/x", argv, nil, nil, longCwd); !errors.Is(err, ErrCwdOverflow) {
		t.Errorf("Build(257-byte cwd) err = %v, want ErrCwdOverflow", err)
	}

	// Exactly 256 bytes is accepted (boundary).
	exactCwd := bytes.Repeat([]byte("a"), MaxStartupCwdBytes)
	if _, err := Build("/bin/x", argv, nil, nil, exactCwd); err != nil {
		t.Errorf("Build(256-byte cwd) err = %v, want nil (boundary)", err)
	}
}
