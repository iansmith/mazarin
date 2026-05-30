package sysid

import "testing"

// MAZ-74 Phase 0: red tests for Execve, Wait4, and the kernel-internal
// CloneExec identifier. These will turn green once the constants exist
// in sysid.go.

func TestExecveExists(t *testing.T) {
	if Execve == Invalid {
		t.Fatal("sysid.Execve must not equal Invalid (the sentinel zero value)")
	}
}

func TestWait4Exists(t *testing.T) {
	if Wait4 == Invalid {
		t.Fatal("sysid.Wait4 must not equal Invalid (the sentinel zero value)")
	}
}

func TestCloneExecExists(t *testing.T) {
	// CloneExec is the kernel-internal combined-clone+exec syscall per
	// MAZ-62's design. It needs a SysID so the kernel dispatch table can
	// route it, but it is not reachable from userspace (no per-arch
	// translation table entry maps to it).
	if CloneExec == Invalid {
		t.Fatal("sysid.CloneExec must not equal Invalid (the sentinel zero value)")
	}
}

func TestNewIDsAreDistinct(t *testing.T) {
	if Execve == Wait4 {
		t.Errorf("Execve and Wait4 must be distinct (both = %d)", Execve)
	}
	if Execve == CloneExec {
		t.Errorf("Execve and CloneExec must be distinct (both = %d)", Execve)
	}
	if Wait4 == CloneExec {
		t.Errorf("Wait4 and CloneExec must be distinct (both = %d)", Wait4)
	}
}

func TestNewIDsDontCollideWithClone(t *testing.T) {
	// Clone (thread-creation flavor) is the existing identifier. The new
	// process-flavor work introduces three new IDs that must not alias it.
	if Execve == Clone {
		t.Errorf("Execve must not alias Clone (both = %d)", Execve)
	}
	if Wait4 == Clone {
		t.Errorf("Wait4 must not alias Clone (both = %d)", Wait4)
	}
	if CloneExec == Clone {
		t.Errorf("CloneExec must not alias Clone (both = %d)", CloneExec)
	}
}

func TestNumIDsAccountsForNewIDs(t *testing.T) {
	// NumIDs is the array-size sentinel — every dispatch table downstream
	// is `[NumIDs]Foo`. The new IDs must fall strictly below it.
	if Execve >= NumIDs {
		t.Errorf("Execve (%d) must be < NumIDs (%d)", Execve, NumIDs)
	}
	if Wait4 >= NumIDs {
		t.Errorf("Wait4 (%d) must be < NumIDs (%d)", Wait4, NumIDs)
	}
	if CloneExec >= NumIDs {
		t.Errorf("CloneExec (%d) must be < NumIDs (%d)", CloneExec, NumIDs)
	}
}

// MAZ-121 Phase 0: red tests for the Pipe2 identifier. pipe2 is a fork/exec
// prerequisite (os.Pipe / the errpipe success handshake). These turn green
// once Pipe2 exists in sysid.go.

func TestPipe2Exists(t *testing.T) {
	if Pipe2 == Invalid {
		t.Fatal("sysid.Pipe2 must not equal Invalid (the sentinel zero value)")
	}
}

func TestPipe2DistinctFromOthers(t *testing.T) {
	// Pipe2 must not alias any neighboring identifier in the iota block.
	others := map[string]ID{
		"Execve": Execve, "Wait4": Wait4, "CloneExec": CloneExec, "Clone": Clone,
	}
	for name, id := range others {
		if Pipe2 == id {
			t.Errorf("Pipe2 must not alias %s (both = %d)", name, id)
		}
	}
}

func TestPipe2BelowNumIDs(t *testing.T) {
	if Pipe2 >= NumIDs {
		t.Errorf("Pipe2 (%d) must be < NumIDs (%d)", Pipe2, NumIDs)
	}
}
