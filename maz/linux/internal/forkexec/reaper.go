// Package forkexec is MAZ-114's acceptance surface for the fork+exec+wait4
// round trip (the MAZ-62 umbrella DoD). It does NOT re-implement the wait4
// decision logic — that single source of truth lives in
// maz/linux/internal/wait (MAZ-80). This package is a thin acceptance
// ADAPTER: it re-expresses wait.Reaper through the four-scenario vocabulary
// MAZ-114's spec is written in (RegisterChild / OnChildExit / Wait4), so the
// acceptance_test.go contract reads as the umbrella DoD rather than as the
// shepherd's internal Decide()/AddZombie() shapes.
//
// Why an adapter and not a re-implementation: universal rule §5 (one
// definition per value). The zombie set, the live-child set, the
// reap-exactly-once delete, the ECHILD / WNOHANG / would-park rules, and the
// Linux (raw&0xff)<<8 status encoding are all owned by package wait. Every
// method here delegates straight into a wait.Reaper; there is no parallel
// bookkeeping or encoder.
package forkexec

import "mazzy/maz/linux/internal/wait"

// ECHILD and WNOHANG mirror the wait package's constants so the acceptance
// test names them through this package's vocabulary. They are the SAME values
// (re-exported, not redefined): a drift here would be caught by the tests,
// which compare Wait4 results against these and against wait's behavior.
const (
	// ECHILD is the Linux "no child processes" errno (negated, as the
	// shepherd returns it to the caller).
	ECHILD = wait.ECHILD
	// WNOHANG is the wait4 option bit meaning "poll, don't block".
	WNOHANG = wait.WNOHANG
)

// Result is one wait4 outcome in MAZ-114's acceptance vocabulary, adapted
// from wait.Outcome:
//   - reaped:     WouldPark == false, Errno == 0, Pid == the reaped child,
//                 Status == the Linux-encoded wait status.
//   - error:      Errno != 0 (e.g. ECHILD).
//   - WNOHANG no-op: Pid == 0, Errno == 0, WouldPark == false.
//   - would-park: WouldPark == true (a blocking wait with a live-but-unexited
//                 child) — the shepherd defers Reply until a child exits.
type Result struct {
	Pid       int32
	Status    int32
	Errno     int
	WouldPark bool
}

// Reaper is the acceptance adapter around wait.Reaper. It holds no state of
// its own — every method forwards to the wrapped reaper, which owns all child
// bookkeeping.
type Reaper struct {
	inner *wait.Reaper
}

// NewReaper returns a Reaper wrapping a fresh wait.Reaper.
func NewReaper() *Reaper { return &Reaper{inner: wait.New()} }

// RegisterChild records childPID as a live child of parentPID — the
// EventExecComplete fact (the child has finished execve and is running).
func (r *Reaper) RegisterChild(parentPID, childPID int32) {
	r.inner.RegisterChild(parentPID, childPID)
}

// OnChildExit records that childPID exited with the given RAW exit code
// (0..255) — the EventChildExit fact. The child becomes a reapable zombie;
// wait owns the live->zombie move and the encoding applied at reap time.
func (r *Reaper) OnChildExit(parentPID, childPID, rawExitCode int32) {
	r.inner.AddZombie(parentPID, childPID, int(rawExitCode))
}

// Wait4 resolves a wait4(pid, options) for a caller in parentPID, adapting
// wait.Decide's Outcome into a Result. pid > 0 selects a specific child;
// pid == -1 (or 0) selects any child; the encoding and reap-exactly-once
// guarantee are wait's.
func (r *Reaper) Wait4(parentPID, pid int32, options int) Result {
	out := r.inner.Decide(parentPID, int64(pid), options)
	return Result{
		Pid:       out.WPid,
		Status:    int32(out.EncodedStatus),
		Errno:     out.Errno,
		WouldPark: out.MustPark,
	}
}
