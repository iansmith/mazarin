// PIDAllocator is the kernel's sequential monotonic PID allocator (MAZ-69). A
// PID is a ShepherdId; pid_allocator_test.go pins the contract. MAZ-150 wires it
// into the kernel to replace the old LIFO shepherd-ID stack.
//
// Design decisions (see Linear MAZ-68 + MAZ-73):
//
//   - Universal PID space (= ShepherdId; no separate concept)
//   - MinPID = 2 (PID 0 invalid per Linux convention; PID 1 reserved for kernel sentinel)
//   - MaxPID = MaxLiveShepherds-1 (MAZ-150 Option A; see the MaxPID const below for
//     the satellite-array-index rationale and the MAZ-152 path back to the full
//     12-bit PCID/ASID range)
//   - Allocator is monotonic forward; freed PIDs do NOT reissue immediately
//   - Wraparound to MinPID when allocator hits MaxPID; skips in-use PIDs on wrap
//   - Exhaustion returns ErrPIDExhausted (matches Linux EAGAIN semantics)
//
// Storage shape: a dense in-use flag array (see the PIDAllocator type below).

package proc

import "errors"

// PID range constants. See MAZ-68 + MAZ-73 for derivation.
const (
	// MinPID is the lowest allocatable PID. PID 0 is invalid (Linux convention);
	// PID 1 is reserved for the kernel sentinel.
	MinPID ShepherdId = 2

	// MaxPID is the highest allocatable PID.
	//
	// MAZ-150 (Option A): capped to MaxLiveShepherds-1 so an allocated PID is always a
	// valid direct index into the kernel arrays that index by raw ShepherdId and are
	// sized [MaxLiveShepherds] — notifyQueues (ksyscall/constraint_notify.go), the
	// uring IPC slots (kmazarin/uring_ipc.go), and the channel pending-message arrays
	// (kmazarin/channels.go). (Other per-shepherd pools — deathSubscribers, uringIDMap,
	// deferredCleanups — linear-scan a stored SID field, so they bound the live COUNT,
	// not the PID value, and do not constrain MaxPID.)
	//
	// On ARM64 the PID is also the live TLB ASID (kmem.SwitchTTBR0WithASID); the range
	// fits the 16-bit ASID field. x86_64 does NOT currently tag TLB entries with the
	// PID (CR3 zeroes the PCID bits, see kmem/asm_barriers_amd64.s), but the range also
	// fits the 12-bit PCID field for if/when that is wired — arch divergence, flagged
	// per CLAUDE.md.
	//
	// This shortens the monotonic runway to MaxLiveShepherds-MinPID spawns before the
	// cursor wraps. MAZ-152 slot-maps the raw-indexed arrays to restore the full range
	// (MaxPID = 4095) with no memory blow-up; update this, the compile-time guard
	// below, and the Phase 0 bound tests when that lands.
	MaxPID ShepherdId = MaxLiveShepherds - 1
)

// ErrPIDExhausted is returned by Alloc when no PID in [MinPID, MaxPID] is
// available. Maps to Linux EAGAIN at the syscall boundary.
var ErrPIDExhausted = errors.New("proc: all PIDs in use")

// pidRangeSize is the number of allocatable PIDs in [MinPID, MaxPID].
const pidRangeSize = MaxPID - MinPID + 1

// Compile-time invariant (MAZ-150 Option A): every allocatable PID must be a valid
// index into the [MaxLiveShepherds]-sized kernel arrays that index by raw ShepherdId
// (notifyQueues, the uring IPC slots, the channel pending-message arrays), i.e.
// MaxPID < MaxLiveShepherds. If MAZ-152 lifts MaxPID past this without first
// slot-mapping those arrays, converting the resulting negative constant to uint
// fails to compile.
const _ = uint(int(MaxLiveShepherds) - 1 - int(MaxPID))

// PIDAllocator allocates ShepherdId values in [MinPID, MaxPID].
//
// The zero value is NOT ready for use — call NewPIDAllocator. (This may
// change once the storage shape is settled in MAZ-73.)
//
// The allocator is NOT goroutine-safe on its own. Callers in the kernel are
// expected to hold an appropriate lock; the eventual production allocator
// will be invoked from contexts where //go:nosplit applies.
//
// Storage shape: dense in-use flag array indexed by (pid - MinPID). The
// allocator scans forward from cursor, wrapping at MaxPID back to MinPID,
// to find the next free slot. cursor is never moved backward on Free —
// that is the mechanism that prevents immediate reuse of freed PIDs.
type PIDAllocator struct {
	// cursor is the next PID position the scan will consider on Alloc.
	cursor ShepherdId
	// freeCount is the number of free slots in inUse. When zero, the
	// allocator is exhausted and Alloc returns ErrPIDExhausted without
	// scanning.
	freeCount uint16
	// inUse[i] is true when PID (MinPID + i) is currently allocated.
	// Fixed-size array — no heap allocation, safe for //go:nosplit callers.
	inUse [pidRangeSize]bool
}

// NewPIDAllocator returns a fresh allocator with no PIDs in use; the first
// Alloc will return MinPID.
func NewPIDAllocator() *PIDAllocator {
	a := &PIDAllocator{}
	a.Init()
	return a
}

// Init resets the allocator in place to the empty state (no PIDs in use; the
// first Alloc returns MinPID). It allocates nothing, so it is safe to call from
// a //go:nosplit kernel init path on a package-level value (MAZ-150 wires it
// into the shepherd PID allocator).
//
//go:nosplit
func (a *PIDAllocator) Init() {
	a.cursor = MinPID
	a.freeCount = uint16(pidRangeSize)
	for i := range a.inUse {
		a.inUse[i] = false
	}
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
	if a.freeCount == 0 {
		return 0, ErrPIDExhausted
	}
	// Scan forward from cursor for the next free slot. freeCount > 0
	// guarantees the loop terminates within pidRangeSize iterations.
	for a.inUse[a.cursor-MinPID] {
		a.cursor++
		if a.cursor > MaxPID {
			a.cursor = MinPID
		}
	}
	pid := a.cursor
	a.inUse[pid-MinPID] = true
	a.freeCount--
	// Advance cursor past the just-issued PID so the next Alloc starts
	// strictly after it. This is what makes allocation monotonic forward.
	a.cursor++
	if a.cursor > MaxPID {
		a.cursor = MinPID
	}
	return pid, nil
}

// Free returns a PID to the allocatable pool. Idempotent — calling Free on
// a PID that is not currently allocated is a no-op (it doesn't panic, and
// doesn't corrupt allocator state).
//
// PIDs outside [MinPID, MaxPID] are silently ignored.
//
// Free deliberately does NOT touch cursor; the "no immediate reuse"
// guarantee depends on cursor's monotonic advance through the PID range.
//
//go:nosplit
func (a *PIDAllocator) Free(pid ShepherdId) {
	if pid < MinPID || pid > MaxPID {
		return
	}
	if !a.inUse[pid-MinPID] {
		return
	}
	a.inUse[pid-MinPID] = false
	a.freeCount++
}

// InUse reports whether the given PID is currently allocated.
// Returns false for PIDs outside [MinPID, MaxPID].
//
//go:nosplit
func (a *PIDAllocator) InUse(pid ShepherdId) bool {
	if pid < MinPID || pid > MaxPID {
		return false
	}
	return a.inUse[pid-MinPID]
}
