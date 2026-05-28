// ShepherdStorage — sparse PID-keyed storage for live Shepherd records (MAZ-73).
//
// Today's shepherd storage is a dense `ShepherdListData [MaxShepherds=32]Shepherd`
// array. Under the post-MAZ-68/-73 PID model, PIDs run 2..4095 but the number
// of LIVE shepherds at any time is much smaller (peak ~100 for go-build-style
// workloads). A dense PID-indexed array of Shepherd structs would be tens of
// MB — too much. Sparse storage with a PID-to-slot index is the chosen shape.
//
// This file declares the API. The METHODS ARE STUBS; the Phase B implementation
// agent fills them in. Phase 0 tests in shepherd_storage_test.go pin behavior.
//
// Design decisions implemented here (see Linear MAZ-68 + MAZ-73):
//
//   - Sparse: only live shepherds occupy slots.
//   - Bounded: at most MaxLiveShepherds slots (start at 256; tunable later).
//   - O(1) lookup by PID via an internal PID→slot index.
//   - Exhaustion returns ErrShepherdSlotExhausted.
//   - Reallocating an existing PID returns ErrShepherdPIDInUse.
//   - PIDs outside [MinPID, MaxPID] are rejected.
//
// Out of scope here:
//
//   - The int16 → int32 ShepherdId sweep (Phase B implementation step;
//     no unit test — either the codebase compiles or it doesn't).
//   - Switching today's `createUserspaceThreadImpl` from the
//     `StaticAllocator + ShepherdListData[MaxShepherds]` pattern to this new
//     storage (cutover is also a Phase B step but driven by separate review).

package proc

import "errors"

// PID range constants — same set defined in MAZ-69's pid_allocator.go.
// When both branches merge to master, keep one definition (whichever lands
// first); these are identical and the merge collapses cleanly with a
// manual resolution.
//
//	MinPID = 2   — PID 0 invalid (Linux convention); PID 1 reserved (kernel sentinel)
//	MaxPID = 4095 — 12-bit cap, fits x86 PCID & ARM ASID hardware tag widths
const (
	MinPID ShepherdId = 2
	MaxPID ShepherdId = 4095
)

// MaxLiveShepherds bounds the concurrent live shepherd count. Tunable —
// start at 256 (well above the ~100 we expect from go-build-style peaks).
// Storage cost: MaxLiveShepherds × sizeof(Shepherd) ≈ 256 × 4KB ≈ 1MB
// (pre-MAZ-70; ~3MB post-MAZ-70). Acceptable for a kernel.
const MaxLiveShepherds = 256

// ErrShepherdSlotExhausted is returned by Allocate when all
// MaxLiveShepherds slots are in use. Translates to EAGAIN at the syscall
// boundary (matches MAZ-73's hard-fail decision).
var ErrShepherdSlotExhausted = errors.New("proc: shepherd storage at capacity")

// ErrShepherdPIDInUse is returned by Allocate when the requested PID
// already has a live slot.
var ErrShepherdPIDInUse = errors.New("proc: PID already allocated in storage")

// ErrShepherdPIDOutOfRange is returned by Allocate when the PID is not
// in [MinPID, MaxPID]. PIDs outside this range cannot be allocated.
var ErrShepherdPIDOutOfRange = errors.New("proc: PID out of range")

// ShepherdStorage holds live Shepherd records, keyed by PID, with O(1)
// lookup. Storage is fixed-size (no heap allocations); slot count is
// MaxLiveShepherds; PID space is [MinPID, MaxPID] from MAZ-68.
//
// Concurrency: NOT goroutine-safe on its own. Callers in the kernel are
// expected to hold an appropriate lock (typically schedulerLock).
type ShepherdStorage struct {
	// Implementation TBD. Phase B agent decides the concrete layout
	// (slots + inUse + pidIndex). Phase 0 tests pin the externally
	// observable behavior.
	_ struct{}
}

// NewShepherdStorage returns an empty storage ready to allocate.
func NewShepherdStorage() *ShepherdStorage {
	return &ShepherdStorage{}
}

// Allocate reserves a slot for pid and returns a pointer to the (zero-valued)
// Shepherd in that slot. Errors:
//
//   - ErrShepherdPIDOutOfRange if pid is not in [MinPID, MaxPID].
//   - ErrShepherdPIDInUse if pid already has a live slot.
//   - ErrShepherdSlotExhausted if all MaxLiveShepherds slots are in use.
//
//go:nosplit
func (s *ShepherdStorage) Allocate(pid ShepherdId) (*Shepherd, error) {
	// STUB — implemented by Phase B agent.
	_ = pid
	return nil, ErrShepherdSlotExhausted
}

// Get returns a pointer to the Shepherd for pid. The second return value
// is false if no slot is allocated for pid.
//
//go:nosplit
func (s *ShepherdStorage) Get(pid ShepherdId) (*Shepherd, bool) {
	// STUB — implemented by Phase B agent.
	_ = pid
	return nil, false
}

// Release frees the slot for pid. Idempotent — releasing a PID that has
// no allocated slot is a no-op. PIDs outside [MinPID, MaxPID] are silently
// ignored.
//
//go:nosplit
func (s *ShepherdStorage) Release(pid ShepherdId) {
	// STUB — implemented by Phase B agent.
	_ = pid
}

// Len returns the number of live shepherds currently in storage.
//
//go:nosplit
func (s *ShepherdStorage) Len() int32 {
	// STUB — implemented by Phase B agent.
	return 0
}

// ForEach calls fn for each live Shepherd in the storage. Iteration
// order is implementation-defined. If fn returns false, iteration stops
// early.
//
//go:nosplit
func (s *ShepherdStorage) ForEach(fn func(*Shepherd) bool) {
	// STUB — implemented by Phase B agent.
	_ = fn
}
