// Package cloneexec holds the linux shepherd's per-thread "post-clone,
// pre-execve buffering window" state machine (MAZ-118).
//
// Background — the clone_exec dance:
//
//	When a Linux program does the classic fork()+exec() idiom, glibc's
//	posix_spawn / Go's os/exec issue a process-flavor clone() followed by a
//	short sequence of in-child setup syscalls (dup3 to wire up stdio, close
//	to drop pipe ends, fcntl(F_SETFD) to set FD_CLOEXEC, optionally chdir)
//	and finally execve(). The mazzy linux shepherd does NOT run real child
//	processes between the clone and the execve; instead it BUFFERS that
//	in-child setup into a structured intent and replays it inside the new
//	shepherd at startup, immediately before the target ELF entry point. The
//	kernel-internal clone_exec call (MAZ-75) carries the buffered intent;
//	the kernel stores it on the child Shepherd (StartupIntent / StartupCwd)
//	and the child startup stub (MAZ-113) applies it.
//
// This package is the PURE, host-testable half of that machinery: the state
// machine that accumulates the buffered intent. It performs NO IPC and NO fs
// calls — it is a value-machine, fully unit-testable under `task test` via
// ./maz/linux/internal/... (Taskfile.yml).
//
// Boundaries (who owns what):
//
//   - MAZ-78 (clone dispatch): inspects clone flags, routes process-flavor
//     clones here via Open, and routes the buffered in-child syscalls here
//     via Buffer / SetCwd / Poison. Plumbing a caller-TID into
//     sys.SyscallRequest is MAZ-78's prereq — this package only consumes a
//     tid it is handed.
//   - MAZ-79 (execve dispatch): consumes Flush to marshal the clone_exec
//     request to the kernel.
//   - MAZ-113 / clone_exec SVC: applies the buffered ops inside the child.
//
// Single source of truth (MAZ-118 locked decision): the op encoding
// (IntentOp / IntentKind) and the caps (MaxStartupIntentOps /
// MaxStartupCwdBytes) are IMPORTED from shared/linuxabi — the SAME types the
// kernel (kmazarin/proc) uses via aliases. There is NO shepherd-local mirror;
// drift is structurally impossible because both sides reference one
// definition.
//
// Concurrency: NOT goroutine-safe. The window is keyed per cloning thread
// (TID); each TID's window is touched only by that thread's syscall path
// between its clone and its matching execve (MAZ-62: forkAndExecInChild runs
// the buffered sequence on the same parent thread). The Registry's map is the
// only shared structure; if MAZ-78 introduces concurrent dispatch across
// TIDs, wrap the Registry with a sync.Mutex.
//
// =====================================================================
// STUB — Phase 0 (MAZ-118). The types and method set below exist so the RED
// tests in window_test.go COMPILE and FAIL on assertions (RED state),
// matching the processrecord Phase-0 pattern. The bodies are deliberately
// non-functional (they return zero values, NOT panics, so the test binary
// runs and each test reports a clean per-assertion FAIL); the implementation
// phase replaces them with the real state machine.
// =====================================================================

package cloneexec

import (
	"errors"

	"mazzy/shared/linuxabi"
)

// Sentinel errors returned by the Registry API. They are distinct so callers
// (and tests) can branch on the failure category via errors.Is.
var (
	// ErrNoWindow — operation referenced a TID with no open window.
	ErrNoWindow = errors.New("cloneexec: no open window for tid")
	// ErrWindowExists — Open called for a TID that already has a window.
	ErrWindowExists = errors.New("cloneexec: window already open for tid")
	// ErrPoisoned — the window is poisoned; Flush refuses to emit partial intent.
	ErrPoisoned = errors.New("cloneexec: window poisoned by unrecognized syscall")
	// ErrIntentOverflow — buffering would exceed linuxabi.MaxStartupIntentOps (E2BIG-equiv).
	ErrIntentOverflow = errors.New("cloneexec: buffered intent op count exceeds cap")
	// ErrCwdOverflow — SetCwd path exceeds linuxabi.MaxStartupCwdBytes.
	ErrCwdOverflow = errors.New("cloneexec: chdir path exceeds cap")
)

// Registry maps a cloning thread's TID to its buffering window. The linux
// shepherd holds one Registry. NOT goroutine-safe (see package doc).
type Registry struct {
	windows map[int32]*window
}

// window is the per-TID buffered intent. Unexported: callers go through the
// Registry, which enforces the open/poison/cap invariants.
type window struct {
	ops      []linuxabi.IntentOp
	cwd      []byte
	poisoned bool
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{windows: make(map[int32]*window)}
}

// Open begins a buffering window for tid (called by MAZ-78 on a
// process-flavor clone). Returns ErrWindowExists if one is already open.
func (r *Registry) Open(tid int32) error {
	return nil // STUB
}

// IsOpen reports whether tid currently has a window (open or poisoned but not
// yet flushed/aborted).
func (r *Registry) IsOpen(tid int32) bool {
	return false // STUB
}

// Buffer appends one FD-flavored op (dup3 / close / F_SETFD) to tid's window.
// Overflow past linuxabi.MaxStartupIntentOps returns ErrIntentOverflow and
// poisons the window (no truncation). An IntentNone (or otherwise
// unrecognized) kind is treated as an unbufferable syscall and poisons the
// window.
func (r *Registry) Buffer(tid int32, op linuxabi.IntentOp) error {
	return nil // STUB
}

// Poison marks tid's window poisoned (called by MAZ-78 when an unrecognized /
// unbufferable syscall arrives in the window). A poisoned window still exists
// (so the matching execve can observe the failure via Flush) but Flush will
// refuse to emit its intent.
func (r *Registry) Poison(tid int32) error {
	return nil // STUB
}

// SetCwd records a chdir target for tid's window. Replaces any prior cwd
// (last chdir wins). A path longer than linuxabi.MaxStartupCwdBytes returns
// ErrCwdOverflow and poisons the window.
func (r *Registry) SetCwd(tid int32, path []byte) error {
	return nil // STUB
}

// Flush consumes tid's window and returns the buffered ops + cwd for the
// matching execve (MAZ-79). After a successful Flush the window is removed (a
// second Flush returns ErrNoWindow). A poisoned window returns ErrPoisoned
// and emits no partial intent. cwd is nil when no chdir was set.
func (r *Registry) Flush(tid int32) (ops []linuxabi.IntentOp, cwd []byte, err error) {
	return nil, nil, nil // STUB
}

// Abort drops tid's window without producing intent. Called on thread-exit or
// shepherd cleanup (syscalls.go) when a clone had no matching execve.
// Idempotent — aborting an absent TID is a no-op.
func (r *Registry) Abort(tid int32) {
	// STUB
}

// Len returns the number of windows currently tracked (open + poisoned, not
// yet flushed/aborted). Used to assert no leaked windows.
func (r *Registry) Len() int {
	return 0 // STUB
}
