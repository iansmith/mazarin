// Phase 0 red tests for MAZ-63 (FD-table inheritance on fork/exec).
//
// BEHAVIOR UNDER TEST:
//   When a process-flavor clone happens (os/exec's fork+exec model), the child
//   shepherd must start with a COPY of the parent's FD table, not a fresh empty
//   one. Two gaps exist today:
//
//   Gap 1: Table.Copy() does not exist. The child shepherd is created with
//     fdtable.New() (maz/linux/syscalls.go:193), losing the parent's open FDs.
//
//   Gap 2: CloseCloexecFDs is never called at execve time (zero non-test
//     callers), AND it doesn't call Pipe.Close() for pipe entries. Consequence:
//     os/exec's errpipe write-end (O_CLOEXEC) stays open across exec, so the
//     parent's os/exec.Run() blocks forever waiting for EOF on the errpipe read.
//
//   Gap 3: Cwd must be inherited from the parent and the buffered chdir (from
//     the clone window) must be applied to the child's copy, not the parent's.
//
// These tests fail on current code — Copy() is undefined → compile error (RED).
// They pass when Copy() is implemented correctly, CloseCloexecFDs is called at
// execve, and CloseCloexecFDs calls Pipe.Close() for pipe entries.
//
// WHY THIS MATTERS
//   os/exec.Run() stalls indefinitely on the current code because the errpipe
//   write-end survives exec (no cloexec sweep). Every forkexectest stage that
//   uses Run() (stages 1–3) hangs at "child exits 42, parent never returns from
//   Run()". Stage 4 (clean ECHILD) does not use Run and already passes.

package fdtable

import (
	"testing"

	"mazzy/maz/linux/internal/pipe"
)

// TestCopy_InheritsNonCloexecEntries verifies that after Copy(), the child has
// the same open FDs as the parent — stdio plus any explicitly opened ones. This
// is Gap 1: the child currently gets an empty fdtable.New() instead.
//
// The inherited entries must be independent structs (not shared *Entry pointers)
// so that a subsequent dup3/close in the child does not bleed back into the
// parent's table.
func TestCopy_InheritsNonCloexecEntries(t *testing.T) {
	parent := New()
	parent.Put(3, &Entry{Kind: KindFile, Handle: 0xBEEF, Path: "/data/db.bin"})
	parent.Put(4, &Entry{Kind: KindDir, Handle: 0xCAFE, Path: "/var/log"})

	child := parent.Copy() // COMPILE ERROR on current code: Copy undefined → RED

	for _, fd := range []int{0, 1, 2, 3, 4} {
		pe := parent.Get(fd)
		ce := child.Get(fd)
		if pe == nil {
			t.Errorf("fd %d: parent.Get returned nil; expected a seeded entry", fd)
			continue
		}
		if ce == nil {
			t.Errorf("fd %d: child.Get returned nil after Copy; want entry inherited from parent", fd)
			continue
		}
		if pe.Handle != ce.Handle || pe.Kind != ce.Kind || pe.Path != ce.Path {
			t.Errorf("fd %d: parent={Kind:%d Handle:%#x Path:%q} vs child={Kind:%d Handle:%#x Path:%q}; want identical field values",
				fd, pe.Kind, pe.Handle, pe.Path, ce.Kind, ce.Handle, ce.Path)
		}
		// The copy must produce an independent struct — not a shared pointer.
		// A shared pointer would let the child's mutations (dup3, close) bleed
		// into the parent's table, breaking fd isolation.
		if pe == ce {
			t.Errorf("fd %d: parent and child share the same *Entry pointer; Copy must produce independent Entry structs", fd)
		}
	}
}

// TestCopy_InheritsCwd verifies that the child's Cwd is set to the parent's
// Cwd at copy time (Gap 3). Without this, path resolution in the child uses "/"
// even when the parent had chdir'd elsewhere.
func TestCopy_InheritsCwd(t *testing.T) {
	parent := New()
	parent.Cwd = "/var/log/mazzy"

	child := parent.Copy()

	if child.Cwd != parent.Cwd {
		t.Errorf("child.Cwd = %q, want %q (parent's Cwd at copy time)", child.Cwd, parent.Cwd)
	}
}

// TestCopy_FreeFDInChildDoesNotAffectParent verifies that freeing an FD in the
// child does not remove the entry from the parent's table. The two FD tables
// must be independent.
func TestCopy_FreeFDInChildDoesNotAffectParent(t *testing.T) {
	parent := New()
	parent.Put(3, &Entry{Kind: KindFile, Handle: 1, Path: "/x"})

	child := parent.Copy()
	child.Free(3)

	if parent.Get(3) == nil {
		t.Error("Free on child's fd 3 removed it from the parent's table; the two tables must be independent")
	}
	if child.Get(3) != nil {
		t.Error("fd 3 not removed from child's table after child.Free(3)")
	}
}

// TestErrpipeCloseContract is the integration test for Gaps 1 + 2:
// the os/exec errpipe handshake that causes Run() to hang when these gaps are
// present.
//
// Scenario (mirrors Go's os/exec internals):
//   1. Parent creates a pipe (rEnd, wEnd). wEnd is O_CLOEXEC.
//   2. Clone: child inherits a COPY of parent's FDT (Gap 1 fix).
//      After the copy buf.writers must be 2 (one for each live write-end).
//   3. execve in the child: CloseCloexecFDs sweeps wEnd in the child (Gap 2 fix).
//      This must call wEnd.Close() (decrement writers: 2 → 1).
//   4. Post-exec, parent closes its own write-end (releaseFDResources): writers 1 → 0.
//   5. Parent reads from rEnd → gets EOF (0, nil). os/exec interprets this as
//      "exec succeeded".
//
// Without Gap 1: child has no wEnd at all, CloseCloexecFDs closes nothing,
// writers stays 1, parent read blocks forever.
// Without the Pipe.Close() call in CloseCloexecFDs: writers stays 2, parent
// read blocks forever even after parent closes its own write-end.
func TestErrpipeCloseContract(t *testing.T) {
	parent := New()
	rEnd, wEnd := pipe.New(pipe.OCLOEXEC)
	// Pipe read-end is NOT cloexec (parent keeps it to read exec-status).
	parent.Put(3, &Entry{Kind: KindPipeRead, Pipe: rEnd, Cloexec: false})
	// Pipe write-end IS cloexec: must close at exec.
	parent.Put(4, &Entry{Kind: KindPipeWrite, Pipe: wEnd, Cloexec: true})

	// Step 2: clone — child inherits a copy of the parent's FDT.
	// After copy, buf.writers must be 2 (parent write-end + child write-end).
	child := parent.Copy()

	if child.Get(4) == nil {
		t.Fatal("child.Get(4) == nil after Copy; write-end not inherited (Gap 1 not fixed)")
	}

	// Step 3: child execve — sweep cloexec FDs.
	// This must call Pipe.Close() on the child's write-end, decrementing writers: 2 → 1.
	child.CloseCloexecFDs(nil)

	// Step 4: parent closes its own write-end (releaseFDResources equivalent).
	// writers: 1 → 0.
	if e := parent.Get(4); e != nil && e.Pipe != nil {
		e.Pipe.Close()
	}
	parent.Free(4)

	// Step 5: read must return EOF (n=0, err=nil) — not ErrWouldBlock.
	buf := make([]byte, 1)
	n, err := rEnd.Read(buf)
	if n != 0 || err != nil {
		t.Errorf("errpipe read after both write-ends closed: got (%d, %v), want (0, nil) [EOF].\n"+
			"If err==pipe.ErrWouldBlock: writers>0 (CloseCloexecFDs did not call Pipe.Close).\n"+
			"If child.Get(4)==nil above: Copy did not inherit the write-end.", n, err)
	}
}

// TestCopy_CloexecEntryNotInChild verifies that Copy followed by
// CloseCloexecFDs leaves cloexec FDs absent from the child but present in the
// parent. This is the post-exec state: the child's exec step closed them; the
// parent's table is untouched.
func TestCopy_CloexecEntryNotInChild(t *testing.T) {
	parent := New()
	// fd 3: cloexec — must vanish from child after sweep.
	parent.Put(3, &Entry{Kind: KindFile, Handle: 0x10, Cloexec: true})
	// fd 4: NOT cloexec — must survive in child.
	parent.Put(4, &Entry{Kind: KindFile, Handle: 0x20, Cloexec: false})

	child := parent.Copy()
	child.CloseCloexecFDs(nil)

	if child.Get(3) != nil {
		t.Error("fd 3 (cloexec) still open in child after CloseCloexecFDs sweep")
	}
	if child.Get(4) == nil {
		t.Error("fd 4 (non-cloexec) was wrongly swept from child")
	}
	// Parent's table must be untouched by the child's sweep.
	if parent.Get(3) == nil {
		t.Error("child's CloseCloexecFDs removed fd 3 from the PARENT's table; tables must be independent")
	}
}
