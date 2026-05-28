// Process bookkeeping for Linux emulation (MAZ-70).
//
// Today's Shepherd struct was designed for boot-time launched shepherds that
// never fork. Linux emulation introduces forked children, parent/child
// relationships, zombie reaping, and environ inheritance across execve.
//
// This file declares the public API the kernel uses for that bookkeeping.
// The METHODS ARE STUBS — they return zero/error values until the Phase B
// implementation agent fills them in. The Phase 0 red tests in
// shepherd_state_test.go pin the behavior.
//
// Field additions on the Shepherd struct (ParentPID, Children, NumChildren,
// Zombie, ExitStatus, Environ, EnvironLen) live in proc.go because Go
// requires struct fields to be declared in the struct literal.
//
// Out of scope for MAZ-70:
//   - wait4 dispatch / coordination with the linux shepherd (MAZ-80)
//   - SIGCHLD raise on child exit (MAZ-89)
//   - Cross-process signal delivery (MAZ-64 container)
//
// In scope:
//   - Field storage + sizing on Shepherd
//   - Methods to manipulate child lists, zombie state, and environ
//   - All methods must be //go:nosplit (kernel context)

package proc

import "errors"

// Capacity constants for the per-Shepherd bookkeeping.
const (
	// MaxChildrenPerShepherd caps the number of concurrent live children.
	// `go build -p N` peaks around tens of children; 64 covers comfortably.
	// Tunable later if real workloads need more.
	MaxChildrenPerShepherd = 64

	// MaxEnvironBytes caps the environ block size for execve.
	// Typical environs are < 1KB; 8KB tolerates large PATH / GOPATH /
	// GOFLAGS-heavy build environments. Tunable later.
	MaxEnvironBytes = 8192
)

// Error sentinels for the bookkeeping methods.
var (
	// ErrTooManyChildren is returned by AddChild when the child list is full.
	ErrTooManyChildren = errors.New("proc: child list full")

	// ErrEnvironTooLarge is returned by SetEnviron when the env exceeds
	// MaxEnvironBytes.
	ErrEnvironTooLarge = errors.New("proc: environ exceeds MaxEnvironBytes")
)

// AddChild inserts child into this shepherd's child list. Returns
// ErrTooManyChildren if the list is full. Adding a child already present
// is a no-op (returns nil, does NOT increment the count).
//
//go:nosplit
func (s *Shepherd) AddChild(child ShepherdId) error {
	// STUB — implemented by Phase B agent.
	_ = child
	return ErrTooManyChildren
}

// RemoveChild removes child from this shepherd's child list. Idempotent
// — removing a PID not currently in the list is a no-op.
//
//go:nosplit
func (s *Shepherd) RemoveChild(child ShepherdId) {
	// STUB — implemented by Phase B agent.
	_ = child
}

// HasChild reports whether child is currently in this shepherd's
// child list.
//
//go:nosplit
func (s *Shepherd) HasChild(child ShepherdId) bool {
	// STUB — implemented by Phase B agent.
	_ = child
	return false
}

// ChildCount returns the number of children currently in the list.
//
//go:nosplit
func (s *Shepherd) ChildCount() int32 {
	// STUB — implemented by Phase B agent.
	return 0
}

// EachChild calls fn for each child PID in the list. Iteration order is
// implementation-defined. If fn returns false, iteration stops early.
//
//go:nosplit
func (s *Shepherd) EachChild(fn func(child ShepherdId) bool) {
	// STUB — implemented by Phase B agent.
	_ = fn
}

// MarkZombie marks this shepherd as exited and records the exit status.
// If the shepherd is already a zombie, MarkZombie is a no-op (the original
// exit status is preserved).
//
//go:nosplit
func (s *Shepherd) MarkZombie(status int32) {
	// STUB — implemented by Phase B agent.
	_ = status
}

// IsZombie reports whether the shepherd has exited but not been reaped.
//
//go:nosplit
func (s *Shepherd) IsZombie() bool {
	// STUB — implemented by Phase B agent.
	return false
}

// Reap clears the zombie state and returns the recorded exit status.
// On a non-zombie shepherd, returns 0 and does not mutate state.
//
//go:nosplit
func (s *Shepherd) Reap() int32 {
	// STUB — implemented by Phase B agent.
	return 0
}

// SetEnviron stores the raw environ bytes for use at the next execve.
// Returns ErrEnvironTooLarge if env exceeds MaxEnvironBytes. An empty
// env (len == 0) is allowed and clears any previously stored environ.
//
// The bytes are COPIED into the Shepherd's Environ array; the caller's
// slice is safe to modify or release after return.
//
//go:nosplit
func (s *Shepherd) SetEnviron(env []byte) error {
	// STUB — implemented by Phase B agent.
	_ = env
	return ErrEnvironTooLarge
}

// GetEnviron returns a read-only view of the stored environ bytes.
// The returned slice aliases the Shepherd's Environ array; callers MUST
// NOT modify it. Returns an empty slice if no environ has been set.
//
//go:nosplit
func (s *Shepherd) GetEnviron() []byte {
	// STUB — implemented by Phase B agent.
	return nil
}
