// Phase 0 red tests for MAZ-80 (linux shepherd wait4 — shepherd-side
// decision logic: status encoding, child selection, zombie-set reaping,
// WNOHANG, ECHILD, and the "would block / must park" signal).
//
// These tests describe the behavior the ticket says we want. They fail
// against the not-yet-written implementation in this package — that is
// the point of the RED phase. The implementation must make them pass
// WITHOUT weakening any assertion.
//
// Scope note: this package holds ONLY the pure, host-testable decision
// logic. The live park/unpark over a real kernel EventChildExit
// notification is exercised end-to-end in MAZ-114, not here.
//
// ABI references (see runtime-patches/syscall/syscall_linux.go):
//   - WIFEXITED(status)  ⇔ status & 0x7F == 0
//   - WEXITSTATUS(status) ⇔ (status >> 8) & 0xFF
//   - WNOHANG option      == 1
//   - wait4(pid, *wstatus, options, *rusage)
//       pid >  0 → wait for that specific child
//       pid == -1 → wait for any child

package wait

import "testing"

// wifexited mirrors the kernel/libc WIFEXITED macro for assertions:
// the low 7 bits of the encoded status are zero for a normal exit.
func wifexited(status int) bool { return status&0x7F == 0 }

// wexitstatus mirrors WEXITSTATUS: the exit code lives in bits 8..15.
func wexitstatus(status int) int { return (status >> 8) & 0xFF }

// TestWaitStatusEncodingNormalExit — a child that exited normally with code
// 42 must encode to 0x2A00, which decodes (per the Linux ABI) as a normal
// exit with status 42.
func TestWaitStatusEncodingNormalExit(t *testing.T) {
	got := EncodeWaitStatus(42)
	if got != 0x2A00 {
		t.Fatalf("EncodeWaitStatus(42) = %#x, want 0x2A00", got)
	}
	if !wifexited(got) {
		t.Errorf("WIFEXITED(%#x) = false, want true (normal exit)", got)
	}
	if ws := wexitstatus(got); ws != 42 {
		t.Errorf("WEXITSTATUS(%#x) = %d, want 42", got, ws)
	}
	// Exit code 0 must encode to 0 and still read as a normal exit.
	if z := EncodeWaitStatus(0); z != 0 || !wifexited(z) || wexitstatus(z) != 0 {
		t.Errorf("EncodeWaitStatus(0) = %#x; want 0 with WIFEXITED && WEXITSTATUS==0", z)
	}
}

// TestWait4SpecificPIDReapsZombie — wait4(pid=100) with a zombie child 100
// (raw exit 7) returns (100, 7<<8), removes the zombie, and a second wait4
// for that PID returns ECHILD (the child was reaped exactly once).
func TestWait4SpecificPIDReapsZombie(t *testing.T) {
	r := New()
	const parent = 1
	r.RegisterChild(parent, 100)
	r.AddZombie(parent, 100, 7)

	out := r.Decide(parent, 100, 0)
	if out.MustPark {
		t.Fatalf("Decide(pid=100) parked; want immediate reap of the zombie")
	}
	if out.Errno != 0 {
		t.Fatalf("Decide(pid=100) errno = %d, want 0", out.Errno)
	}
	if out.WPid != 100 {
		t.Errorf("Decide(pid=100) WPid = %d, want 100", out.WPid)
	}
	if out.EncodedStatus != EncodeWaitStatus(7) {
		t.Errorf("Decide(pid=100) EncodedStatus = %#x, want %#x", out.EncodedStatus, EncodeWaitStatus(7))
	}

	// Reaped exactly once: a second wait4 for 100 now has no child → ECHILD.
	out2 := r.Decide(parent, 100, 0)
	if out2.Errno != ECHILD {
		t.Errorf("second Decide(pid=100) errno = %d, want ECHILD (%d)", out2.Errno, ECHILD)
	}
	if out2.MustPark {
		t.Errorf("second Decide(pid=100) parked; a reaped/unknown PID must return ECHILD, not block")
	}
}

// TestWait4AnyChildSelectsAZombie — with two zombies {100:1, 200:2}, a
// wait4(-1) reaps exactly one of them; the other remains for a later call.
func TestWait4AnyChildSelectsAZombie(t *testing.T) {
	r := New()
	const parent = 1
	r.RegisterChild(parent, 100)
	r.RegisterChild(parent, 200)
	r.AddZombie(parent, 100, 1)
	r.AddZombie(parent, 200, 2)

	out := r.Decide(parent, -1, 0)
	if out.MustPark || out.Errno != 0 {
		t.Fatalf("Decide(-1) parked=%v errno=%d; want an immediate reap", out.MustPark, out.Errno)
	}
	if out.WPid != 100 && out.WPid != 200 {
		t.Fatalf("Decide(-1) WPid = %d, want one of {100, 200}", out.WPid)
	}
	reaped := out.WPid
	wantStatus := EncodeWaitStatus(map[int32]int{100: 1, 200: 2}[reaped])
	if out.EncodedStatus != wantStatus {
		t.Errorf("Decide(-1) reaped %d with status %#x, want %#x", reaped, out.EncodedStatus, wantStatus)
	}

	// Exactly one zombie consumed: the next wait4(-1) reaps the OTHER one.
	out2 := r.Decide(parent, -1, 0)
	if out2.MustPark || out2.Errno != 0 {
		t.Fatalf("second Decide(-1) parked=%v errno=%d; want the remaining zombie", out2.MustPark, out2.Errno)
	}
	if out2.WPid == reaped {
		t.Errorf("second Decide(-1) re-reaped %d; each zombie must be reaped once", reaped)
	}
	if out2.WPid != 100 && out2.WPid != 200 {
		t.Errorf("second Decide(-1) WPid = %d, want the other of {100,200}", out2.WPid)
	}
}

// TestWait4NoChildrenReturnsECHILD — wait4 with no living or zombie children
// returns ECHILD (not a block, not success).
func TestWait4NoChildrenReturnsECHILD(t *testing.T) {
	r := New()
	const parent = 1

	any := r.Decide(parent, -1, 0)
	if any.Errno != ECHILD {
		t.Errorf("Decide(-1) with no children: errno = %d, want ECHILD (%d)", any.Errno, ECHILD)
	}
	if any.MustPark {
		t.Errorf("Decide(-1) with no children parked; want ECHILD")
	}

	specific := r.Decide(parent, 999, 0)
	if specific.Errno != ECHILD {
		t.Errorf("Decide(pid=999) with no such child: errno = %d, want ECHILD (%d)", specific.Errno, ECHILD)
	}
}

// TestWait4NoZombieWouldBlock — a live child with no pending zombie must
// signal "would block / must park" when WNOHANG is clear, and return wpid==0
// (no error, no status) when WNOHANG is set.
func TestWait4NoZombieWouldBlock(t *testing.T) {
	r := New()
	const parent = 1
	r.RegisterChild(parent, 100) // alive, has not exited

	// Blocking wait4: must park (the shepherd defers Reply until exit).
	blocking := r.Decide(parent, -1, 0)
	if !blocking.MustPark {
		t.Errorf("Decide(-1) with a live-but-not-exited child: MustPark=false, want true")
	}
	if blocking.Errno != 0 {
		t.Errorf("would-block Decide errno = %d, want 0 (it parks, not errors)", blocking.Errno)
	}

	// WNOHANG: do not block — return wpid==0 immediately, no error.
	nohang := r.Decide(parent, -1, WNOHANG)
	if nohang.MustPark {
		t.Errorf("Decide(-1, WNOHANG) parked; WNOHANG must never block")
	}
	if nohang.Errno != 0 {
		t.Errorf("Decide(-1, WNOHANG) errno = %d, want 0", nohang.Errno)
	}
	if nohang.WPid != 0 {
		t.Errorf("Decide(-1, WNOHANG) WPid = %d, want 0 (live child, nothing to reap)", nohang.WPid)
	}
}

// TestWait4MultipleConcurrentZombies — 8 children exit; eight wait4(-1)
// calls reap all eight distinct PIDs, and the ninth returns ECHILD.
// Guards the `go build -p N` concurrent-children case.
func TestWait4MultipleConcurrentZombies(t *testing.T) {
	r := New()
	const parent = 1
	for i := int32(0); i < 8; i++ {
		pid := 100 + i
		r.RegisterChild(parent, pid)
		r.AddZombie(parent, pid, int(i)) // distinct raw exit codes 0..7
	}

	seen := make(map[int32]bool)
	for i := 0; i < 8; i++ {
		out := r.Decide(parent, -1, 0)
		if out.MustPark || out.Errno != 0 {
			t.Fatalf("reap #%d parked=%v errno=%d; want a zombie", i, out.MustPark, out.Errno)
		}
		if seen[out.WPid] {
			t.Fatalf("reap #%d returned already-seen PID %d; each child reaped once", i, out.WPid)
		}
		seen[out.WPid] = true
		wantRaw := int(out.WPid - 100)
		if out.EncodedStatus != EncodeWaitStatus(wantRaw) {
			t.Errorf("reap #%d PID %d status = %#x, want %#x", i, out.WPid, out.EncodedStatus, EncodeWaitStatus(wantRaw))
		}
	}
	if len(seen) != 8 {
		t.Fatalf("reaped %d distinct PIDs, want 8", len(seen))
	}
	// Ninth call: nothing left → ECHILD.
	ninth := r.Decide(parent, -1, 0)
	if ninth.Errno != ECHILD {
		t.Errorf("9th Decide(-1) errno = %d, want ECHILD (%d) — all children reaped", ninth.Errno, ECHILD)
	}
}
