// Package wait holds the linux shepherd's pure, host-testable wait4
// decision logic: Linux exit-status encoding, child selection (specific
// PID vs any-child), the per-parent live-child and zombie sets, and the
// WNOHANG / ECHILD / would-block ("must park") rules.
//
// Why this is in `internal/`: maz/linux's main package is `package main`
// (a shepherd binary), which makes unit-testing painful. Lifting the
// decision logic here keeps the shepherd thin and gives us a testable home.
// Matches the pattern at maz/linux/internal/{processrecord,execve}.
//
// Concurrency: the Reaper IS goroutine-safe. wait4 dispatch runs on the
// linux shepherd's ring-2 file-lane WORKER POOL (several goroutines), while
// child-exit / exec-complete notifications are funneled onto the single
// delegate-loop goroutine. Both lanes touch the same Reaper, so every
// method takes the Reaper's internal mutex. (The notification funnel keeps
// the non-goroutine-safe processrecord.Table single-threaded; the Reaper
// has its own state and guards it directly.)
//
// Status-encoding contract: the KERNEL ships the RAW exit code (MAZ-77);
// the linux shepherd owns the Linux ABI and applies the <<8 encoding here
// (EncodeWaitStatus). Recording a zombie does NOT consume the underlying
// ChildExit event — the zombie persists in the set until a wait4 reaps it,
// which is also what the future SIGCHLD path (MAZ-64) needs.

package wait

import "sync"

// ECHILD is the Linux errno for "no child processes" (negated, as the
// shepherd returns it to the blocked caller). maz/linux/errno.go carries
// the matching `ECHILD int64 = -10` used at the dispatch boundary.
const ECHILD = -10

// WNOHANG is the wait4 option bit meaning "return immediately if no child
// has exited" rather than blocking.
const WNOHANG = 1

// anyChild is the wait4 `pid` sentinel meaning "reap any child" (Linux's
// pid == -1). Specific-child waits pass pid > 0.
const anyChild = -1

// Outcome is the result of a wait4 decision. Exactly one of three shapes:
//   - reap:        Errno == 0, MustPark == false, WPid == reaped child PID,
//                  EncodedStatus == the Linux-encoded wait status.
//   - error:       Errno != 0 (e.g. ECHILD).
//   - would-block: MustPark == true — the shepherd defers Reply (parks the
//                  caller) until a matching child exits. WNOHANG turns this
//                  into a (WPid == 0, Errno == 0) immediate return instead.
type Outcome struct {
	WPid          int32
	EncodedStatus int
	Errno         int
	MustPark      bool
}

// Reaper holds the shepherd-side child bookkeeping for the linux shepherd:
// which children are alive, which have exited (zombies) awaiting a wait4 to
// reap them, and which callers are parked waiting for a child to exit.
// Keyed by parent PID so each linux program reaps only its own children.
type Reaper struct {
	mu sync.Mutex
	// live maps parentPID -> set of live child PIDs. Lets Decide tell
	// "no such child -> ECHILD" apart from "child alive, not yet exited ->
	// would block".
	live map[int32]map[int32]struct{}
	// zombies maps parentPID -> (childPID -> RAW exit status). A child moves
	// from live to zombies on exit and is removed on reap.
	zombies map[int32]map[int32]int
}

// New returns an empty Reaper.
func New() *Reaper {
	return &Reaper{
		live:    make(map[int32]map[int32]struct{}),
		zombies: make(map[int32]map[int32]int),
	}
}

// EncodeWaitStatus converts a RAW exit code (0..255, as delivered by the
// kernel in ProcessNotification.ExitStatus) into a Linux wait status:
// (raw & 0xff) << 8, which decodes as a normal exit (WIFEXITED) with
// WEXITSTATUS == raw.
func EncodeWaitStatus(rawExitCode int) int {
	return (rawExitCode & 0xff) << 8
}

// RegisterChild records that childPID is a live child of parentPID. Lets
// Decide distinguish "no such child -> ECHILD" from "child alive but not
// yet exited -> would block". Called when the parent's EventExecComplete
// notification arrives.
func (r *Reaper) RegisterChild(parentPID, childPID int32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	set := r.live[parentPID]
	if set == nil {
		set = make(map[int32]struct{})
		r.live[parentPID] = set
	}
	set[childPID] = struct{}{}
}

// AddZombie records that childPID (a child of parentPID) has exited with the
// given RAW exit code, making it reapable by a subsequent wait4. The child
// is removed from the live set in the same step. Called when the parent's
// EventChildExit notification arrives.
func (r *Reaper) AddZombie(parentPID, childPID int32, rawExitStatus int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if set := r.live[parentPID]; set != nil {
		delete(set, childPID)
		if len(set) == 0 {
			delete(r.live, parentPID)
		}
	}
	z := r.zombies[parentPID]
	if z == nil {
		z = make(map[int32]int)
		r.zombies[parentPID] = z
	}
	z[childPID] = rawExitStatus
}

// Decide computes the wait4 outcome for a caller in parentPID waiting on
// `pid` (>0 specific child; -1 any child) with the given options bitmask.
// It reaps a matching zombie if one exists (removing it so it is reaped
// exactly once), otherwise reports ECHILD, would-block, or — under
// WNOHANG — a (0, 0) no-op.
func (r *Reaper) Decide(parentPID int32, pid int64, options int) Outcome {
	r.mu.Lock()
	defer r.mu.Unlock()

	if pid > 0 {
		return r.decideSpecific(parentPID, int32(pid), options)
	}
	// pid == -1 (and, conservatively, any other non-positive value): any child.
	return r.decideAny(parentPID, options)
}

// decideSpecific resolves wait4(pid > 0). Caller holds r.mu.
func (r *Reaper) decideSpecific(parentPID, child int32, options int) Outcome {
	if z := r.zombies[parentPID]; z != nil {
		if raw, ok := z[child]; ok {
			r.reapLocked(parentPID, child)
			return Outcome{WPid: child, EncodedStatus: EncodeWaitStatus(raw)}
		}
	}
	if _, alive := r.live[parentPID][child]; alive {
		return wouldBlock(options)
	}
	return Outcome{Errno: ECHILD}
}

// decideAny resolves wait4(-1). Caller holds r.mu.
func (r *Reaper) decideAny(parentPID int32, options int) Outcome {
	if z := r.zombies[parentPID]; len(z) > 0 {
		for child, raw := range z {
			r.reapLocked(parentPID, child)
			return Outcome{WPid: child, EncodedStatus: EncodeWaitStatus(raw)}
		}
	}
	if len(r.live[parentPID]) > 0 {
		return wouldBlock(options)
	}
	return Outcome{Errno: ECHILD}
}

// wouldBlock turns a "no reapable child yet, but one is alive" condition into
// the right Outcome: park when blocking, or a (0, 0) no-op under WNOHANG.
func wouldBlock(options int) Outcome {
	if options&WNOHANG != 0 {
		return Outcome{} // WPid 0, no error, no park
	}
	return Outcome{MustPark: true}
}

// reapLocked removes a reaped zombie from the per-parent set (and prunes the
// now-empty parent map). Caller holds r.mu. This is the "reaped exactly once"
// guarantee: the entry is gone before Decide returns.
func (r *Reaper) reapLocked(parentPID, child int32) {
	z := r.zombies[parentPID]
	delete(z, child)
	if len(z) == 0 {
		delete(r.zombies, parentPID)
	}
}

// DropParent removes ALL bookkeeping for parentPID — live children, pending
// zombies. Called on shepherd death so a dying linux program leaves no stale
// child state behind. (Its parked wait4 callers, if any, are unblocked
// kernel-side by the delegate death-cleanup; the shepherd's own parked-waiter
// store is dropped alongside this in main.go.) Idempotent.
func (r *Reaper) DropParent(parentPID int32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.live, parentPID)
	delete(r.zombies, parentPID)
}
