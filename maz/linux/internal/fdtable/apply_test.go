package fdtable

import (
	"testing"

	"mazzy/shared/linuxabi"
)

// MAZ-113 startup-intent tests. They describe the behavior of
// ApplyStartupIntent — the intent-application step that runs IN maz/linux
// against a new child's FD table BEFORE the child's first delegated FD
// syscall is serviced. The buffered ops are encoded with shared/linuxabi
// (the single source of truth the kernel and shepherd share); these tests
// import that package directly — never a local mirror.
//
// The scheduling / race / end-to-end fork+exec coverage is MAZ-114, not here;
// these are pure, IPC-free unit tests against the lifted table.

// dup3Op builds an IntentDup3 op (Arg0=oldfd, Arg1=newfd, Arg2=flags).
func dup3Op(oldfd, newfd, flags int32) linuxabi.IntentOp {
	return linuxabi.IntentOp{Kind: linuxabi.IntentDup3, Arg0: oldfd, Arg1: newfd, Arg2: flags}
}

// closeOp builds an IntentClose op (Arg0=fd).
func closeOp(fd int32) linuxabi.IntentOp {
	return linuxabi.IntentOp{Kind: linuxabi.IntentClose, Arg0: fd}
}

// fsetfdOp builds an IntentFSetFD op (Arg0=fd, Arg1=flags).
func fsetfdOp(fd, flags int32) linuxabi.IntentOp {
	return linuxabi.IntentOp{Kind: linuxabi.IntentFSetFD, Arg0: fd, Arg1: flags}
}

// TestApplyStartupIntent_Dup3CloseFSetFD is the headline test from the ticket:
// seed fd3, apply [dup3(3,1,0), close(3), F_SETFD(1, FD_CLOEXEC)] with cwd
// "/tmp". Expect fd1 to alias the old fd3, fd3 to be closed, fd1 to be marked
// close-on-exec, and the table cwd to be "/tmp".
func TestApplyStartupIntent_Dup3CloseFSetFD(t *testing.T) {
	tbl := New()
	// Seed a distinctive fd3 so we can prove the dup3 actually copied it.
	seeded := &Entry{Kind: KindFile, Handle: 0xBEEF, Path: "/data/in.txt", Flags: 0}
	tbl.Put(3, seeded)

	ops := []linuxabi.IntentOp{
		dup3Op(3, 1, 0),
		closeOp(3),
		fsetfdOp(1, FD_CLOEXEC),
	}
	if errno := tbl.ApplyStartupIntent(ops, []byte("/tmp")); errno != 0 {
		t.Fatalf("ApplyStartupIntent returned errno %d, want 0", errno)
	}

	// fd1 must now alias the old fd3 contents (handle + path carried over).
	got1 := tbl.Get(1)
	if got1 == nil {
		t.Fatalf("fd1 is nil after dup3(3,1); expected it to alias old fd3")
	}
	if got1.Handle != 0xBEEF || got1.Path != "/data/in.txt" || got1.Kind != KindFile {
		t.Errorf("fd1 not a copy of old fd3: got {Kind:%d Handle:%#x Path:%q}, want {Kind:%d Handle:%#x Path:%q}",
			got1.Kind, got1.Handle, got1.Path, KindFile, uint32(0xBEEF), "/data/in.txt")
	}

	// The dup3 must be a value copy, not a shared pointer: mutating the new
	// fd1 entry must not bleed back into the seeded source object.
	if got1 == seeded {
		t.Errorf("fd1 aliases the SAME *Entry as the dup3 source; want a distinct value copy")
	}

	// fd3 must be closed (IntentClose).
	if got3 := tbl.Get(3); got3 != nil {
		t.Errorf("fd3 not closed after IntentClose: got %+v, want nil", got3)
	}

	// fd1 must be marked close-on-exec (IntentFSetFD with FD_CLOEXEC).
	if !got1.Cloexec {
		t.Errorf("fd1.Cloexec=false after F_SETFD(1, FD_CLOEXEC); want true")
	}

	// cwd must be the parent's resolved StartupCwd.
	if tbl.Cwd != "/tmp" {
		t.Errorf("cwd=%q after applying cwd \"/tmp\"; want \"/tmp\"", tbl.Cwd)
	}
}

// TestApplyStartupIntent_NoneIsNoOp verifies that IntentNone sentinel slots
// (the zeroed trailing slots of Shepherd.StartupIntent) are skipped without
// disturbing the table. Seed fd3, apply only IntentNone ops + empty cwd, and
// assert the table is untouched.
func TestApplyStartupIntent_NoneIsNoOp(t *testing.T) {
	tbl := New()
	seeded := &Entry{Kind: KindFile, Handle: 0x1234, Path: "/keep.txt"}
	tbl.Put(3, seeded)

	ops := []linuxabi.IntentOp{
		{Kind: linuxabi.IntentNone},
		{Kind: linuxabi.IntentNone},
	}
	if errno := tbl.ApplyStartupIntent(ops, nil); errno != 0 {
		t.Fatalf("ApplyStartupIntent returned errno %d, want 0", errno)
	}

	got3 := tbl.Get(3)
	if got3 == nil || got3.Handle != 0x1234 || got3.Path != "/keep.txt" {
		t.Errorf("IntentNone disturbed fd3: got %+v, want the seeded entry untouched", got3)
	}
	if tbl.Cwd != "/" {
		t.Errorf("IntentNone changed cwd to %q; want \"/\" (untouched)", tbl.Cwd)
	}
}

// TestApplyStartupIntent_EmptyCwdSkipsChdir verifies that an empty cwd leaves
// the table's cwd untouched (empty = "no chdir requested"). Apply one real op
// (close fd3) so the test still exercises the loop, but pass an empty cwd.
func TestApplyStartupIntent_EmptyCwdSkipsChdir(t *testing.T) {
	tbl := New()
	tbl.Put(3, &Entry{Kind: KindFile, Handle: 0x9, Path: "/x"})
	// Move cwd away from the default so we can prove empty cwd does NOT
	// reset it back.
	tbl.Cwd = "/var/run"

	ops := []linuxabi.IntentOp{closeOp(3)}
	if errno := tbl.ApplyStartupIntent(ops, []byte{}); errno != 0 {
		t.Fatalf("ApplyStartupIntent returned errno %d, want 0", errno)
	}

	if got3 := tbl.Get(3); got3 != nil {
		t.Errorf("fd3 not closed: got %+v, want nil", got3)
	}
	if tbl.Cwd != "/var/run" {
		t.Errorf("empty cwd changed the table cwd to %q; want \"/var/run\" (unchanged)", tbl.Cwd)
	}
}

// TestApplyStartupIntent_BadFDReturnsEBADF verifies the small contract: a
// malformed op (here, dup3 from a closed/never-opened fd) is rejected with
// EBADF rather than silently producing a nil/garbage entry. The parent
// validates ops at buffering time, so a violation reaching here is a bug —
// surfacing it as EBADF lets the execve caller fail the child cleanly.
func TestApplyStartupIntent_BadFDReturnsEBADF(t *testing.T) {
	tbl := New()
	// fd9 was never opened; dup3(9, 1) has no source entry to copy.
	ops := []linuxabi.IntentOp{dup3Op(9, 1, 0)}
	if errno := tbl.ApplyStartupIntent(ops, nil); errno != errBadFD {
		t.Errorf("dup3 from a closed fd: errno=%d, want errBadFD (%d)", errno, errBadFD)
	}
}
