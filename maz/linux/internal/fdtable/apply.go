package fdtable

import (
	"path"

	"mazzy/shared/linuxabi"
)

// FD_CLOEXEC is the fcntl(F_SETFD) flag bit that marks an fd close-on-exec.
// It is the F_SETFD-flavored value (POSIX FD_CLOEXEC == 1), distinct from the
// open(2) O_CLOEXEC bit (OCLOEXEC == 0x80000): an IntentFSetFD op carries the
// fcntl flags in Arg1, so the cloexec bit is read with this mask.
const FD_CLOEXEC = 1

// errBadFD is the negated Linux EBADF ("Bad file descriptor") errno, matching
// the negated convention ResolveAt already uses in this package. It is
// returned when a startup-intent op references an fd that is out of range or
// not open.
const errBadFD int64 = -9

// ApplyStartupIntent applies a buffered clone_exec startup intent to the table
// BEFORE the child's first delegated FD syscall is serviced. This is the
// "child startup stub": the FD setup the parent buffered between its
// process-flavor clone and the matching execve is replayed here against the
// new child's table.
//
// It walks ops, applying:
//
//   - IntentDup3:  copy entries[Arg0] -> entries[Arg1] as a distinct value,
//     overwriting (dropping) any existing entry at Arg1 — net-new dup3
//     semantics. A dup3 onto itself (Arg0==Arg1) is a no-op, matching Linux's
//     EINVAL-free same-fd shortcut after the parent's validation. Arg2 carries
//     dup3 flags; the only meaningful one is O_CLOEXEC, which sets the new fd's
//     Cloexec.
//   - IntentClose: Free(Arg0).
//   - IntentFSetFD: set entries[Arg0].Cloexec from (Arg1 & FD_CLOEXEC).
//   - IntentNone:  no-op (the zeroed trailing slots of Shepherd.StartupIntent).
//
// After the ops, if len(cwd) > 0, the table's Cwd is direct-set to the
// parent's already-resolved absolute StartupCwd — no fs.Resolve IPC round
// trip, so the applier stays pure and host-testable. An empty cwd means "no
// chdir was requested" and leaves Cwd untouched.
//
// The parent validates each op when it buffers it, so a malformed op reaching
// here is a shepherd/kernel bug rather than a userspace error. Surfacing it as
// EBADF (instead of panicking or silently producing a nil/garbage entry) lets
// the execve dispatch fail the child cleanly. Returns 0 on success.
//
// dup3-over-an-open-fd note: this pure applier only mutates table state. When
// it overwrites a slot that owned an fs-side handle, the displaced handle is
// NOT closed here (the applier has no fs client). The execve integration
// (MAZ-120) owns issuing the fs.Close for any handle this displaces.
func (t *Table) ApplyStartupIntent(ops []linuxabi.IntentOp, cwd []byte) int64 {
	for _, op := range ops {
		switch op.Kind {
		case linuxabi.IntentNone:
			// Sentinel for unused trailing slots — skip.
		case linuxabi.IntentDup3:
			if op.Arg0 == op.Arg1 {
				continue // dup3 onto itself is a no-op
			}
			src := t.Get(int(op.Arg0))
			if src == nil {
				return errBadFD
			}
			if op.Arg1 < 0 || int(op.Arg1) >= MaxFDs {
				return errBadFD
			}
			// Value copy so the new fd is a distinct entry, not a pointer
			// alias of the source (closing one must not disturb the other).
			// dup3's Cloexec is set fresh from its flags (O_CLOEXEC), not
			// inherited from the source — matching Linux dup3 semantics.
			dup := *src
			dup.Cloexec = uint32(op.Arg2)&OCLOEXEC != 0
			t.Put(int(op.Arg1), &dup)
		case linuxabi.IntentClose:
			if t.Get(int(op.Arg0)) == nil {
				return errBadFD
			}
			t.Free(int(op.Arg0))
		case linuxabi.IntentFSetFD:
			e := t.Get(int(op.Arg0))
			if e == nil {
				return errBadFD
			}
			e.Cloexec = op.Arg1&FD_CLOEXEC != 0
		default:
			return errBadFD
		}
	}
	if len(cwd) > 0 {
		t.Cwd = path.Clean(string(cwd))
	}
	return 0
}
