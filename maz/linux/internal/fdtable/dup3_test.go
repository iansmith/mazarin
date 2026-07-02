// Phase 0 red tests for MAZ-119 (linux shepherd: dup3 + fcntl(F_SETFD)).
//
// These tests lock the FD-table behavioral spec for the two ops this ticket
// adds, in the host-testable internal/fdtable package (the shepherd's own
// handlers in `package main` are not unit-testable). They cover DoD items 1
// and 2:
//
//	1. dup3(oldfd, newfd, flags) makes newfd refer to the same open file as
//	   oldfd, closing whatever was previously at newfd, and rejects the call
//	   with EINVAL when oldfd == newfd.
//	2. fcntl(F_SETFD, FD_CLOEXEC) records the flag and F_GETFD reads it back,
//	   per descriptor — which here is exactly the Cloexec field MAZ-122 added.
//
// They must be RED until Table.Dup3 exists.

package fdtable

import (
	"testing"

	"mazzy/maz/linux/internal/pipe"
)

// TestDup3CopiesEntryToNewFD verifies that dup3 installs an independent copy
// of the old entry at newfd, carries the same open-file identity (Handle/Inum/
// Path), and sets Cloexec from the O_CLOEXEC flag.
func TestDup3CopiesEntryToNewFD(t *testing.T) {
	tbl := New()
	tbl.Put(3, &Entry{Kind: KindFile, Handle: 100, Inum: 42, Offset: 7, Size: 99, Path: "/a"})

	got, errno := tbl.Dup3(3, 5, 0, nil)
	if errno != 0 {
		t.Fatalf("Dup3(3,5,0) errno = %d, want 0", errno)
	}
	if got != 5 {
		t.Fatalf("Dup3(3,5,0) returned %d, want newfd 5", got)
	}
	e := tbl.Get(5)
	if e == nil {
		t.Fatal("fd 5 not installed after Dup3")
	}
	if e.Handle != 100 || e.Inum != 42 || e.Path != "/a" {
		t.Errorf("fd 5 entry does not share the open file: %+v", e)
	}
	if e.Cloexec {
		t.Errorf("fd 5 Cloexec = true, want false (flags=0)")
	}
	// Independent struct: mutating fd 5's offset must not move fd 3's.
	e.Offset = 1234
	if tbl.Get(3).Offset == 1234 {
		t.Errorf("dup3 aliased the same *Entry; offsets must be independent copies")
	}
}

// TestDup3SetsCloexecFromFlags verifies the O_CLOEXEC flag on the dup3 call
// sets the new fd's Cloexec bit (dup3's flag arg, unlike dup2).
func TestDup3SetsCloexecFromFlags(t *testing.T) {
	tbl := New()
	tbl.Put(3, &Entry{Kind: KindFile, Handle: 100})
	if _, errno := tbl.Dup3(3, 5, OCLOEXEC, nil); errno != 0 {
		t.Fatalf("Dup3 with O_CLOEXEC errno = %d, want 0", errno)
	}
	if e := tbl.Get(5); e == nil || !e.Cloexec {
		t.Errorf("fd 5 Cloexec = false, want true (O_CLOEXEC passed)")
	}
}

// TestDup3ClosesExistingNewFD verifies dup3 closes whatever occupied newfd
// first, invoking the close callback for an fs-handle-bearing entry.
func TestDup3ClosesExistingNewFD(t *testing.T) {
	tbl := New()
	tbl.Put(3, &Entry{Kind: KindFile, Handle: 100, Path: "/src"})
	tbl.Put(5, &Entry{Kind: KindFile, Handle: 200, Path: "/victim"})

	var closed []uint32
	got, errno := tbl.Dup3(3, 5, 0, func(e *Entry) { closed = append(closed, e.Handle) })
	if errno != 0 || got != 5 {
		t.Fatalf("Dup3(3,5,0) = (%d, %d), want (5, 0)", got, errno)
	}
	if len(closed) != 1 || closed[0] != 200 {
		t.Errorf("displaced newfd close callback fired for %v, want [200]", closed)
	}
	if e := tbl.Get(5); e == nil || e.Handle != 100 {
		t.Errorf("fd 5 not replaced by the dup of fd 3: %+v", e)
	}
}

// TestDup3SameFDIsEINVAL verifies dup3(fd, fd, flags) is EINVAL (the defining
// difference from dup2, which would return fd unchanged). The oldfd==newfd
// check must run BEFORE oldfd-validity, so even a closed fd dup'd onto itself
// is EINVAL, not EBADF — matching Linux's ksys_dup3 ordering.
func TestDup3SameFDIsEINVAL(t *testing.T) {
	tbl := New()
	tbl.Put(3, &Entry{Kind: KindFile, Handle: 100})
	if _, errno := tbl.Dup3(3, 3, 0, nil); errno != -22 { // EINVAL
		t.Errorf("Dup3(3,3,0) errno = %d, want -22 (EINVAL)", errno)
	}
	// Closed fd onto itself: still EINVAL (ordering before EBADF).
	if _, errno := tbl.Dup3(9, 9, 0, nil); errno != -22 {
		t.Errorf("Dup3(9,9,0) on closed fd: errno = %d, want -22 (EINVAL, not EBADF)", errno)
	}
}

// TestDup3UnknownFlagsIsEINVAL verifies dup3 rejects any flag bit other than
// O_CLOEXEC with EINVAL, matching Linux (flags & ~O_CLOEXEC must be 0).
func TestDup3UnknownFlagsIsEINVAL(t *testing.T) {
	tbl := New()
	tbl.Put(3, &Entry{Kind: KindFile, Handle: 100})
	if _, errno := tbl.Dup3(3, 5, 0x1, nil); errno != -22 { // 0x1 is not O_CLOEXEC
		t.Errorf("Dup3 with unknown flag 0x1: errno = %d, want -22 (EINVAL)", errno)
	}
	if tbl.Get(5) != nil {
		t.Errorf("Dup3 with bad flags must not install anything at newfd")
	}
	// O_CLOEXEC alone is accepted.
	if _, errno := tbl.Dup3(3, 5, OCLOEXEC, nil); errno != 0 {
		t.Errorf("Dup3 with O_CLOEXEC: errno = %d, want 0", errno)
	}
}

// TestDup3BadOldFDIsEBADF verifies dup3 with a closed/out-of-range oldfd
// returns EBADF and does not touch newfd.
func TestDup3BadOldFDIsEBADF(t *testing.T) {
	tbl := New()
	if _, errno := tbl.Dup3(9, 5, 0, nil); errno != -9 { // EBADF
		t.Errorf("Dup3(9,5,0) on closed oldfd: errno = %d, want -9 (EBADF)", errno)
	}
	if tbl.Get(5) != nil {
		t.Errorf("Dup3 with bad oldfd must not install anything at newfd")
	}
}

// TestDup3BadNewFDIsEBADF verifies an out-of-range newfd is rejected without
// closing oldfd.
func TestDup3BadNewFDIsEBADF(t *testing.T) {
	tbl := New()
	tbl.Put(3, &Entry{Kind: KindFile, Handle: 100})
	if _, errno := tbl.Dup3(3, MaxFDs, 0, nil); errno != -9 { // EBADF
		t.Errorf("Dup3(3, MaxFDs, 0): errno = %d, want -9 (EBADF)", errno)
	}
	if _, errno := tbl.Dup3(3, -1, 0, nil); errno != -9 {
		t.Errorf("Dup3(3, -1, 0): errno = %d, want -9 (EBADF)", errno)
	}
}

// TestDup3PipeWriteEndForksReference is the MAZ-63 stage-2 regression: os/exec
// relocates a pipe write-end onto fd 1 via dup3(oldfd, 1, 0), then closes
// oldfd (the now-redundant original number). That close must NOT sever the
// write-end living at fd 1 — the two fds must hold INDEPENDENT *pipe.End
// references sharing the same buffer (mirroring Copy's Fork), not the same
// aliased entry. Before the fix, Dup3 did `dup := *old` and left dup.Pipe
// pointing at the exact same *pipe.End as old.Pipe; closing oldfd then closed
// that shared End out from under newfd, undercounting the writer refcount and
// making the reader see a premature EOF before the child ever wrote anything.
func TestDup3PipeWriteEndForksReference(t *testing.T) {
	tbl := New()
	rEnd, wEnd := pipe.New(0)
	tbl.Put(3, &Entry{Kind: KindPipeWrite, Pipe: wEnd})

	if _, errno := tbl.Dup3(3, 1, 0, nil); errno != 0 {
		t.Fatalf("Dup3(3,1,0) errno = %d, want 0", errno)
	}
	newEntry := tbl.Get(1)
	if newEntry == nil || newEntry.Pipe == nil {
		t.Fatal("fd 1 not installed with a Pipe end after Dup3")
	}
	if newEntry.Pipe == wEnd {
		t.Fatal("fd 1's Pipe end aliases the original *pipe.End; Dup3 must Fork() a new one")
	}

	// os/exec's usual next step: close the original fd now that it lives at 1.
	tbl.Get(3).Pipe.Close()
	tbl.Free(3)

	// The write-end at fd 1 must still be live: a write through it must reach
	// the reader, and the reader must NOT see EOF yet (one writer remains).
	if _, err := newEntry.Pipe.Write([]byte("hi")); err != nil {
		t.Fatalf("write through fd 1 after closing the original fd: %v", err)
	}
	buf := make([]byte, 8)
	n, err := rEnd.Read(buf)
	if err != nil || string(buf[:n]) != "hi" {
		t.Fatalf("read got (%d, %v) = %q, want (2, nil) = \"hi\"", n, err, buf[:n])
	}

	// Only now, once fd 1 is also closed, should the reader observe EOF.
	newEntry.Pipe.Close()
	n, err = rEnd.Read(buf)
	if n != 0 || err != nil {
		t.Errorf("read after both ends closed = (%d, %v), want (0, nil) [EOF]", n, err)
	}
}
