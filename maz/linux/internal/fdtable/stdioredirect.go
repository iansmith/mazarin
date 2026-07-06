package fdtable

import "mazzy/shared/linuxabi"

// stdioSimEntry is the simulated (Kind, Cloexec) state of one fd during
// StdioRedirectMaskAfterIntent's dry run of the startup intent.
type stdioSimEntry struct {
	kind    Kind
	cloexec bool
}

// StdioRedirectMaskAfterIntent computes the child's per-process stdio
// redirect mask (MAZ-149, bit assignments linuxabi.StdioRedirectFd1/Fd2) from
// this table's current state plus the effect of a buffered startup intent —
// WITHOUT mutating the table. sysExecve calls it pre-CloneExec, where the
// child's real table must not be built yet (building it pays pipe writer-ref
// increments that would leak on the ELF-load/mmap failure paths), so this
// simulates the fd state evolution that ApplyStartupIntent AND the
// subsequent CloseCloexecFDs exec sweep would produce.
//
// A bit is set when the fd's final Kind is anything other than its console
// kind (KindStdout for fd 1, KindStderr for fd 2) — including closed
// (nil/KindNone): a write to a closed fd must also take the delegate path so
// the shepherd can reply EBADF instead of the console fast path printing it.
// An fd whose final Cloexec flag is set counts as closed — CloseCloexecFDs
// closes it at exec time, before the child's first write.
//
// Op simulation mirrors ApplyStartupIntent's semantics on (Kind, Cloexec):
// IntentDup3 copies the source fd's (possibly already-overridden) Kind onto
// the target with Cloexec derived fresh from the dup3 flags (dup3 semantics
// — never inherited), with the same same-fd no-op shortcut; IntentClose sets
// KindNone; IntentFSetFD sets Cloexec from the fcntl flags; IntentNone is
// skipped. Malformed ops are not validated here — ApplyStartupIntent fails
// the exec with EBADF before the mask could matter.
func (t *Table) StdioRedirectMaskAfterIntent(ops []linuxabi.IntentOp) uint8 {
	// overrides tracks fds whose state the simulated ops changed; every
	// other fd reads through to the live table.
	var overrides map[int]stdioSimEntry
	stateOf := func(fd int32) stdioSimEntry {
		if s, ok := overrides[int(fd)]; ok {
			return s
		}
		if e := t.Get(int(fd)); e != nil {
			return stdioSimEntry{kind: e.Kind, cloexec: e.Cloexec}
		}
		return stdioSimEntry{kind: KindNone}
	}
	setState := func(fd int32, s stdioSimEntry) {
		if overrides == nil {
			overrides = make(map[int]stdioSimEntry, 4)
		}
		overrides[int(fd)] = s
	}
	for _, op := range ops {
		switch op.Kind {
		case linuxabi.IntentDup3:
			if op.Arg0 == op.Arg1 {
				continue // dup3 onto itself is a no-op (ApplyStartupIntent parity)
			}
			setState(op.Arg1, stdioSimEntry{
				kind:    stateOf(op.Arg0).kind,
				cloexec: CloexecFromFlags(op.Arg2),
			})
		case linuxabi.IntentClose:
			setState(op.Arg0, stdioSimEntry{kind: KindNone})
		case linuxabi.IntentFSetFD:
			s := stateOf(op.Arg0)
			s.cloexec = op.Arg1&FD_CLOEXEC != 0
			setState(op.Arg0, s)
		}
	}

	// effectiveKind folds in the exec-time cloexec sweep: a Cloexec'd fd is
	// closed by CloseCloexecFDs before the child's first write.
	effectiveKind := func(fd int32) Kind {
		s := stateOf(fd)
		if s.cloexec {
			return KindNone
		}
		return s.kind
	}
	var mask uint8
	if effectiveKind(1) != KindStdout {
		mask |= linuxabi.StdioRedirectFd1
	}
	if effectiveKind(2) != KindStderr {
		mask |= linuxabi.StdioRedirectFd2
	}
	return mask
}
