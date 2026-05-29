// Package forkexec_test holds MAZ-114's Phase-0 acceptance spec for the
// fork+exec+wait4 round trip (the MAZ-62 umbrella DoD).
//
// WHY THIS TEST EXISTS AND WHY IT IS RED
//
// MAZ-114 is the integration acceptance gate for the fork/exec core. Its
// real Definition of Done is an end-to-end run *inside* mazzy: a test
// program does an os/exec-style fork+exec of a child ELF and recovers the
// child's exit status via wait4, exercising every layer (clone dispatch →
// buffering window → execve flush → kernel clone_exec → child startup stub
// → kernel child-exit notification → shepherd wait4 park/unpark). That
// full path only runs under HVF and only once the entire blocker chain
// (MAZ-77/78/79/80/113/118/119/120/121/122) has landed — see findings.md.
//
// None of that chain is built yet (all blockers are "In Progress", not
// merged — the task brief that called them merged was wrong; git log on
// master shows only the MAZ-69..75 foundation). So the genuinely end-to-end
// HVF stage cannot exist today.
//
// What CAN be locked today, host-testably (`task test` runs
// ./maz/linux/internal/...), is the BEHAVIORAL CONTRACT of the wait4 reaper
// — the shepherd-side component that MAZ-80 builds and MAZ-114 promotes to
// an acceptance assertion. MAZ-80's own description defers "the real
// park/unpark on a live kernel notification" to "MAZ-114 territory". This
// file pins that contract as the four MAZ-114 scenarios expressed against a
// `Reaper`:
//
//   1. Single child   — register a live child, deliver its exit
//                        notification, a parked wait4 wakes with the right
//                        PID + encoded exit status.
//   2. Concurrent     — many live children; each wait4(-1) reaps exactly one
//                        distinct child; no double-report.
//   3. Clean failure  — wait4 with no matching child returns ECHILD, never
//                        a silent hang or a bogus PID.
//   4. WNOHANG / park  — a live-but-not-exited child: WNOHANG clear → "must
//                        park" sentinel; WNOHANG set → wpid==0 (not ECHILD).
//
// The package `mazzy/maz/linux/internal/forkexec` that these tests import
// does not exist yet, so this file does not compile → RED. The plan
// (task_plan.md) makes "these tests turn green" the objective Done-when for
// the host-testable slice, and adds the HVF end-to-end stage as the
// remaining Done-when that lands after the blocker chain.
package forkexec_test

import (
	"testing"

	"mazzy/maz/linux/internal/forkexec"
)

// childExit is the kernel→shepherd child-exit fact the reaper consumes.
// Mirrors proc.NotificationEvent{Type:EventChildExit} narrowed to the
// fields wait4 needs: which child, under which parent, with what raw status.
type childExit struct {
	parent int32
	child  int32
	raw    int32 // raw exit status (0..255), kernel-side; shepherd encodes
}

// encodeNormalExit is the Linux wait-status encoding for a normal exit:
// the low byte of the status is shifted into bits 8..15, low 7 bits zero
// (WIFEXITED). WEXITSTATUS(s) == (s >> 8) & 0xff. MAZ-80 owns the encoder;
// the acceptance test pins the observable result.
func encodeNormalExit(raw int32) int32 { return (raw & 0xff) << 8 }

// TestSingleChildWait4RecoversExitStatus is MAZ-114 scenario 1, the literal
// MAZ-62 DoD: "a test program inside mazzy can fork+exec a child binary and
// recover the exit status via wait4." A parent has one live child; the
// child-exit notification arrives; wait4(pid) returns that PID and the
// encoded exit status; a second wait4 for the same PID returns ECHILD.
func TestSingleChildWait4RecoversExitStatus(t *testing.T) {
	r := forkexec.NewReaper()

	const parent, child = int32(2), int32(3)
	const rawStatus = int32(42)

	r.RegisterChild(parent, child)

	// Before the exit notification, wait4 must NOT claim the child — it must
	// report "would park" (no zombie yet, WNOHANG clear).
	if res := r.Wait4(parent, child, 0); !res.WouldPark {
		t.Fatalf("wait4 before exit: got %+v, want WouldPark=true", res)
	}

	// Kernel delivers the child-exit notification.
	r.OnChildExit(parent, child, rawStatus)

	res := r.Wait4(parent, child, 0)
	if res.WouldPark {
		t.Fatalf("wait4 after exit unexpectedly parked: %+v", res)
	}
	if res.Pid != child {
		t.Errorf("wait4 pid = %d, want %d", res.Pid, child)
	}
	if want := encodeNormalExit(rawStatus); res.Status != want {
		t.Errorf("wait4 status = 0x%04x, want 0x%04x (WEXITSTATUS %d)",
			res.Status, want, rawStatus)
	}

	// Reaped once: a second wait4 for the same PID must be ECHILD, not a
	// duplicate report.
	if res2 := r.Wait4(parent, child, 0); res2.Errno != forkexec.ECHILD {
		t.Errorf("second wait4(%d) errno = %d, want ECHILD (%d)",
			child, res2.Errno, forkexec.ECHILD)
	}
}

// TestConcurrentChildrenEachReapedOnce is MAZ-114 scenario 3 (concurrent
// children, the `go build -p N` shape). Eight children exit; eight wait4(-1)
// calls reap eight DISTINCT pids; the ninth returns ECHILD.
func TestConcurrentChildrenEachReapedOnce(t *testing.T) {
	r := forkexec.NewReaper()

	const parent = int32(2)
	const n = 8
	for i := 0; i < n; i++ {
		child := int32(10 + i)
		r.RegisterChild(parent, child)
		r.OnChildExit(parent, child, int32(i)) // raw status = index
	}

	seen := map[int32]bool{}
	for i := 0; i < n; i++ {
		res := r.Wait4(parent, -1, 0) // pid==-1 → any child
		if res.WouldPark || res.Errno != 0 {
			t.Fatalf("wait4(-1) call %d: got %+v, want a reaped child", i, res)
		}
		if seen[res.Pid] {
			t.Fatalf("wait4(-1) returned pid %d twice — double reap", res.Pid)
		}
		seen[res.Pid] = true
	}
	if len(seen) != n {
		t.Errorf("reaped %d distinct children, want %d", len(seen), n)
	}
	// All drained: the next wait4(-1) must be ECHILD.
	if res := r.Wait4(parent, -1, 0); res.Errno != forkexec.ECHILD {
		t.Errorf("wait4(-1) after draining all: errno = %d, want ECHILD (%d)",
			res.Errno, forkexec.ECHILD)
	}
}

// TestNoChildrenReturnsECHILD is MAZ-114 scenario 4 (clean failure). A
// parent with no children at all must get ECHILD from wait4 — never a hang
// (WouldPark) and never a bogus pid.
func TestNoChildrenReturnsECHILD(t *testing.T) {
	r := forkexec.NewReaper()

	res := r.Wait4(int32(2), -1, 0)
	if res.WouldPark {
		t.Fatalf("wait4 with no children parked; want ECHILD (a hang here is the silent-drop bug)")
	}
	if res.Errno != forkexec.ECHILD {
		t.Errorf("wait4 with no children: errno = %d, want ECHILD (%d)", res.Errno, forkexec.ECHILD)
	}
}

// TestWNOHANGOnLiveChildReturnsZeroNotECHILD pins the WNOHANG corner of the
// clean-failure / park contract: a live child that has NOT exited must make
// a WNOHANG wait4 return wpid==0 (poll, nothing reapable yet) — distinctly
// NOT ECHILD (which would lie that the child is gone) and NOT a park.
func TestWNOHANGOnLiveChildReturnsZeroNotECHILD(t *testing.T) {
	r := forkexec.NewReaper()

	const parent, child = int32(2), int32(5)
	r.RegisterChild(parent, child)

	res := r.Wait4(parent, -1, forkexec.WNOHANG)
	if res.WouldPark {
		t.Fatalf("WNOHANG wait4 parked; WNOHANG must never block")
	}
	if res.Errno == forkexec.ECHILD {
		t.Fatalf("WNOHANG wait4 on a LIVE child returned ECHILD; child still exists")
	}
	if res.Pid != 0 {
		t.Errorf("WNOHANG wait4 with no zombie: pid = %d, want 0 (nothing reapable)", res.Pid)
	}
}
