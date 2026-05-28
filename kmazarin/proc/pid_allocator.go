// PIDAllocator stub for MAZ-69 — see pid_allocator_test.go for the contract.
//
// This file establishes the API the kernel will use for sequential monotonic
// PID allocation. The METHODS ARE STUBS — they return zero/error values until
// the Phase B implementation agent fills them in. The Phase 0 red tests in
// pid_allocator_test.go pin the behavior; the agent's job is to make them pass.
//
// Design decisions this allocator implements (see Linear MAZ-68 + MAZ-73):
//
//   - Universal PID space (= ShepherdId; no separate concept)
//   - MinPID = 2 (PID 0 invalid per Linux convention; PID 1 reserved for kernel sentinel)
//   - MaxPID = 4095 (12-bit, fits x86 PCID & ARM ASID hardware tag widths)
//   - Allocator is monotonic forward; freed PIDs do NOT reissue immediately
//   - Wraparound to MinPID when allocator hits MaxPID; skips in-use PIDs on wrap
//   - Exhaustion returns ErrPIDExhausted (matches Linux EAGAIN semantics)
//
// The concrete storage shape (dense array vs hash vs two-level) is left to
// MAZ-73's design phase. This file declares the abstract API only.

package proc

import "errors"

// PID range constants. See MAZ-68 + MAZ-73 for derivation.
const (
	// MinPID is the lowest allocatable PID. PID 0 is invalid (Linux convention);
	// PID 1 is reserved for the kernel sentinel.
	MinPID ShepherdId = 2

	// MaxPID is the highest allocatable PID. Chosen so a PID can double as a
	// hardware TLB context tag — x86 PCID is 12-bit (4096 contexts) and binds
	// tighter than ARM64's 16-bit ASID.
	MaxPID ShepherdId = 4095
)

// ErrPIDExhausted is returned by Alloc when no PID in [MinPID, MaxPID] is
// available. Maps to Linux EAGAIN at the syscall boundary.
var ErrPIDExhausted = errors.New("proc: all PIDs in use")

// PIDAllocator allocates ShepherdId values in [MinPID, MaxPID].
//
// The zero value is NOT ready for use — call NewPIDAllocator. (This may
// change once the storage shape is settled in MAZ-73.)
//
// The allocator is NOT goroutine-safe on its own. Callers in the kernel are
// expected to hold an appropriate lock; the eventual production allocator
// will be invoked from contexts where //go:nosplit applies.
type PIDAllocator struct {
	// Implementation TBD. Phase B agent decides the storage shape; the unit
	// tests in pid_allocator_test.go pin the externally observable behavior.
	_ struct{} // placeholder so the struct is non-empty until impl lands
}

// NewPIDAllocator returns a fresh allocator with no PIDs in use; the first
// Alloc will return MinPID.
func NewPIDAllocator() *PIDAllocator {
	return &PIDAllocator{}
}

// Alloc returns the next available PID in [MinPID, MaxPID], or
// (0, ErrPIDExhausted) if every PID in range is currently in use.
//
// Allocation is monotonic forward: each successful Alloc returns a PID
// strictly greater than the previous Alloc until MaxPID is reached, at
// which point the allocator wraps to MinPID and skips PIDs still in use.
// Freed PIDs are NOT reissued by the next Alloc — they only re-enter the
// allocatable pool after wraparound.
//
//go:nosplit
func (a *PIDAllocator) Alloc() (ShepherdId, error) {
	// STUB — implemented by Phase B agent.
	return 0, ErrPIDExhausted
}

// Free returns a PID to the allocatable pool. Idempotent — calling Free on
// a PID that is not currently allocated is a no-op (it doesn't panic, and
// doesn't corrupt allocator state).
//
// PIDs outside [MinPID, MaxPID] are silently ignored.
//
//go:nosplit
func (a *PIDAllocator) Free(pid ShepherdId) {
	// STUB — implemented by Phase B agent.
	_ = pid
}

// InUse reports whether the given PID is currently allocated.
// Returns false for PIDs outside [MinPID, MaxPID].
//
//go:nosplit
func (a *PIDAllocator) InUse(pid ShepherdId) bool {
	// STUB — implemented by Phase B agent.
	_ = pid
	return false
}
