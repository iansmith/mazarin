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

// MAZ-119 Phase 0: red tests for the Dup3 identifier. dup3 has no shepherd
// path today — there is no Dup3 sysid. fcntl already has the Fcntl sysid
// (reused, not re-added). These tests turn green once Dup3 is appended to
// sysid.go before the NumIDs sentinel.

func TestDup3Exists(t *testing.T) {
	if Dup3 == Invalid {
		t.Fatal("sysid.Dup3 must not equal Invalid (the sentinel zero value)")
	}
}

func TestDup3IsDistinct(t *testing.T) {
	// Dup3 must not alias any existing identifier it could plausibly collide
	// with: the reused Fcntl id (this ticket touches both), Close (dup3
	// closes newfd first), or Clone (process-creation neighborhood).
	if Dup3 == Fcntl {
		t.Errorf("Dup3 must not alias Fcntl (both = %d)", Dup3)
	}
	if Dup3 == Close {
		t.Errorf("Dup3 must not alias Close (both = %d)", Dup3)
	}
	if Dup3 == Clone {
		t.Errorf("Dup3 must not alias Clone (both = %d)", Dup3)
	}
	if Dup3 == Execve {
		t.Errorf("Dup3 must not alias Execve (both = %d)", Dup3)
	}
}

func TestDup3BelowNumIDs(t *testing.T) {
	// NumIDs is the array-size sentinel for every [NumIDs]Foo dispatch table.
	// Dup3 must fall strictly below it so the kernel can index it.
	if Dup3 >= NumIDs {
		t.Errorf("Dup3 (%d) must be < NumIDs (%d)", Dup3, NumIDs)
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
		"Execve": Execve, "Wait4": Wait4, "CloneExec": CloneExec, "Clone": Clone, "Dup3": Dup3,
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
