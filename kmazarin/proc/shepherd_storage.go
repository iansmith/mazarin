// ShepherdStorage — sparse PID-keyed storage for live Shepherd records (MAZ-73).
//
// Under the post-MAZ-68/-73 PID model, PIDs run 2..4095 but the number of LIVE
// shepherds at any time is much smaller (peak ~100 for go-build-style workloads).
// A dense PID-indexed array of Shepherd structs would be tens of MB — too much.
// Sparse storage with a PID-to-slot index is the chosen shape.
//
// Design decisions implemented here (see Linear MAZ-68 + MAZ-73):
//
//   - Sparse: only live shepherds occupy slots.
//   - Bounded: at most MaxLiveShepherds slots (start at 256; tunable later).
//   - O(1) lookup by PID via an internal PID→slot index.
//   - Exhaustion returns ErrShepherdSlotExhausted.
//   - Reallocating an existing PID returns ErrShepherdPIDInUse.
//   - PIDs outside [MinPID, MaxPID] are rejected.

package proc

import "errors"

// MinPID and MaxPID constants live in pid_allocator.go (MAZ-69 landed first).
// This file uses them by reference.

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
//
// Layout:
//   - slots: fixed-size array of Shepherd values.
//   - inUse: parallel occupancy bitmap (one bool per slot).
//   - pidIndex: PID → slot index map (-1 = no slot). Sized over the full
//     PID range so lookups are O(1) array access with no scan.
//   - count: number of live shepherds; mirrored from inUse for fast Len().
type ShepherdStorage struct {
	slots    [MaxLiveShepherds]Shepherd
	inUse    [MaxLiveShepherds]bool
	pidIndex [MaxPID - MinPID + 1]int16 // -1 means "no slot for this PID"
	count    int32
}

// NewShepherdStorage returns an empty storage ready to allocate.
func NewShepherdStorage() *ShepherdStorage {
	s := &ShepherdStorage{}
	for i := range s.pidIndex {
		s.pidIndex[i] = -1
	}
	return s
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
	if pid < MinPID || pid > MaxPID {
		return nil, ErrShepherdPIDOutOfRange
	}
	if s.pidIndex[pid-MinPID] >= 0 {
		return nil, ErrShepherdPIDInUse
	}
	for i := 0; i < MaxLiveShepherds; i++ {
		if !s.inUse[i] {
			s.inUse[i] = true
			s.pidIndex[pid-MinPID] = int16(i)
			s.count++
			return &s.slots[i], nil
		}
	}
	return nil, ErrShepherdSlotExhausted
}

// Get returns a pointer to the Shepherd for pid. The second return value
// is false if no slot is allocated for pid.
//
//go:nosplit
func (s *ShepherdStorage) Get(pid ShepherdId) (*Shepherd, bool) {
	if pid < MinPID || pid > MaxPID {
		return nil, false
	}
	idx := s.pidIndex[pid-MinPID]
	if idx < 0 || idx >= MaxLiveShepherds {
		return nil, false
	}
	return &s.slots[idx], true
}

// Release frees the slot for pid. Idempotent — releasing a PID that has
// no allocated slot is a no-op. PIDs outside [MinPID, MaxPID] are silently
// ignored.
//
//go:nosplit
func (s *ShepherdStorage) Release(pid ShepherdId) {
	if pid < MinPID || pid > MaxPID {
		return
	}
	idx := s.pidIndex[pid-MinPID]
	if idx < 0 {
		return
	}
	s.slots[idx] = Shepherd{}
	s.inUse[idx] = false
	s.pidIndex[pid-MinPID] = -1
	s.count--
}

// Len returns the number of live shepherds currently in storage.
//
//go:nosplit
func (s *ShepherdStorage) Len() int32 {
	return s.count
}

// ForEach calls fn for each live Shepherd in the storage. Iteration
// order is implementation-defined. If fn returns false, iteration stops
// early.
//
//go:nosplit
func (s *ShepherdStorage) ForEach(fn func(*Shepherd) bool) {
	for i := 0; i < MaxLiveShepherds; i++ {
		if s.inUse[i] {
			if !fn(&s.slots[i]) {
				return
			}
		}
	}
}
