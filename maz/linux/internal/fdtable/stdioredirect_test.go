package fdtable

import (
	"testing"

	"mazzy/shared/linuxabi"
)

// MAZ-149 — spec for the per-process stdio redirect mask computation.
// sysExecve calls StdioRedirectMaskAfterIntent pre-CloneExec on the child's
// base table (the vfork transient's pending table, or a stand-in for the
// parent copy) with the buffered clone-window intent; the result rides the
// CloneExec params to Shepherd.StdioRedirectMask, where the kernel's
// SyscallWrite uses it to split console (fast path) from redirected
// (blocking delegate) fd 1/2 writes.

// TestStdioRedirectMaskConsoleDefault — a fresh table (stdin/stdout/stderr)
// with no intent is pure console: mask 0, kernel fast path.
func TestStdioRedirectMaskConsoleDefault(t *testing.T) {
	if got := New().StdioRedirectMaskAfterIntent(nil); got != 0 {
		t.Errorf("mask = %#b, want 0 (console stdio)", got)
	}
}

// TestStdioRedirectMaskTableAlreadyRedirected — the vfork transient's dup3s
// land in the pending child table BEFORE execve (resolveTargetFDT routing),
// so the base table itself can carry the redirect with no intent ops.
func TestStdioRedirectMaskTableAlreadyRedirected(t *testing.T) {
	tbl := New()
	tbl.Put(1, &Entry{Kind: KindPipeWrite})
	if got := tbl.StdioRedirectMaskAfterIntent(nil); got != linuxabi.StdioRedirectFd1 {
		t.Errorf("mask = %#b, want %#b (fd 1 → pipe in base table)", got, linuxabi.StdioRedirectFd1)
	}
}

// TestStdioRedirectMaskDup3Intent — the clone-window path: a buffered
// dup3(pipe → 1) marks fd 1 redirected even though the base table still says
// console.
func TestStdioRedirectMaskDup3Intent(t *testing.T) {
	tbl := New()
	tbl.Put(5, &Entry{Kind: KindPipeWrite})
	ops := []linuxabi.IntentOp{{Kind: linuxabi.IntentDup3, Arg0: 5, Arg1: 1}}
	if got := tbl.StdioRedirectMaskAfterIntent(ops); got != linuxabi.StdioRedirectFd1 {
		t.Errorf("mask = %#b, want %#b (dup3 5→1)", got, linuxabi.StdioRedirectFd1)
	}
}

// TestStdioRedirectMaskBothRedirected — stdout to a pipe, stderr to a file:
// both bits set.
func TestStdioRedirectMaskBothRedirected(t *testing.T) {
	tbl := New()
	tbl.Put(5, &Entry{Kind: KindPipeWrite})
	tbl.Put(6, &Entry{Kind: KindFile})
	ops := []linuxabi.IntentOp{
		{Kind: linuxabi.IntentDup3, Arg0: 5, Arg1: 1},
		{Kind: linuxabi.IntentDup3, Arg0: 6, Arg1: 2},
	}
	want := linuxabi.StdioRedirectFd1 | linuxabi.StdioRedirectFd2
	if got := tbl.StdioRedirectMaskAfterIntent(ops); got != want {
		t.Errorf("mask = %#b, want %#b", got, want)
	}
}

// TestStdioRedirectMaskClosedFdDelegates — a CLOSED fd 1/2 must also set its
// bit: the write has to reach the shepherd so it can reply EBADF; the console
// fast path would wrongly print it.
func TestStdioRedirectMaskClosedFdDelegates(t *testing.T) {
	tbl := New()
	ops := []linuxabi.IntentOp{{Kind: linuxabi.IntentClose, Arg0: 2}}
	if got := tbl.StdioRedirectMaskAfterIntent(ops); got != linuxabi.StdioRedirectFd2 {
		t.Errorf("mask = %#b, want %#b (closed fd 2)", got, linuxabi.StdioRedirectFd2)
	}
}

// TestStdioRedirectMaskDup3Chain — ops apply in order and later ops see
// earlier ops' effects: dup3(5→3) then dup3(3→1) carries the pipe Kind
// through the intermediate fd.
func TestStdioRedirectMaskDup3Chain(t *testing.T) {
	tbl := New()
	tbl.Put(5, &Entry{Kind: KindPipeWrite})
	ops := []linuxabi.IntentOp{
		{Kind: linuxabi.IntentDup3, Arg0: 5, Arg1: 3},
		{Kind: linuxabi.IntentDup3, Arg0: 3, Arg1: 1},
	}
	if got := tbl.StdioRedirectMaskAfterIntent(ops); got != linuxabi.StdioRedirectFd1 {
		t.Errorf("mask = %#b, want %#b (dup3 chain 5→3→1)", got, linuxabi.StdioRedirectFd1)
	}
}

// TestStdioRedirectMaskRestoredToConsole — a redirect that is later dup3'd
// back to a console Kind ends clear (e.g. save/restore of stdout around a
// capture): the FINAL Kind decides, not any intermediate state.
func TestStdioRedirectMaskRestoredToConsole(t *testing.T) {
	tbl := New()
	tbl.Put(5, &Entry{Kind: KindPipeWrite})
	tbl.Put(6, &Entry{Kind: KindStdout}) // saved console stdout (dup of fd 1)
	ops := []linuxabi.IntentOp{
		{Kind: linuxabi.IntentDup3, Arg0: 5, Arg1: 1}, // redirect...
		{Kind: linuxabi.IntentDup3, Arg0: 6, Arg1: 1}, // ...then restore
	}
	if got := tbl.StdioRedirectMaskAfterIntent(ops); got != 0 {
		t.Errorf("mask = %#b, want 0 (fd 1 restored to console)", got)
	}
}

// TestStdioRedirectMaskSameFdNoop — dup3(1,1) is a no-op (ApplyStartupIntent
// parity) and must not disturb the mask.
func TestStdioRedirectMaskSameFdNoop(t *testing.T) {
	tbl := New()
	ops := []linuxabi.IntentOp{{Kind: linuxabi.IntentDup3, Arg0: 1, Arg1: 1}}
	if got := tbl.StdioRedirectMaskAfterIntent(ops); got != 0 {
		t.Errorf("mask = %#b, want 0 (same-fd dup3 is a no-op)", got)
	}
}

// TestStdioRedirectMaskFSetFDCloexec — fcntl(2, F_SETFD, FD_CLOEXEC) means
// the exec-time CloseCloexecFDs sweep CLOSES fd 2 before the child's first
// write: the bit must be set so the write delegates (EBADF from the table)
// instead of the console fast path printing it.
func TestStdioRedirectMaskFSetFDCloexec(t *testing.T) {
	tbl := New()
	ops := []linuxabi.IntentOp{{Kind: linuxabi.IntentFSetFD, Arg0: 2, Arg1: FD_CLOEXEC}}
	if got := tbl.StdioRedirectMaskAfterIntent(ops); got != linuxabi.StdioRedirectFd2 {
		t.Errorf("mask = %#b, want %#b (cloexec'd fd 2 closes at exec)", got, linuxabi.StdioRedirectFd2)
	}
}

// TestStdioRedirectMaskFSetFDCleared — setting then clearing FD_CLOEXEC on a
// console fd leaves it console (bit clear): only the FINAL state counts.
func TestStdioRedirectMaskFSetFDCleared(t *testing.T) {
	tbl := New()
	ops := []linuxabi.IntentOp{
		{Kind: linuxabi.IntentFSetFD, Arg0: 1, Arg1: FD_CLOEXEC},
		{Kind: linuxabi.IntentFSetFD, Arg0: 1, Arg1: 0},
	}
	if got := tbl.StdioRedirectMaskAfterIntent(ops); got != 0 {
		t.Errorf("mask = %#b, want 0 (cloexec cleared before exec)", got)
	}
}

// TestStdioRedirectMaskDup3CloexecFlag — dup3(src, 1, O_CLOEXEC): the new
// fd 1 is closed by the exec sweep, so the bit is set even though the source
// was a live pipe.
func TestStdioRedirectMaskDup3CloexecFlag(t *testing.T) {
	tbl := New()
	tbl.Put(5, &Entry{Kind: KindPipeWrite})
	ops := []linuxabi.IntentOp{{Kind: linuxabi.IntentDup3, Arg0: 5, Arg1: 1, Arg2: OCLOEXEC}}
	if got := tbl.StdioRedirectMaskAfterIntent(ops); got != linuxabi.StdioRedirectFd1 {
		t.Errorf("mask = %#b, want %#b (dup3 O_CLOEXEC target closes at exec)", got, linuxabi.StdioRedirectFd1)
	}
}

// TestStdioRedirectMaskBaseTableCloexec — a base-table fd 1 entry already
// carrying Cloexec (primed by the transient's dup3 with O_CLOEXEC) is closed
// by the exec sweep → bit set.
func TestStdioRedirectMaskBaseTableCloexec(t *testing.T) {
	tbl := New()
	tbl.Put(1, &Entry{Kind: KindPipeWrite, Cloexec: true})
	if got := tbl.StdioRedirectMaskAfterIntent(nil); got != linuxabi.StdioRedirectFd1 {
		t.Errorf("mask = %#b, want %#b (base-table cloexec fd 1)", got, linuxabi.StdioRedirectFd1)
	}
}

// TestStdioRedirectMaskDoesNotMutate — the computation is a pure read; the
// table must be byte-identical after (sysExecve runs it on the LIVE pending
// child table).
func TestStdioRedirectMaskDoesNotMutate(t *testing.T) {
	tbl := New()
	tbl.Put(5, &Entry{Kind: KindPipeWrite})
	ops := []linuxabi.IntentOp{
		{Kind: linuxabi.IntentDup3, Arg0: 5, Arg1: 1},
		{Kind: linuxabi.IntentClose, Arg0: 2},
	}
	tbl.StdioRedirectMaskAfterIntent(ops)
	if e := tbl.Get(1); e == nil || e.Kind != KindStdout {
		t.Errorf("fd 1 mutated by mask computation: %+v", e)
	}
	if e := tbl.Get(2); e == nil || e.Kind != KindStderr {
		t.Errorf("fd 2 mutated by mask computation: %+v", e)
	}
}
