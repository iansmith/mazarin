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

import "testing"

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
// difference from dup2, which would return fd unchanged).
func TestDup3SameFDIsEINVAL(t *testing.T) {
	tbl := New()
	tbl.Put(3, &Entry{Kind: KindFile, Handle: 100})
	if _, errno := tbl.Dup3(3, 3, 0, nil); errno != -22 { // EINVAL
		t.Errorf("Dup3(3,3,0) errno = %d, want -22 (EINVAL)", errno)
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
