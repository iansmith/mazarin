package fdtable

import "mazzy/shared/linuxabi"

// StdioRedirectMaskAfterIntent computes the child's per-process stdio
// redirect mask (MAZ-149, bit assignments linuxabi.StdioRedirectFd1/Fd2) from
// this table's current state plus the effect of a buffered startup intent —
// WITHOUT mutating the table. sysExecve calls it pre-CloneExec, where the
// child's real table must not be built yet (building it pays pipe writer-ref
// increments that would leak on the ELF-load/mmap failure paths), so this
// simulates just the fd Kind evolution that ApplyStartupIntent would produce.
//
// A bit is set when the fd's final Kind is anything other than its console
// kind (KindStdout for fd 1, KindStderr for fd 2) — including closed
// (nil/KindNone): a write to a closed fd must also take the delegate path so
// the shepherd can reply EBADF instead of the console fast path printing it.
//
// Op simulation mirrors ApplyStartupIntent's semantics on Kinds only:
// IntentDup3 copies the source fd's (possibly already-overridden) Kind onto
// the target, with the same same-fd no-op shortcut; IntentClose sets
// KindNone; IntentFSetFD/IntentNone don't change Kinds. Malformed ops are not
// validated here — ApplyStartupIntent fails the exec with EBADF before the
// mask could matter.
func (t *Table) StdioRedirectMaskAfterIntent(ops []linuxabi.IntentOp) uint8 {
	// overrides tracks fds whose Kind the simulated ops changed; every other
	// fd reads through to the live table.
	var overrides map[int]Kind
	kindOf := func(fd int32) Kind {
		if k, ok := overrides[int(fd)]; ok {
			return k
		}
		if e := t.Get(int(fd)); e != nil {
			return e.Kind
		}
		return KindNone
	}
	for _, op := range ops {
		switch op.Kind {
		case linuxabi.IntentDup3:
			if op.Arg0 == op.Arg1 {
				continue // dup3 onto itself is a no-op (ApplyStartupIntent parity)
			}
			if overrides == nil {
				overrides = make(map[int]Kind, 4)
			}
			overrides[int(op.Arg1)] = kindOf(op.Arg0)
		case linuxabi.IntentClose:
			if overrides == nil {
				overrides = make(map[int]Kind, 4)
			}
			overrides[int(op.Arg0)] = KindNone
		}
	}

	var mask uint8
	if kindOf(1) != KindStdout {
		mask |= linuxabi.StdioRedirectFd1
	}
	if kindOf(2) != KindStderr {
		mask |= linuxabi.StdioRedirectFd2
	}
	return mask
}
