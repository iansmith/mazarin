// Package wait holds the linux shepherd's pure, host-testable wait4
// decision logic: Linux exit-status encoding, child selection (specific
// PID vs any-child), the per-parent zombie set, and the WNOHANG / ECHILD /
// would-block ("must park") rules.
//
// Why this is in `internal/`: maz/linux's main package is `package main`
// (a shepherd binary), which makes unit-testing painful. Lifting the
// decision logic here keeps the shepherd thin and gives us a testable home.
// Matches the pattern at maz/linux/internal/{processrecord,execve}.
//
// Concurrency: NOT goroutine-safe on its own. The linux shepherd's main
// event loop is the sole user, and ChildExit notifications are routed onto
// that same lane via delegateCh (MAZ-80 step 3) so the zombie set and
// waiter map are only ever touched from one goroutine. If concurrent
// access ever becomes a requirement, wrap with a mutex.
//
// Status-encoding contract: the KERNEL ships the RAW exit code (MAZ-77);
// the linux shepherd owns the Linux ABI and applies the <<8 encoding here
// (EncodeWaitStatus). Reaping must NOT consume the underlying ChildExit
// event in a way that hides it from the future SIGCHLD path (MAZ-64).
//
// =====================================================================
// PHASE 0 STUB. These declarations exist so the RED tests in wait_test.go
// COMPILE and FAIL on assertions (not on "undefined: X"). The real
// implementation lands in the MAZ-80 implementation phase. Do not treat
// the zero-value behavior below as a spec — wait_test.go is the spec.
// =====================================================================

package wait

// ECHILD is the Linux errno for "no child processes" (negated, as the
// shepherd returns it to the blocked caller). The implementation phase
// also adds the matching ECHILD = -10 to maz/linux/errno.go.
//
// This constant carries its REAL value even in the Phase-0 stub so the
// no-children RED test fails for the right reason (the stub Decide returns
// Errno 0, not ECHILD) instead of trivially matching a zero-valued stub.
const ECHILD = -10

// WNOHANG is the wait4 option bit meaning "return immediately if no child
// has exited" rather than blocking.
const WNOHANG = 1

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

// Reaper holds the shepherd-side child bookkeeping for a single linux
// shepherd: which children are alive, and which have exited (zombies)
// awaiting a wait4 to reap them. Keyed internally by parent PID so a
// future multi-process shepherd reaps only its own children.
type Reaper struct {
	// STUB: real fields (live-child set + per-parent zombie map) land in
	// the implementation phase.
}

// New returns an empty Reaper.
func New() *Reaper {
	return &Reaper{} // STUB
}

// EncodeWaitStatus converts a RAW exit code (0..255, as delivered by the
// kernel in ProcessNotification.ExitStatus) into a Linux wait status:
// (raw & 0xff) << 8, which decodes as a normal exit (WIFEXITED) with
// WEXITSTATUS == raw.
func EncodeWaitStatus(rawExitCode int) int {
	return 0 // STUB
}

// RegisterChild records that childPID is a live child of parentPID. Lets
// Decide distinguish "no such child → ECHILD" from "child alive but not
// yet exited → would block".
func (r *Reaper) RegisterChild(parentPID, childPID int32) {
	// STUB
}

// AddZombie records that childPID (a child of parentPID) has exited with
// the given RAW exit code, making it reapable by a subsequent wait4.
func (r *Reaper) AddZombie(parentPID, childPID int32, rawExitStatus int) {
	// STUB
}

// Decide computes the wait4 outcome for a caller in parentPID waiting on
// `pid` (>0 specific child; -1 any child) with the given options bitmask.
// It reaps a matching zombie if one exists (removing it so it is reaped
// exactly once), otherwise reports ECHILD, would-block, or — under
// WNOHANG — a (0, 0) no-op.
func (r *Reaper) Decide(parentPID int32, pid int64, options int) Outcome {
	return Outcome{} // STUB
}
