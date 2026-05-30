// Phase 0 red tests for MAZ-122 (linux shepherd: honor O_CLOEXEC end-to-end).
//
// These tests lock the behavioral spec for the FD-table cloexec contract
// BEFORE it is implemented. They fail on current code (the package
// maz/linux/internal/fdtable does not exist yet — the live FD table is in
// maz/linux's `package main`, which is not host-testable). The
// implementation work lifts the FD-table contract into this internal
// package, adds the Cloexec field, and provides the CloseCloexecFDs sweep.
//
// The three landing sites this contract unifies (per the ticket):
//  1. SET   — openat (this ticket) / pipe2 (MAZ-121) set Cloexec when the
//     caller passes O_CLOEXEC.
//  2. TOGGLE — fcntl(F_SETFD, FD_CLOEXEC) / F_GETFD flips it (MAZ-119).
//  3. CLOSE — the exec step sweeps and closes every Cloexec FD (MAZ-113),
//     via CloseCloexecFDs.
//
// These tests cover the two sites this ticket owns (SET via openat-flag
// derivation, and the CLOSE sweep helper). They must be RED until the
// fdtable package implements the contract.

package fdtable

import "testing"

// O_CLOEXEC is the Linux ARM64/amd64 value (octal 0o2000000 == 0x80000).
// The fdtable package must export the same constant so callers (openat,
// pipe2, fcntl) derive the cloexec bit from one source of truth.
const oCLOEXEC = 0x80000

// TestOpenatOCloexecSetsBit verifies that an FD created with the O_CLOEXEC
// flag has Cloexec == true, and one created without it has Cloexec == false.
//
// CloexecFromFlags is the single derivation point the openat path (and
// pipe2) call so the bit is computed identically everywhere.
func TestOpenatOCloexecSetsBit(t *testing.T) {
	cases := []struct {
		name  string
		flags int32
		want  bool
	}{
		{"with O_CLOEXEC", oCLOEXEC, true},
		{"with O_CLOEXEC|O_RDWR", oCLOEXEC | 0x2, true},
		{"without O_CLOEXEC (O_RDONLY)", 0x0, false},
		{"without O_CLOEXEC (O_WRONLY)", 0x1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The constant must agree with Linux's O_CLOEXEC.
			if OCLOEXEC != oCLOEXEC {
				t.Fatalf("fdtable.OCLOEXEC = 0x%x, want 0x%x", OCLOEXEC, oCLOEXEC)
			}
			if got := CloexecFromFlags(tc.flags); got != tc.want {
				t.Errorf("CloexecFromFlags(0x%x) = %v, want %v", tc.flags, got, tc.want)
			}

			// And the field must be settable on an Entry installed in a
			// Table, mirroring what the openat handler does.
			tbl := New()
			fd := tbl.Alloc(3)
			if fd < 0 {
				t.Fatalf("Alloc(3) returned %d, want a free fd", fd)
			}
			tbl.Put(fd, &Entry{Kind: KindFile, Cloexec: CloexecFromFlags(tc.flags)})
			e := tbl.Get(fd)
			if e == nil {
				t.Fatalf("Get(%d) returned nil after Put", fd)
			}
			if e.Cloexec != tc.want {
				t.Errorf("entry.Cloexec = %v, want %v", e.Cloexec, tc.want)
			}
		})
	}
}

// TestCloseCloexecFDsSweep verifies the exec-step sweep: CloseCloexecFDs
// closes (and frees) exactly the FDs whose Cloexec bit is set, leaving
// every non-cloexec FD — including the stdio FDs 0/1/2 — intact.
//
// The sweep takes a per-handle close callback so the fs-side handle is
// released through the caller's fs client. The callback must be invoked
// once per closed cloexec FD that owns an fs handle, and the swept slots
// must become free in the table.
func TestCloseCloexecFDsSweep(t *testing.T) {
	tbl := New()

	// fd 0/1/2 are the pre-populated stdio entries (non-cloexec by
	// construction). Build a mix on top of them.
	//
	//   fd 3: file, cloexec      → must be closed + freed
	//   fd 4: file, NOT cloexec  → must survive
	//   fd 5: dir,  cloexec      → must be closed + freed
	//   fd 6: file, cloexec, no fs handle (handle==0) → freed, no close cb
	tbl.Put(3, &Entry{Kind: KindFile, Handle: 100, Cloexec: true})
	tbl.Put(4, &Entry{Kind: KindFile, Handle: 200, Cloexec: false})
	tbl.Put(5, &Entry{Kind: KindDir, Handle: 300, Cloexec: true})
	tbl.Put(6, &Entry{Kind: KindFile, Handle: 0, Cloexec: true})

	var closed []uint32
	tbl.CloseCloexecFDs(func(handle uint32) {
		closed = append(closed, handle)
	})

	// Exactly the cloexec FDs that own a real fs handle get the close cb.
	wantClosed := map[uint32]bool{100: true, 300: true}
	if len(closed) != len(wantClosed) {
		t.Fatalf("close callback fired for handles %v, want exactly %v", closed, []uint32{100, 300})
	}
	for _, h := range closed {
		if !wantClosed[h] {
			t.Errorf("close callback fired for unexpected handle %d", h)
		}
	}

	// Swept cloexec slots are now free.
	for _, fd := range []int{3, 5, 6} {
		if e := tbl.Get(fd); e != nil {
			t.Errorf("fd %d still open after sweep (Cloexec entry should be freed); got %+v", fd, e)
		}
	}

	// Non-cloexec FDs survive, including the handle-bearing one.
	if e := tbl.Get(4); e == nil {
		t.Errorf("fd 4 (non-cloexec) was wrongly freed by the sweep")
	} else if e.Handle != 200 {
		t.Errorf("fd 4 entry mutated by sweep: Handle = %d, want 200", e.Handle)
	}

	// stdio FDs 0/1/2 survive — they are never cloexec.
	for _, fd := range []int{0, 1, 2} {
		if tbl.Get(fd) == nil {
			t.Errorf("stdio fd %d was wrongly freed by the cloexec sweep", fd)
		}
	}
}
