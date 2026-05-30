// Phase-0 RED tests for MAZ-118 (linux shepherd clone_exec buffering-window
// state machine).
//
// These tests describe the EXPECTED post-implementation behavior of the
// cloneexec.Registry state machine. They compile against the Phase-0 stub in
// window.go (whose method bodies return zero values) and therefore FAIL on
// their assertions — that is the RED state. The implementation phase replaces
// the stubs with the real value-machine and turns every test below green.
//
// The op encoding (linuxabi.IntentOp / IntentKind) and caps
// (linuxabi.MaxStartupIntentOps / MaxStartupCwdBytes) are imported from
// shared/linuxabi — the SAME single source of truth the kernel uses — so
// there is no local mirror to drift.
//
// Behaviors locked here map 1:1 to the ticket's RED test plan:
//
//	TestBufferedIntentRoundtrip       — Open/Buffer/SetCwd/Flush happy path + consume-once
//	TestUnrecognizedSyscallPoisons    — poison ⇒ Flush errors, no partial intent escapes
//	TestIntentOverflowFails           — 17th op ⇒ clean E2BIG-equiv error, no truncation
//	TestCwdOverflowFails              — 257-byte cwd ⇒ clean error
//	TestNoMatchingExecveCleansUp      — Open then Abort ⇒ no leaked window
//
// FD_CLOEXEC is the Linux fcntl flag value (1); spelled as a literal here to
// avoid pulling in golang.org/x/sys/unix for a single constant.

package cloneexec

import (
	"bytes"
	"errors"
	"testing"

	"mazzy/shared/linuxabi"
)

const fdCloexec int32 = 1 // FD_CLOEXEC

// TestBufferedIntentRoundtrip exercises the happy path: open a window, buffer
// the canonical os/exec setup sequence (dup3 stdout, close a pipe end,
// F_SETFD FD_CLOEXEC), record a chdir, then flush. Flush must return the ops
// in buffered order, the recorded cwd, and consume the window so a second
// flush fails.
func TestBufferedIntentRoundtrip(t *testing.T) {
	const tid int32 = 7
	r := New()

	if err := r.Open(tid); err != nil {
		t.Fatalf("Open(%d) returned %v, want nil", tid, err)
	}
	if !r.IsOpen(tid) {
		t.Fatalf("IsOpen(%d) = false right after Open; want true", tid)
	}

	want := []linuxabi.IntentOp{
		{Kind: linuxabi.IntentDup3, Arg0: 3, Arg1: 1, Arg2: 0},
		{Kind: linuxabi.IntentClose, Arg0: 4},
		{Kind: linuxabi.IntentFSetFD, Arg0: 4, Arg1: fdCloexec},
	}
	for i, op := range want {
		if err := r.Buffer(tid, op); err != nil {
			t.Fatalf("Buffer(%d, op#%d=%+v) returned %v, want nil", tid, i, op, err)
		}
	}
	if err := r.SetCwd(tid, []byte("/tmp")); err != nil {
		t.Fatalf("SetCwd(%d, /tmp) returned %v, want nil", tid, err)
	}

	ops, cwd, err := r.Flush(tid)
	if err != nil {
		t.Fatalf("Flush(%d) returned err %v, want nil", tid, err)
	}
	if len(ops) != len(want) {
		t.Fatalf("Flush returned %d ops, want %d", len(ops), len(want))
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Errorf("Flush ops[%d] = %+v, want %+v", i, ops[i], want[i])
		}
	}
	if string(cwd) != "/tmp" {
		t.Errorf("Flush cwd = %q, want %q", cwd, "/tmp")
	}

	// Window is consumed: it must be gone and a second Flush must fail
	// cleanly (no double-emit of the same intent).
	if r.IsOpen(tid) {
		t.Errorf("IsOpen(%d) = true after Flush; window should be consumed", tid)
	}
	if got := r.Len(); got != 0 {
		t.Errorf("Len() = %d after Flush, want 0", got)
	}
	if _, _, err := r.Flush(tid); !errors.Is(err, ErrNoWindow) {
		t.Errorf("second Flush(%d) returned %v, want ErrNoWindow", tid, err)
	}
}

// TestUnrecognizedSyscallPoisonsWindow verifies that poisoning a window makes
// Flush refuse to emit any intent — no partial buffered ops escape.
func TestUnrecognizedSyscallPoisonsWindow(t *testing.T) {
	const tid int32 = 11
	r := New()
	if err := r.Open(tid); err != nil {
		t.Fatalf("Open(%d) returned %v", tid, err)
	}
	// One legitimately-buffered op precedes the poison, to prove the partial
	// intent does NOT escape once poisoned.
	if err := r.Buffer(tid, linuxabi.IntentOp{Kind: linuxabi.IntentDup3, Arg0: 3, Arg1: 1}); err != nil {
		t.Fatalf("Buffer(%d) returned %v", tid, err)
	}

	if err := r.Poison(tid); err != nil {
		t.Fatalf("Poison(%d) returned %v, want nil", tid, err)
	}

	ops, cwd, err := r.Flush(tid)
	if !errors.Is(err, ErrPoisoned) {
		t.Errorf("Flush of poisoned window returned err %v, want ErrPoisoned", err)
	}
	if ops != nil {
		t.Errorf("Flush of poisoned window returned %d ops, want none (no partial intent)", len(ops))
	}
	if cwd != nil {
		t.Errorf("Flush of poisoned window returned cwd %q, want nil", cwd)
	}
	// Poison is consumed-on-flush: the window is gone and the failure is
	// reported exactly once.
	if r.IsOpen(tid) {
		t.Errorf("IsOpen(%d) = true after Flush of poisoned window; should be consumed", tid)
	}
}

// TestIntentOverflowFails verifies that buffering past
// linuxabi.MaxStartupIntentOps fails cleanly (E2BIG-equivalent) rather than
// truncating. The window is poisoned by the overflow, so a subsequent Flush
// also refuses to emit the silently-truncated prefix.
func TestIntentOverflowFails(t *testing.T) {
	const tid int32 = 13
	r := New()
	if err := r.Open(tid); err != nil {
		t.Fatalf("Open(%d) returned %v", tid, err)
	}

	// Fill exactly to the cap — these must all succeed.
	for i := 0; i < linuxabi.MaxStartupIntentOps; i++ {
		if err := r.Buffer(tid, linuxabi.IntentOp{Kind: linuxabi.IntentClose, Arg0: int32(i)}); err != nil {
			t.Fatalf("Buffer #%d (within cap) returned %v, want nil", i, err)
		}
	}
	// The one-past-cap op must fail with the overflow sentinel, not truncate.
	overflowErr := r.Buffer(tid, linuxabi.IntentOp{Kind: linuxabi.IntentClose, Arg0: 999})
	if !errors.Is(overflowErr, ErrIntentOverflow) {
		t.Errorf("Buffer past cap returned %v, want ErrIntentOverflow", overflowErr)
	}

	// Overflow poisons: Flush must hand back ErrPoisoned, not the truncated prefix.
	if _, _, err := r.Flush(tid); !errors.Is(err, ErrPoisoned) {
		t.Errorf("Flush after overflow returned %v, want ErrPoisoned (truncated intent must not escape)", err)
	}
}

// TestCwdOverflowFails verifies an over-long chdir path is rejected cleanly
// rather than stored truncated.
func TestCwdOverflowFails(t *testing.T) {
	const tid int32 = 17
	r := New()
	if err := r.Open(tid); err != nil {
		t.Fatalf("Open(%d) returned %v", tid, err)
	}

	// MaxStartupCwdBytes exactly is allowed; one byte over is not.
	atCap := bytes.Repeat([]byte("a"), linuxabi.MaxStartupCwdBytes)
	if err := r.SetCwd(tid, atCap); err != nil {
		t.Fatalf("SetCwd at cap (%d bytes) returned %v, want nil", linuxabi.MaxStartupCwdBytes, err)
	}

	tooLong := bytes.Repeat([]byte("a"), linuxabi.MaxStartupCwdBytes+1)
	if err := r.SetCwd(tid, tooLong); !errors.Is(err, ErrCwdOverflow) {
		t.Errorf("SetCwd over cap (%d bytes) returned %v, want ErrCwdOverflow", linuxabi.MaxStartupCwdBytes+1, err)
	}

	// The over-long cwd must not have replaced the at-cap value with a
	// truncated string; and an overflow poisons the window.
	if _, _, err := r.Flush(tid); !errors.Is(err, ErrPoisoned) {
		t.Errorf("Flush after cwd overflow returned %v, want ErrPoisoned (truncated cwd must not escape)", err)
	}
}

// TestNoMatchingExecveCleansUp verifies that a clone whose window is never
// flushed (no matching execve) is dropped by Abort, leaving no leaked state.
func TestNoMatchingExecveCleansUp(t *testing.T) {
	const tid int32 = 19
	r := New()
	if err := r.Open(tid); err != nil {
		t.Fatalf("Open(%d) returned %v", tid, err)
	}
	if err := r.Buffer(tid, linuxabi.IntentOp{Kind: linuxabi.IntentDup3, Arg0: 0, Arg1: 1}); err != nil {
		t.Fatalf("Buffer(%d) returned %v", tid, err)
	}
	if got := r.Len(); got != 1 {
		t.Fatalf("Len() = %d after Open+Buffer, want 1", got)
	}

	r.Abort(tid)

	if r.IsOpen(tid) {
		t.Errorf("IsOpen(%d) = true after Abort; window should be gone", tid)
	}
	if got := r.Len(); got != 0 {
		t.Errorf("Len() = %d after Abort, want 0 (no leaked window)", got)
	}
	// Abort is idempotent — a second Abort on the same TID must not panic.
	r.Abort(tid)
}
