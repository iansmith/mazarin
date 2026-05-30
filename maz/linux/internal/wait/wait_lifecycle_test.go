// Lifecycle + concurrency tests for the wait4 Reaper (MAZ-80). These cover
// behavior beyond the Phase-0 decision-logic tests: the live-child set that
// distinguishes ECHILD from would-block across an exit, per-parent isolation,
// DropParent cleanup on shepherd death, and goroutine-safety of the Reaper
// (run with `-race` to verify the mutex; correctness holds either way).

package wait

import (
	"sync"
	"testing"
)

// TestRegisterThenExitThenReap walks one child through its full lifecycle:
// live (would block) -> exited (reap with status) -> reaped (ECHILD).
func TestRegisterThenExitThenReap(t *testing.T) {
	r := New()
	const parent = 1

	// Live child, no zombie: a blocking wait4 must park.
	r.RegisterChild(parent, 100)
	if out := r.Decide(parent, -1, 0); !out.MustPark {
		t.Fatalf("Decide(-1) with a live child: MustPark=%v, want true", out.MustPark)
	}

	// Child exits: the zombie is now reapable, and the live set no longer
	// reports it (a second exit notification wouldn't double-count).
	r.AddZombie(parent, 100, 9)
	out := r.Decide(parent, -1, 0)
	if out.MustPark || out.Errno != 0 || out.WPid != 100 {
		t.Fatalf("Decide(-1) after exit = %+v, want reap of 100", out)
	}
	if out.EncodedStatus != EncodeWaitStatus(9) {
		t.Errorf("reaped status = %#x, want %#x", out.EncodedStatus, EncodeWaitStatus(9))
	}

	// Reaped: nothing left, neither live nor zombie -> ECHILD.
	if out := r.Decide(parent, -1, 0); out.Errno != ECHILD {
		t.Errorf("Decide(-1) after reap: errno = %d, want ECHILD (%d)", out.Errno, ECHILD)
	}
}

// TestPerParentIsolation — a zombie under parent A is invisible to parent B.
func TestPerParentIsolation(t *testing.T) {
	r := New()
	const parentA, parentB = 1, 2
	r.RegisterChild(parentA, 100)
	r.AddZombie(parentA, 100, 3)

	// Parent B has no children at all -> ECHILD, must not steal A's zombie.
	if out := r.Decide(parentB, -1, 0); out.Errno != ECHILD {
		t.Errorf("Decide(parentB, -1): errno = %d, want ECHILD (%d)", out.Errno, ECHILD)
	}
	// Parent A still reaps its own zombie.
	if out := r.Decide(parentA, -1, 0); out.WPid != 100 {
		t.Errorf("Decide(parentA, -1): WPid = %d, want 100", out.WPid)
	}
}

// TestDropParentClearsBookkeeping — DropParent removes both the live set and
// pending zombies for a parent (shepherd-death cleanup), and is idempotent.
func TestDropParentClearsBookkeeping(t *testing.T) {
	r := New()
	const parent = 1
	r.RegisterChild(parent, 100)
	r.RegisterChild(parent, 200)
	r.AddZombie(parent, 200, 5) // 200 is now a zombie, 100 still live

	r.DropParent(parent)

	// Everything for this parent is gone -> ECHILD for both any and specific.
	if out := r.Decide(parent, -1, 0); out.Errno != ECHILD {
		t.Errorf("Decide(-1) after DropParent: errno = %d, want ECHILD (%d)", out.Errno, ECHILD)
	}
	if out := r.Decide(parent, 100, 0); out.Errno != ECHILD {
		t.Errorf("Decide(pid=100) after DropParent: errno = %d, want ECHILD (%d)", out.Errno, ECHILD)
	}
	// Idempotent: a second DropParent is a no-op.
	r.DropParent(parent)
}

// TestConcurrentReapNoDoubleReap — many goroutines exit children and reap
// concurrently; every zombie is reaped exactly once, never twice. Guards the
// Reaper's mutex (run with `-race`).
func TestConcurrentReapNoDoubleReap(t *testing.T) {
	r := New()
	const parent = 1
	const n = 64
	for i := int32(0); i < n; i++ {
		r.RegisterChild(parent, 1000+i)
	}

	// Producers: each adds one zombie.
	var wg sync.WaitGroup
	for i := int32(0); i < n; i++ {
		wg.Add(1)
		go func(child int32) {
			defer wg.Done()
			r.AddZombie(parent, child, int(child)&0xff)
		}(1000 + i)
	}
	wg.Wait()

	// Consumers: reap concurrently, recording each reaped PID. A double-reap
	// would surface as a PID seen twice or more than n successful reaps.
	var mu sync.Mutex
	seen := make(map[int32]int)
	reaps := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := r.Decide(parent, -1, 0)
			if out.Errno != 0 || out.MustPark {
				return
			}
			mu.Lock()
			seen[out.WPid]++
			reaps++
			mu.Unlock()
		}()
	}
	wg.Wait()

	if reaps != n {
		t.Fatalf("reaped %d children, want %d", reaps, n)
	}
	for pid, count := range seen {
		if count != 1 {
			t.Errorf("PID %d reaped %d times, want exactly 1", pid, count)
		}
	}
	// Nothing left.
	if out := r.Decide(parent, -1, 0); out.Errno != ECHILD {
		t.Errorf("Decide(-1) after draining: errno = %d, want ECHILD (%d)", out.Errno, ECHILD)
	}
}
