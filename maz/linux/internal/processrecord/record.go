// Package processrecord holds the linux shepherd's per-PID process state.
//
// This package is the shepherd-side counterpart to the kernel's proc.Shepherd
// state. Where the kernel tracks "this is a process" (Shepherd), the linux
// shepherd tracks "this is what Linux sees about that process" — its FD
// table, cwd, environ, signal mask, pending signals, etc.
//
// MAZ-72 stands up the keyed-record infrastructure; the actual fields are
// MOSTLY PLACEHOLDERS for downstream tickets to populate:
//
//   - FDTable      → [MAZ-63] (FD plumbing)
//   - CWD          → [MAZ-63] (per-process cwd lifecycle)
//   - Environ      → [MAZ-62] / [MAZ-63] (set by execve / inherited on clone)
//   - SigMask      → [MAZ-64] (signal mask manipulation)
//   - PendingSigs  → [MAZ-64] (process-directed pending signals)
//
// Why this is in `internal/`: maz/linux's main package is `package main`
// (a shepherd binary), which makes unit-testing painful. Lifting this
// table into an internal package keeps the linux shepherd thin and gives
// us a testable home. Matches the pattern at `maz/protocol-http/internal/`.
//
// Concurrency: NOT goroutine-safe on its own. The linux shepherd's main
// event loop is the sole user; if downstream tickets introduce concurrent
// access, add a sync.RWMutex.

package processrecord

import "errors"

// PID is the Linux-visible process identifier. Mirrors the kernel's
// proc.ShepherdId (currently int16, future int32 per MAZ-68); we use a
// local int32 type to keep this package free of kmazarin/proc imports
// (clean address-space separation; the kernel→linux shepherd transport
// crosses a wire boundary anyway).
type PID int32

// PerPIDRecord holds Linux-emulation per-process state for a single PID.
// Most fields are placeholders for downstream tickets — see the package
// doc for the mapping.
type PerPIDRecord struct {
	PID PID

	// CWD — current working directory. Populated by MAZ-63.
	CWD string

	// Environ — environ block (raw, null-separated bytes). Set on execve,
	// inherited on clone. Populated by MAZ-62 / MAZ-63 plumbing.
	Environ []byte

	// SigMask — blocked signal mask (1 bit per signal, 1..64). MAZ-64.
	SigMask uint64

	// PendingSigs — pending process-directed signals. MAZ-64.
	PendingSigs uint64

	// FDTable — placeholder for MAZ-63's FD table implementation.
	// Typed as `any` so MAZ-63 can plug in whatever concrete type fits;
	// callers in this package don't introspect it.
	FDTable any
}

// ErrPIDExists is returned by Table.Create when a record for the given
// PID already exists.
var ErrPIDExists = errors.New("processrecord: PID already in table")

// Table indexes PerPIDRecords by PID. Lookup is O(1).
//
// NOT goroutine-safe — the linux shepherd's main event loop is the sole
// user. If concurrent access becomes a requirement, wrap with a mutex.
type Table struct {
	// Implementation TBD. Phase B agent decides between map vs. sparse
	// array vs. other; tests pin the externally observable behavior.
}

// New returns an empty Table.
func New() *Table {
	return &Table{}
}

// Create allocates a new record for pid and returns a pointer to it.
// Returns ErrPIDExists if a record for pid is already present.
func (t *Table) Create(pid PID) (*PerPIDRecord, error) {
	// STUB — implemented by Phase B agent.
	_ = pid
	return nil, ErrPIDExists
}

// Get returns the record for pid. The second return value is false if no
// record exists.
func (t *Table) Get(pid PID) (*PerPIDRecord, bool) {
	// STUB — implemented by Phase B agent.
	_ = pid
	return nil, false
}

// Remove deletes the record for pid. Idempotent — removing a PID that is
// not in the table is a no-op.
func (t *Table) Remove(pid PID) {
	// STUB — implemented by Phase B agent.
	_ = pid
}

// Len returns the number of records currently in the table.
func (t *Table) Len() int {
	// STUB — implemented by Phase B agent.
	return 0
}
